package berserk

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrcha/gymlogger/internal/model"
	"github.com/mrcha/gymlogger/internal/store"
)

func f(v float64) *float64        { return &v }
func ip(v int) *int               { return &v }
func up(v model.Unit) *model.Unit { return &v }

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "berserk.db"), time.UTC)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	st.SetSetting(ctx, "bodyweight_kg", "80")
	st.SetSetting(ctx, "height_cm", "178")
	st.SetSetting(ctx, "bodyfat_pct", "12.5")
	st.SetSetting(ctx, "bodyfat_source", "caliper")
	return st
}

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d.Add(12 * time.Hour)
}

func logDay(t *testing.T, st *store.Store, date string, exercises ...model.Exercise) {
	t.Helper()
	p := &model.ParsedSession{
		Exercises: exercises,
		RawText:   "test",
		LoggedAt:  day(t, date),
	}
	if _, err := st.Persist(context.Background(), p); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

func lift(name string, equip model.Equipment, basis model.LoadBasis, weight float64, nSets, reps int) model.Exercise {
	var sets []model.Set
	for i := 0; i < nSets; i++ {
		sets = append(sets, model.Set{
			SetType: model.SetWorking, LoadBasis: basis,
			Weight: f(weight), Unit: up(model.UnitKg), Reps: ip(reps),
		})
	}
	return model.Exercise{Name: name, RawName: name, Equipment: equip, Sets: sets}
}

// TestEmptyDatabaseProducesAValidRank is the end-to-end form of Erratum 3. A
// brand-new account has no tested patterns, nothing to impute from, and every
// pattern score at zero. Before the floor this produced MIGHT = NaN and an
// undefined rank, and the failure surfaced to the user.
func TestEmptyDatabaseProducesAValidRank(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)

	r, err := c.Compute(context.Background(), day(t, "2026-08-25"))
	if err != nil {
		t.Fatalf("compute on an empty database: %v", err)
	}
	for name, v := range map[string]float64{
		"RS": r.RS, "MIGHT": r.Attributes.Might, "DOMINION": r.Attributes.Dominion,
		"FRAME": r.Attributes.Frame, "VIGOR": r.Attributes.Vigor,
		"DISCIPLINE": r.Attributes.Discipline, "MASTERY": r.Attributes.Mastery,
	} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("%s = %v on an empty database", name, v)
		}
	}
	// The rank sits at the bottom of the ladder but is not necessarily
	// COMMONER: FRAME scores from body composition alone, so a lifter with a
	// recorded height and weight and no logged sets still carries a few RS.
	// That is the formulas being consistent, not a leak -- confidence is low,
	// every pattern reads untested, and nothing above BLOODED is reachable
	// without proven patterns.
	if r.RankIndex > 3 {
		t.Errorf("an empty database should rank near the bottom, got %s", r.Rank)
	}
	if r.Confidence > 0.5 {
		t.Errorf("an empty database must report low confidence, got %.2f", r.Confidence)
	}
	// The true zeros must survive to the UI as "untested" (Erratum 3).
	for _, p := range r.Patterns {
		if p.Score != 0 || p.Status != Unproven {
			t.Errorf("%s should be an untested zero, got %.1f/%s", p.Pattern, p.Score, p.Status)
		}
	}
}

