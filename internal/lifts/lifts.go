// Package lifts answers "how is this one lift going".
//
// The rank engine scores you across six patterns; this is the other view, the
// one you want when you are standing in front of a bar deciding what to load.
// It reads the same set rows and produces a single lift's history: the estimated
// max over the last twelve weeks, the records worth beating, and the next load
// to attempt.
//
// A lift is keyed on (name, equipment, load_basis), the same LiftKey the PR
// logic uses. Keying on the name alone would merge a 30 kg per-side dumbbell
// row with a 30 kg total machine row, and every number below would be wrong for
// both.
package lifts

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mrcha/gymlogger/internal/berserk"
	"github.com/mrcha/gymlogger/internal/model"
	"github.com/mrcha/gymlogger/internal/store"
)

// Summary is one tracked lift, for the list.
type Summary struct {
	Key       string  `json:"key"`
	Name      string  `json:"name"`
	Display   string  `json:"display"`
	Equipment string  `json:"equipment"`
	LoadBasis string  `json:"load_basis"`
	Sessions  int     `json:"sessions"`
	LastDate  string  `json:"last_date,omitempty"`
	BestE1RM  float64 `json:"best_e1rm_kg"`
}

// Point is one week's best estimated max.
type Point struct {
	Week  string  `json:"week"` // W1..W12, oldest first
	Date  string  `json:"date"`
	E1RM  float64 `json:"e1rm_kg"`
	Empty bool    `json:"empty"` // no qualifying set that week
}

// Record is one row of the record book.
type Record struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	Date    string `json:"date"`
	IsToday bool   `json:"is_today"`
}

// Detail is everything the lift screen renders.
type Detail struct {
	Summary
	// Level and BWMultiple are the two badges under the title. Level comes from
	// the rank engine's own reference load rather than a second set of
	// standards, so a lift cannot read "advanced" here and score 40 there.
	Level      string  `json:"level"`
	BWMultiple float64 `json:"bw_multiple"`
	Pattern    string  `json:"pattern,omitempty"`

	Series  []Point  `json:"series"`
	Records []Record `json:"records"`
	// SeriesNote explains an empty chart. "No qualifying sets" is accurate but
	// reads as though the app lost the work: a lifter who did dips for sets of
	// twelve wants to be told that twelve reps cannot produce an estimated max,
	// not that nothing was found.
	SeriesNote string `json:"series_note,omitempty"`

	// NextStep is the load to attempt next, or "" when there is no honest
	// suggestion to make.
	NextStep string `json:"next_step,omitempty"`
	// NextStepWhy explains it, because a number with no reason behind it is
	// the thing the whole app is built to avoid.
	NextStepWhy string `json:"next_step_why,omitempty"`
}

const (
	weeks          = 12
	historyDays    = weeks * 7
	recordLookback = 365 * 3
)

// keyOf renders the composite key used in URLs.
func keyOf(name, equipment, basis string) string {
	return strings.Join([]string{name, equipment, basis}, "|")
}

// ParseKey splits a key back into its parts.
func ParseKey(k string) (name, equipment, basis string, ok bool) {
	parts := strings.Split(k, "|")
	if len(parts) != 3 || parts[0] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func display(name, basis string) string {
	return model.LiftKey{
		Name:      name,
		Equipment: model.Equipment(""),
		LoadBasis: model.LoadBasis(basis),
	}.Display()
}

// List returns every lift with logged working sets, most recently trained first.
func List(ctx context.Context, st *store.Store, asOf time.Time) ([]Summary, error) {
	start := asOf.AddDate(0, 0, -recordLookback).Format("2006-01-02")
	rows, err := st.DB().QueryContext(ctx, `
		SELECT name, equipment, load_basis,
		       COUNT(DISTINCT local_date) AS sessions,
		       MAX(local_date) AS last_date
		FROM v_sets
		WHERE set_type = 'working' AND local_date >= ?
		GROUP BY name, equipment, load_basis
		ORDER BY last_date DESC, sessions DESC`, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Summary{}
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.Name, &s.Equipment, &s.LoadBasis, &s.Sessions, &s.LastDate); err != nil {
			return nil, err
		}
		s.Key = keyOf(s.Name, s.Equipment, s.LoadBasis)
		s.Display = display(s.Name, s.LoadBasis)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Best e1RM per lift, in one pass rather than a query each. The profile is
	// needed because a bodyweight lift carries the lifter's mass and would
	// otherwise score zero: a dip logged at "+0 kg" is not a zero-load set.
	p, err := berserk.LoadProfile(ctx, st, asOf)
	if err != nil {
		return nil, err
	}
	best, err := bestE1RMs(ctx, st, asOf, p.BodyweightKg)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].BestE1RM = round1(best[out[i].Key])
	}
	return out, nil
}

