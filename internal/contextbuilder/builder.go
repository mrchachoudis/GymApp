package contextbuilder

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mrcha/gymlogger/internal/model"
	"github.com/mrcha/gymlogger/internal/store"
)

type Builder struct {
	st *store.Store

	// LoadJumpPct is the single-session increase on a compound that counts as
	// a risk rather than a win.
	LoadJumpPct float64
	// FailureSetLimit is the number of sets to failure in one session past
	// which the session is producing fatigue rather than stimulus.
	FailureSetLimit int
	// NoRestWindowDays is how many consecutive training days without a rest
	// day is worth flagging.
	NoRestWindowDays int
	// StaleMinSessions and StaleMinDays must BOTH be met before a lift counts
	// as stalled, which is what makes staleness frequency-aware.
	StaleMinSessions int
	StaleMinDays     int
}

func New(st *store.Store) *Builder {
	return &Builder{
		st:               st,
		LoadJumpPct:      15,
		FailureSetLimit:  3,
		NoRestWindowDays: 6,
		StaleMinSessions: 4,
		StaleMinDays:     21,
	}
}

// repPRWeightFloor is how close to his best-ever weight a set must be before
// extra reps count as a record. Ten percent below top weight is still a top
// set; forty percent below is a different exercise.
const repPRWeightFloor = 0.9

// compoundLifts are the movements where a fast load jump actually carries
// injury risk. Adding 5 kg to a lateral raise is not the same event.
var compoundLifts = map[string]bool{
	"bench press": true, "squat": true, "rdl": true, "deadlift": true,
	"overhead press": true, "shoulder press": true, "incline db press": true,
	"dips": true, "pull-up": true, "chin-up": true, "barbell row": true,
	"front squat": true, "romanian deadlift": true,
}

