package muscle

import (
	"testing"
)

// TestEveryAliasResolves is the test that matters most in this package. An
// alias pointing at a name the dataset does not contain fails silently: the
// lookup falls through to the fuzzy pass and either finds something wrong or
// finds nothing, and either way VIGOR quietly degrades with no error anywhere.
// Thirteen of the first draft's aliases were wrong this way.
func TestEveryAliasResolves(t *testing.T) {
	load()
	for from, to := range aliases {
		if len(byName[normalize(to)]) == 0 {
			t.Errorf("alias %q -> %q: target is not in the library", from, to)
		}
	}
}

// TestScoredLiftsAllMap walks every lift the rank system scores and asserts it
// lands on a plausible primary group. These are the names that feed V_volume,
// so a miss here is a hole in the attribute.
func TestScoredLiftsAllMap(t *testing.T) {
	cases := []struct {
		name, equipment string
		want            Group
	}{
		{"bench press", "barbell", Chest},
		{"paused bench press", "barbell", Chest},
		{"incline bench press", "barbell", Chest},
		{"dumbbell bench press", "dumbbell", Chest},
		{"dumbbell press", "dumbbell", Chest},
		{"dips", "bodyweight", Chest},
		{"weighted dip", "bodyweight", Chest},
		{"machine chest press", "machine", Chest},

		{"overhead press", "barbell", Shoulders},
		{"shoulder press", "barbell", Shoulders},
		{"seated overhead press", "barbell", Shoulders},
		{"push press", "barbell", Shoulders},
		{"dumbbell shoulder press", "dumbbell", Shoulders},

		{"squat", "barbell", Quads},
		{"back squat", "barbell", Quads},
		{"front squat", "barbell", Quads},
		{"hack squat", "machine", Quads},
		{"leg press", "machine", Quads},
		{"bulgarian split squat", "dumbbell", Quads},

		// Conventional deadlift keeps the dataset's glutes-primary label: it is
		// a hip hinge, and unlike the squat and RDL rows above there is no
		// biomechanical case for promoting something else.
		{"deadlift", "barbell", Glutes},
		{"sumo deadlift", "barbell", Glutes},
		{"rdl", "barbell", Hamstrings},
		{"romanian deadlift", "barbell", Hamstrings},
		{"trap bar deadlift", "barbell", Glutes},
		{"hip thrust", "barbell", Glutes},
		{"good morning", "barbell", Hamstrings},

		{"pull-up", "bodyweight", Back},
		{"weighted pull-up", "bodyweight", Back},
		{"chin-up", "bodyweight", Back},
		{"lat pulldown", "cable", Back},

		{"barbell row", "barbell", Back},
		{"pendlay row", "barbell", Back},
		{"chest-supported row", "machine", Back},
		{"dumbbell row", "dumbbell", Back},
		{"seated cable row", "cable", Back},
	}

	for _, tc := range cases {
		got, ok := Primary(tc.name, tc.equipment)
		if !ok {
			t.Errorf("%s (%s): no mapping found", tc.name, tc.equipment)
			continue
		}
		if got != tc.want {
			t.Errorf("%s (%s): primary group %s, want %s", tc.name, tc.equipment, got, tc.want)
		}
	}
}

// TestSecondariesAreCredited: a bench press has to register as triceps and
// shoulder work, or a push-heavy week reads as if the triceps were never
// trained and the per-group median understates the real volume.
func TestSecondariesAreCredited(t *testing.T) {
	w, ok := Of("bench press", "barbell")
	if !ok {
		t.Fatal("bench press did not resolve")
	}
	if w[Chest] != 1 {
		t.Errorf("chest should be the full-weight primary, got %.2f", w[Chest])
	}
	for _, g := range []Group{Triceps, Shoulders} {
		if w[g] != secondaryWeight {
			t.Errorf("%s should be credited at %.1f, got %.2f", g, secondaryWeight, w[g])
		}
	}
	if _, present := w[Hamstrings]; present {
		t.Error("bench press must not credit hamstrings")
	}
}

