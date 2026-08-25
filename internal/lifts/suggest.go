package lifts

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mrcha/gymlogger/internal/exercises"
	"github.com/mrcha/gymlogger/internal/store"
)

// Suggestion is one autocomplete entry.
//
// The important field is Snippet: text ready to drop into the log box, already
// carrying the load and reps from the last time this lift was trained. Typing
// "bench 100 x 5, 5, 5" every session is the actual friction in a freeform
// logger, and a name-only autocomplete removes almost none of it.
type Suggestion struct {
	Name      string `json:"name"`
	Display   string `json:"display"`
	Equipment string `json:"equipment"`
	LoadBasis string `json:"load_basis"`

	// Snippet is the insertable line. For a lift with history it reproduces the
	// last session's working sets; for a library movement it is just the name,
	// because inventing a weight nobody has lifted would be worse than nothing.
	Snippet string `json:"snippet"`
	// Detail is the human summary shown under the name: "100 kg x 5, 5, 4 · 20 Aug".
	Detail string `json:"detail,omitempty"`

	LastWeightKg float64 `json:"last_weight_kg,omitempty"`
	LastDate     string  `json:"last_date,omitempty"`
	Sessions     int     `json:"sessions,omitempty"`

	// Source is "history" or "library". History always ranks first: the lift you
	// did last Tuesday is far more likely than the 900th entry in a catalogue.
	Source string `json:"source"`
}

const suggestLimit = 12