// TestProvenThenVerified walks the v1.2 Patch 5 evidence ladder.
func TestProvenThenVerified(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)
	ctx := context.Background()

	logDay(t, st, "2026-08-01", lift("bench press", model.EquipBarbell, model.BasisTotal, 100, 3, 5))
	r, err := c.Compute(ctx, day(t, "2026-08-02"))
	if err != nil {
		t.Fatal(err)
	}
	if got := findPattern(r, HPress); got.Status != Proven {
		t.Fatalf("one qualifying set should be PROVEN, got %s", got.Status)
	}

	// A second set inside the fourteen-day window is still only PROVEN: the
	// spacing is what makes verification cost a month rather than an afternoon.
	logDay(t, st, "2026-08-10", lift("bench press", model.EquipBarbell, model.BasisTotal, 102.5, 3, 5))
	r, _ = c.Compute(ctx, day(t, "2026-08-11"))
	if got := findPattern(r, HPress); got.Status != Proven {
		t.Fatalf("sets 9 days apart must not verify, got %s", got.Status)
	}

	logDay(t, st, "2026-08-16", lift("bench press", model.EquipBarbell, model.BasisTotal, 105, 3, 5))
	r, _ = c.Compute(ctx, day(t, "2026-08-17"))
	if got := findPattern(r, HPress); got.Status != Verified {
		t.Fatalf("sets 15 days apart should VERIFY, got %s", got.Status)
	}
}

// TestUntestedPatternsAreImputedAndFlagged covers v1.0 4.3: imputation lets a
// beginner rank without owning a full gym, and always drags the harmonic mean
// down so there is a standing reason to go prove it.
func TestUntestedPatternsAreImputedAndFlagged(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)

	logDay(t, st, "2026-08-01",
		lift("bench press", model.EquipBarbell, model.BasisTotal, 100, 3, 5),
		lift("squat", model.EquipBarbell, model.BasisTotal, 140, 3, 5))

	r, err := c.Compute(context.Background(), day(t, "2026-08-02"))
	if err != nil {
		t.Fatal(err)
	}

	var tested, imputed []PatternScore
	for _, p := range r.Patterns {
		if p.Imputed {
			imputed = append(imputed, p)
		} else {
			tested = append(tested, p)
		}
	}
	if len(tested) != 2 || len(imputed) != 4 {
		t.Fatalf("expected 2 tested and 4 imputed, got %d/%d", len(tested), len(imputed))
	}

	mean := (tested[0].Score + tested[1].Score) / 2
	for _, p := range imputed {
		if math.Abs(p.Score-round1(imputationFactor*mean)) > 0.15 {
			t.Errorf("%s imputed at %.1f, want 0.75 x %.1f", p.Pattern, p.Score, mean)
		}
	}
	// The imputed value must sit below the tested mean, or it would not drag.
	if imputed[0].Score >= mean {
		t.Errorf("imputation must drag the mean down: %.1f against %.1f", imputed[0].Score, mean)
	}
	if len(r.Notes) == 0 {
		t.Error("untested patterns must be explained in the notes")
	}
}

// TestWeeklyBloodUsesRollingMondayBoundaries is Erratum 5's instruction not to
// hardcode a monthly figure. August 2026 contains five Mondays, so a lifter who
// trains every week that month earns five awards, not 4.33.
func TestWeeklyBloodUsesRollingMondayBoundaries(t *testing.T) {
	st := newStore(t)
	l := NewLedger(st)
	ctx := context.Background()

	// Mondays in August 2026: 3, 10, 17, 24, 31. Train three days in each of
	// the five weeks beginning on those Mondays.
	for _, monday := range []string{"2026-08-03", "2026-08-10", "2026-08-17", "2026-08-24", "2026-08-31"} {
		start := day(t, monday)
		for i := 0; i < 3; i++ {
			d := start.AddDate(0, 0, i).Format("2006-01-02")
			logDay(t, st, d, lift("squat", model.EquipBarbell, model.BasisTotal, 100, 3, 5))
		}
	}

	// Evaluated after the last of those weeks has closed.
	if err := l.AwardWeeks(ctx, day(t, "2026-09-08"), 8); err != nil {
		t.Fatal(err)
	}
	b, err := l.Total(ctx, day(t, "2026-09-08"))
	if err != nil {
		t.Fatal(err)
	}
	want := 5 * BloodQualifyingWeek
	if b.Total != want {
		t.Fatalf("five qualifying weeks should pay %.0f Blood, got %.0f", want, b.Total)
	}

	// Idempotent: a nightly recompute must not pay a week twice.
	if err := l.AwardWeeks(ctx, day(t, "2026-09-08"), 8); err != nil {
		t.Fatal(err)
	}
	b2, _ := l.Total(ctx, day(t, "2026-09-08"))
	if b2.Total != want {
		t.Fatalf("re-running the award changed the total: %.0f then %.0f", b.Total, b2.Total)
	}
}

