package muscle

import (
	"sort"
	"strings"

	"github.com/mrcha/gymlogger/internal/exercises"
)

// Name resolution, which is the whole difficulty of using this library.
//
// The parser emits what a lifter says: "bench press", "rdl", "squat". The
// dataset names things formally and equipment first: "barbell bench press",
// "barbell romanian deadlift", "barbell full squat". Of the thirty-seven lifts
// the rank system scores, exactly five match by name. So a bare join finds
// almost nothing, and the gap has to be closed deliberately rather than by
// hoping the strings line up.
//
// Four passes, most confident first. Each returns as soon as it hits, so a
// hand-written alias always beats a fuzzy guess.

// aliases pins the lifts that matter most: everything the rank system scores,
// plus the common shorthand the parser is known to produce. These are asserted
// by test, because a silent miss here degrades VIGOR without any visible error.
var aliases = map[string]string{
	// Horizontal press
	"bench press":            "barbell bench press",
	"paused bench press":     "barbell bench press",
	"incline bench press":    "barbell incline bench press",
	"incline barbell":        "barbell incline bench press",
	"decline bench press":    "barbell decline bench press",
	"dumbbell bench press":   "dumbbell bench press",
	"dumbbell press":         "dumbbell bench press",
	"close grip bench":       "barbell close-grip bench press",
	"close grip bench press": "barbell close-grip bench press",
	"dips":                   "chest dip",
	"dip":                    "chest dip",
	"weighted dip":           "weighted straight bar dip",
	"machine chest press":    "lever chest press",
	"chest press":            "lever chest press",
	"push-up":                "push-up",
	"push up":                "push-up",
	"pushup":                 "push-up",

	// Vertical press
	"overhead press":          "barbell standing close grip military press",
	"ohp":                     "barbell standing close grip military press",
	"military press":          "barbell standing close grip military press",
	"shoulder press":          "barbell standing close grip military press",
	"seated overhead press":   "barbell seated overhead press",
	"push press":              "dumbbell push press",
	"dumbbell shoulder press": "dumbbell seated shoulder press",
	"arnold press":            "dumbbell arnold press",
	"lateral raise":           "dumbbell lateral raise",
	"side raise":              "dumbbell lateral raise",

	// Squat
	"squat":                 "barbell full squat",
	"back squat":            "barbell full squat",
	"barbell squat":         "barbell full squat",
	"front squat":           "barbell front squat",
	"hack squat":            "sled hack squat",
	"pendulum squat":        "sled hack squat",
	"leg press":             "smith leg press",
	"bulgarian split squat": "dumbbell single leg split squat",
	"split squat":           "dumbbell single leg split squat",
	"lunge":                 "dumbbell lunge",
	"leg extension":         "lever leg extension",

	// Hinge
	"deadlift":              "barbell deadlift",
	"conventional deadlift": "barbell deadlift",
	"sumo deadlift":         "barbell sumo deadlift",
	"rdl":                   "barbell romanian deadlift",
	"romanian deadlift":     "barbell romanian deadlift",
	"stiff leg deadlift":    "barbell straight leg deadlift",
	"trap bar deadlift":     "trap bar deadlift",
	"hip thrust":            "barbell glute bridge",
	"good morning":          "barbell good morning",
	"leg curl":              "lever seated leg curl",
	"back extension":        "hyperextension",

	// Vertical pull
	"pull-up":          "pull-up",
	"pullup":           "pull-up",
	"pull up":          "pull-up",
	"weighted pull-up": "weighted pull-up",
	"chin-up":          "chin-up",
	"chinup":           "chin-up",
	"lat pulldown":     "cable pulldown",
	"pulldown":         "cable pulldown",

	// Horizontal pull
	"barbell row":         "barbell bent over row",
	"bent over row":       "barbell bent over row",
	"row":                 "barbell bent over row",
	"pendlay row":         "barbell pendlay row",
	"chest-supported row": "lever t bar row",
	"chest supported row": "lever t bar row",
	"t bar row":           "lever t bar row",
	"dumbbell row":        "dumbbell bent over row",
	"seated cable row":    "cable seated row",
	"cable row":           "cable seated row",
	"face pull":           "cable rear delt row (with rope)",

	// Arms and accessories the parser sees constantly
	"bicep curl":        "barbell curl",
	"biceps curl":       "barbell curl",
	"curl":              "barbell curl",
	"hammer curl":       "dumbbell hammer curl",
	"preacher curl":     "barbell preacher curl",
	"tricep extension":  "cable alternate triceps extension",
	"skull crusher":     "barbell lying triceps extension",
	"skull crushers":    "barbell lying triceps extension",
	"tricep pushdown":   "cable pushdown",
	"pushdown":          "cable pushdown",
	"shrug":             "barbell shrug",
	"calf raise":        "lever standing calf raise",
	"plank":             "front plank with twist",
	"hanging leg raise": "hanging leg raise",
	"leg raise":         "lying leg raise flat bench",
	"crunch":            "crunch floor",
	"sit-up":            "3/4 sit-up",
	"russian twist":     "russian twist",
}