// Build assembles the CONTEXT block for a session that has already been
// persisted. It must run after Persist, because today's rows have to be in the
// database for the comparison queries to see them.
func (b *Builder) Build(ctx context.Context, sessionID int64) (*Context, error) {
	var localDate, loggedAt string
	var postShift int
	var pain sql.NullString
	var rawText string
	err := b.st.DB().QueryRowContext(ctx,
		`SELECT local_date, logged_at, post_nightshift, pain, raw_text FROM sessions WHERE id = ?`,
		sessionID).Scan(&localDate, &loggedAt, &postShift, &pain, &rawText)
	if err != nil {
		return nil, fmt.Errorf("load session %d: %w", sessionID, err)
	}

	c := &Context{
		Date:           localDate,
		PostNightShift: postShift == 1,
		Flags:          []Flag{},
		LiftHistory:    []LiftEntry{},
		StaleLifts:     []StaleLift{},
	}

	if c.DaysSinceLastRest, err = b.daysSinceLastRest(ctx, localDate); err != nil {
		return nil, err
	}
	if c.SessionsThisWeek, err = b.sessionsThisWeek(ctx, localDate); err != nil {
		return nil, err
	}

	bodyweight := b.st.BodyweightKg(ctx)
	if c.LiftHistory, err = b.liftHistory(ctx, sessionID, loggedAt, bodyweight); err != nil {
		return nil, err
	}
	if c.StaleLifts, err = b.staleLifts(ctx, localDate); err != nil {
		return nil, err
	}
	if c.Streaks, err = b.streaks(ctx, sessionID); err != nil {
		return nil, err
	}

	failureSets, err := b.failureSetCount(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	c.Flags = b.evaluateFlags(ctx, flagInput{
		sessionID:     sessionID,
		pain:          pain,
		rawText:       rawText,
		postShift:     postShift == 1,
		failureSets:   failureSets,
		daysSinceRest: c.DaysSinceLastRest,
		lifts:         c.LiftHistory,
		stale:         c.StaleLifts,
	})

	c.Suggestion = b.suggestion(c)

	// A nil slice marshals to JSON null, and a client that declares these as
	// arrays fails to decode rather than falling back to a default. That broke
	// logging outright: the session was stored, the coach replied, and the
	// phone threw on "flags":null while parsing the response.
	//
	// Guaranteeing empty arrays here is also just correct for the wire format --
	// "no flags" is [], not null.
	if c.Flags == nil {
		c.Flags = []Flag{}
	}
	if c.LiftHistory == nil {
		c.LiftHistory = []LiftEntry{}
	}
	if c.StaleLifts == nil {
		c.StaleLifts = []StaleLift{}
	}
	return c, nil
}

// ---------- flags ----------

type flagInput struct {
	sessionID     int64
	pain          sql.NullString
	rawText       string
	postShift     bool
	failureSets   int
	daysSinceRest int
	lifts         []LiftEntry
	stale         []StaleLift
}

func (b *Builder) evaluateFlags(ctx context.Context, in flagInput) []Flag {
	var flags []Flag

	if in.pain.Valid && strings.TrimSpace(in.pain.String) != "" {
		flags = append(flags, Flag{
			Type:     FlagPain,
			Detail:   strings.TrimSpace(in.pain.String),
			Priority: prioPain,
		})
	}

	if detail, ok := b.unsafeSetup(ctx, in.rawText, in.lifts); ok {
		flags = append(flags, Flag{Type: FlagUnsafeSetup, Detail: detail, Priority: prioUnsafe})
	}

	for _, l := range in.lifts {
		if l.LoadJumpPct != nil && *l.LoadJumpPct > b.LoadJumpPct && compoundLifts[l.Name] {
			flags = append(flags, Flag{
				Type: FlagLoadJump,
				Detail: fmt.Sprintf("%s jumped %.0f percent in one session, from %s to %s",
					l.Display, *l.LoadJumpPct, l.PreviousTopSet, l.TopSetToday),
				Priority: prioLoadJump,
			})
		}
	}

	if in.failureSets >= b.FailureSetLimit {
		flags = append(flags, Flag{
			Type: FlagFailureOveruse,
			Detail: fmt.Sprintf("%d sets taken to failure in one session, that is fatigue rather than stimulus",
				in.failureSets),
			Priority: prioFailure,
		})
	}

	if in.daysSinceRest >= b.NoRestWindowDays {
		flags = append(flags, Flag{
			Type:     FlagNoRest,
			Detail:   fmt.Sprintf("%d logged days without a rest day", in.daysSinceRest),
			Priority: prioNoRest,
		})
	}

	if in.postShift {
		if heavy := heaviestCompound(in.lifts); heavy != "" {
			flags = append(flags, Flag{
				Type:     FlagNightShift,
				Detail:   fmt.Sprintf("heavy %s straight off a night shift", heavy),
				Priority: prioNightShift,
			})
		}
	}

	for _, s := range in.stale {
		flags = append(flags, Flag{
			Type: FlagStale,
			Detail: fmt.Sprintf("%s has sat at %.1f kg for %d sessions across %d days",
				s.Display, s.TopWeightKg, s.Sessions, s.Days),
			Priority: prioStale,
		})
		break // one stale callout per reply is plenty
	}

	sort.SliceStable(flags, func(i, j int) bool { return flags[i].Priority < flags[j].Priority })
	return flags
}

// unsafeSetup detects lifting heavy with nowhere for a failed rep to go.
// Mario squats off a dip bar, so this is a real condition rather than a
// theoretical one, and the setting makes it explicit instead of inferred.
func (b *Builder) unsafeSetup(ctx context.Context, rawText string, lifts []LiftEntry) (string, bool) {
	lower := strings.ToLower(rawText)
	for _, phrase := range []string{"no rack", "no spotter", "no safeties", "off the dip bar",
		"dip bars", "no j-hooks", "no pins", "alone"} {
		if strings.Contains(lower, phrase) {
			return "lifting heavy with no rack or spotter, a failed rep has nowhere to go", true
		}
	}

	if b.st.Setting(ctx, "no_rack", "false") != "true" {
		return "", false
	}
	for _, l := range lifts {
		if !compoundLifts[l.Name] {
			continue
		}
		if l.Est1RMToday != nil {
			return fmt.Sprintf("%s at %s with no rack, a failed rep has nowhere to go",
				l.Display, l.TopSetToday), true
		}
	}
	return "", false
}

func heaviestCompound(lifts []LiftEntry) string {
	best := ""
	var bestVal float64
	for _, l := range lifts {
		if !compoundLifts[l.Name] || l.Est1RMToday == nil {
			continue
		}
		if *l.Est1RMToday > bestVal {
			bestVal, best = *l.Est1RMToday, l.Display
		}
	}
	return best
}

// ---------- suggestion ----------

// suggestion produces the progression line. Computing it here rather than
// asking the model keeps "add 2.5 kg" from becoming "add 20 kg" on a bad day.
func (b *Builder) suggestion(c *Context) string {
	// Never suggest progression in the same breath as pain.
	for _, f := range c.Flags {
		if f.Type == FlagPain || f.Type == FlagUnsafeSetup {
			return ""
		}
	}

	for _, l := range c.LiftHistory {
		if l.IsWeightPR || l.IsBaseline {
			continue
		}
		if l.IsRepPR && l.Est1RMToday != nil {
			step := progressionStep(l.LoadBasis, l.Name)
			return fmt.Sprintf("next %s session, add %.1f kg and reset the reps", l.Display, step)
		}
	}

	if len(c.StaleLifts) > 0 {
		s := c.StaleLifts[0]
		return fmt.Sprintf("%s has not moved in %d days, either add %.1f kg or add a set",
			s.Display, s.Days, progressionStep("", s.Name))
	}
	return ""
}

// progressionStep is the smallest jump worth making, which depends on the
// implement. Dumbbells come in fixed increments; a barbell does not.
func progressionStep(basis, name string) float64 {
	switch basis {
	case string(model.BasisPerSide):
		return 2.0
	case string(model.BasisPerLimb):
		return 2.5
	}
	if compoundLifts[name] {
		return 2.5
	}
	return 1.25
}

// ---------- queries ----------

func (b *Builder) daysSinceLastRest(ctx context.Context, localDate string) (int, error) {
	// Counts distinct calendar days that were logged as training, walking
	// backwards from today, stopping at the first rest day or gap.
	rows, err := b.st.DB().QueryContext(ctx, `
		SELECT local_date, MAX(is_rest_day)
		FROM sessions
		WHERE local_date <= ?
		GROUP BY local_date
		ORDER BY local_date DESC
		LIMIT 30`, localDate)
	if err != nil {
		return 0, fmt.Errorf("days since rest: %w", err)
	}
	defer rows.Close()

	count := 0
	var prev time.Time
	for rows.Next() {
		var d string
		var isRest int
		if err := rows.Scan(&d, &isRest); err != nil {
			return 0, err
		}
		if isRest == 1 {
			break
		}
		day, err := time.Parse("2006-01-02", d)
		if err != nil {
			continue
		}
		// A calendar gap is itself a rest day, even if nothing was logged.
		if !prev.IsZero() && prev.Sub(day) > 24*time.Hour {
			break
		}
		prev = day
		count++
	}
	return count, rows.Err()
}

func (b *Builder) sessionsThisWeek(ctx context.Context, localDate string) (int, error) {
	d, err := time.Parse("2006-01-02", localDate)
	if err != nil {
		return 0, err
	}
	// ISO week: Monday start.
	offset := (int(d.Weekday()) + 6) % 7
	monday := d.AddDate(0, 0, -offset).Format("2006-01-02")

	var n int
	err = b.st.DB().QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT local_date) FROM sessions
		WHERE local_date >= ? AND local_date <= ? AND is_rest_day = 0`,
		monday, localDate).Scan(&n)
	return n, err
}

func (b *Builder) failureSetCount(ctx context.Context, sessionID int64) (int, error) {
	var n int
	err := b.st.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM v_sets
		WHERE session_id = ? AND to_failure = 1 AND set_type = 'working'`,
		sessionID).Scan(&n)
	return n, err
}