// TestIncompleteWeekIsNotPaid guards the one case the dedup key cannot catch:
// paying a week in progress and paying it again when it closes would be the
// same key for a different amount of work.
func TestIncompleteWeekIsNotPaid(t *testing.T) {
	st := newStore(t)
	l := NewLedger(st)
	ctx := context.Background()

	for _, d := range []string{"2026-08-24", "2026-08-25", "2026-08-26"} {
		logDay(t, st, d, lift("squat", model.EquipBarbell, model.BasisTotal, 100, 3, 5))
	}
	// Wednesday of that same week.
	if err := l.AwardWeeks(ctx, day(t, "2026-08-26"), 8); err != nil {
		t.Fatal(err)
	}
	b, _ := l.Total(ctx, day(t, "2026-08-26"))
	if b.Total != 0 {
		t.Fatalf("an in-progress week must not pay, got %.0f Blood", b.Total)
	}

	// Once it closes, it pays exactly once.
	if err := l.AwardWeeks(ctx, day(t, "2026-09-01"), 8); err != nil {
		t.Fatal(err)
	}
	b, _ = l.Total(ctx, day(t, "2026-09-01"))
	if b.Total != BloodQualifyingWeek {
		t.Fatalf("the closed week should pay %.0f, got %.0f", BloodQualifyingWeek, b.Total)
	}
}

// TestWeekBelowThreeSessionsDoesNotPay checks the qualifying bar.
func TestWeekBelowThreeSessionsDoesNotPay(t *testing.T) {
	st := newStore(t)
	l := NewLedger(st)
	ctx := context.Background()

	logDay(t, st, "2026-08-03", lift("squat", model.EquipBarbell, model.BasisTotal, 100, 3, 5))
	logDay(t, st, "2026-08-05", lift("squat", model.EquipBarbell, model.BasisTotal, 100, 3, 5))
	if err := l.AwardWeeks(ctx, day(t, "2026-08-17"), 4); err != nil {
		t.Fatal(err)
	}
	b, _ := l.Total(ctx, day(t, "2026-08-17"))
	if b.Total != 0 {
		t.Fatalf("two sessions is not a qualifying week, got %.0f Blood", b.Total)
	}
}

