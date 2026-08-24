package contextbuilder

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
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		loc = time.UTC
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"), loc)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// logSession inserts one exercise's worth of sets on a given day.
func logSession(t *testing.T, st *store.Store, day string, ex model.Exercise) int64 {
	t.Helper()
	when, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("bad day %q: %v", day, err)
	}
	// Noon local keeps the session on the intended calendar date regardless of
	// the timezone offset.
	when = time.Date(when.Year(), when.Month(), when.Day(), 12, 0, 0, 0, st.Location())

	st.SetSetting(context.Background(), "bodyweight_kg", "80")

	p := &model.ParsedSession{
		Exercises: []model.Exercise{ex},
		RawText:   "test",
		LoggedAt:  when,
	}
	id, err := st.Persist(context.Background(), p)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	return id
}

func barbellSets(weight float64, reps ...int) []model.Set {
	var out []model.Set
	for _, r := range reps {
		out = append(out, model.Set{
			SetType: model.SetWorking, LoadBasis: model.BasisTotal,
			Weight: f(weight), Unit: up(model.UnitKg), Reps: ip(r),
		})
	}
	return out
}

func bench(weight float64, reps ...int) model.Exercise {
	return model.Exercise{
		Name: "bench press", RawName: "bench", Equipment: model.EquipBarbell,
		Sets: barbellSets(weight, reps...),
	}
}

func TestFirstEverLiftIsBaseline(t *testing.T) {
	st := newStore(t)
	id := logSession(t, st, "2026-08-10", bench(100, 5))

	c, err := New(st).Build(context.Background(), id)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(c.LiftHistory) != 1 {
		t.Fatalf("expected one lift, got %d", len(c.LiftHistory))
	}
	if !c.LiftHistory[0].IsBaseline {
		t.Fatal("a lift with no history must be marked a baseline, not a PR")
	}
	if c.LiftHistory[0].IsWeightPR || c.LiftHistory[0].IsRepPR {
		t.Fatal("a baseline must never be reported as a PR")
	}
}

func TestWeightPR(t *testing.T) {
	st := newStore(t)
	logSession(t, st, "2026-08-01", bench(100, 3))
	id := logSession(t, st, "2026-08-08", bench(105, 2))

	c, _ := New(st).Build(context.Background(), id)
	got := c.LiftHistory[0]
	if !got.IsWeightPR {
		t.Fatal("105 kg after a best of 100 kg is a weight PR")
	}
	if got.IsRepPR {
		t.Fatal("a weight PR must not also claim a rep PR")
	}
}

func TestRepPRAtSameWeight(t *testing.T) {
	st := newStore(t)
	logSession(t, st, "2026-08-01", bench(100, 3))
	id := logSession(t, st, "2026-08-08", bench(100, 4))

	c, _ := New(st).Build(context.Background(), id)
	got := c.LiftHistory[0]
	if got.IsWeightPR {
		t.Fatal("the same weight is not a weight PR")
	}
	if !got.IsRepPR {
		t.Fatal("4 reps at a weight previously done for 3 is a rep PR")
	}
}

// More reps at a lighter weight is not a record. Without the weight guard the
// coach congratulates a deload.
func TestMoreRepsAtLighterWeightIsNotAPR(t *testing.T) {
	st := newStore(t)
	logSession(t, st, "2026-08-01", bench(100, 5))
	id := logSession(t, st, "2026-08-08", bench(70, 12))

	c, _ := New(st).Build(context.Background(), id)
	got := c.LiftHistory[0]
	if got.IsWeightPR || got.IsRepPR {
		t.Fatal("70 kg x 12 after 100 kg x 5 is not a PR of any kind")
	}
}

