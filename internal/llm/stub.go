package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Stub is a deterministic Client that needs no API key and no network.
//
// It is not a toy: the parser half is a real heuristic parser that handles the
// common "bench 100 x 5, 5, 4" shapes. That makes it useful twice over, first
// as a way to exercise the whole pipeline for free, and later as a degraded
// fallback when OpenCode is down and Mario still wants his set logged.
type Stub struct{}

func (Stub) Complete(_ context.Context, r CompletionRequest) (string, error) {
	if strings.Contains(r.System, "strength-training log parser") {
		return stubParse(r.User)
	}
	return stubCoach(r.User)
}

var (
	// Numbers with an optional trailing unit, captured with positions so the
	// parser can reason about which number is a load and which is a rep count.
	reNumber = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(kg|lbs?|kilos?|pounds?)?`)
	// Where the exercise name stops: the first digit, or a bodyweight marker.
	reNameEnd = regexp.MustCompile(`(?i)(\d|\bbw\b|\bbodyweight\b)`)
)

// splitNameAndLoad separates "incline db press 30 x 10, 10, 9" into the name
// and the part carrying the numbers.
//
// A lazy regex over the whole line does not work here: `([a-z][a-z\s]*?)`
// matches a single letter and hands the rest of the word to the load half,
// which is how "dips" became "d" plus "ips bw x 12".
func splitNameAndLoad(line string) (name, load string) {
	idx := reNameEnd.FindStringIndex(line)
	if idx == nil {
		return strings.TrimSpace(line), ""
	}
	name = strings.TrimSpace(line[:idx[0]])
	name = strings.Trim(name, " :-\t")
	return strings.ToLower(name), strings.TrimSpace(line[idx[0]:])
}

type numTok struct {
	val   float64
	unit  string
	start int
	end   int
}

func numbers(s string) []numTok {
	var out []numTok
	for _, m := range reNumber.FindAllStringSubmatchIndex(s, -1) {
		valStr := s[m[2]:m[3]]
		v, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			continue
		}
		unit := ""
		if m[4] >= 0 {
			unit = strings.ToLower(s[m[4]:m[5]])
		}
		out = append(out, numTok{val: v, unit: unit, start: m[0], end: m[1]})
	}
	return out
}

// parseLoadAndReps decides which numbers are load and which are reps.
//
// The ambiguity is real: "100 x 5" is a hundred kilos for five reps, while
// "3 x 10" is three sets of ten. The heuristics below are what a human uses
// without noticing - a unit settles it, a large first number settles it, and
// a small first number followed by exactly one larger number is a set count.
func parseLoadAndReps(load string, bodyweight bool) (weight *float64, unit string, reps []int) {
	toks := numbers(load)
	if len(toks) == 0 {
		return nil, "", nil
	}

	first := toks[0]
	hasUnit := first.unit != ""
	normUnit := "kg"
	if strings.HasPrefix(first.unit, "lb") || strings.HasPrefix(first.unit, "pound") {
		normUnit = "lb"
	}

	// "3 x 10": a small leading number with exactly one number after it, no
	// unit, is a set count rather than a load.
	if !hasUnit && !bodyweight && len(toks) == 2 && first.val <= 10 && toks[1].val >= first.val &&
		strings.ContainsAny(load[first.end:toks[1].start], "x*") {
		n := int(first.val)
		r := int(toks[1].val)
		for i := 0; i < n && i < 20; i++ {
			reps = append(reps, r)
		}
		return nil, "", reps
	}

	start := 0
	if !bodyweight && (hasUnit || first.val >= 25 || len(toks) > 1) {
		w := first.val
		weight = &w
		unit = normUnit
		start = 1
	}

	for _, t := range toks[start:] {
		if t.val > 0 && t.val < 200 {
			reps = append(reps, int(t.val))
		}
	}
	if len(reps) == 0 && weight != nil {
		// A lone number with no reps was a rep count, not a load.
		r := int(*weight)
		return nil, "", []int{r}
	}
	return weight, unit, reps
}

func stubParse(user string) (string, error) {
	sess := map[string]any{
		"session_type":          nil,
		"exercises":             []any{},
		"cardio":                []any{},
		"subjective":            map[string]any{"energy": nil, "pain": nil, "sleep_or_shift": nil, "notes": nil},
		"assumptions":           []any{},
		"unresolved_references": []any{},
		"unparsed":              nil,
	}

	lower := strings.ToLower(user)
	if strings.Contains(lower, "pain") || strings.Contains(lower, "tweak") || strings.Contains(lower, "hurt") {
		sess["subjective"].(map[string]any)["pain"] = extractSentenceWith(user, "pain", "tweak", "hurt")
	}
	if strings.Contains(lower, "night shift") || strings.Contains(lower, "no sleep") || strings.Contains(lower, "nightshift") {
		sess["subjective"].(map[string]any)["sleep_or_shift"] = extractSentenceWith(user, "night shift", "no sleep", "nightshift")
	}

	var exercises []any
	var assumptions []any
	var unparsed []string

	for _, line := range splitEntries(user) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, load := splitNameAndLoad(line)
		if name == "" || load == "" {
			unparsed = append(unparsed, line)
			continue
		}

		equipment := guessEquipment(name)
		lowerLoad := strings.ToLower(load)
		bw := strings.Contains(lowerLoad, "bw") ||
			strings.Contains(lowerLoad, "bodyweight") || equipment == "bodyweight"

		basis := "total"
		switch {
		case bw:
			basis = "bodyweight"
		case equipment == "dumbbell":
			basis = "per_side"
			assumptions = append(assumptions, map[string]any{
				"path": fmt.Sprintf("exercises[%d]", len(exercises)), "field": "load_basis",
				"value": "per_side", "reason": "dumbbell exercise with a bare number",
			})
		}

		w, unitStr, reps := parseLoadAndReps(load, bw)
		if len(reps) == 0 {
			unparsed = append(unparsed, line)
			continue
		}

		var weight any
		var unit any
		if w != nil {
			weight = *w
			unit = unitStr
			if bw {
				// A load alongside a bodyweight marker means added weight.
				basis = "bodyweight_plus"
			}
		}

		var sets []any
		for _, rp := range reps {
			sets = append(sets, map[string]any{
				"set_type": "working", "weight": weight, "unit": unit,
				"load_basis": basis, "reps": rp, "reps_uncertain": false,
				"reps_low": nil, "reps_high": nil,
				"to_failure": strings.Contains(lowerLoad, "failure"),
				"clean_reps": nil, "rpe": nil, "notes": nil,
			})
		}
		exercises = append(exercises, map[string]any{
			"name": name, "raw_name": name, "equipment": equipment, "sets": sets,
		})
	}

	if exercises != nil {
		sess["exercises"] = exercises
	}
	if assumptions != nil {
		sess["assumptions"] = assumptions
	}
	if len(unparsed) > 0 {
		sess["unparsed"] = strings.Join(unparsed, " | ")
	}

	b, err := json.Marshal(sess)
	return string(b), err
}

// splitEntries breaks freeform text into candidate exercise lines. Users type
// newlines, semicolons, and "then" interchangeably.
func splitEntries(s string) []string {
	repl := strings.NewReplacer(";", "\n", " then ", "\n", ". ", "\n")
	return strings.Split(repl.Replace(s), "\n")
}

func guessEquipment(name string) string {
	switch {
	case strings.Contains(name, "db ") || strings.Contains(name, "dumbbell") ||
		strings.Contains(name, "hammer") || strings.Contains(name, "zottman") ||
		strings.Contains(name, "lateral raise"):
		return "dumbbell"
	case strings.Contains(name, "cable") || strings.Contains(name, "pushdown") ||
		strings.Contains(name, "pulldown"):
		return "cable"
	case strings.Contains(name, "dip") || strings.Contains(name, "pull-up") ||
		strings.Contains(name, "pull up") || strings.Contains(name, "chin"):
		return "bodyweight"
	case strings.Contains(name, "machine") || strings.Contains(name, "extension") ||
		strings.Contains(name, "curl machine"):
		return "machine"
	case strings.Contains(name, "bench") || strings.Contains(name, "squat") ||
		strings.Contains(name, "rdl") || strings.Contains(name, "deadlift") ||
		strings.Contains(name, "ez"):
		return "barbell"
	}
	return "other"
}

func extractSentenceWith(s string, needles ...string) string {
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '.' || r == '\n' || r == ';'
	}) {
		lp := strings.ToLower(part)
		for _, n := range needles {
			if strings.Contains(lp, n) {
				return strings.TrimSpace(part)
			}
		}
	}
	return ""
}

// stubCoach produces a plausible reply from the context block so the UI and
// the post-processing filters can be exercised without a paid call.
func stubCoach(user string) (string, error) {
	var payload struct {
		Context struct {
			Flags []struct {
				Type   string `json:"type"`
				Detail string `json:"detail"`
			} `json:"flags"`
			LiftHistory []struct {
				Name        string `json:"name"`
				TopSetToday string `json:"top_set_today"`
				IsWeightPR  bool   `json:"is_weight_pr"`
				IsRepPR     bool   `json:"is_rep_pr"`
				IsBaseline  bool   `json:"is_baseline"`
			} `json:"lift_history"`
			Suggestion string `json:"suggestion"`
		} `json:"CONTEXT"`
	}
	_ = json.Unmarshal([]byte(user), &payload)

	var b strings.Builder
	b.WriteString("Logged.\n")

	for _, l := range payload.Context.LiftHistory {
		switch {
		case l.IsWeightPR:
			fmt.Fprintf(&b, "%s at %s is a weight PR. That one counts.\n", l.Name, l.TopSetToday)
		case l.IsRepPR:
			fmt.Fprintf(&b, "%s %s, rep PR on the same load.\n", l.Name, l.TopSetToday)
		case l.IsBaseline:
			fmt.Fprintf(&b, "%s %s goes down as your baseline.\n", l.Name, l.TopSetToday)
		}
	}

	if len(payload.Context.Flags) > 0 {
		fmt.Fprintf(&b, "Heads up: %s.\n", payload.Context.Flags[0].Detail)
	} else {
		b.WriteString("Nothing dumb in there, clean session.\n")
	}

	if payload.Context.Suggestion != "" {
		b.WriteString(payload.Context.Suggestion + "\n")
	}
	return strings.TrimSpace(b.String()), nil
}