// TestCuttingRaisesRankWithoutTouchingTheBar is the behaviour v1.0 9 calls the
// single most important in the whole document. Lean mass is held constant while
// bodyweight falls, so the strength references do not move and MIGHT is
// untouched, while DOMINION and FRAME both climb.
func TestCuttingRaisesRankWithoutTouchingTheBar(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)
	ctx := context.Background()

	// A fixed set of lifts, logged once, never changed.
	logDay(t, st, "2026-08-01",
		lift("bench press", model.EquipBarbell, model.BasisTotal, 100, 3, 5),
		lift("squat", model.EquipBarbell, model.BasisTotal, 140, 3, 5),
		lift("deadlift", model.EquipBarbell, model.BasisTotal, 170, 3, 5),
		lift("overhead press", model.EquipBarbell, model.BasisTotal, 60, 3, 5),
		lift("barbell row", model.EquipBarbell, model.BasisTotal, 85, 3, 5),
		lift("pull-up", model.EquipBodyweight, model.BasisBWPlus, 10, 3, 5))

	// Heavy: 100 kg at 28% body fat, LBM 72.
	st.SetSetting(ctx, "bodyweight_kg", "100")
	st.SetSetting(ctx, "bodyfat_pct", "28")
	before, err := c.Compute(ctx, day(t, "2026-08-02"))
	if err != nil {
		t.Fatal(err)
	}

	// Lean: 85 kg at 15% body fat, LBM 72.25. Same lean mass, same bar.
	st.SetSetting(ctx, "bodyweight_kg", "85")
	st.SetSetting(ctx, "bodyfat_pct", "15")
	after, err := c.Compute(ctx, day(t, "2026-08-02"))
	if err != nil {
		t.Fatal(err)
	}

	// v1.0 9 says MIGHT holds across a cut that holds the lifts, and it very
	// nearly does: five of the six references are LBM-derived and do not move.
	// The vertical pull is the exception, because v1.0 3.3 scores it on total
	// system load, so a lifter 15 kg lighter with the same belt weight is
	// genuinely moving less. That is the same mechanism that pays the cut out
	// through DOMINION, and it costs about two points of MIGHT here. The prose
	// says "holds"; the arithmetic says "holds except on the one pattern where
	// bodyweight is the load", and the arithmetic is right.
	if d := math.Abs(after.Attributes.Might - before.Attributes.Might); d > 2.5 {
		t.Errorf("MIGHT moved %.2f across a cut that held lean mass; only the vertical pull should move", d)
	}
	if after.Attributes.Dominion <= before.Attributes.Dominion {
		t.Errorf("DOMINION should rise on a cut: %.1f then %.1f",
			before.Attributes.Dominion, after.Attributes.Dominion)
	}
	if after.Attributes.Frame <= before.Attributes.Frame {
		t.Errorf("FRAME should rise on a cut: %.1f then %.1f",
			before.Attributes.Frame, after.Attributes.Frame)
	}
	if after.RS <= before.RS {
		t.Errorf("RS should rise on a cut that held the lifts: %.1f then %.1f", before.RS, after.RS)
	}
}

// TestSnapshotRoundTrip checks the persistence hysteresis depends on.
func TestSnapshotRoundTrip(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)
	ctx := context.Background()

	logDay(t, st, "2026-08-01", lift("bench press", model.EquipBarbell, model.BasisTotal, 100, 3, 5))
	r, err := c.Compute(ctx, day(t, "2026-08-02"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(ctx, day(t, "2026-08-02"), r); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Saving twice for one day must update rather than fail.
	if err := c.Save(ctx, day(t, "2026-08-02"), r); err != nil {
		t.Fatalf("re-save: %v", err)
	}

	got, firstEver, err := c.currentRung(ctx, day(t, "2026-08-03"))
	if err != nil {
		t.Fatal(err)
	}
	if firstEver {
		t.Fatal("a saved snapshot means this is no longer the first computation")
	}
	if got.Index != r.RankIndex {
		t.Fatalf("round trip lost the rank: saved %d, read %d", r.RankIndex, got.Index)
	}

	var version string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT system_version FROM berserk_snapshots WHERE local_date = ?`,
		"2026-08-02").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != Version {
		t.Fatalf("every stored score must carry its constants version, got %q", version)
	}
}

// TestRecalibrationIsExplained covers v1.0 14.4/14.5: when the system version
// changes the user sees "recalibrated", never an unexplained rank move.
func TestRecalibrationIsExplained(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)
	ctx := context.Background()

	logDay(t, st, "2026-08-01", lift("bench press", model.EquipBarbell, model.BasisTotal, 100, 3, 5))
	r, _ := c.Compute(ctx, day(t, "2026-08-02"))
	if err := c.Save(ctx, day(t, "2026-08-02"), r); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE berserk_snapshots SET system_version = '1.2' WHERE local_date = ?`,
		"2026-08-02"); err != nil {
		t.Fatal(err)
	}

	delta, err := c.Delta(ctx, day(t, "2026-08-03"), r)
	if err != nil {
		t.Fatal(err)
	}
	if delta == "" || !contains(delta, "recalibrated") {
		t.Fatalf("a version change must be announced as a recalibration, got %q", delta)
	}
}

