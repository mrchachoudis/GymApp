// Package rank computes the engagement score.
//
// Design constraints, because a rank you designed yourself is only interesting
// while you cannot see the lever that moves it:
//
//  1. Junk volume must not move it. Consistency counts qualifying training
//     days, not sets, and caps the credit any single week can earn.
//  2. A deload must not crater it. Strength reads the best estimate inside a
//     trailing window, so one light week changes nothing.
//  3. Not training must move it down. Consistency decays on a 28-day window,
//     which is the part that actually creates the pull to train.
//  4. It must not be possible to reach the top by logging fake sets, because
//     the strength half is measured against bodyweight standards, and lying to
//     it means lying in the same place the coach reads from.
package rank

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/mrcha/gymlogger/internal/model"
	"github.com/mrcha/gymlogger/internal/store"
)

// Tier ladder. Seven tiers with three divisions each gives 21 steps, so
// progress is visible on a weekly timescale rather than a yearly one.
var tiers = []string{"Iron", "Bronze", "Silver", "Gold", "Platinum", "Diamond", "Mythic"}

const divisionsPerTier = 3

type Rank struct {
	Score       float64 `json:"score"`       // 0..100
	Tier        string  `json:"tier"`        // e.g. "Gold II"
	TierIndex   int     `json:"tier_index"`  // 0..20
	Consistency float64 `json:"consistency"` // 0..100
	Strength    float64 `json:"strength"`    // 0..100
	NextTier    string  `json:"next_tier"`
	ToNext      float64 `json:"to_next"` // score points to the next division
	Detail      Detail  `json:"detail"`
}

type Detail struct {
	QualifyingDays28 int            `json:"qualifying_days_28"`
	LongestGapDays   int            `json:"longest_gap_days"`
	RestDiscipline   float64        `json:"rest_discipline"`
	LiftStandards    []LiftStandard `json:"lift_standards"`
	Notes            []string       `json:"notes,omitempty"`
}

type LiftStandard struct {
	Name       string  `json:"name"`
	BestE1RM   float64 `json:"best_e1rm_kg"`
	BwMultiple float64 `json:"bw_multiple"`
	Score      float64 `json:"score"`
	Level      string  `json:"level"`
}

// standard holds bodyweight multiples for novice / intermediate / advanced /
// elite on a given lift. These are conventional strength-standard figures for
// a trained male lifter; they exist to place a score on a curve, not to be a
// verdict on anyone.
type standard struct {
	novice, intermediate, advanced, elite float64
	// bodyweightBased lifts already include the lifter's mass in the load, so
	// the multiple is (bodyweight + added) / bodyweight.
	bodyweightBased bool
}

var standards = map[string]standard{
	"bench press":    {0.75, 1.00, 1.25, 1.50, false},
	"squat":          {1.00, 1.50, 2.00, 2.50, false},
	"deadlift":       {1.25, 1.75, 2.25, 2.75, false},
	"rdl":            {1.00, 1.50, 1.90, 2.30, false},
	"shoulder press": {0.50, 0.70, 0.90, 1.10, false},
	"overhead press": {0.50, 0.70, 0.90, 1.10, false},
	"dips":           {1.00, 1.25, 1.50, 1.80, true},
	"pull-up":        {1.00, 1.20, 1.45, 1.70, true},
	"chin-up":        {1.00, 1.25, 1.50, 1.75, true},
	"barbell row":    {0.75, 1.00, 1.25, 1.50, false},
}

type Calculator struct {
	st *store.Store

	// ConsistencyWeight and StrengthWeight must sum to 1.
	ConsistencyWeight float64
	StrengthWeight    float64

	// WindowDays is the consistency lookback. 28 days is long enough that one
	// bad week does not wreck the score and short enough that a month off does.
	WindowDays int
	// TargetDaysPerWeek is the training frequency worth full consistency credit.
	TargetDaysPerWeek float64
	// MaxCountedDaysPerWeek stops seven-days-a-week from scoring above four,
	// so the rank never rewards never resting.
	MaxCountedDaysPerWeek int
	// MinWorkingSets is what makes a logged day count. Below it, the day was
	// a token entry rather than a session.
	MinWorkingSets int
	// StrengthWindowDays is the lookback for best e1RM, which is what keeps a
	// deload from dropping the rank.
	StrengthWindowDays int
}

