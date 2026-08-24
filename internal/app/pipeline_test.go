package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrcha/gymlogger/internal/llm"
	"github.com/mrcha/gymlogger/internal/store"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newApp(t *testing.T, client llm.Client) *App {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"), time.UTC)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	st.SetSetting(context.Background(), "bodyweight_kg", "80")
	return New(st, client, quietLogger())
}

// The whole pipeline on the offline stub: parse, validate, persist, build
// context, coach. No API key, no network.
func TestEndToEndLogWithStub(t *testing.T) {
	a := newApp(t, llm.Stub{})
	ctx := context.Background()

	res, err := a.Log(ctx, "bench press 100 x 5, 5, 4; incline db press 30 x 10, 10, 9", time.Now())
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if res.SessionID == 0 {
		t.Fatalf("session was not persisted: %+v", res)
	}
	if res.Context == nil || len(res.Context.LiftHistory) != 2 {
		t.Fatalf("expected two lifts in context, got %+v", res.Context)
	}
	if res.Reply == "" {
		t.Fatal("expected a coach reply")
	}
	if res.Rank == nil || res.Rank.Tier == "" {
		t.Fatal("expected a rank")
	}
}

// Logging the same lift heavier the following week must produce a weight PR,
// end to end, through the real SQL.
func TestEndToEndProducesWeightPR(t *testing.T) {
	a := newApp(t, llm.Stub{})
	ctx := context.Background()

	week1 := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if _, err := a.Log(ctx, "bench press 100 x 3", week1); err != nil {
		t.Fatalf("week 1: %v", err)
	}

	week2 := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	res, err := a.Log(ctx, "bench press 105 x 3", week2)
	if err != nil {
		t.Fatalf("week 2: %v", err)
	}
	if len(res.Context.LiftHistory) == 0 {
		t.Fatal("no lift history")
	}
	if !res.Context.LiftHistory[0].IsWeightPR {
		t.Fatalf("105 after 100 should be a weight PR: %+v", res.Context.LiftHistory[0])
	}
}

// An implausible parse must be parked for confirmation rather than written
// into the history, where it would poison every later comparison.
func TestImplausibleParseIsParked(t *testing.T) {
	a := newApp(t, &fakeClient{parse: `{
		"session_type":"chest","exercises":[{"name":"bench press","raw_name":"bench",
		"equipment":"barbell","sets":[{"set_type":"working","weight":900,"unit":"kg",
		"load_basis":"total","reps":5,"reps_uncertain":false,"to_failure":false}]}],
		"cardio":[],"subjective":{},"assumptions":[],"unresolved_references":[],"unparsed":null}`})

	res, err := a.Log(context.Background(), "bench 900 x 5", time.Now())
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if res.SessionID != 0 {
		t.Fatal("a 900 kg bench must not be persisted")
	}
	if res.PendingID == 0 || res.Needs == "" {
		t.Fatalf("expected the parse to be parked for confirmation, got %+v", res)
	}
}

// A reference the parser could not resolve is a hole, not a guess, so it has
// to be confirmed before anything is written.
func TestUnresolvedReferenceIsParked(t *testing.T) {
	a := newApp(t, &fakeClient{parse: `{
		"session_type":"back","exercises":[{"name":"db row","raw_name":"same as last time",
		"equipment":"dumbbell","sets":[{"set_type":"working","weight":null,"unit":null,
		"load_basis":"per_side","reps":10,"reps_uncertain":false,"to_failure":false}]}],
		"cardio":[],"subjective":{},"assumptions":[],
		"unresolved_references":[{"path":"exercises[0]","text":"same as last time"}],
		"unparsed":null}`})

	res, err := a.Log(context.Background(), "db row same as last time for 10", time.Now())
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if res.SessionID != 0 {
		t.Fatal("an unresolved reference must not be persisted silently")
	}
	if !strings.Contains(res.Needs, "same as last time") {
		t.Fatalf("the confirmation prompt should name the reference, got %q", res.Needs)
	}
}