// TestThreatLevelDecaysButRankDoesNot is v1.1 29, which replaced the dormancy
// flag. Taking a rank away is how you get uninstalls.
func TestThreatLevelDecaysButRankDoesNot(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)
	ctx := context.Background()

	logDay(t, st, "2026-01-05", lift("bench press", model.EquipBarbell, model.BasisTotal, 100, 3, 5))

	fresh, err := c.threatLevel(ctx, day(t, "2026-01-20"))
	if err != nil {
		t.Fatal(err)
	}
	if fresh != 100 {
		t.Errorf("inside the 21-day grace the threat level should be 100, got %.1f", fresh)
	}

	stale, err := c.threatLevel(ctx, day(t, "2026-03-05"))
	if err != nil {
		t.Fatal(err)
	}
	if stale >= 100 || stale < 60 {
		t.Errorf("after two months idle the threat level should have decayed inside [60,100), got %.1f", stale)
	}

	// The floor holds however long the layoff runs.
	ancient, err := c.threatLevel(ctx, day(t, "2030-01-01"))
	if err != nil {
		t.Fatal(err)
	}
	if ancient != 60 {
		t.Errorf("the threat level floors at 60, got %.1f", ancient)
	}
}

// TestWeakLinkNamesAPatternAndANumber holds v1.0 14.3 to its own standard: the
// line has to name the lift and the load, not offer encouragement.
func TestWeakLinkNamesAPatternAndANumber(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)

	logDay(t, st, "2026-08-01",
		lift("bench press", model.EquipBarbell, model.BasisTotal, 110, 3, 5),
		lift("squat", model.EquipBarbell, model.BasisTotal, 150, 3, 5),
		lift("deadlift", model.EquipBarbell, model.BasisTotal, 180, 3, 5),
		lift("overhead press", model.EquipBarbell, model.BasisTotal, 65, 3, 5),
		lift("pull-up", model.EquipBodyweight, model.BasisBWPlus, 15, 3, 5),
		lift("barbell row", model.EquipBarbell, model.BasisTotal, 60, 3, 5))

	r, err := c.Compute(context.Background(), day(t, "2026-08-02"))
	if err != nil {
		t.Fatal(err)
	}
	if r.WeakLink == "" {
		t.Fatal("expected a weak-link line")
	}
	// The row is the obvious laggard here, and the line must say so.
	if !contains(r.WeakLink, "H-PULL") {
		t.Errorf("weak link should name the horizontal pull, got %q", r.WeakLink)
	}
	if !containsDigit(r.WeakLink) {
		t.Errorf("weak link must carry a number, got %q", r.WeakLink)
	}
}

// TestBlockedByUnprovenPatternIsSaidPlainly: no amount of load fixes an
// untested pattern, so the advice has to be "go log it", not "add weight".
func TestBlockedByUnprovenPatternIsSaidPlainly(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)

	logDay(t, st, "2026-08-01",
		lift("bench press", model.EquipBarbell, model.BasisTotal, 120, 3, 5),
		lift("squat", model.EquipBarbell, model.BasisTotal, 170, 3, 5))

	r, err := c.Compute(context.Background(), day(t, "2026-08-02"))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(r.WeakLink, "unproven") && !contains(r.WeakLink, "imputed") {
		t.Errorf("with four untested patterns the advice should be to prove one, got %q", r.WeakLink)
	}
}