func bestE1RMs(ctx context.Context, st *store.Store, asOf time.Time, bodyweightKg float64) (map[string]float64, error) {
	start := asOf.AddDate(0, 0, -recordLookback).Format("2006-01-02")
	rows, err := st.DB().QueryContext(ctx, `
		SELECT name, equipment, load_basis, COALESCE(weight_kg, 0), COALESCE(reps, reps_low, 0)
		FROM v_sets
		WHERE set_type = 'working' AND local_date >= ?
		  AND COALESCE(reps, reps_low, 0) BETWEEN 1 AND 8`, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]float64{}
	for rows.Next() {
		var name, equip, basis string
		var w float64
		var reps int
		if err := rows.Scan(&name, &equip, &basis, &w, &reps); err != nil {
			return nil, err
		}
		if e, ok := model.Epley1RM(scoredLoad(basis, w, bodyweightKg), reps); ok {
			k := keyOf(name, equip, basis)
			if e > out[k] {
				out[k] = e
			}
		}
	}
	return out, rows.Err()
}

// totalLoad resolves per-side and per-limb entries to the load actually moved.
// It knows nothing about bodyweight; use scoredLoad where that matters.
func totalLoad(basis string, w float64) float64 {
	switch model.LoadBasis(basis) {
	case model.BasisPerSide, model.BasisPerLimb:
		return w * 2
	}
	return w
}

// scoredLoad is totalLoad plus the lifter's mass on bodyweight movements.
//
// Without it a dip or a pull-up logged at bodyweight resolves to zero load, no
// e1RM is produced, and the lift shows a best of 0.0 forever — which is exactly
// what the list did before this existed.
func scoredLoad(basis string, w, bodyweightKg float64) float64 {
	if isBodyweight(basis) {
		return bodyweightKg + w
	}
	return totalLoad(basis, w)
}