// equipmentCandidates maps the app's six equipment values onto the dataset's
// twenty-eight. The app records how a lift was loaded; the dataset records what
// piece of gear it is named for, and those are close but not the same question.
var equipmentCandidates = map[string][]string{
	"barbell":    {"barbell", "ez barbell", "olympic barbell", "smith machine", "trap bar"},
	"dumbbell":   {"dumbbell", "kettlebell"},
	"cable":      {"cable", "band", "resistance band", "rope"},
	"machine":    {"leverage machine", "sled machine", "smith machine", "stepmill machine", "elliptical machine", "stationary bike", "skierg machine", "upper body ergometer"},
	"bodyweight": {"body weight", "weighted", "assisted"},
}

// normalize delegates to the library so the two cannot disagree about what
// counts as the same name.
func normalize(s string) string { return exercises.Normalize(s) }

func tokens(s string) []string { return strings.Fields(normalize(s)) }

// lookup resolves a logged exercise to a library entry.
func lookup(name, equipment string) (Entry, bool) {
	key := normalize(name)
	if key == "" {
		return Entry{}, false
	}

	// 1. Hand-written alias.
	if target, ok := aliases[key]; ok {
		if e, ok := pick(exercises.ByName(target), equipment); ok {
			return e, true
		}
	}

	// 2. Exact name match.
	if e, ok := pick(exercises.ByName(key), equipment); ok {
		return e, true
	}

	// 3. Equipment-prefixed match: "bench press" logged with a barbell becomes
	// "barbell bench press", which is how the dataset would have named it.
	for _, eq := range equipmentCandidates[strings.ToLower(equipment)] {
		if e, ok := pick(exercises.ByName(eq+" "+name), equipment); ok {
			return e, true
		}
	}

	// 4. Token containment. Every word of the logged name must appear in the
	// candidate, so "row" can match "barbell bent over row" but "bent over row"
	// can never match a bare "row". Ties break toward the shortest name and
	// then toward matching equipment, which prefers the plain movement over an
	// exotic variant of it.
	return fuzzy(key, equipment)
}

// pick chooses among entries sharing a name, preferring one whose equipment is
// consistent with how the set was actually logged.
func pick(candidates []*exercises.Exercise, equipment string) (Entry, bool) {
	if len(candidates) == 0 {
		return Entry{}, false
	}
	if len(candidates) == 1 {
		return entryOf(*candidates[0]), true
	}
	allowed := equipmentCandidates[strings.ToLower(equipment)]
	for _, e := range candidates {
		for _, eq := range allowed {
			if e.Equipment == eq {
				return entryOf(*e), true
			}
		}
	}
	return entryOf(*candidates[0]), true
}

func fuzzy(key, equipment string) (Entry, bool) {
	want := tokens(key)
	if len(want) == 0 {
		return Entry{}, false
	}
	allowed := equipmentCandidates[strings.ToLower(equipment)]
	eqOK := func(e Entry) bool {
		for _, q := range allowed {
			if e.Equipment == q {
				return true
			}
		}
		return false
	}

	var best []Entry
	for _, e := range exercises.All() {
		hay := " " + normalize(e.Name) + " "
		ok := true
		for _, t := range want {
			if !strings.Contains(hay, " "+t+" ") {
				ok = false
				break
			}
		}
		if ok {
			best = append(best, entryOf(e))
		}
	}
	if len(best) == 0 {
		return Entry{}, false
	}
	sort.SliceStable(best, func(i, j int) bool {
		ei, ej := eqOK(best[i]), eqOK(best[j])
		if ei != ej {
			return ei
		}
		li, lj := len(best[i].Name), len(best[j].Name)
		if li != lj {
			return li < lj
		}
		return best[i].Name < best[j].Name
	})
	return best[0], true
}