// TestJunkVolumeDoesNotMoveTheRank preserves the original rank design
// constraint through the rewrite: high-rep work is volume, not strength.
func TestJunkVolumeDoesNotMoveTheRank(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)
	ctx := context.Background()

	logDay(t, st, "2026-08-01", lift("bench press", model.EquipBarbell, model.BasisTotal, 100, 3, 5))
	before, err := c.Compute(ctx, day(t, "2026-08-02"))
	if err != nil {
		t.Fatal(err)
	}

	// Twenty sets of twenty reps: real work, but it says nothing about maximal
	// strength and Epley wildly overestimates there.
	logDay(t, st, "2026-08-02", lift("bench press", model.EquipBarbell, model.BasisTotal, 60, 20, 20))
	after, err := c.Compute(ctx, day(t, "2026-08-03"))
	if err != nil {
		t.Fatal(err)
	}
	if after.Attributes.Might != before.Attributes.Might {
		t.Errorf("high-rep volume must not move MIGHT: %.2f then %.2f",
			before.Attributes.Might, after.Attributes.Might)
	}
}

func findPattern(r *Rank, p Pattern) PatternScore {
	for _, ps := range r.Patterns {
		if ps.Pattern == p {
			return ps
		}
	}
	return PatternScore{}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func containsDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// strongSession is a set of lifts that score around 85 against the reference
// lifter's LBM-70 references, i.e. right at the MIGHT gate.
func strongSession(pullupAdded float64) []model.Exercise {
	return []model.Exercise{
		lift("bench press", model.EquipBarbell, model.BasisTotal, 110, 3, 3),
		lift("overhead press", model.EquipBarbell, model.BasisTotal, 70, 3, 3),
		lift("squat", model.EquipBarbell, model.BasisTotal, 155, 3, 3),
		lift("deadlift", model.EquipBarbell, model.BasisTotal, 190, 3, 3),
		lift("pull-up", model.EquipBodyweight, model.BasisBWPlus, pullupAdded, 3, 3),
		lift("barbell row", model.EquipBarbell, model.BasisTotal, 95, 3, 3),
	}
}

// TestStrongLifterReachesTheGateReadout walks the whole engine on real rows: six
// patterns verified through the 14-day spacing rule, high attribute scores, and
// the Erratum 1 switch from a composite to a gate table.
func TestStrongLifterReachesTheGateReadout(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)
	ctx := context.Background()

	st.SetSetting(ctx, "bodyweight_kg", "85")
	st.SetSetting(ctx, "bodyfat_pct", "11")
	st.SetSetting(ctx, "bodyfat_source", "dexa")
	st.SetSetting(ctx, "training_months", "120")
	st.SetSetting(ctx, "vo2max_est", "48")

	// Two sessions 21 days apart verifies every pattern.
	logDay(t, st, "2026-07-01", strongSession(28)...)
	logDay(t, st, "2026-07-22", strongSession(30)...)

	r, err := c.Compute(ctx, day(t, "2026-07-23"))
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range r.Patterns {
		if p.Status != Verified {
			t.Errorf("%s should be VERIFIED after two sessions 21 days apart, got %s",
				p.Pattern, p.Status)
		}
		if p.Imputed {
			t.Errorf("%s should not be imputed", p.Pattern)
		}
	}

	if r.Attributes.Might < 75 {
		t.Errorf("these lifts should produce a high MIGHT, got %.1f", r.Attributes.Might)
	}
	// Six gates plus the two structural requirements are all reported, whether
	// or not they pass.
	if len(r.Berserk.Gates) != 6 {
		t.Fatalf("expected six gates, got %d", len(r.Berserk.Gates))
	}
	if r.Berserk.PatternsVerified != 6 {
		t.Errorf("expected 6 verified patterns, got %d", r.Berserk.PatternsVerified)
	}
	if r.Berserk.Summary == "" {
		t.Error("the gate readout must carry a summary line")
	}
	// Every failing gate must carry a computed instruction, never silence.
	for _, g := range r.Berserk.Gates {
		if !g.Pass && g.Fix == "" {
			t.Errorf("failing gate %s has no fix line", g.Name)
		}
	}
	t.Logf("rank=%s RS=%.1f showGates=%v verified=%d summary=%q",
		r.Rank, r.RS, r.ShowGates, r.Berserk.PatternsVerified, r.Berserk.Summary)
	for _, g := range r.Berserk.Gates {
		t.Logf("  %-11s %6.1f / %-3.0f pass=%v  %s", g.Name, g.Value, g.Threshold, g.Pass, g.Fix)
	}
}