// Get assembles the detail view for one lift.
func Get(ctx context.Context, st *store.Store, asOf time.Time, name, equipment, basis string) (*Detail, error) {
	d := &Detail{Summary: Summary{
		Key:       keyOf(name, equipment, basis),
		Name:      name,
		Display:   display(name, basis),
		Equipment: equipment,
		LoadBasis: basis,
	}}

	if err := st.DB().QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT local_date), COALESCE(MAX(local_date), '')
		FROM v_sets
		WHERE set_type = 'working' AND name = ? AND equipment = ? AND load_basis = ?`,
		name, equipment, basis).Scan(&d.Sessions, &d.LastDate); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if d.Sessions == 0 {
		return nil, sql.ErrNoRows
	}

	profile, err := berserk.LoadProfile(ctx, st, asOf)
	if err != nil {
		return nil, err
	}

	if err := d.buildSeries(ctx, st, asOf, profile); err != nil {
		return nil, err
	}
	if err := d.buildRecords(ctx, st, asOf); err != nil {
		return nil, err
	}
	d.classify(profile)
	d.suggest()
	return d, nil
}

// buildSeries fills twelve weekly buckets with the best estimated max in each.
//
// Weeks with no qualifying set are marked empty rather than zeroed. A zero would
// draw as a bar at the floor and read as a collapse in strength, when what
// actually happened is that the lift was not trained.
func (d *Detail) buildSeries(ctx context.Context, st *store.Store, asOf time.Time, p berserk.Profile) error {
	start := asOf.AddDate(0, 0, -historyDays+1)
	rows, err := st.DB().QueryContext(ctx, `
		SELECT local_date, COALESCE(weight_kg, 0), COALESCE(reps, reps_low, 0)
		FROM v_sets
		WHERE set_type = 'working' AND name = ? AND equipment = ? AND load_basis = ?
		  AND local_date >= ? AND local_date <= ?
		  AND COALESCE(reps, reps_low, 0) BETWEEN 1 AND 8`,
		d.Name, d.Equipment, d.LoadBasis,
		start.Format("2006-01-02"), st.LocalDate(asOf))
	if err != nil {
		return err
	}
	defer rows.Close()

	bucket := make([]float64, weeks)
	any := false
	for rows.Next() {
		var ds string
		var w float64
		var reps int
		if err := rows.Scan(&ds, &w, &reps); err != nil {
			return err
		}
		day, err := time.Parse("2006-01-02", ds)
		if err != nil {
			continue
		}
		idx := int(day.Sub(start).Hours() / 24 / 7)
		if idx < 0 || idx >= weeks {
			continue
		}
		if e, ok := model.Epley1RM(scoredLoad(d.LoadBasis, w, p.BodyweightKg), reps); ok {
			any = true
			if e > bucket[idx] {
				bucket[idx] = e
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Distinguish "not trained" from "trained, but nothing Epley can score".
	if !any {
		var logged int
		if err := st.DB().QueryRowContext(ctx, `
			SELECT COUNT(*) FROM v_sets
			WHERE set_type = 'working' AND name = ? AND equipment = ? AND load_basis = ?
			  AND local_date >= ? AND local_date <= ?`,
			d.Name, d.Equipment, d.LoadBasis,
			start.Format("2006-01-02"), st.LocalDate(asOf)).Scan(&logged); err == nil && logged > 0 {
			d.SeriesNote = fmt.Sprintf(
				"%d sets logged, none at 8 reps or fewer. An estimated max needs a heavy set; "+
					"Epley drifts badly above eight.", logged)
		}
	}

	d.Series = make([]Point, weeks)
	for i := range bucket {
		wkStart := start.AddDate(0, 0, i*7)
		d.Series[i] = Point{
			Week:  fmt.Sprintf("W%d", i+1),
			Date:  wkStart.Format("2006-01-02"),
			E1RM:  round1(bucket[i]),
			Empty: bucket[i] == 0,
		}
		if bucket[i] > d.BestE1RM {
			d.BestE1RM = round1(bucket[i])
		}
	}
	return nil
}

func isBodyweight(basis string) bool {
	b := model.LoadBasis(basis)
	return b == model.BasisBW || b == model.BasisBWPlus
}

// buildRecords assembles the record book: the three numbers worth chasing.
func (d *Detail) buildRecords(ctx context.Context, st *store.Store, asOf time.Time) error {
	today := st.LocalDate(asOf)
	start := asOf.AddDate(0, 0, -recordLookback).Format("2006-01-02")

	rows, err := st.DB().QueryContext(ctx, `
		SELECT local_date, COALESCE(weight_kg, 0), COALESCE(reps, reps_low, 0)
		FROM v_sets
		WHERE set_type = 'working' AND name = ? AND equipment = ? AND load_basis = ?
		  AND local_date >= ? AND COALESCE(reps, reps_low, 0) > 0`,
		d.Name, d.Equipment, d.LoadBasis, start)
	if err != nil {
		return err
	}
	defer rows.Close()

	type set struct {
		date string
		w    float64
		reps int
	}
	var all []set
	volumeByDate := map[string]float64{}
	for rows.Next() {
		var s set
		if err := rows.Scan(&s.date, &s.w, &s.reps); err != nil {
			return err
		}
		all = append(all, s)
		volumeByDate[s.date] += totalLoad(d.LoadBasis, s.w) * float64(s.reps)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(all) == 0 {
		return nil
	}

	// Heaviest: the top load, and the most reps achieved at it.
	heaviest := all[0]
	for _, s := range all {
		if s.w > heaviest.w || (s.w == heaviest.w && s.reps > heaviest.reps) {
			heaviest = s
		}
	}
	d.Records = append(d.Records, Record{
		Label:   "HEAVIEST",
		Value:   fmt.Sprintf("%s x %d", trimKg(heaviest.w), heaviest.reps),
		Date:    prettyDate(heaviest.date, today),
		IsToday: heaviest.date == today,
	})

	// Most reps at the heaviest load, which is the honest "rep record": most
	// reps at ANY load would be won permanently by the lightest warmup-ish set
	// ever logged.
	bestReps, bestRepsDate := 0, ""
	for _, s := range all {
		if s.w == heaviest.w && s.reps > bestReps {
			bestReps, bestRepsDate = s.reps, s.date
		}
	}
	if bestReps > 0 {
		d.Records = append(d.Records, Record{
			Label:   fmt.Sprintf("MOST REPS @%s", trimKg(heaviest.w)),
			Value:   fmt.Sprintf("%d", bestReps),
			Date:    prettyDate(bestRepsDate, today),
			IsToday: bestRepsDate == today,
		})
	}

	// Best single-session volume.
	bestVol, bestVolDate := 0.0, ""
	for date, v := range volumeByDate {
		if v > bestVol {
			bestVol, bestVolDate = v, date
		}
	}
	if bestVol > 0 {
		d.Records = append(d.Records, Record{
			Label:   "BEST VOLUME",
			Value:   fmt.Sprintf("%.1f t", bestVol/1000),
			Date:    prettyDate(bestVolDate, today),
			IsToday: bestVolDate == today,
		})
	}
	return nil
}

// classify places the lift on the rank engine's own curve.
//
// Deliberately not a second set of strength standards: the reference load comes
// from berserk.Ref, so a lift cannot read "advanced" on this screen and score 40
// on the rank screen.
func (d *Detail) classify(p berserk.Profile) {
	if p.BodyweightKg > 0 && d.BestE1RM > 0 {
		d.BWMultiple = round2(d.BestE1RM / p.BodyweightKg)
	}
	pat, ok := berserk.PatternOf(d.Name)
	if !ok {
		return
	}
	d.Pattern = string(pat)
	ref := berserk.Ref(p, pat)
	if ref <= 0 || d.BestE1RM <= 0 {
		return
	}
	score := 100 * d.BestE1RM / ref
	switch {
	case score < 25:
		d.Level = "UNTRAINED"
	case score < 50:
		d.Level = "NOVICE"
	case score < 75:
		d.Level = "INTERMEDIATE"
	case score < 95:
		d.Level = "ADVANCED"
	default:
		d.Level = "ELITE"
	}
}

// suggest produces the next load to attempt.
//
// The rule is the conservative one the coach already follows: the top load only
// advances once the rep target has been met at it. Suggesting more weight after
// a session that missed reps is how an app talks someone into a stall.
func (d *Detail) suggest() {
	if len(d.Series) == 0 || d.BestE1RM <= 0 {
		return
	}
	var heaviest float64
	var atHeaviest int
	for _, r := range d.Records {
		if r.Label == "HEAVIEST" {
			fmt.Sscanf(r.Value, "%f x %d", &heaviest, &atHeaviest)
		}
	}
	if heaviest <= 0 {
		return
	}

	step := stepFor(d.Name, d.LoadBasis)
	const targetReps = 5
	if atHeaviest >= targetReps {
		d.NextStep = fmt.Sprintf("%s for 3 x %d", trimKg(heaviest+step), targetReps)
		d.NextStepWhy = fmt.Sprintf("%d reps at %s clears the target, so the load moves.",
			atHeaviest, trimKg(heaviest))
		return
	}
	d.NextStep = fmt.Sprintf("%s for 3 x %d", trimKg(heaviest), targetReps)
	d.NextStepWhy = fmt.Sprintf("Best at %s is %d reps. Hold the load until it is %d.",
		trimKg(heaviest), atHeaviest, targetReps)
}

// stepFor mirrors the increments the coach uses, so the two never disagree
// about what "add weight" means.
func stepFor(name, basis string) float64 {
	switch model.LoadBasis(basis) {
	case model.BasisPerSide:
		return 2.0
	case model.BasisPerLimb:
		return 2.5
	}
	switch name {
	case "squat", "back squat", "deadlift", "conventional deadlift", "sumo deadlift",
		"bench press", "barbell row", "front squat", "rdl", "romanian deadlift":
		return 2.5
	}
	return 1.25
}

func prettyDate(date, today string) string {
	if date == today {
		return "TODAY"
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return strings.ToUpper(d.Format("2 Jan"))
}

// trimKg drops a pointless trailing zero: 102.5 stays, 100.0 becomes 100.
func trimKg(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// SortByRecency is exposed for tests that want a stable order.
func SortByRecency(s []Summary) {
	sort.SliceStable(s, func(i, j int) bool { return s[i].LastDate > s[j].LastDate })
}
