package rank

import (
	"context"
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
	st, err := store.Open(filepath.Join(t.TempDir(), "rank.db"), time.UTC)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	st.SetSetting(context.Background(), "bodyweight_kg", "80")
	return st
}

func logDay(t *testing.T, st *store.Store, day string, exercises ...model.Exercise) {
	t.Helper()
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	p := &model.ParsedSession{
		Exercises: exercises,
		RawText:   "test",
		LoggedAt:  time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, time.UTC),
	}
	if _, err := st.Persist(context.Background(), p); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

func lift(name string, equip model.Equipment, weight float64, nSets, reps int) model.Exercise {
	var sets []model.Set
	for i := 0; i < nSets; i++ {
		sets = append(sets, model.Set{
			SetType: model.SetWorking, LoadBasis: model.BasisTotal,
			Weight: f(weight), Unit: up(model.UnitKg), Reps: ip(reps),
		})
	}
	return model.Exercise{Name: name, RawName: name, Equipment: equip, Sets: sets}
}

func TestTierLadderCoversTheRange(t *testing.T) {
	seen := map[string]bool{}
	for s := 0.0; s <= 100; s += 0.5 {
		_, name, _, _ := tierFor(s)
		seen[name] = true
	}
	if len(seen) != len(tiers)*divisionsPerTier {
		t.Fatalf("expected %d distinct ranks, saw %d", len(tiers)*divisionsPerTier, len(seen))
	}

	_, low, _, _ := tierFor(0)
	if low != "Iron III" {
		t.Fatalf("bottom of the ladder should be Iron III, got %q", low)
	}
	_, high, next, _ := tierFor(100)
	if high != "Mythic I" || next != "" {
		t.Fatalf("top of the ladder should be Mythic I with no next, got %q / %q", high, next)
	}
}

// Junk volume must not move the rank. Twenty token sets of an empty bar should
// score no better than the same days trained properly.
func TestJunkVolumeDoesNotInflateRank(t *testing.T) {
	st := newStore(t)
	// Below MinWorkingSets, so these days do not qualify at all.
	for _, d := range []string{"2026-08-01", "2026-08-02", "2026-08-03"} {
		logDay(t, st, d, lift("lateral raise", model.EquipDumbbell, 5, 2, 20))
	}

	c := NewCalculator(st)
	r, err := c.Compute(context.Background(), time.Date(2026, 8, 3, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if r.Detail.QualifyingDays28 != 0 {
		t.Fatalf("two-set days must not qualify, got %d", r.Detail.QualifyingDays28)
	}
	if r.Consistency > 0 {
		t.Fatalf("token sessions should earn no consistency, got %v", r.Consistency)
	}
}

// A deload week must not crater the score, because the strength half reads the
// best estimate in the window rather than the most recent one.
func TestDeloadDoesNotCraterRank(t *testing.T) {
	st := newStore(t)
	logDay(t, st, "2026-07-01", lift("bench press", model.EquipBarbell, 100, 3, 5))
	c := NewCalculator(st)

	before, err := c.Compute(context.Background(), time.Date(2026, 7, 1, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	// A light week afterwards.
	logDay(t, st, "2026-07-08", lift("bench press", model.EquipBarbell, 60, 3, 5))
	after, err := c.Compute(context.Background(), time.Date(2026, 7, 8, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	if after.Strength < before.Strength {
		t.Fatalf("a deload dropped the strength score from %v to %v", before.Strength, after.Strength)
	}
}

// One strong lift should not read as elite. The coverage guard scales the
// strength half until several tracked lifts have data.
func TestCoverageGuardScalesSingleLift(t *testing.T) {
	st := newStore(t)
	logDay(t, st, "2026-08-01", lift("bench press", model.EquipBarbell, 140, 3, 3))

	c := NewCalculator(st)
	one, err := c.Compute(context.Background(), time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	st2 := newStore(t)
	logDay(t, st2, "2026-08-01",
		lift("bench press", model.EquipBarbell, 140, 3, 3),
		lift("squat", model.EquipBarbell, 180, 3, 3),
		lift("deadlift", model.EquipBarbell, 220, 3, 3),
	)
	three, err := NewCalculator(st2).Compute(context.Background(), time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	if one.Strength >= three.Strength {
		t.Fatalf("one lift (%v) should score below three comparable lifts (%v)", one.Strength, three.Strength)
	}
	if len(one.Detail.Notes) == 0 {
		t.Fatal("the user should be told why strength is scaled down")
	}
}

// Not training has to cost something, otherwise the rank creates no pull.
func TestInactivityDecaysConsistency(t *testing.T) {
	st := newStore(t)
	for _, d := range []string{"2026-07-01", "2026-07-02", "2026-07-04", "2026-07-06"} {
		logDay(t, st, d, lift("bench press", model.EquipBarbell, 100, 8, 5))
	}
	c := NewCalculator(st)

	fresh, err := c.Compute(context.Background(), time.Date(2026, 7, 6, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	stale, err := c.Compute(context.Background(), time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if stale.Consistency >= fresh.Consistency {
		t.Fatalf("three weeks off should lower consistency: %v then %v", fresh.Consistency, stale.Consistency)
	}
}

// Training every single day should not beat training five days with a rest.
func TestRestDisciplineIsNotPunishedRelativeToGrinding(t *testing.T) {
	everyDay := newStore(t)
	for d := 1; d <= 28; d++ {
		logDay(t, everyDay, time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
			lift("bench press", model.EquipBarbell, 100, 8, 5))
	}
	sensible := newStore(t)
	for d := 1; d <= 28; d++ {
		if d%7 == 0 || d%7 == 6 {
			continue // two rest days a week
		}
		logDay(t, sensible, time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
			lift("bench press", model.EquipBarbell, 100, 8, 5))
	}

	asOf := time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC)
	grind, err := NewCalculator(everyDay).Compute(context.Background(), asOf)
	if err != nil {
		t.Fatal(err)
	}
	rested, err := NewCalculator(sensible).Compute(context.Background(), asOf)
	if err != nil {
		t.Fatal(err)
	}

	if grind.Consistency > rested.Consistency {
		t.Fatalf("grinding every day (%v) should not outscore a sane schedule (%v)",
			grind.Consistency, rested.Consistency)
	}
}

func TestSnapshotAndDelta(t *testing.T) {
	st := newStore(t)
	c := NewCalculator(st)

	day1 := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	logDay(t, st, "2026-08-01", lift("bench press", model.EquipBarbell, 100, 8, 5))
	r1, err := c.Compute(context.Background(), day1)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(context.Background(), day1, r1); err != nil {
		t.Fatalf("save: %v", err)
	}

	day2 := time.Date(2026, 8, 2, 18, 0, 0, 0, time.UTC)
	logDay(t, st, "2026-08-02",
		lift("bench press", model.EquipBarbell, 120, 8, 5),
		lift("squat", model.EquipBarbell, 160, 8, 5),
		lift("deadlift", model.EquipBarbell, 200, 8, 5),
	)
	r2, err := c.Compute(context.Background(), day2)
	if err != nil {
		t.Fatal(err)
	}
	delta, err := c.Delta(context.Background(), day2, r2)
	if err != nil {
		t.Fatalf("delta: %v", err)
	}
	if r2.TierIndex > r1.TierIndex && delta == "" {
		t.Fatal("a tier promotion should produce a delta line")
	}
}