func NewCalculator(st *store.Store) *Calculator {
	return &Calculator{
		st:                    st,
		ConsistencyWeight:     0.5,
		StrengthWeight:        0.5,
		WindowDays:            28,
		TargetDaysPerWeek:     4,
		MaxCountedDaysPerWeek: 5,
		MinWorkingSets:        6,
		StrengthWindowDays:    90,
	}
}

func (c *Calculator) Compute(ctx context.Context, asOf time.Time) (*Rank, error) {
	bodyweight := c.st.BodyweightKg(ctx)
	localDate := c.st.LocalDate(asOf)

	cons, consDetail, err := c.consistency(ctx, localDate)
	if err != nil {
		return nil, fmt.Errorf("consistency: %w", err)
	}
	str, lifts, err := c.strength(ctx, localDate, bodyweight)
	if err != nil {
		return nil, fmt.Errorf("strength: %w", err)
	}

	score := c.ConsistencyWeight*cons + c.StrengthWeight*str
	score = clamp(score, 0, 100)

	r := &Rank{
		Score:       round1(score),
		Consistency: round1(cons),
		Strength:    round1(str),
		Detail:      consDetail,
	}
	r.Detail.LiftStandards = lifts
	switch {
	case len(lifts) == 0:
		r.Detail.Notes = append(r.Detail.Notes,
			"no tracked main lifts yet, strength half is unscored until bench, squat, rdl, dips or pull-ups appear")
	case len(lifts) < coverageTarget:
		r.Detail.Notes = append(r.Detail.Notes, fmt.Sprintf(
			"only %d of %d tracked lifts have recent data, so strength is scaled down until the others appear",
			len(lifts), coverageTarget))
	}

	r.TierIndex, r.Tier, r.NextTier, r.ToNext = tierFor(score)
	return r, nil
}

// consistency scores training frequency over the trailing window, with three
// corrections: only qualifying days count, each week's credit is capped, and
// a long gap costs points directly.
func (c *Calculator) consistency(ctx context.Context, localDate string) (float64, Detail, error) {
	var d Detail
	end, err := time.Parse("2006-01-02", localDate)
	if err != nil {
		return 0, d, err
	}
	start := end.AddDate(0, 0, -c.WindowDays+1)

	rows, err := c.st.DB().QueryContext(ctx, `
		SELECT local_date,
		       SUM(CASE WHEN set_type = 'working' THEN 1 ELSE 0 END) AS working_sets
		FROM v_sets
		WHERE local_date >= ? AND local_date <= ?
		GROUP BY local_date
		ORDER BY local_date`,
		start.Format("2006-01-02"), localDate)
	if err != nil {
		return 0, d, err
	}
	defer rows.Close()

	var days []time.Time
	for rows.Next() {
		var ds string
		var sets int
		if err := rows.Scan(&ds, &sets); err != nil {
			return 0, d, err
		}
		if sets < c.MinWorkingSets {
			continue // a token entry is not a session
		}
		day, err := time.Parse("2006-01-02", ds)
		if err != nil {
			continue
		}
		days = append(days, day)
	}
	if err := rows.Err(); err != nil {
		return 0, d, err
	}
	d.QualifyingDays28 = len(days)

	// Per-week capping. Training seven days straight earns the same credit as
	// five, so the rank never argues for skipping rest.
	weekCounts := map[int]int{}
	for _, day := range days {
		week := int(day.Sub(start).Hours() / 24 / 7)
		weekCounts[week]++
	}
	counted := 0.0
	for _, n := range weekCounts {
		counted += float64(min(n, c.MaxCountedDaysPerWeek))
	}

	weeks := float64(c.WindowDays) / 7.0
	target := c.TargetDaysPerWeek * weeks
	base := 0.0
	if target > 0 {
		base = clamp(counted/target, 0, 1) * 100
	}

	// Gap penalty. Frequency alone hides the pattern where three hard weeks
	// are followed by ten days of nothing.
	d.LongestGapDays = longestGap(days, start, end)
	gapPenalty := 0.0
	if d.LongestGapDays > gapGraceDays {
		// Capped so a long layoff floors the score rather than driving it
		// negative, which would make every layoff look identical.
		gapPenalty = clamp(float64(d.LongestGapDays-gapGraceDays)*3, 0, 40)
	}

	// Rest discipline. Zero rest days inside any 7-day stretch is a problem
	// the coach flags, so the rank should not be paying for it either.
	d.RestDiscipline = restDiscipline(days, start, end)
	restPenalty := (1 - d.RestDiscipline) * 10

	score := clamp(base-gapPenalty-restPenalty, 0, 100)
	return score, d, nil
}