// TestPrimaryNeverDoubleCounted guards the case where a muscle is both the
// target and repeated in the secondary list.
func TestPrimaryNeverDoubleCounted(t *testing.T) {
	for _, e := range Library() {
		w, ok := Of(e.Name, e.Equipment)
		if !ok {
			continue
		}
		for g, v := range w {
			if v != 1 && v != secondaryWeight {
				t.Fatalf("%s: group %s has weight %.2f, expected 1 or %.1f",
					e.Name, g, v, secondaryWeight)
			}
		}
	}
}

// TestEveryLibraryMuscleIsMapped catches a dataset refresh that introduces a
// muscle name the fold does not know. An unmapped name silently drops volume,
// so it should break the build instead.
func TestEveryLibraryMuscleIsMapped(t *testing.T) {
	// Deliberately unmapped: not resistance-volume units.
	skip := map[string]bool{
		"cardiovascular system": true,
		"sternocleidomastoid":   true,
	}
	seen := map[string]bool{}
	for _, e := range Library() {
		names := append([]string{e.Target}, e.Secondary...)
		for _, n := range names {
			if seen[n] || skip[n] {
				continue
			}
			seen[n] = true
			if _, ok := GroupFor(n); !ok {
				t.Errorf("muscle %q from the library has no group mapping", n)
			}
		}
	}
}

// TestLibraryCoverage records how much of the library resolves at all. It is a
// ratchet rather than an exact figure: the point is to notice a regression in
// the resolver, not to pin a number.
func TestLibraryCoverage(t *testing.T) {
	lib := Library()
	if len(lib) < 1000 {
		t.Fatalf("library looks truncated: %d entries", len(lib))
	}
	resolved := 0
	for _, e := range lib {
		if _, ok := Of(e.Name, e.Equipment); ok {
			resolved++
		}
	}
	pct := float64(resolved) / float64(len(lib)) * 100
	if pct < 95 {
		t.Errorf("only %.1f%% of the library resolves to a group", pct)
	}
	t.Logf("library coverage: %d/%d (%.1f%%)", resolved, len(lib), pct)
}

// TestUnknownExerciseIsNotGuessed: a lift the library has never heard of must
// report a miss rather than being forced into a group. Volume attributed to the
// wrong muscle is worse than volume left uncounted, because the second is
// visible as a gap and the first is not.
func TestUnknownExerciseIsNotGuessed(t *testing.T) {
	for _, name := range []string{"zercher jefferson curl of doom", "qqqq", ""} {
		if g, ok := Primary(name, "barbell"); ok {
			t.Errorf("%q should not resolve, got %s", name, g)
		}
	}
}

// TestNormalizeCollapsesPunctuation covers the shapes the parser actually emits.
func TestNormalizeCollapsesPunctuation(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Chest-Supported Row", "chest supported row"},
		{"chest supported row", "chest supported row"},
		{"3/4 sit-up", "3 4 sit up"},
		{"  Pull-Up  ", "pull up"},
		{"sled 45° leg press", "sled 45 leg press"},
	} {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEquipmentDisambiguates: the same movement name under different equipment
// should be allowed to resolve differently, since that is the whole reason
// equipment is a parameter.
func TestEquipmentDisambiguates(t *testing.T) {
	bb, ok1 := lookup("bench press", "barbell")
	db, ok2 := lookup("dumbbell bench press", "dumbbell")
	if !ok1 || !ok2 {
		t.Fatal("both bench variants should resolve")
	}
	if bb.Equipment == db.Equipment {
		t.Errorf("expected different library entries, both resolved to %s", bb.Equipment)
	}
}

// TestMajorIsASubsetOfGroups guards the two lists drifting apart.
func TestMajorIsASubsetOfGroups(t *testing.T) {
	all := map[Group]bool{}
	for _, g := range Groups {
		all[g] = true
	}
	for _, g := range Major {
		if !all[g] {
			t.Errorf("Major contains %s, which is not in Groups", g)
		}
	}
	if len(Major) >= len(Groups) {
		t.Error("Major should be a strict subset")
	}
}
