// Package muscle maps a logged exercise to the muscle groups it trains.
//
// Why this exists: the rank system's VIGOR attribute wants weekly hard sets as
// a per-muscle-group median (v1.0 6.3), and the muscle map screen wants sets
// per group over a trailing window. Neither is answerable from the sets table
// alone, because an exercise name carries no anatomy. Until this package there
// was no exercise-to-muscle-group mapping anywhere in the app, and V_volume
// stood in the six movement patterns as a documented approximation.
//
// # Provenance
//
// The exercise library in exercises.json is derived from
// hasaneyldrm/exercises-dataset, by way of the openGym project which slimmed
// and normalized it. That dataset is NOT under openGym's AGPL; openGym's own
// NOTICE.md records it as Creative Commons under the upstream dataset's terms,
// and it is used here on that basis. Only four fields are kept -- name,
// equipment, primary target and secondary muscles -- and no instructional text,
// image or animation is reproduced.
//
// Nothing else from openGym is used. Its application code is AGPL-3.0 and
// copying it would relicense this repository.
package muscle

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

// Group is a recovery and volume unit. The eleven groups match the scheduler's
// MuscleGroup names exactly, so the two subsystems cannot drift into disagreeing
// about what "back" means.
type Group string

const (
	Chest      Group = "chest"
	Back       Group = "back"
	Shoulders  Group = "shoulders"
	Biceps     Group = "biceps"
	Triceps    Group = "triceps"
	Quads      Group = "quads"
	Hamstrings Group = "hamstrings"
	Glutes     Group = "glutes"
	Calves     Group = "calves"
	Forearms   Group = "forearms"
	Core       Group = "core"
)

// Groups is the canonical display order, torso down.
var Groups = []Group{
	Chest, Back, Shoulders, Biceps, Triceps,
	Quads, Hamstrings, Glutes, Calves, Forearms, Core,
}

// Major is the subset whose volume is judged against the 16-sets-a-week target.
//
// Calves, forearms and core are deliberately excluded: their volume norms are
// different enough that including them drags the median down for every lifter
// who trains sensibly, which would turn VIGOR into a measure of whether someone
// does direct calf work rather than a measure of work capacity.
var Major = []Group{Chest, Back, Shoulders, Biceps, Triceps, Quads, Hamstrings, Glutes}

func (g Group) Display() string { return strings.ToUpper(string(g)) }

// muscleToGroup folds both vocabularies in the dataset into the eleven groups.
//
// Two vocabularies, because the dataset is not internally consistent: the
// primary target field uses nineteen tidy values, while the secondary list is
// free text with forty variants that overlap ("traps" and "trapezius", "quads"
// and "quadriceps", "lats" and "latissimus dorsi" and plain "back"). Both are
// folded here so a caller never has to care which field a name came from.
var muscleToGroup = map[string]Group{
	// chest
	"pectorals": Chest, "chest": Chest, "upper chest": Chest,
	"serratus anterior": Chest,

	// back
	"lats": Back, "latissimus dorsi": Back, "upper back": Back, "back": Back,
	"traps": Back, "trapezius": Back, "rhomboids": Back, "lower back": Back,
	"spine": Back, "levator scapulae": Back,

	// shoulders
	"delts": Shoulders, "deltoids": Shoulders, "shoulders": Shoulders,
	"rear deltoids": Shoulders, "rotator cuff": Shoulders,

	"biceps": Biceps, "brachialis": Biceps,
	"triceps": Triceps,

	// legs
	"quads": Quads, "quadriceps": Quads, "adductors": Quads, "inner thighs": Quads,
	"groin": Quads, "hip flexors": Quads,
	"hamstrings": Hamstrings,
	"glutes":     Glutes, "abductors": Glutes,
	"calves": Calves, "soleus": Calves, "shins": Calves,
	"ankles": Calves, "ankle stabilizers": Calves, "feet": Calves,

	// arms, distal
	"forearms": Forearms, "wrists": Forearms, "wrist flexors": Forearms,
	"wrist extensors": Forearms, "hands": Forearms, "grip muscles": Forearms,

	// core
	"abs": Core, "abdominals": Core, "lower abs": Core, "obliques": Core,
	"core": Core,

	// Deliberately unmapped: "cardiovascular system" and
	// "sternocleidomastoid". Neither is a resistance-volume unit, and silently
	// folding them into a group would inflate that group's set count.
}

