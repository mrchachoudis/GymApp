package validate

import (
	"testing"

	"github.com/mrcha/gymlogger/internal/model"
)

func f(v float64) *float64       { return &v }
func i(v int) *int               { return &v }
func u(v model.Unit) *model.Unit { return &v }

func session(sets ...model.Set) *model.ParsedSession {
	return &model.ParsedSession{
		Exercises: []model.Exercise{{
			Name: "bench press", RawName: "bench", Equipment: model.EquipBarbell, Sets: sets,
		}},
	}
}

// The classic model failure: "100 x 5" comes back with the fields the wrong
// way round. Persisting it would create a 100-rep set at 5 kg and a permanent
// hole in the volume history.
func TestSwappedWeightAndReps(t *testing.T) {
	p := session(model.Set{
		SetType: model.SetWorking, LoadBasis: model.BasisTotal,
		Weight: f(5), Unit: u(model.UnitKg), Reps: i(100),
	})
	res := Session(p)
	if res.Fatal() {
		t.Fatalf("a swap should be repaired, not rejected: %s", res.Reason())
	}
	got := p.Exercises[0].Sets[0]
	if *got.Weight != 100 || *got.Reps != 5 {
		t.Fatalf("expected weight 100 and reps 5 after repair, got %v x %v", *got.Weight, *got.Reps)
	}
	if len(res.Repairs()) == 0 {
		t.Fatal("the repair should be reported to the user")
	}
}

func TestImplausibleWeightIsFatal(t *testing.T) {
	p := session(model.Set{
		SetType: model.SetWorking, LoadBasis: model.BasisTotal,
		Weight: f(1000), Unit: u(model.UnitKg), Reps: i(5),
	})
	if res := Session(p); !res.Fatal() {
		t.Fatal("a 1000 kg bench must not reach the database")
	}
}

// Per-side bounds are much tighter than total bounds. A 200 kg dumbbell is
// not a heavy day, it is a parse error.
func TestPerSideBoundsAreTighter(t *testing.T) {
	p := &model.ParsedSession{Exercises: []model.Exercise{{
		Name: "hammer curl", RawName: "hammer curl", Equipment: model.EquipDumbbell,
		Sets: []model.Set{{
			SetType: model.SetWorking, LoadBasis: model.BasisPerSide,
			Weight: f(200), Unit: u(model.UnitKg), Reps: i(10),
		}},
	}}}
	if res := Session(p); !res.Fatal() {
		t.Fatal("200 kg per side should be rejected")
	}
}

func TestCleanRepsClampedToReps(t *testing.T) {
	p := session(model.Set{
		SetType: model.SetWorking, LoadBasis: model.BasisTotal,
		Weight: f(80), Unit: u(model.UnitKg), Reps: i(8), CleanReps: i(12),
	})
	res := Session(p)
	if res.Fatal() {
		t.Fatalf("unexpected fatal: %s", res.Reason())
	}
	if *p.Exercises[0].Sets[0].CleanReps != 8 {
		t.Fatalf("clean reps should clamp to 8, got %d", *p.Exercises[0].Sets[0].CleanReps)
	}
}

func TestBodyweightWithLoadBecomesWeighted(t *testing.T) {
	p := &model.ParsedSession{Exercises: []model.Exercise{{
		Name: "dips", RawName: "dips", Equipment: model.EquipBodyweight,
		Sets: []model.Set{{
			SetType: model.SetWorking, LoadBasis: model.BasisBW,
			Weight: f(20), Unit: u(model.UnitKg), Reps: i(8),
		}},
	}}}
	Session(p)
	if got := p.Exercises[0].Sets[0].LoadBasis; got != model.BasisBWPlus {
		t.Fatalf("a loaded bodyweight set should become bodyweight_plus, got %s", got)
	}
}

func TestEmptyExerciseIsDropped(t *testing.T) {
	p := &model.ParsedSession{Exercises: []model.Exercise{
		{Name: "squat", Equipment: model.EquipBarbell, Sets: nil},
		{Name: "bench press", Equipment: model.EquipBarbell, Sets: []model.Set{{
			SetType: model.SetWorking, LoadBasis: model.BasisTotal,
			Weight: f(80), Unit: u(model.UnitKg), Reps: i(5),
		}}},
	}}
	Session(p)
	if len(p.Exercises) != 1 || p.Exercises[0].Name != "bench press" {
		t.Fatalf("the setless exercise should be dropped, got %d exercises", len(p.Exercises))
	}
}

func TestUnknownEnumsAreRepairedNotRejected(t *testing.T) {
	p := session(model.Set{
		SetType: "hard", LoadBasis: "sideways",
		Weight: f(60), Unit: u(model.UnitKg), Reps: i(10),
	})
	res := Session(p)
	if res.Fatal() {
		t.Fatalf("unknown enums should be repaired: %s", res.Reason())
	}
	got := p.Exercises[0].Sets[0]
	if got.SetType != model.SetWorking || got.LoadBasis != model.BasisTotal {
		t.Fatalf("expected repaired enums, got %s / %s", got.SetType, got.LoadBasis)
	}
}
