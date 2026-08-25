package muscle

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
	st, err := store.Open(filepath.Join(t.TempDir(), "muscle.db"), time.UTC)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
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

func logDay(t *testing.T, st *store.Store, date string, ex ...model.Exercise) {
	t.Helper()
	p := &model.ParsedSession{Exercises: ex, RawText: "test", LoggedAt: day(t, date)}
	if _, err := st.Persist(context.Background(), p); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

func lift(name string, eq model.Equipment, nSets int) model.Exercise {
	var sets []model.Set
	for i := 0; i < nSets; i++ {
		sets = append(sets, model.Set{
			SetType: model.SetWorking, LoadBasis: model.BasisTotal,
			Weight: f(100), Unit: up(model.UnitKg), Reps: ip(5),
		})
	}
	return model.Exercise{Name: name, RawName: name, Equipment: eq, Sets: sets}
}

func volumeOf(rep Report, g Group) Volume {
	for _, v := range rep.Volumes {
		if v.Group == g {
			return v
		}
	}
	return Volume{}
}

// TestWindowCreditsPrimaryAndSecondary walks a realistic push day through the
// whole chain: parser names, equipment, resolution, and the weighted split.
func TestWindowCreditsPrimaryAndSecondary(t *testing.T) {
	st := newStore(t)
	logDay(t, st, "2026-08-24",
		lift("bench press", model.EquipBarbell, 4),
		lift("overhead press", model.EquipBarbell, 3))

	rep, err := Window(context.Background(), st, day(t, "2026-08-24"), day(t, "2026-08-24"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalSets != 7 {
		t.Fatalf("total sets = %d, want 7", rep.TotalSets)
	}
	if len(rep.Unmatched) != 0 {
		t.Fatalf("both lifts should resolve, unmatched: %v", rep.Unmatched)
	}

	// Bench is 4 sets of chest at full weight.
	if got := volumeOf(rep, Chest).Sets; got != 4 {
		t.Errorf("chest = %.1f, want 4", got)
	}
	// Shoulders: 3 primary from the press, plus 4 secondary from the bench at
	// half. Triceps: half of both.
	if got := volumeOf(rep, Shoulders).Sets; got != 3+4*secondaryWeight {
		t.Errorf("shoulders = %.1f, want %.1f", got, 3+4*secondaryWeight)
	}
	if got := volumeOf(rep, Triceps).Sets; got != (4+3)*secondaryWeight {
		t.Errorf("triceps = %.1f, want %.1f", got, (4+3)*secondaryWeight)
	}
	// Legs untouched, and reported at zero rather than omitted.
	if got := volumeOf(rep, Quads).Sets; got != 0 {
		t.Errorf("quads = %.1f, want 0", got)
	}
	if len(rep.Volumes) != len(Groups) {
		t.Errorf("every group should be reported, got %d of %d", len(rep.Volumes), len(Groups))
	}
}

// TestUnmatchedIsReportedNotSwallowed: volume credited to nothing is invisible
// in the figures, so the report has to admit which exercises it could not map.
func TestUnmatchedIsReportedNotSwallowed(t *testing.T) {
	st := newStore(t)
	logDay(t, st, "2026-08-24",
		lift("bench press", model.EquipBarbell, 3),
		lift("zercher jefferson curl of doom", model.EquipBarbell, 2))

	rep, err := Window(context.Background(), st, day(t, "2026-08-24"), day(t, "2026-08-24"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalSets != 5 {
		t.Errorf("total should count every logged set including unmapped, got %d", rep.TotalSets)
	}
	if len(rep.Unmatched) != 1 || rep.Unmatched[0] != "zercher jefferson curl of doom" {
		t.Errorf("expected the unknown lift to be named, got %v", rep.Unmatched)
	}
}

// TestNeglectedNamesTheLongestGap covers the line under the body figures.
func TestNeglectedNamesTheLongestGap(t *testing.T) {
	st := newStore(t)
	// Everything but legs, recently.
	logDay(t, st, "2026-08-20",
		lift("bench press", model.EquipBarbell, 3),
		lift("barbell row", model.EquipBarbell, 3),
		lift("overhead press", model.EquipBarbell, 3),
		lift("bicep curl", model.EquipBarbell, 3))

	rep, err := Window(context.Background(), st, day(t, "2026-08-14"), day(t, "2026-08-20"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Neglected == "" {
		t.Fatal("expected a neglected group with legs untrained")
	}
	switch rep.Neglected {
	case "QUADS", "HAMSTRINGS", "GLUTES":
	default:
		t.Errorf("neglected = %q, expected an untrained leg group", rep.Neglected)
	}
}

// TestWeeklyMedianIgnoresOneBuriedGroup is the property that makes a median the
// right choice for V_volume: hammering one muscle must not read as high overall
// work capacity, which is exactly what the term exists to not reward.
func TestWeeklyMedianIgnoresOneBuriedGroup(t *testing.T) {
	ctx := context.Background()

	lopsided := newStore(t)
	for _, d := range []string{"2026-08-18", "2026-08-20", "2026-08-22"} {
		logDay(t, lopsided, d, lift("bicep curl", model.EquipBarbell, 15))
	}
	lop, err := WeeklyMedian(ctx, lopsided, day(t, "2026-08-24"), 1)
	if err != nil {
		t.Fatal(err)
	}

	balanced := newStore(t)
	for _, d := range []string{"2026-08-18", "2026-08-20", "2026-08-22"} {
		logDay(t, balanced, d,
			lift("bench press", model.EquipBarbell, 4),
			lift("barbell row", model.EquipBarbell, 4),
			lift("squat", model.EquipBarbell, 4),
			lift("romanian deadlift", model.EquipBarbell, 3),
			lift("overhead press", model.EquipBarbell, 3))
	}
	bal, err := WeeklyMedian(ctx, balanced, day(t, "2026-08-24"), 1)
	if err != nil {
		t.Fatal(err)
	}

	if lop >= bal {
		t.Errorf("45 sets of curls (%.1f) must not out-score a balanced week (%.1f)", lop, bal)
	}
	t.Logf("lopsided median %.2f, balanced median %.2f", lop, bal)
}

// TestWarmupsAreExcluded holds the same rule the rest of the app follows.
func TestWarmupsAreExcluded(t *testing.T) {
	st := newStore(t)
	ex := model.Exercise{
		Name: "bench press", RawName: "bench press", Equipment: model.EquipBarbell,
		Sets: []model.Set{
			{SetType: model.SetWarmup, LoadBasis: model.BasisTotal, Weight: f(40), Unit: up(model.UnitKg), Reps: ip(10)},
			{SetType: model.SetWarmup, LoadBasis: model.BasisTotal, Weight: f(60), Unit: up(model.UnitKg), Reps: ip(5)},
			{SetType: model.SetWorking, LoadBasis: model.BasisTotal, Weight: f(100), Unit: up(model.UnitKg), Reps: ip(5)},
		},
	}
	logDay(t, st, "2026-08-24", ex)

	rep, err := Window(context.Background(), st, day(t, "2026-08-24"), day(t, "2026-08-24"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalSets != 1 {
		t.Errorf("only the working set should count, got %d", rep.TotalSets)
	}
	if got := volumeOf(rep, Chest).Sets; got != 1 {
		t.Errorf("chest = %.1f, want 1", got)
	}
}

// TestEmptyWindowIsSafe: no training is a valid state, not an error.
func TestEmptyWindowIsSafe(t *testing.T) {
	st := newStore(t)
	rep, err := Window(context.Background(), st, day(t, "2026-08-01"), day(t, "2026-08-07"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalSets != 0 || len(rep.Volumes) != len(Groups) {
		t.Errorf("expected an empty but complete report, got %d sets and %d groups",
			rep.TotalSets, len(rep.Volumes))
	}
	m, err := WeeklyMedian(context.Background(), st, day(t, "2026-08-07"), 4)
	if err != nil {
		t.Fatal(err)
	}
	if m != 0 {
		t.Errorf("median over an empty log should be 0, got %.2f", m)
	}
}

// TestNeglectedIsSilentWhenNothingIsNeglected guards the bug this line had: it
// used to name whichever major group was trained least recently, even when that
// group had plenty of volume, producing a UI line that read "CHEST - 0 SETS IN
// 5 D" for a chest that had seen seven sets that week.
func TestNeglectedIsSilentWhenNothingIsNeglected(t *testing.T) {
	st := newStore(t)
	// Every major group gets work across the window.
	logDay(t, st, "2026-08-19",
		lift("bench press", model.EquipBarbell, 4),
		lift("overhead press", model.EquipBarbell, 3))
	logDay(t, st, "2026-08-21",
		lift("squat", model.EquipBarbell, 4),
		lift("romanian deadlift", model.EquipBarbell, 3))
	logDay(t, st, "2026-08-23",
		lift("barbell row", model.EquipBarbell, 4),
		lift("bicep curl", model.EquipBarbell, 3),
		lift("tricep pushdown", model.EquipCable, 3))

	rep, err := Window(context.Background(), st, day(t, "2026-08-18"), day(t, "2026-08-24"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range Major {
		if volumeOf(rep, g).Sets == 0 {
			t.Fatalf("test setup: %s got no work, so the case is not exercised", g)
		}
	}
	if rep.Neglected != "" {
		t.Errorf("nothing was neglected, but the report named %q", rep.Neglected)
	}
}

// TestNeglectedOnlyNamesUntrainedGroups: whatever is named must genuinely have
// zero volume in the window, or the sentence the UI builds from it is false.
func TestNeglectedOnlyNamesUntrainedGroups(t *testing.T) {
	st := newStore(t)
	logDay(t, st, "2026-08-19", lift("bench press", model.EquipBarbell, 5))
	logDay(t, st, "2026-08-22", lift("barbell row", model.EquipBarbell, 5))

	rep, err := Window(context.Background(), st, day(t, "2026-08-18"), day(t, "2026-08-24"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Neglected == "" {
		t.Fatal("legs were untrained, expected a neglected group")
	}
	for _, v := range rep.Volumes {
		if v.Name == rep.Neglected && v.Sets != 0 {
			t.Errorf("neglected names %s, which has %.1f sets in the window",
				rep.Neglected, v.Sets)
		}
	}
}
