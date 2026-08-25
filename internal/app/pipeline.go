// Package app wires the pieces into the log pipeline.
//
// Parse, validate, persist, build context, coach. The ordering matters: the
// context queries compare today against history, so today's rows have to be in
// the database before Build runs, which in turn means validation has to happen
// before that, because a bad row is permanent.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/mrcha/gymlogger/internal/berserk"
	"github.com/mrcha/gymlogger/internal/contextbuilder"
	"github.com/mrcha/gymlogger/internal/llm"
	"github.com/mrcha/gymlogger/internal/model"
	"github.com/mrcha/gymlogger/internal/prompt"
	"github.com/mrcha/gymlogger/internal/store"
	"github.com/mrcha/gymlogger/internal/validate"
)

type App struct {
	Store  *store.Store
	LLM    llm.Client
	Ctx    *contextbuilder.Builder
	Rank   *berserk.Calculator
	Logger *slog.Logger

	ParserModel string
	CoachModel  string
}

func New(st *store.Store, client llm.Client, logger *slog.Logger) *App {
	return &App{
		Store:       st,
		LLM:         client,
		Ctx:         contextbuilder.New(st),
		Rank:        berserk.NewCalculator(st),
		Logger:      logger,
		ParserModel: llm.ModelParser,
		CoachModel:  llm.ModelCoach,
	}
}

// LogResult is what the phone gets back.
type LogResult struct {
	SessionID int64                   `json:"session_id,omitempty"`
	PendingID int64                   `json:"pending_id,omitempty"`
	Needs     string                  `json:"needs_confirmation,omitempty"`
	Parsed    *model.ParsedSession    `json:"parsed"`
	Context   *contextbuilder.Context `json:"context,omitempty"`
	Rank      *berserk.Rank           `json:"rank,omitempty"`
	Reply     string                  `json:"reply,omitempty"`
	Repairs   []string                `json:"repairs,omitempty"`
}

// Log runs the full pipeline on a piece of freeform text.
func (a *App) Log(ctx context.Context, raw string, at time.Time) (*LogResult, error) {
	parsed, err := a.Parse(ctx, raw)
	if err != nil {
		return nil, err
	}
	parsed.RawText = raw
	parsed.LoggedAt = at

	vr := validate.Session(parsed)
	res := &LogResult{Parsed: parsed, Repairs: vr.Repairs()}

	// Two reasons to stop before writing: the parse is implausible, or it
	// points at information the model did not have. Either way, asking is
	// cheaper than a permanently wrong PR table.
	switch {
	case vr.Fatal():
		id, err := a.Store.StashPending(ctx, raw, parsed, vr.Reason())
		if err != nil {
			return nil, err
		}
		res.PendingID, res.Needs = id, "implausible values: "+vr.Reason()
		return res, nil
	case parsed.NeedsConfirmation():
		reason := "references something I do not have: " + parsed.UnresolvedReferences[0].Text
		id, err := a.Store.StashPending(ctx, raw, parsed, reason)
		if err != nil {
			return nil, err
		}
		res.PendingID, res.Needs = id, reason
		return res, nil
	case parsed.IsEmpty():
		res.Needs = "no workout found in that message"
		return res, nil
	}

	return a.Commit(ctx, parsed, res)
}

// Commit persists an already-validated session and produces the coach reply.
// It is separate from Log so the confirm-and-fix path can reuse it.
func (a *App) Commit(ctx context.Context, parsed *model.ParsedSession, res *LogResult) (*LogResult, error) {
	if res == nil {
		res = &LogResult{Parsed: parsed}
	}

	sessionID, err := a.Store.Persist(ctx, parsed)
	if err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	res.SessionID = sessionID

	cb, err := a.Ctx.Build(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("build context: %w", err)
	}
	res.Context = cb

	when := parsed.LoggedAt
	if when.IsZero() {
		when = time.Now()
	}
	// Blood for this session's records is settled before the rank is computed,
	// so the total the phone renders already includes what just happened. The
	// ledger is idempotent on its dedup keys, so a retried log does not pay
	// twice.
	if err := a.awardSessionBlood(ctx, sessionID, when, cb); err != nil {
		a.Logger.Warn("blood award failed", "err", err)
	}

	r, err := a.Rank.Compute(ctx, when)
	if err != nil {
		a.Logger.Warn("rank compute failed", "err", err)
	} else {
		if delta, err := a.Rank.Delta(ctx, when, r); err == nil {
			cb.RankDelta = delta
		}
		if err := a.Rank.Save(ctx, when, r); err != nil {
			a.Logger.Warn("rank save failed", "err", err)
		}
		res.Rank = r
	}

	reply, err := a.Coach(ctx, parsed, cb)
	if err != nil {
		// A failed coach call must not lose the session. The lift is logged;
		// the commentary is the disposable half.
		a.Logger.Error("coach call failed", "err", err, "session", sessionID)
		res.Reply = ""
		return res, nil
	}
	res.Reply = reply
	if err := a.Store.SaveCoachReply(ctx, sessionID, reply); err != nil {
		a.Logger.Warn("save coach reply failed", "err", err)
	}
	return res, nil
}