// The bug the whole LiftKey design exists to prevent: 30 kg per side on
// dumbbells is 60 kg of load, and must never be compared against a 30 kg
// total machine row as though they were the same lift.
func TestLoadBasisPreventsFakePR(t *testing.T) {
	st := newStore(t)
	logSession(t, st, "2026-08-01", model.Exercise{
		Name: "db row", RawName: "db row", Equipment: model.EquipDumbbell,
		Sets: []model.Set{{
			SetType: model.SetWorking, LoadBasis: model.BasisPerSide,
			Weight: f(30), Unit: up(model.UnitKg), Reps: ip(8),
		}},
	})
	id := logSession(t, st, "2026-08-08", model.Exercise{
		Name: "db row", RawName: "machine row", Equipment: model.EquipMachine,
		Sets: []model.Set{{
			SetType: model.SetWorking, LoadBasis: model.BasisTotal,
			Weight: f(40), Unit: up(model.UnitKg), Reps: ip(8),
		}},
	})

	c, _ := New(st).Build(context.Background(), id)
	if len(c.LiftHistory) != 1 {
		t.Fatalf("expected one lift today, got %d", len(c.LiftHistory))
	}
	if !c.LiftHistory[0].IsBaseline {
		t.Fatal("a different equipment and load basis is a different lift, so it must be a baseline")
	}
	if c.LiftHistory[0].IsWeightPR {
		t.Fatal("comparing 40 kg total against 30 kg per side produced a fake PR")
	}
}

// Warmup sets vary with sleep and mood. Counting them makes volume comparisons
// meaningless, which is what the original schema did by not having set_type.
func TestWarmupsExcludedFromVolume(t *testing.T) {
	st := newStore(t)
	ex := model.Exercise{
		Name: "bench press", RawName: "bench", Equipment: model.EquipBarbell,
		Sets: []model.Set{
			{SetType: model.SetWarmup, LoadBasis: model.BasisTotal,
				Weight: f(40), Unit: up(model.UnitKg), Reps: ip(10)},
			{SetType: model.SetWorking, LoadBasis: model.BasisTotal,
				Weight: f(100), Unit: up(model.UnitKg), Reps: ip(5)},
		},
	}
	id := logSession(t, st, "2026-08-10", ex)

	c, _ := New(st).Build(context.Background(), id)
	if got := c.LiftHistory[0].VolumeTodayKg; got != 500 {
		t.Fatalf("volume should be 100x5=500 with the warmup excluded, got %v", got)
	}
}

// The top set must be the heaviest working set, not the last one entered and
// not a warmup that happened to sort first.
func TestTopSetIgnoresWarmups(t *testing.T) {
	st := newStore(t)
	ex := model.Exercise{
		Name: "squat", RawName: "squat", Equipment: model.EquipBarbell,
		Sets: []model.Set{
			{SetType: model.SetWarmup, LoadBasis: model.BasisTotal,
				Weight: f(140), Unit: up(model.UnitKg), Reps: ip(1)},
			{SetType: model.SetWorking, LoadBasis: model.BasisTotal,
				Weight: f(120), Unit: up(model.UnitKg), Reps: ip(5)},
		},
	}
	id := logSession(t, st, "2026-08-10", ex)

	c, _ := New(st).Build(context.Background(), id)
	if got := c.LiftHistory[0].TopSetToday; got != "120 kg x 5" {
		t.Fatalf("top set should be the heaviest working set, got %q", got)
	}
}

func TestLoadJumpFlag(t *testing.T) {
	st := newStore(t)
	logSession(t, st, "2026-08-01", bench(100, 5))
	id := logSession(t, st, "2026-08-08", bench(120, 3))

	c, _ := New(st).Build(context.Background(), id)
	found := false
	for _, fl := range c.Flags {
		if fl.Type == FlagLoadJump {
			found = true
		}
	}
	if !found {
		t.Fatalf("a 20 percent jump on a compound should flag, got flags %+v", c.Flags)
	}
}

// Pain outranks everything. If it is not first, the coach will hype through
// an injury.
func TestPainOutranksOtherFlags(t *testing.T) {
	st := newStore(t)
	logSession(t, st, "2026-08-01", bench(100, 5))

	when := time.Date(2026, 8, 8, 12, 0, 0, 0, st.Location())
	pain := "left shoulder pinching on the last set"
	p := &model.ParsedSession{
		Exercises:  []model.Exercise{bench(120, 3)},
		Subjective: model.Subjective{Pain: &pain},
		RawText:    "test",
		LoggedAt:   when,
	}
	id, err := st.Persist(context.Background(), p)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	c, _ := New(st).Build(context.Background(), id)
	if len(c.Flags) == 0 {
		t.Fatal("expected flags")
	}
	if c.Flags[0].Type != FlagPain {
		t.Fatalf("pain must sort first, got %s", c.Flags[0].Type)
	}
	if c.Suggestion != "" {
		t.Fatalf("no progression should be suggested alongside pain, got %q", c.Suggestion)
	}
}