// Suggest returns autocomplete entries for a partial exercise name.
//
// An empty query is not an error: it returns your most recent lifts, which is
// what makes the box useful before a single character is typed.
func Suggest(ctx context.Context, st *store.Store, asOf time.Time, q string, limit int) ([]Suggestion, error) {
	if limit <= 0 || limit > 50 {
		limit = suggestLimit
	}
	needle := exercises.Normalize(q)

	hist, err := historySuggestions(ctx, st, asOf, needle)
	if err != nil {
		return nil, err
	}

	out := hist
	if len(out) < limit {
		seen := map[string]bool{}
		for _, s := range out {
			seen[exercises.Normalize(s.Name)] = true
		}
		for _, e := range librarySuggestions(needle, limit-len(out), seen) {
			out = append(out, e)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// historySuggestions finds lifts already trained, newest first, and rebuilds
// the last session's line for each.
func historySuggestions(ctx context.Context, st *store.Store, asOf time.Time, needle string) ([]Suggestion, error) {
	start := asOf.AddDate(0, 0, -recordLookback).Format("2006-01-02")
	rows, err := st.DB().QueryContext(ctx, `
		SELECT name, equipment, load_basis,
		       COUNT(DISTINCT local_date) AS sessions,
		       MAX(local_date) AS last_date
		FROM v_sets
		WHERE set_type IN ('working','backoff','drop') AND local_date >= ?
		GROUP BY name, equipment, load_basis
		ORDER BY last_date DESC`, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type row struct {
		name, equip, basis, lastDate string
		sessions                     int
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.equip, &r.basis, &r.sessions, &r.lastDate); err != nil {
			return nil, err
		}
		if needle != "" && !matches(r.name, needle) {
			continue
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Suggestion, 0, len(all))
	for _, r := range all {
		s := Suggestion{
			Name: r.name, Display: display(r.name, r.basis),
			Equipment: r.equip, LoadBasis: r.basis,
			LastDate: r.lastDate, Sessions: r.sessions, Source: "history",
		}
		line, detail, weight, err := lastSessionLine(ctx, st, r.name, r.equip, r.basis, r.lastDate)
		if err != nil {
			return nil, err
		}
		s.Snippet, s.Detail, s.LastWeightKg = line, detail, weight
		out = append(out, s)
	}
	return out, nil
}

// lastSessionLine rebuilds the log line for a lift's most recent session.
//
// It reproduces what was actually done rather than a tidied version: three sets
// of 5, 5 and 4 come back as "5, 5, 4", because the point is to hand back a
// line that is already true and let the user edit the one number that changed.
func lastSessionLine(ctx context.Context, st *store.Store, name, equip, basis, date string) (snippet, detail string, weight float64, err error) {
	rows, err := st.DB().QueryContext(ctx, `
		SELECT COALESCE(weight_kg, 0), COALESCE(reps, reps_low, 0)
		FROM v_sets
		WHERE set_type IN ('working','backoff','drop')
		  AND name = ? AND equipment = ? AND load_basis = ? AND local_date = ?
		ORDER BY exercise_id, position`, name, equip, basis, date)
	if err != nil {
		return "", "", 0, err
	}
	defer rows.Close()

	var reps []int
	for rows.Next() {
		var w float64
		var r int
		if err := rows.Scan(&w, &r); err != nil {
			return "", "", 0, err
		}
		// The top load of the session is the one worth offering back. A backoff
		// set is not the number you are about to repeat.
		if w > weight {
			weight = w
		}
		if r > 0 {
			reps = append(reps, r)
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", 0, err
	}

	repList := joinReps(reps)
	switch {
	case isBodyweight(basis) && weight == 0:
		snippet = fmt.Sprintf("%s bw x %s", name, repList)
		detail = fmt.Sprintf("bodyweight x %s · %s", repList, prettyShort(date))
	case isBodyweight(basis):
		snippet = fmt.Sprintf("%s bw+%s x %s", name, trimKg(weight), repList)
		detail = fmt.Sprintf("bw+%s kg x %s · %s", trimKg(weight), repList, prettyShort(date))
	case weight > 0:
		snippet = fmt.Sprintf("%s %s x %s", name, trimKg(weight), repList)
		detail = fmt.Sprintf("%s kg x %s · %s", trimKg(weight), repList, prettyShort(date))
	default:
		snippet = name
		detail = prettyShort(date)
	}
	if len(reps) == 0 {
		snippet = name
	}
	return snippet, detail, weight, nil
}

func joinReps(reps []int) string {
	if len(reps) == 0 {
		return ""
	}
	parts := make([]string, len(reps))
	for i, r := range reps {
		parts[i] = fmt.Sprint(r)
	}
	return strings.Join(parts, ", ")
}

// librarySuggestions fills the remainder from the exercise library, for
// movements never logged before.
func librarySuggestions(needle string, room int, seen map[string]bool) []Suggestion {
	if room <= 0 || needle == "" {
		// With no query there is nothing sensible to offer from a catalogue of
		// 1,318 entries, so the box shows history only.
		return nil
	}
	res := exercises.Search(exercises.Query{Text: needle, Limit: room * 3})
	out := make([]Suggestion, 0, room)
	for _, e := range res.Exercises {
		if seen[exercises.Normalize(e.Name)] {
			continue
		}
		out = append(out, Suggestion{
			Name: e.Name, Display: e.Name, Equipment: e.Equipment,
			// No weight: this movement has never been logged, and offering a
			// number nobody has lifted is worse than offering none.
			Snippet: e.Name + " ",
			Detail:  e.Equipment + " · " + e.Target,
			Source:  "library",
		})
		if len(out) == room {
			break
		}
	}
	return out
}

// matches ranks a history entry against the query: prefix beats contains, and
// both beat nothing.
func matches(name, needle string) bool {
	n := exercises.Normalize(name)
	return strings.Contains(" "+n, " "+needle) || strings.Contains(n, needle)
}

func prettyShort(date string) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return strings.ToUpper(d.Format("2 Jan"))
}

// SortSuggestions puts history before library and, within history, the most
// recently trained first.
func SortSuggestions(s []Suggestion) {
	sort.SliceStable(s, func(i, j int) bool {
		if (s[i].Source == "history") != (s[j].Source == "history") {
			return s[i].Source == "history"
		}
		return s[i].LastDate > s[j].LastDate
	})
}