// GroupFor resolves a single dataset muscle name. ok is false for names that
// are intentionally unmapped, so callers can tell "not a lifting group" apart
// from "we forgot one".
func GroupFor(name string) (Group, bool) {
	g, ok := muscleToGroup[strings.ToLower(strings.TrimSpace(name))]
	return g, ok
}

// Entry is one library exercise, reduced to the fields the mapping needs.
type Entry struct {
	Name      string   `json:"n"`
	Equipment string   `json:"e"`
	Target    string   `json:"t"`
	Secondary []string `json:"s"`
}

//go:embed exercises.json
var libraryJSON []byte

var (
	loadOnce sync.Once
	library  []Entry
	byName   map[string][]Entry
)

func load() {
	loadOnce.Do(func() {
		if err := json.Unmarshal(libraryJSON, &library); err != nil {
			// An embedded asset that does not parse is a build error wearing a
			// runtime costume. Leave the library empty; every lookup then
			// misses and the caller falls back, rather than panicking a service
			// that is otherwise healthy.
			library = nil
			return
		}
		byName = make(map[string][]Entry, len(library))
		for _, e := range library {
			k := normalize(e.Name)
			byName[k] = append(byName[k], e)
		}
	})
}

// Library exposes the parsed entries, for tests and for tooling that wants to
// audit coverage.
func Library() []Entry {
	load()
	return library
}

// primaryOverride corrects a systematic bias in the upstream dataset.
//
// The dataset labels 144 upper-leg exercises glutes-primary against 44 for
// quads and 27 for hamstrings, and that includes the squat, the front squat,
// the hack squat, the Romanian deadlift and the conventional deadlift. Taken
// literally, a lifter squatting three times a week reads as barely training
// quads at all, which distorts both the muscle map and the per-group median
// behind V_volume.
//
// The corrections are deliberately narrow: only the compound squat and hinge
// patterns, where the dataset's own secondary list already names the muscle
// being promoted. Glutes are not dropped -- they fall back to a secondary and
// keep half credit -- and nothing outside this table is second-guessed.
var primaryOverride = map[string]Group{
	"barbell full squat":              Quads,
	"barbell front squat":             Quads,
	"barbell hack squat":              Quads,
	"sled hack squat":                 Quads,
	"smith leg press":                 Quads,
	"sled 45 degrees one leg press":   Quads,
	"lever alternate leg press":       Quads,
	"dumbbell single leg split squat": Quads,
	"barbell romanian deadlift":       Hamstrings,
	"dumbbell romanian deadlift":      Hamstrings,
	"barbell good morning":            Hamstrings,
}

// Weights is one exercise's contribution to each group it trains.
//
// A set is credited in full to the primary target and at half to each secondary
// muscle. Half is the conventional split and it is what the muscle map screen
// assumes; the important property is only that a secondary counts for something
// and for less than a primary, so that a bench press registers as triceps work
// without reading as a triceps session.
type Weights map[Group]float64

const secondaryWeight = 0.5

// Of returns the group weights for a logged exercise.
//
// equipment narrows the match: the library distinguishes "barbell bench press"
// from "dumbbell bench press", and they train the same groups here but not
// everywhere -- a barbell row and a cable row differ in how much they ask of
// the lower back. Pass an empty string when equipment is unknown.
func Of(name, equipment string) (Weights, bool) {
	load()
	e, ok := lookup(name, equipment)
	if !ok {
		return nil, false
	}

	primary, hasPrimary := GroupFor(e.Target)
	if o, ok := primaryOverride[normalize(e.Name)]; ok {
		primary, hasPrimary = o, true
	}

	w := Weights{}
	if hasPrimary {
		w[primary] = 1
	}
	// The demoted group still earns secondary credit, so an override moves
	// emphasis rather than deleting work the lift genuinely does.
	secondaries := e.Secondary
	if g, ok := GroupFor(e.Target); ok && g != primary {
		secondaries = append(append([]string(nil), secondaries...), e.Target)
	}
	for _, s := range secondaries {
		g, ok := GroupFor(s)
		if !ok {
			continue
		}
		// A muscle that is both the primary target and listed again as a
		// secondary must not be paid twice.
		if w[g] < secondaryWeight {
			w[g] = secondaryWeight
		}
	}
	if len(w) == 0 {
		return nil, false
	}
	return w, true
}

// Primary returns just the group an exercise most trains, which is what the
// scheduler's recovery tracking wants.
func Primary(name, equipment string) (Group, bool) {
	load()
	e, ok := lookup(name, equipment)
	if !ok {
		return "", false
	}
	if o, ok := primaryOverride[normalize(e.Name)]; ok {
		return o, true
	}
	return GroupFor(e.Target)
}
