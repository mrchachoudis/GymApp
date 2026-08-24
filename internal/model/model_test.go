package model

import "testing"

func f(v float64) *float64 { return &v }
func i(v int) *int         { return &v }
func u(v Unit) *Unit       { return &v }

func TestWeightKgConvertsPounds(t *testing.T) {
	s := Set{Weight: f(100), Unit: u(UnitLb), LoadBasis: BasisTotal}
	got := s.WeightKg()
	if got == nil {
		t.Fatal("expected a converted weight")
	}
	if *got < 45.35 || *got > 45.37 {
		t.Fatalf("100 lb should be about 45.36 kg, got %v", *got)
	}
}

// A per-side dumbbell in pounds must stay per-side after conversion. Folding
// the doubling into the unit conversion is the bug that makes 20 lb dumbbells
// look like a 18 kg total lift.
func TestWeightKgPreservesLoadBasis(t *testing.T) {
	s := Set{Weight: f(20), Unit: u(UnitLb), LoadBasis: BasisPerSide}
	got := *s.WeightKg()
	if got < 9.06 || got > 9.08 {
		t.Fatalf("per-side conversion should be 9.07, got %v", got)
	}
	if eff := s.EffectiveLoadKg(80); eff < 18.1 || eff > 18.2 {
		t.Fatalf("effective load should double to about 18.14, got %v", eff)
	}
}

func TestEffectiveLoadBodyweight(t *testing.T) {
	bw := Set{LoadBasis: BasisBW}
	if got := bw.EffectiveLoadKg(82); got != 82 {
		t.Fatalf("bodyweight set should move bodyweight, got %v", got)
	}

	weighted := Set{Weight: f(20), Unit: u(UnitKg), LoadBasis: BasisBWPlus}
	if got := weighted.EffectiveLoadKg(82); got != 102 {
		t.Fatalf("weighted set should be bodyweight plus load, got %v", got)
	}
}

// An uncertain rep range must resolve to the low end. Crediting the high end
// inflates every downstream comparison in the user's favour.
func TestEffectiveRepsUsesLowEnd(t *testing.T) {
	s := Set{RepsUncertain: true, RepsLow: i(10), RepsHigh: i(12)}
	if got := s.EffectiveReps(); got != 10 {
		t.Fatalf("expected the pessimistic 10, got %d", got)
	}
}

func TestEpleyRefusesHighReps(t *testing.T) {
	if _, ok := Epley1RM(60, 15); ok {
		t.Fatal("Epley must not report a value above 8 reps, it stops being meaningful")
	}
	v, ok := Epley1RM(100, 5)
	if !ok {
		t.Fatal("expected a value at 5 reps")
	}
	if v < 116.6 || v > 116.8 {
		t.Fatalf("100x5 should estimate about 116.7, got %v", v)
	}
}

// The lift key is what stops a per-side dumbbell row and a total-load machine
// row from being compared as the same lift.
func TestLiftKeyDistinguishesLoadBasis(t *testing.T) {
	dumbbell := LiftKey{Name: "db row", Equipment: EquipDumbbell, LoadBasis: BasisPerSide}
	machine := LiftKey{Name: "db row", Equipment: EquipMachine, LoadBasis: BasisTotal}
	if dumbbell.String() == machine.String() {
		t.Fatal("different equipment and basis must not share a key")
	}
}

func TestSetTypeVolumeRules(t *testing.T) {
	if SetWarmup.CountsTowardVolume() {
		t.Fatal("warmups must be excluded from volume")
	}
	if !SetWorking.CountsTowardVolume() {
		t.Fatal("working sets must count toward volume")
	}
	if SetDrop.CountsTowardPR() {
		t.Fatal("a drop set must not be eligible for a PR")
	}
}