// strength scores absolute strength against bodyweight standards, using the
// best estimate inside the trailing window so a deload costs nothing.
func (c *Calculator) strength(ctx context.Context, localDate string, bodyweight float64) (float64, []LiftStandard, error) {
	end, err := time.Parse("2006-01-02", localDate)
	if err != nil {
		return 0, nil, err
	}
	start := end.AddDate(0, 0, -c.StrengthWindowDays)

	rows, err := c.st.DB().QueryContext(ctx, `
		SELECT name, load_basis, COALESCE(weight_kg, 0) AS w, COALESCE(reps, reps_low, 0) AS r
		FROM v_sets
		WHERE set_type = 'working'
		  AND local_date >= ? AND local_date <= ?
		  AND COALESCE(reps, reps_low, 0) BETWEEN 1 AND 8`,
		start.Format("2006-01-02"), localDate)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	best := map[string]float64{}
	for rows.Next() {
		var name, basis string
		var w float64
		var r int
		if err := rows.Scan(&name, &basis, &w, &r); err != nil {
			return 0, nil, err
		}
		std, ok := standards[name]
		if !ok {
			continue
		}
		// Bodyweight lifts carry the lifter's mass; the bar does not know the
		// difference, and neither should the standard.
		load := w
		if std.bodyweightBased {
			load = bodyweight + w
		} else if model.LoadBasis(basis) == model.BasisPerSide || model.LoadBasis(basis) == model.BasisPerLimb {
			load = w * 2
		}
		if e, ok := model.Epley1RM(load, r); ok && e > best[name] {
			best[name] = e
		}
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	if len(best) == 0 {
		return 0, nil, nil
	}

	var out []LiftStandard
	var total float64
	for name, e1rm := range best {
		std := standards[name]
		mult := e1rm / bodyweight
		s := scoreAgainst(std, mult)
		out = append(out, LiftStandard{
			Name:       name,
			BestE1RM:   round1(e1rm),
			BwMultiple: round2(mult),
			Score:      round1(s),
			Level:      levelFor(std, mult),
		})
		total += s
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })

	// Coverage guard. Averaging over whatever happens to be in the window lets
	// one strong lift carry the whole score: bench 100x5 and nothing else
	// should not read as elite. Full credit needs at least coverageTarget
	// tracked lifts, which in practice means a push, a pull and a leg movement.
	avg := total / float64(len(out))
	coverage := float64(min(len(out), coverageTarget)) / float64(coverageTarget)
	return avg * coverage, out, nil
}

// coverageTarget is how many distinct tracked main lifts earn full strength
// credit.
const coverageTarget = 3

// gapGraceDays is how long a break can run before it costs anything. Four days
// covers a normal rest pattern and a bad week at work.
const gapGraceDays = 4

// scoreAgainst places a bodyweight multiple on a 0..100 curve by interpolating
// between the four standard bands.
func scoreAgainst(s standard, mult float64) float64 {
	switch {
	case mult <= 0:
		return 0
	case mult < s.novice:
		return lerp(0, 25, mult/s.novice)
	case mult < s.intermediate:
		return lerp(25, 50, (mult-s.novice)/(s.intermediate-s.novice))
	case mult < s.advanced:
		return lerp(50, 75, (mult-s.intermediate)/(s.advanced-s.intermediate))
	case mult < s.elite:
		return lerp(75, 95, (mult-s.advanced)/(s.elite-s.advanced))
	default:
		// Past elite the curve flattens rather than stopping, so there is
		// always somewhere left to go.
		return clamp(95+(mult-s.elite)/s.elite*20, 95, 100)
	}
}

func levelFor(s standard, mult float64) string {
	switch {
	case mult < s.novice:
		return "untrained"
	case mult < s.intermediate:
		return "novice"
	case mult < s.advanced:
		return "intermediate"
	case mult < s.elite:
		return "advanced"
	default:
		return "elite"
	}
}

func tierFor(score float64) (idx int, name, next string, toNext float64) {
	steps := len(tiers) * divisionsPerTier // 21
	span := 100.0 / float64(steps)
	idx = int(score / span)
	if idx >= steps {
		idx = steps - 1
	}
	if idx < 0 {
		idx = 0
	}

	tier := tiers[idx/divisionsPerTier]
	// Divisions count down inside a tier, so Gold III is the entry and
	// Gold I is the top, which is the convention people already know.
	div := divisionsPerTier - (idx % divisionsPerTier)
	name = fmt.Sprintf("%s %s", tier, roman(div))

	if idx+1 >= steps {
		return idx, name, "", 0
	}
	nTier := tiers[(idx+1)/divisionsPerTier]
	nDiv := divisionsPerTier - ((idx + 1) % divisionsPerTier)
	next = fmt.Sprintf("%s %s", nTier, roman(nDiv))
	toNext = round1(float64(idx+1)*span - score)
	return idx, name, next, toNext
}