// The coach's emoji and metaphor rules are enforced in Go, so a
// non-compliant model still produces a compliant reply.
func TestCoachOutputIsSanitized(t *testing.T) {
	fc := &fakeClient{
		parse: `{"session_type":"chest","exercises":[{"name":"bench press","raw_name":"bench",
			"equipment":"barbell","sets":[{"set_type":"working","weight":100,"unit":"kg",
			"load_basis":"total","reps":5,"reps_uncertain":false,"to_failure":false}]}],
			"cardio":[],"subjective":{},"assumptions":[],"unresolved_references":[],"unparsed":null}`,
		coachReplies: []string{
			"Logged. 💪 Time to dive into the next block, big fish.",
			"Logged. Solid top set, nothing to complain about.",
		},
	}
	a := newApp(t, fc)

	res, err := a.Log(context.Background(), "bench 100 x 5", time.Now())
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if strings.Contains(res.Reply, "fish") || strings.Contains(res.Reply, "dive into") {
		t.Fatalf("banned metaphor survived: %q", res.Reply)
	}
	if strings.ContainsRune(res.Reply, '💪') {
		t.Fatalf("emoji survived: %q", res.Reply)
	}
	if fc.coachCalls != 2 {
		t.Fatalf("expected exactly one regeneration, got %d coach calls", fc.coachCalls)
	}
}

// A fenced JSON reply is the single most common open-model failure, and it
// must not surface as an error to the user.
func TestFencedParseIsRecovered(t *testing.T) {
	a := newApp(t, &fakeClient{parse: "```json\n" + `{"session_type":"chest",
		"exercises":[{"name":"bench press","raw_name":"bench","equipment":"barbell",
		"sets":[{"set_type":"working","weight":100,"unit":"kg","load_basis":"total",
		"reps":5,"reps_uncertain":false,"to_failure":false}]}],
		"cardio":[],"subjective":{},"assumptions":[],"unresolved_references":[],
		"unparsed":null}` + "\n```"})

	res, err := a.Log(context.Background(), "bench 100 x 5", time.Now())
	if err != nil {
		t.Fatalf("fenced JSON should be recovered: %v", err)
	}
	if res.SessionID == 0 {
		t.Fatal("session should have been persisted")
	}
}

// A coach failure must not lose the lift. The session is the valuable half.
func TestCoachFailureStillPersistsSession(t *testing.T) {
	a := newApp(t, &fakeClient{
		parse: `{"session_type":"chest","exercises":[{"name":"bench press","raw_name":"bench",
			"equipment":"barbell","sets":[{"set_type":"working","weight":100,"unit":"kg",
			"load_basis":"total","reps":5,"reps_uncertain":false,"to_failure":false}]}],
			"cardio":[],"subjective":{},"assumptions":[],"unresolved_references":[],"unparsed":null}`,
		coachErr: true,
	})

	res, err := a.Log(context.Background(), "bench 100 x 5", time.Now())
	if err != nil {
		t.Fatalf("a coach failure should not fail the log: %v", err)
	}
	if res.SessionID == 0 {
		t.Fatal("the session must survive a failed coach call")
	}
	if res.Reply != "" {
		t.Fatal("expected an empty reply when the coach call failed")
	}
}

// ---------- test double ----------

type fakeClient struct {
	parse        string
	coachReplies []string
	coachErr     bool
	coachCalls   int
}

func (f *fakeClient) Complete(_ context.Context, r llm.CompletionRequest) (string, error) {
	if strings.Contains(r.System, "strength-training log parser") {
		return f.parse, nil
	}
	f.coachCalls++
	if f.coachErr {
		return "", errFake
	}
	if len(f.coachReplies) == 0 {
		return "Logged. Clean session.", nil
	}
	idx := f.coachCalls - 1
	if idx >= len(f.coachReplies) {
		idx = len(f.coachReplies) - 1
	}
	return f.coachReplies[idx], nil
}

type fakeErr struct{}

func (fakeErr) Error() string { return "fake coach failure" }

var errFake = fakeErr{}
