package lifts

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
	st, err := store.Open(filepath.Join(t.TempDir(), "lifts.db"), time.UTC)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	st.SetSetting(ctx, "bodyweight_kg", "85")
	st.SetSetting(ctx, "height_cm", "178")
	st.SetSetting(ctx, "bodyfat_pct", "15")
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

// logSets writes one exercise with explicit (weight, reps) pairs.
func logSets(t *testing.T, st *store.Store, date, name string, eq model.Equipment,
	basis model.LoadBasis, pairs ...[2]float64) {
	t.Helper()
	var sets []model.Set
	for _, p := range pairs {
		sets = append(sets, model.Set{
			SetType: model.SetWorking, LoadBasis: basis,
			Weight: f(p[0]), Unit: up(model.UnitKg), Reps: ip(int(p[1])),
		})
	}
	ps := &model.ParsedSession{
		Exercises: []model.Exercise{{Name: name, RawName: name, Equipment: eq, Sets: sets}},
		RawText:   "test",
		LoggedAt:  day(t, date),
	}
	if _, err := st.Persist(context.Background(), ps); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

func TestListKeysOnNameEquipmentAndBasis(t *testing.T) {
	st := newStore(t)
	// Same name, different loading. These are different lifts and must not be
	// merged: a 30 kg per-side dumbbell row is 60 kg, the machine row is 30.
	logSets(t, st, "2026-08-20", "dumbbell row", model.EquipDumbbell, model.BasisPerSide, [2]float64{30, 8})
	logSets(t, st, "2026-08-21", "dumbbell row", model.EquipMachine, model.BasisTotal, [2]float64{30, 8})

	out, err := List(context.Background(), st, day(t, "2026-08-22"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected two distinct lifts, got %d: %+v", len(out), out)
	}
	// The per-side entry is the heavier of the two once doubled.
	var perSide, total Summary
	for _, s := range out {
		if s.LoadBasis == string(model.BasisPerSide) {
			perSide = s
		} else {
			total = s
		}
	}
	if perSide.BestE1RM <= total.BestE1RM {
		t.Errorf("per-side should double: %.1f vs %.1f", perSide.BestE1RM, total.BestE1RM)
	}
}

func TestKeyRoundTrip(t *testing.T) {
	k := keyOf("bench press", "barbell", "total")
	n, e, b, ok := ParseKey(k)
	if !ok || n != "bench press" || e != "barbell" || b != "total" {
		t.Fatalf("round trip failed: %q -> %q %q %q %v", k, n, e, b, ok)
	}
	for _, bad := range []string{"", "a", "a|b", "|b|c"} {
		if _, _, _, ok := ParseKey(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

// TestEmptyWeeksAreMarkedNotZeroed is the charting bug this guards against: a
// week with no training drawn as a zero bar reads as a collapse in strength,
// when nothing happened at all.
func TestEmptyWeeksAreMarkedNotZeroed(t *testing.T) {
	st := newStore(t)
	logSets(t, st, "2026-08-20", "bench press", model.EquipBarbell, model.BasisTotal, [2]float64{100, 5})

	d, err := Get(context.Background(), st, day(t, "2026-08-22"),
		"bench press", "barbell", "total")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Series) != weeks {
		t.Fatalf("expected %d weeks, got %d", weeks, len(d.Series))
	}
	filled, empty := 0, 0
	for _, p := range d.Series {
		if p.Empty {
			empty++
			if p.E1RM != 0 {
				t.Errorf("an empty week should carry no value, got %.1f", p.E1RM)
			}
		} else {
			filled++
		}
	}
	if filled != 1 || empty != weeks-1 {
		t.Errorf("expected 1 filled and %d empty weeks, got %d/%d", weeks-1, filled, empty)
	}
}

func TestSeriesTracksImprovement(t *testing.T) {
	st := newStore(t)
	// Three weeks, load climbing.
	logSets(t, st, "2026-06-08", "bench press", model.EquipBarbell, model.BasisTotal, [2]float64{95, 5})
	logSets(t, st, "2026-06-15", "bench press", model.EquipBarbell, model.BasisTotal, [2]float64{100, 5})
	logSets(t, st, "2026-06-22", "bench press", model.EquipBarbell, model.BasisTotal, [2]float64{105, 5})

	d, err := Get(context.Background(), st, day(t, "2026-06-24"),
		"bench press", "barbell", "total")
	if err != nil {
		t.Fatal(err)
	}
	var vals []float64
	for _, p := range d.Series {
		if !p.Empty {
			vals = append(vals, p.E1RM)
		}
	}
	if len(vals) != 3 {
		t.Fatalf("expected three filled weeks, got %d", len(vals))
	}
	for i := 1; i < len(vals); i++ {
		if vals[i] <= vals[i-1] {
			t.Errorf("series should climb: %v", vals)
			break
		}
	}
	if d.BestE1RM != vals[len(vals)-1] {
		t.Errorf("best should be the last value: %.1f vs %.1f", d.BestE1RM, vals[len(vals)-1])
	}
}

// TestMostRepsIsAtTheHeaviestLoad guards the record that is easy to get wrong:
// most reps at ANY load is won permanently by the lightest set ever logged.
func TestMostRepsIsAtTheHeaviestLoad(t *testing.T) {
	st := newStore(t)
	logSets(t, st, "2026-08-10", "bench press", model.EquipBarbell, model.BasisTotal,
		[2]float64{60, 8}) // light and high-rep
	logSets(t, st, "2026-08-17", "bench press", model.EquipBarbell, model.BasisTotal,
		[2]float64{100, 3}, [2]float64{100, 5}) // heavy

	d, err := Get(context.Background(), st, day(t, "2026-08-18"),
		"bench press", "barbell", "total")
	if err != nil {
		t.Fatal(err)
	}

	var heaviest, mostReps Record
	for _, r := range d.Records {
		switch {
		case r.Label == "HEAVIEST":
			heaviest = r
		case len(r.Label) > 9 && r.Label[:9] == "MOST REPS":
			mostReps = r
		}
	}
	if heaviest.Value != "100 x 5" {
		t.Errorf("heaviest = %q, want 100 x 5", heaviest.Value)
	}
	if mostReps.Label != "MOST REPS @100" {
		t.Errorf("rep record should be at the heaviest load, got %q", mostReps.Label)
	}
	if mostReps.Value != "5" {
		t.Errorf("most reps at 100 = %q, want 5", mostReps.Value)
	}
}

func TestBestVolumeIsPerSession(t *testing.T) {
	st := newStore(t)
	logSets(t, st, "2026-08-10", "bench press", model.EquipBarbell, model.BasisTotal,
		[2]float64{100, 5}, [2]float64{100, 5}, [2]float64{100, 5}) // 1500 kg
	logSets(t, st, "2026-08-17", "bench press", model.EquipBarbell, model.BasisTotal,
		[2]float64{100, 5}) // 500 kg

	d, err := Get(context.Background(), st, day(t, "2026-08-18"),
		"bench press", "barbell", "total")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range d.Records {
		if r.Label == "BEST VOLUME" {
			if r.Value != "1.5 t" {
				t.Errorf("best volume = %q, want 1.5 t", r.Value)
			}
			return
		}
	}
	t.Error("no volume record")
}

// TestNextStepHoldsLoadUntilRepsAreMet is the property that stops the app
// talking someone into a stall.
func TestNextStepHoldsLoadUntilRepsAreMet(t *testing.T) {
	st := newStore(t)
	logSets(t, st, "2026-08-17", "bench press", model.EquipBarbell, model.BasisTotal,
		[2]float64{100, 3})

	d, err := Get(context.Background(), st, day(t, "2026-08-18"),
		"bench press", "barbell", "total")
	if err != nil {
		t.Fatal(err)
	}
	if d.NextStep != "100 for 3 x 5" {
		t.Errorf("with 3 reps at 100 the load must hold, got %q", d.NextStep)
	}
	if d.NextStepWhy == "" {
		t.Error("a suggested number needs a reason")
	}
}

func TestNextStepAdvancesOnceRepsAreMet(t *testing.T) {
	st := newStore(t)
	logSets(t, st, "2026-08-17", "bench press", model.EquipBarbell, model.BasisTotal,
		[2]float64{100, 5})

	d, err := Get(context.Background(), st, day(t, "2026-08-18"),
		"bench press", "barbell", "total")
	if err != nil {
		t.Fatal(err)
	}
	// Bench is a compound: 2.5 kg.
	if d.NextStep != "102.5 for 3 x 5" {
		t.Errorf("5 reps at 100 should advance to 102.5, got %q", d.NextStep)
	}
}

// TestLevelComesFromTheRankEngine: a lift must not read "advanced" here and
// score badly on the rank screen, so classification uses berserk.Ref.
func TestLevelComesFromTheRankEngine(t *testing.T) {
	st := newStore(t)
	logSets(t, st, "2026-08-17", "bench press", model.EquipBarbell, model.BasisTotal,
		[2]float64{140, 3})

	d, err := Get(context.Background(), st, day(t, "2026-08-18"),
		"bench press", "barbell", "total")
	if err != nil {
		t.Fatal(err)
	}
	if d.Pattern != "h_press" {
		t.Errorf("bench should map to h_press, got %q", d.Pattern)
	}
	if d.Level == "" || d.Level == "UNTRAINED" {
		t.Errorf("a 140x3 bench should not read as %q", d.Level)
	}
	if d.BWMultiple <= 1.5 {
		t.Errorf("expected a bodyweight multiple above 1.5 at 85 kg, got %.2f", d.BWMultiple)
	}
}

func TestUnknownLiftIsNotFound(t *testing.T) {
	st := newStore(t)
	_, err := Get(context.Background(), st, day(t, "2026-08-18"),
		"nonexistent lift", "barbell", "total")
	if err == nil {
		t.Fatal("expected an error for a lift with no sets")
	}
}

func TestTrimKg(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{{100, "100"}, {102.5, "102.5"}, {60, "60"}, {2.5, "2.5"}} {
		if got := trimKg(tc.in); got != tc.want {
			t.Errorf("trimKg(%.1f) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSeriesNoteExplainsAnEmptyChart: a lifter who does dips for sets of twelve
// has trained the lift, but Epley cannot score twelve reps. Saying only "no
// qualifying sets" reads as though the app lost the work.
func TestSeriesNoteExplainsAnEmptyChart(t *testing.T) {
	st := newStore(t)
	logSets(t, st, "2026-08-20", "dips", model.EquipBodyweight, model.BasisBW,
		[2]float64{0, 12}, [2]float64{0, 10})

	d, err := Get(context.Background(), st, day(t, "2026-08-21"),
		"dips", "bodyweight", "bodyweight")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range d.Series {
		if !p.Empty {
			t.Fatalf("12 and 10 reps must not produce an estimated max, got %.1f", p.E1RM)
		}
	}
	if d.SeriesNote == "" {
		t.Error("an empty chart with logged sets needs an explanation")
	}

	// And a lift with no sets at all gets no note, because there is nothing to
	// explain.
	logSets(t, st, "2026-08-20", "bench press", model.EquipBarbell, model.BasisTotal,
		[2]float64{100, 5})
	b, err := Get(context.Background(), st, day(t, "2026-08-21"),
		"bench press", "barbell", "total")
	if err != nil {
		t.Fatal(err)
	}
	if b.SeriesNote != "" {
		t.Errorf("a scoring lift needs no note, got %q", b.SeriesNote)
	}
}