// Parse runs the parser call and decodes the result, retrying once with the
// decode error appended.
//
// The retry is only useful because the appended error changes the input; at
// temperature 0 an identical request produces an identical failure, which is
// the trap in "just retry once".
func (a *App) Parse(ctx context.Context, raw string) (*model.ParsedSession, error) {
	user := raw

	for attempt := 0; attempt < 2; attempt++ {
		out, err := a.LLM.Complete(ctx, llm.CompletionRequest{
			Model:       a.ParserModel,
			System:      prompt.Parser,
			User:        user,
			Temperature: 0,
			MaxTokens:   2000,
			JSONMode:    true,
		})
		if err != nil {
			return nil, fmt.Errorf("parser call: %w", err)
		}

		var p model.ParsedSession
		decodeErr := json.Unmarshal([]byte(llm.StripFences(out)), &p)
		if decodeErr == nil {
			return &p, nil
		}

		a.Logger.Warn("parser returned unparseable json", "attempt", attempt+1, "err", decodeErr)
		user = fmt.Sprintf(
			"%s\n\n[Your previous reply could not be parsed as JSON. The error was: %s. "+
				"Return only the JSON object, no fences and no prose.]", raw, decodeErr)
	}
	return nil, fmt.Errorf("parser produced unparseable JSON twice")
}

// Coach runs the second call and enforces the prompt rules that can be checked
// deterministically. One regeneration on a violation, then it ships regardless
// rather than looping on a model that will not comply.
func (a *App) Coach(ctx context.Context, parsed *model.ParsedSession, cb *contextbuilder.Context) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"LOGGED_SESSION": parsed,
		"CONTEXT":        cb,
	})
	if err != nil {
		return "", err
	}

	user := string(payload)
	var reply string

	for attempt := 0; attempt < 2; attempt++ {
		out, err := a.LLM.Complete(ctx, llm.CompletionRequest{
			Model:       a.CoachModel,
			System:      prompt.Coach,
			User:        user,
			Temperature: 0.8,
			MaxTokens:   400,
		})
		if err != nil {
			return "", fmt.Errorf("coach call: %w", err)
		}

		reply = llm.StripEmoji(out)
		v, bad := llm.CheckReply(reply)
		if !bad {
			return reply, nil
		}

		a.Logger.Warn("coach reply violated a rule", "kind", v.Kind, "term", v.Term, "attempt", attempt+1)
		user = string(payload) + fmt.Sprintf(
			"\n\n[Your previous reply used the banned %s %q. Rewrite it without that word or anything like it.]",
			v.Kind, v.Term)
	}

	// Second strike: ship it anyway. A slightly off-voice reply beats no reply,
	// and the session is already safely logged either way.
	return reply, nil
}

// ConfirmPending commits a stashed parse after the user has corrected it.
func (a *App) ConfirmPending(ctx context.Context, pendingID int64, corrected *model.ParsedSession) (*LogResult, error) {
	if corrected == nil {
		p, raw, err := a.Store.Pending(ctx, pendingID)
		if err != nil {
			return nil, fmt.Errorf("load pending %d: %w", pendingID, err)
		}
		p.RawText = raw
		corrected = p
	}
	if corrected.LoggedAt.IsZero() {
		corrected.LoggedAt = time.Now()
	}

	// A user-corrected session still goes through validation. The correction
	// came from a phone keyboard, which is not a trustworthy source either.
	vr := validate.Session(corrected)
	if vr.Fatal() {
		return nil, fmt.Errorf("corrected session still invalid: %s", vr.Reason())
	}

	res, err := a.Commit(ctx, corrected, &LogResult{Parsed: corrected, Repairs: vr.Repairs()})
	if err != nil {
		return nil, err
	}
	if err := a.Store.ResolvePending(ctx, pendingID); err != nil {
		a.Logger.Warn("resolve pending failed", "err", err)
	}
	return res, nil
}

// awardSessionBlood prices the records the context builder already found.
//
// The split of responsibility matters: contextbuilder decides what a PR IS --
// the exact-weight rule, the 10% band, the session-timeline cut -- and this
// only decides what a PR is WORTH. Duplicating the PR rules here would give
// the app two definitions of a record that could disagree.
func (a *App) awardSessionBlood(ctx context.Context, sessionID int64, when time.Time,
	cb *contextbuilder.Context) error {

	prof, err := berserk.LoadProfile(ctx, a.Store, when)
	if err != nil {
		return err
	}

	// A lift is stale when it has gone 180 days without moving. Erratum 5
	// prices breaking that at 50 Blood, which is the largest single non-tier
	// award in the system, because a plateau broken after six months is the
	// most motivating event in a long training life.
	stale := map[string]bool{}
	for _, s := range cb.StaleLifts {
		if s.Days >= 180 {
			stale[s.Name] = true
		}
	}

	var awards []berserk.PRAward
	for _, lift := range cb.LiftHistory {
		if !berserk.IsScored(lift.Name) {
			continue
		}
		switch {
		case lift.IsWeightPR:
			var delta float64
			if lift.Est1RMToday != nil && lift.Est1RMPrevious != nil {
				delta = berserk.ScoreDelta(prof, lift.Name, *lift.Est1RMToday, *lift.Est1RMPrevious)
			}
			awards = append(awards, berserk.PRAward{
				SessionID: sessionID, Lift: lift.Name, Kind: "strength",
				DeltaS: delta, Stagnant: stale[lift.Name],
			})
		case lift.IsRepPR:
			awards = append(awards, berserk.PRAward{
				SessionID: sessionID, Lift: lift.Name, Kind: "rep",
			})
		}
	}
	if len(awards) == 0 {
		return nil
	}
	_, err = a.Rank.Ledger.AwardPRs(ctx, when, awards)
	return err
}