// liftHistory builds one entry per lift key trained today, with the previous
// session and the all-time bests already compared.
func (b *Builder) liftHistory(ctx context.Context, sessionID int64, loggedAt string, bodyweight float64) ([]LiftEntry, error) {
	// Today's top working set per lift key. Ordering by weight then reps is
	// what "top set" means; COALESCE keeps bodyweight lifts (NULL weight) in
	// the ranking instead of dropping them.
	rows, err := b.st.DB().QueryContext(ctx, `
		SELECT name, equipment, load_basis,
		       COALESCE(weight_kg, 0) AS w,
		       COALESCE(reps, reps_low, 0) AS r
		FROM v_sets
		WHERE session_id = ? AND set_type = 'working'
		ORDER BY name, equipment, load_basis, w DESC, r DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("today top sets: %w", err)
	}
	defer rows.Close()

	type todayTop struct {
		key    model.LiftKey
		weight float64
		reps   int
	}
	var order []model.LiftKey
	tops := map[string]todayTop{}

	for rows.Next() {
		var name, equip, basis string
		var w float64
		var r int
		if err := rows.Scan(&name, &equip, &basis, &w, &r); err != nil {
			return nil, err
		}
		k := model.LiftKey{Name: name, Equipment: model.Equipment(equip), LoadBasis: model.LoadBasis(basis)}
		if _, seen := tops[k.String()]; seen {
			continue // rows are pre-sorted, so the first row per key is the top set
		}
		tops[k.String()] = todayTop{key: k, weight: w, reps: r}
		order = append(order, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]LiftEntry, 0, len(order))
	for _, k := range order {
		t := tops[k.String()]
		e := LiftEntry{
			Name:        k.Name,
			Display:     k.Display(),
			Equipment:   string(k.Equipment),
			LoadBasis:   string(k.LoadBasis),
			TopSetToday: formatSet(t.weight, t.reps, k.LoadBasis),
		}

		if v, ok := model.Epley1RM(t.weight, t.reps); ok {
			e.Est1RMToday = &v
		}

		vol, err := b.volumeFor(ctx, sessionID, k, bodyweight)
		if err != nil {
			return nil, err
		}
		e.VolumeTodayKg = vol

		if err := b.fillPrevious(ctx, &e, k, t.weight, t.reps, sessionID, loggedAt, bodyweight); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// fillPrevious compares today against history for one lift key.
//
// A weight PR is today's top working weight beating the all-time best for the
// SAME key. A rep PR is more reps at a weight already achieved before. Keying
// on (name, equipment, load_basis) is what stops a 30 kg per-side dumbbell row
// from being compared against a 30 kg total machine row.
// The history cut is by session timestamp rather than calendar date, and it
// excludes only the current session. Cutting on date meant a second session
// logged the same day could not see the first, so an evening entry after a
// morning one reported every lift as a fresh baseline.
func (b *Builder) fillPrevious(ctx context.Context, e *LiftEntry, k model.LiftKey,
	todayW float64, todayR int, sessionID int64, loggedAt string, bodyweight float64) error {

	var prevW sql.NullFloat64
	var prevR sql.NullInt64
	var prevDate sql.NullString
	err := b.st.DB().QueryRowContext(ctx, `
		SELECT COALESCE(weight_kg, 0), COALESCE(reps, reps_low, 0), local_date
		FROM v_sets
		WHERE name = ? AND equipment = ? AND load_basis = ?
		  AND set_type = 'working' AND logged_at <= ? AND session_id != ?
		ORDER BY logged_at DESC, COALESCE(weight_kg,0) DESC, COALESCE(reps,reps_low,0) DESC
		LIMIT 1`,
		k.Name, string(k.Equipment), string(k.LoadBasis), loggedAt, sessionID).Scan(&prevW, &prevR, &prevDate)

	if errors.Is(err, sql.ErrNoRows) {
		e.IsBaseline = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("previous top set for %s: %w", k, err)
	}

	e.PreviousTopSet = formatSet(prevW.Float64, int(prevR.Int64), k.LoadBasis)
	e.PreviousDate = prevDate.String
	if v, ok := model.Epley1RM(prevW.Float64, int(prevR.Int64)); ok {
		e.Est1RMPrevious = &v
	}

	// All-time best weight, and best reps at today's weight, both excluding
	// today. These are what a PR is actually measured against; the immediately
	// previous session is only for narrative.
	var bestW sql.NullFloat64
	if err := b.st.DB().QueryRowContext(ctx, `
		SELECT MAX(COALESCE(weight_kg, 0)) FROM v_sets
		WHERE name = ? AND equipment = ? AND load_basis = ?
		  AND set_type = 'working' AND logged_at <= ? AND session_id != ?`,
		k.Name, string(k.Equipment), string(k.LoadBasis), loggedAt, sessionID).Scan(&bestW); err != nil {
		return err
	}

	// Best reps at this exact weight, not at "this weight or heavier". The
	// looser comparison is what made 70 kg x 12 outrank 100 kg x 5 and get
	// announced as a rep PR, which is a deload wearing a medal.
	var bestRepsAtWeight sql.NullInt64
	if err := b.st.DB().QueryRowContext(ctx, `
		SELECT MAX(COALESCE(reps, reps_low, 0)) FROM v_sets
		WHERE name = ? AND equipment = ? AND load_basis = ?
		  AND set_type = 'working' AND logged_at <= ? AND session_id != ?
		  AND ABS(COALESCE(weight_kg, 0) - ?) < 0.01`,
		k.Name, string(k.Equipment), string(k.LoadBasis), loggedAt, sessionID, todayW).Scan(&bestRepsAtWeight); err != nil {
		return err
	}

	if bestW.Valid && todayW > bestW.Float64 {
		e.IsWeightPR = true
	}

	// A rep PR needs three things: the weight has been handled before, the
	// reps beat the best at that same weight, and the weight is near his
	// current best. Without the last condition, adding reps to a warmup-weight
	// backoff set reads as progress.
	nearTopWeight := !bestW.Valid || bestW.Float64 <= 0 || todayW >= bestW.Float64*repPRWeightFloor
	if !e.IsWeightPR && bestRepsAtWeight.Valid && todayR > int(bestRepsAtWeight.Int64) &&
		todayR > 0 && nearTopWeight {
		e.IsRepPR = true
	}

	if prevW.Float64 > 0 && todayW > 0 {
		jump := (todayW - prevW.Float64) / prevW.Float64 * 100
		jump = math.Round(jump*10) / 10
		if jump > 0 {
			e.LoadJumpPct = &jump
		}
	}

	if prevDate.Valid {
		prevVol, err := b.volumeOnDate(ctx, k, prevDate.String, bodyweight)
		if err != nil {
			return err
		}
		if prevVol > 0 {
			e.VolumePreviousKg = &prevVol
		}
	}
	return nil
}

// volumeFor sums load times reps across counted sets for one lift in one
// session. Warmups are excluded by SetType.CountsTowardVolume.
func (b *Builder) volumeFor(ctx context.Context, sessionID int64, k model.LiftKey, bodyweight float64) (float64, error) {
	rows, err := b.st.DB().QueryContext(ctx, `
		SELECT set_type, COALESCE(weight_kg, 0), COALESCE(reps, reps_low, 0), load_basis
		FROM v_sets
		WHERE session_id = ? AND name = ? AND equipment = ? AND load_basis = ?`,
		sessionID, k.Name, string(k.Equipment), string(k.LoadBasis))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	return sumVolume(rows, bodyweight)
}

func (b *Builder) volumeOnDate(ctx context.Context, k model.LiftKey, date string, bodyweight float64) (float64, error) {
	rows, err := b.st.DB().QueryContext(ctx, `
		SELECT set_type, COALESCE(weight_kg, 0), COALESCE(reps, reps_low, 0), load_basis
		FROM v_sets
		WHERE local_date = ? AND name = ? AND equipment = ? AND load_basis = ?`,
		date, k.Name, string(k.Equipment), string(k.LoadBasis))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	return sumVolume(rows, bodyweight)
}

func sumVolume(rows *sql.Rows, bodyweight float64) (float64, error) {
	var total float64
	for rows.Next() {
		var st, basis string
		var w float64
		var r int
		if err := rows.Scan(&st, &w, &r, &basis); err != nil {
			return 0, err
		}
		if !model.SetType(st).CountsTowardVolume() {
			continue
		}
		set := model.Set{LoadBasis: model.LoadBasis(basis)}
		wv := w
		set.Weight = &wv
		kg := model.UnitKg
		set.Unit = &kg
		total += set.EffectiveLoadKg(bodyweight) * float64(r)
	}
	return math.Round(total), rows.Err()
}

// staleLifts finds lifts whose top working weight has not moved across enough
// appearances AND enough elapsed days. Requiring both is what makes it
// frequency-aware: a lift trained three times a week needs many more
// appearances than a weekly one before the calendar agrees it has stalled.
func (b *Builder) staleLifts(ctx context.Context, localDate string) ([]StaleLift, error) {
	rows, err := b.st.DB().QueryContext(ctx, `
		SELECT name, equipment, load_basis, local_date, MAX(COALESCE(weight_kg, 0)) AS top
		FROM v_sets
		WHERE set_type = 'working' AND local_date <= ?
		GROUP BY name, equipment, load_basis, local_date
		ORDER BY name, equipment, load_basis, local_date DESC`, localDate)
	if err != nil {
		return nil, fmt.Errorf("stale lifts: %w", err)
	}
	defer rows.Close()

	type acc struct {
		key       model.LiftKey
		topWeight float64
		sessions  int
		newest    time.Time
		oldest    time.Time
		closed    bool
	}
	accs := map[string]*acc{}
	var order []string

	for rows.Next() {
		var name, equip, basis, date string
		var top float64
		if err := rows.Scan(&name, &equip, &basis, &date, &top); err != nil {
			return nil, err
		}
		k := model.LiftKey{Name: name, Equipment: model.Equipment(equip), LoadBasis: model.LoadBasis(basis)}
		id := k.String()
		d, err := time.Parse("2006-01-02", date)
		if err != nil {
			continue
		}

		a, ok := accs[id]
		if !ok {
			a = &acc{key: k, topWeight: top, sessions: 1, newest: d, oldest: d}
			accs[id] = a
			order = append(order, id)
			continue
		}
		if a.closed {
			continue
		}
		// Rows arrive newest first, so the streak ends the moment the top
		// weight differs from the most recent one.
		if top != a.topWeight {
			a.closed = true
			continue
		}
		a.sessions++
		a.oldest = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []StaleLift
	for _, id := range order {
		a := accs[id]
		days := int(a.newest.Sub(a.oldest).Hours() / 24)
		if a.sessions >= b.StaleMinSessions && days >= b.StaleMinDays && a.topWeight > 0 {
			out = append(out, StaleLift{
				Name:        a.key.Name,
				Display:     a.key.Display(),
				TopWeightKg: a.topWeight,
				Sessions:    a.sessions,
				Days:        days,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Days > out[j].Days })
	return out, nil
}

// streaks reports consecutive sessions of clean-rep progression, which is the
// kind of progress a weight-only view misses entirely.
func (b *Builder) streaks(ctx context.Context, sessionID int64) ([]string, error) {
	rows, err := b.st.DB().QueryContext(ctx, `
		SELECT DISTINCT name, equipment, load_basis FROM v_sets WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []model.LiftKey
	for rows.Next() {
		var n, e, l string
		if err := rows.Scan(&n, &e, &l); err != nil {
			return nil, err
		}
		keys = append(keys, model.LiftKey{Name: n, Equipment: model.Equipment(e), LoadBasis: model.LoadBasis(l)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []string
	for _, k := range keys {
		n, err := b.cleanRepStreak(ctx, k)
		if err != nil {
			return nil, err
		}
		if n >= 3 {
			out = append(out, fmt.Sprintf("%s: %d sessions of clean-rep progression", k.Display(), n))
		}
	}
	return out, nil
}

func (b *Builder) cleanRepStreak(ctx context.Context, k model.LiftKey) (int, error) {
	rows, err := b.st.DB().QueryContext(ctx, `
		SELECT local_date, SUM(COALESCE(clean_reps, 0)) AS clean
		FROM v_sets
		WHERE name = ? AND equipment = ? AND load_basis = ? AND set_type = 'working'
		GROUP BY local_date
		ORDER BY local_date DESC
		LIMIT 10`, k.Name, string(k.Equipment), string(k.LoadBasis))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var vals []float64
	for rows.Next() {
		var d string
		var c float64
		if err := rows.Scan(&d, &c); err != nil {
			return 0, err
		}
		vals = append(vals, c)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	streak := 0
	for i := 0; i+1 < len(vals); i++ {
		if vals[i] > vals[i+1] && vals[i] > 0 {
			streak++
			continue
		}
		break
	}
	if streak > 0 {
		streak++ // the streak includes the session that started it
	}
	return streak, nil
}

func formatSet(weightKg float64, reps int, basis model.LoadBasis) string {
	if basis == model.BasisBW || weightKg == 0 {
		return fmt.Sprintf("bodyweight x %d", reps)
	}
	suffix := ""
	switch basis {
	case model.BasisPerSide:
		suffix = " per side"
	case model.BasisPerLimb:
		suffix = " per leg"
	case model.BasisBWPlus:
		suffix = " added"
	}
	return fmt.Sprintf("%.4g kg%s x %d", weightKg, suffix, reps)
}