func TestFailureOveruseFlag(t *testing.T) {
	st := newStore(t)
	sets := barbellSets(80, 8, 8, 8)
	for i := range sets {
		sets[i].ToFailure = true
	}
	id := logSession(t, st, "2026-08-10", model.Exercise{
		Name: "bench press", RawName: "bench", Equipment: model.EquipBarbell, Sets: sets,
	})

	c, _ := New(st).Build(context.Background(), id)
	found := false
	for _, fl := range c.Flags {
		if fl.Type == FlagFailureOveruse {
			found = true
		}
	}
	if !found {
		t.Fatalf("three sets to failure should flag, got %+v", c.Flags)
	}
}

// Staleness needs both enough appearances and enough elapsed days. A lift
// trained three times a week hitting the same weight four times is under two
// weeks, which is normal, not a stall.
func TestStalenessRequiresElapsedDays(t *testing.T) {
	st := newStore(t)
	for _, d := range []string{"2026-08-01", "2026-08-03", "2026-08-05", "2026-08-07"} {
		logSession(t, st, d, bench(100, 5))
	}
	id := logSession(t, st, "2026-08-09", bench(100, 5))

	c, _ := New(st).Build(context.Background(), id)
	if len(c.StaleLifts) != 0 {
		t.Fatalf("eight days at the same weight is not a stall, got %+v", c.StaleLifts)
	}

	st2 := newStore(t)
	for _, d := range []string{"2026-06-01", "2026-06-15", "2026-07-01", "2026-07-15"} {
		logSession(t, st2, d, bench(100, 5))
	}
	id2 := logSession(t, st2, "2026-08-01", bench(100, 5))

	c2, _ := New(st2).Build(context.Background(), id2)
	if len(c2.StaleLifts) == 0 {
		t.Fatal("two months at the same weight is a stall and should be reported")
	}
}

// Two sessions on the same calendar day must still see each other. Cutting
// history on local_date made the second entry of a day report every lift as a
// fresh baseline, which is wrong for a morning-and-evening split and for any
// correction logged later the same day.
func TestSameDaySessionsSeeEachOther(t *testing.T) {
	st := newStore(t)
	day := time.Date(2026, 8, 10, 0, 0, 0, 0, st.Location())

	morning := &model.ParsedSession{
		Exercises: []model.Exercise{bench(100, 3)},
		RawText:   "morning",
		LoggedAt:  day.Add(9 * time.Hour),
	}
	if _, err := st.Persist(context.Background(), morning); err != nil {
		t.Fatalf("persist morning: %v", err)
	}

	evening := &model.ParsedSession{
		Exercises: []model.Exercise{bench(105, 3)},
		RawText:   "evening",
		LoggedAt:  day.Add(19 * time.Hour),
	}
	id, err := st.Persist(context.Background(), evening)
	if err != nil {
		t.Fatalf("persist evening: %v", err)
	}

	c, err := New(st).Build(context.Background(), id)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := c.LiftHistory[0]
	if got.IsBaseline {
		t.Fatal("the evening session should see the morning one, not report a baseline")
	}
	if !got.IsWeightPR {
		t.Fatalf("105 after 100 earlier the same day is a weight PR: %+v", got)
	}
}

func TestDaysSinceLastRestCountsConsecutiveDays(t *testing.T) {
	st := newStore(t)
	days := []string{"2026-08-05", "2026-08-06", "2026-08-07", "2026-08-08"}
	var id int64
	for _, d := range days {
		id = logSession(t, st, d, bench(80, 5))
	}

	c, _ := New(st).Build(context.Background(), id)
	if c.DaysSinceLastRest != 4 {
		t.Fatalf("four consecutive training days expected, got %d", c.DaysSinceLastRest)
	}
}
