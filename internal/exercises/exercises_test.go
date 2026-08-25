package exercises

import (
	"strings"
	"testing"
)

func TestLibraryLoads(t *testing.T) {
	if err := Err(); err != nil {
		t.Fatalf("embedded library failed to load: %v", err)
	}
	all := All()
	if len(all) < 1300 {
		t.Fatalf("library looks truncated: %d entries", len(all))
	}
	for _, e := range all {
		if e.ID == "" || e.Name == "" || e.Equipment == "" || e.Target == "" {
			t.Fatalf("incomplete entry: %+v", e)
		}
		if e.Name != strings.ToLower(e.Name) {
			t.Fatalf("names are expected lowercase, got %q", e.Name)
		}
	}
}

// TestEveryExerciseHasInstructions: the library is only worth shipping if it
// tells you how to do the movement. A silent drop of the steps field would
// leave a browsable list of names and nothing else.
func TestEveryExerciseHasInstructions(t *testing.T) {
	missing := 0
	for _, e := range All() {
		if len(e.Steps) == 0 {
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("%d exercises have no instruction steps", missing)
	}
}

func TestEveryExerciseHasMediaFilenames(t *testing.T) {
	for _, e := range All() {
		if e.Image == "" || e.Animation == "" {
			t.Errorf("%s (%s) is missing a media filename", e.Name, e.ID)
			return
		}
	}
}

func TestByID(t *testing.T) {
	all := All()
	want := all[len(all)/2]
	got, ok := ByID(want.ID)
	if !ok || got.Name != want.Name {
		t.Fatalf("ByID(%q) = %+v, %v", want.ID, got, ok)
	}
	if _, ok := ByID("nope"); ok {
		t.Error("an unknown id should not resolve")
	}
}

// TestSearchRanksTheObviousAnswerFirst is the property that makes a library of
// this size usable.
//
// The dataset names movements equipment-first, so ranking on a prefix put
// "squat jerk" and "squat on bosu ball" above the barbell squat, and "band
// bench press" above the barbell bench press. Suffix-then-equipment fixes it,
// and these cases are the record of what "fixed" means.
func TestSearchRanksTheObviousAnswerFirst(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"pull-up", "pull-up"},
		{"chin-up", "chin-up"},
		{"push-up", "push-up"},
		{"barbell bench press", "barbell bench press"},
		{"squat", "barbell full squat"},
		{"bench press", "barbell bench press"},
		{"deadlift", "barbell deadlift"},
		{"curl", "barbell curl"},
		{"romanian deadlift", "barbell romanian deadlift"},
	} {
		res := Search(Query{Text: tc.query})
		if res.Total == 0 {
			t.Errorf("%q returned nothing", tc.query)
			continue
		}
		if got := res.Exercises[0].Name; got != tc.want {
			t.Errorf("%q ranked %q first, want %q", tc.query, got, tc.want)
		}
	}
}

// TestCanonicalEquipmentLeads: among variants of one movement, the barbell
// version comes before the bosu-ball version.
func TestCanonicalEquipmentLeads(t *testing.T) {
	res := Search(Query{Text: "bench press", Limit: 5})
	if res.Exercises[0].Equipment != "barbell" {
		t.Errorf("expected a barbell variant to lead, got %q (%s)",
			res.Exercises[0].Name, res.Exercises[0].Equipment)
	}
}

// TestSearchTermsNarrow: every term must match, so adding a word can only
// reduce the result set. The opposite behaviour makes a search box feel broken.
func TestSearchTermsNarrow(t *testing.T) {
	broad := Search(Query{Text: "press", Limit: 1}).Total
	narrow := Search(Query{Text: "incline press", Limit: 1}).Total
	if narrow == 0 {
		t.Fatal("incline press should match something")
	}
	if narrow >= broad {
		t.Errorf("adding a term must narrow: %d then %d", broad, narrow)
	}
}

func TestSearchFilters(t *testing.T) {
	res := Search(Query{Equipment: "barbell", Limit: 1000})
	if res.Total == 0 {
		t.Fatal("no barbell exercises")
	}
	for _, e := range res.Exercises {
		if e.Equipment != "barbell" {
			t.Fatalf("equipment filter leaked %q", e.Equipment)
		}
	}

	combined := Search(Query{Equipment: "barbell", Target: "biceps", Limit: 1000})
	if combined.Total == 0 || combined.Total >= res.Total {
		t.Errorf("combining filters should narrow: %d then %d", res.Total, combined.Total)
	}
	for _, e := range combined.Exercises {
		if e.Equipment != "barbell" || e.Target != "biceps" {
			t.Fatalf("combined filter leaked %+v", e)
		}
	}
}

// TestSearchPaginates checks the contract the phone relies on to say
// "showing 50 of 214".
func TestSearchPaginates(t *testing.T) {
	first := Search(Query{Limit: 10})
	if len(first.Exercises) != 10 {
		t.Fatalf("expected 10 results, got %d", len(first.Exercises))
	}
	if first.Total <= 10 {
		t.Fatal("total should report the unpaged count")
	}

	second := Search(Query{Limit: 10, Offset: 10})
	if second.Total != first.Total {
		t.Errorf("total must not change with offset: %d then %d", first.Total, second.Total)
	}
	if second.Exercises[0].Name == first.Exercises[0].Name {
		t.Error("the second page repeats the first")
	}

	// Past the end is an empty page, not an error and not a panic.
	past := Search(Query{Limit: 10, Offset: 99999})
	if len(past.Exercises) != 0 || past.Total != first.Total {
		t.Errorf("offset past the end should give an empty page, got %d", len(past.Exercises))
	}
}

func TestSearchNoMatch(t *testing.T) {
	res := Search(Query{Text: "zercher jefferson curl of doom"})
	if res.Total != 0 || len(res.Exercises) != 0 {
		t.Errorf("expected no matches, got %d", res.Total)
	}
}

func TestFacetsCoverTheLibrary(t *testing.T) {
	f := AllFacets()
	if len(f.BodyParts) == 0 || len(f.Equipment) == 0 || len(f.Targets) == 0 {
		t.Fatal("facets should not be empty")
	}
	total := 0
	for _, x := range f.Equipment {
		total += x.Count
	}
	if total != len(All()) {
		t.Errorf("equipment facet counts sum to %d, library has %d", total, len(All()))
	}
	// Sorted by count descending, so the phone's chips lead with the useful ones.
	for i := 1; i < len(f.Equipment); i++ {
		if f.Equipment[i-1].Count < f.Equipment[i].Count {
			t.Error("facets should be ordered by count descending")
			break
		}
	}
}

func TestNormalize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Chest-Supported Row", "chest supported row"},
		{"3/4 sit-up", "3 4 sit up"},
		{"  Pull-Up  ", "pull up"},
		{"sled 45° leg press", "sled 45 leg press"},
		{"", ""},
	} {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