// TestFirstGrantIsImmediate holds v1.1 21 against v1.2 Patch 3. The two rules
// conflict on day one: Patch 3 promotes only after ten consecutive qualifying
// days, and 21 says rank is current capability granted immediately from real
// numbers. Holding a lifter who walks in benching 110 kg at COMMONER for ten
// days is the onboarding insult 21 exists to prevent, so the first grant is
// exempt and the hold applies only to subsequent changes.
func TestFirstGrantIsImmediate(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)
	ctx := context.Background()

	st.SetSetting(ctx, "bodyweight_kg", "85")
	st.SetSetting(ctx, "bodyfat_pct", "11")
	st.SetSetting(ctx, "training_months", "120")
	st.SetSetting(ctx, "vo2max_est", "48")
	logDay(t, st, "2026-07-01", strongSession(28)...)
	logDay(t, st, "2026-07-22", strongSession(30)...)

	r, err := c.Compute(ctx, day(t, "2026-07-23"))
	if err != nil {
		t.Fatal(err)
	}
	if r.RankIndex != r.EligibleIndex {
		t.Fatalf("the first grant must equal the eligible rank, got %s (eligible %s)",
			r.Rank, RungByIndex(r.EligibleIndex).Name)
	}
	if r.RankIndex <= 1 {
		t.Fatalf("a strong lifter must not start at COMMONER, got %s", r.Rank)
	}

	// Once a snapshot exists the hold applies again, so a further promotion has
	// to be earned over ten days rather than granted on the spot.
	if err := c.Save(ctx, day(t, "2026-07-23"), r); err != nil {
		t.Fatal(err)
	}
	cur, firstEver, err := c.currentRung(ctx, day(t, "2026-07-24"))
	if err != nil {
		t.Fatal(err)
	}
	if firstEver {
		t.Fatal("expected the exemption to be spent after the first snapshot")
	}
	higher := RungByIndex(cur.Index + 1)
	if got := ApplyHysteresis(higher, 99, cur, 1, false); got.Index != cur.Index {
		t.Fatalf("a later promotion must still serve the ten-day hold, got %s", got.Name)
	}
}

// TestUnlockedSkillsDoNotDeadlock is a regression test for a bug that bricked
// the service permanently.
//
// The store caps the connection pool at one. AwardVerification used to INSERT a
// Blood award while its own "SELECT skill FROM skill_unlocks" cursor was still
// open, so the INSERT waited for a connection the SELECT was holding. It worked
// for as long as the table was empty -- rows.Next() returned false immediately
// and released the connection -- and hung forever the first time a user
// unlocked a skill. Every later request queued behind it.
//
// The test runs with a timeout because the failure mode is a hang, not an error:
// without the guard this blocks rather than fails, and a blocked test that never
// reports is nearly as bad as the bug.
func TestUnlockedSkillsDoNotDeadlock(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	// More than one skill, because a single row could still pass by accident if
	// the cursor happened to be released after the last Next.
	for _, sk := range []string{"pistol squat", "strict pull-up x10", "weighted dip"} {
		if err := st.SetSkill(ctx, sk, true); err != nil {
			t.Fatal(err)
		}
	}
	logDay(t, st, "2026-08-20", lift("bench press", model.EquipBarbell, model.BasisTotal, 100, 3, 5))

	c := NewCalculator(st)
	done := make(chan error, 1)
	go func() {
		_, err := c.Compute(ctx, day(t, "2026-08-21"))
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("compute with unlocked skills: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Compute deadlocked with skills unlocked; a write is running inside an open cursor")
	}

	// And the awards actually landed, so the fix did not simply skip them.
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blood_ledger WHERE source = 'skill'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected 3 skill awards, got %d", n)
	}
}