func roman(n int) string {
	switch n {
	case 1:
		return "I"
	case 2:
		return "II"
	default:
		return "III"
	}
}

// ---------- persistence ----------

// Save writes a daily snapshot so the app can show a trend and, more
// importantly, so it can tell whether the rank changed since the last session.
func (c *Calculator) Save(ctx context.Context, asOf time.Time, r *Rank) error {
	blob, err := json.Marshal(r.Detail)
	if err != nil {
		return err
	}
	_, err = c.st.DB().ExecContext(ctx, `
		INSERT INTO rank_snapshots
		  (local_date, score, tier, tier_index, consistency, strength, detail_json, created_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(local_date) DO UPDATE SET
		  score = excluded.score, tier = excluded.tier, tier_index = excluded.tier_index,
		  consistency = excluded.consistency, strength = excluded.strength,
		  detail_json = excluded.detail_json`,
		c.st.LocalDate(asOf), r.Score, r.Tier, r.TierIndex, r.Consistency, r.Strength,
		string(blob), time.Now().UTC().Format(time.RFC3339))
	return err
}

// Previous returns the most recent snapshot strictly before the given date.
func (c *Calculator) Previous(ctx context.Context, asOf time.Time) (tier string, idx int, ok bool, err error) {
	row := c.st.DB().QueryRowContext(ctx, `
		SELECT tier, tier_index FROM rank_snapshots
		WHERE local_date < ? ORDER BY local_date DESC LIMIT 1`, c.st.LocalDate(asOf))
	err = row.Scan(&tier, &idx)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	return tier, idx, true, nil
}

// Delta renders a one-line rank change for the coach context, or "" when
// nothing worth mentioning happened.
func (c *Calculator) Delta(ctx context.Context, asOf time.Time, cur *Rank) (string, error) {
	prevTier, prevIdx, ok, err := c.Previous(ctx, asOf)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	switch {
	case cur.TierIndex > prevIdx:
		return fmt.Sprintf("promoted from %s to %s", prevTier, cur.Tier), nil
	case cur.TierIndex < prevIdx:
		return fmt.Sprintf("dropped from %s to %s", prevTier, cur.Tier), nil
	case cur.ToNext > 0 && cur.ToNext <= 2:
		return fmt.Sprintf("%.1f points from %s", cur.ToNext, cur.NextTier), nil
	}
	return "", nil
}

// ---------- helpers ----------

// longestGap measures the longest stretch of not training, counting gaps
// between sessions and the gap since the last one.
//
// It deliberately ignores the stretch between the window start and the first
// session. That leading emptiness usually means the log simply does not go
// back that far, and charging a new user a gap penalty for not having a
// training history yet pins the score at zero on day one.
func longestGap(days []time.Time, _, end time.Time) int {
	if len(days) == 0 {
		return 0
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	longest := 0
	for i := 1; i < len(days); i++ {
		if g := int(days[i].Sub(days[i-1]).Hours()/24) - 1; g > longest {
			longest = g
		}
	}
	if g := int(end.Sub(days[len(days)-1]).Hours() / 24); g > longest {
		longest = g
	}
	return longest
}

// restDiscipline is the fraction of 7-day windows that contained at least one
// non-training day. 1.0 means he never went a full week without a break.
func restDiscipline(days []time.Time, start, end time.Time) float64 {
	set := map[string]bool{}
	for _, d := range days {
		set[d.Format("2006-01-02")] = true
	}
	windows, good := 0, 0
	for d := start; !d.After(end.AddDate(0, 0, -6)); d = d.AddDate(0, 0, 1) {
		windows++
		trained := 0
		for i := 0; i < 7; i++ {
			if set[d.AddDate(0, 0, i).Format("2006-01-02")] {
				trained++
			}
		}
		if trained < 7 {
			good++
		}
	}
	if windows == 0 {
		return 1
	}
	return float64(good) / float64(windows)
}

func lerp(a, b, t float64) float64 { return a + (b-a)*clamp(t, 0, 1) }

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
