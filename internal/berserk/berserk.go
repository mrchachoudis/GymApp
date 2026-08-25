package berserk

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/mrcha/gymlogger/internal/store"
)

// Rank is the complete computed standing. Everything the UI, the coach context
// and the ledger need comes from one struct, computed once per call, because
// the attributes are cross-referential -- the Berserk gate reads MASTERY,
// MASTERY reads movement scores, movement scores read the profile -- and
// recomputing pieces separately is how they drift apart.
type Rank struct {
	RS        float64 `json:"rs"`
	Rank      string  `json:"rank"`
	RankIndex int     `json:"rank_index"`
	// EligibleIndex is what the numbers justify before hysteresis. Stored so a
	// user who is mid-promotion can be told they are three days from it.
	EligibleIndex int `json:"eligible_index"`

	Attributes Attributes `json:"attributes"`
	Breakdown  Breakdown  `json:"breakdown"`
	Weights    Weights    `json:"weights"`

	Patterns []PatternScore `json:"patterns"`
	Berserk  BerserkStatus  `json:"berserk"`
	Blood    Blood          `json:"blood"`

	NextRank string  `json:"next_rank,omitempty"`
	ToNext   float64 `json:"to_next,omitempty"`
	// BandProgress is progress through the current RS band, 0..1. It is only
	// meaningful below Berserk; at the boundary the UI shows gates instead.
	BandProgress float64 `json:"band_progress"`
	// ShowGates tells the client to switch from the composite readout to the
	// gate readout. Erratum 1: a composite is actively misleading at the
	// boundary, because a lifter can sit above the old RS threshold and still
	// be one point of MASTERY from the rank.
	ShowGates bool `json:"show_gates"`

	ThreatLevel float64 `json:"threat_level"`
	Confidence  float64 `json:"confidence"`
	Journey     Journey `json:"journey"`

	WeakLink string   `json:"weak_link,omitempty"`
	Notes    []string `json:"notes,omitempty"`
	Version  string   `json:"version"`
}

// Journey is v1.1 21-22: rank is current capability, granted immediately from
// real numbers, while the journey is the progression the app actually
// witnessed. It starts at zero for everyone, including the lifter who walks in
// benching 140 kg, and that separation is what lets onboarding be generous
// without the rank becoming meaningless.
type Journey struct {
	Days     int     `json:"days"`
	Sessions int     `json:"sessions"`
	RSGain   float64 `json:"rs_gain"`
	StartRS  float64 `json:"start_rs"`
}

// Calculator computes and persists the rank.
type Calculator struct {
	st     *store.Store
	Ledger *Ledger
}

func NewCalculator(st *store.Store) *Calculator {
	return &Calculator{st: st, Ledger: NewLedger(st)}
}

// Compute evaluates the whole system as of a moment.
func (c *Calculator) Compute(ctx context.Context, asOf time.Time) (*Rank, error) {
	p, err := LoadProfile(ctx, c.st, asOf)
	if err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}

	lifts, err := scoreLifts(ctx, c.st, p, asOf)
	if err != nil {
		return nil, fmt.Errorf("lift scores: %w", err)
	}
	scores, err := patternScores(ctx, c.st, p, lifts)
	if err != nil {
		return nil, fmt.Errorf("pattern scores: %w", err)
	}

	var a Attributes
	var b Breakdown

	a.Might, b.HM, b.LOW = might(scores)
	if a.Dominion, err = dominion(ctx, c.st, p, asOf, &b); err != nil {
		return nil, fmt.Errorf("dominion: %w", err)
	}
	a.Frame = frame(p, &b)
	if a.Vigor, err = vigor(ctx, c.st, p, asOf, &b); err != nil {
		return nil, fmt.Errorf("vigor: %w", err)
	}
	if a.Discipline, err = discipline(ctx, c.st, p, asOf, &b); err != nil {
		return nil, fmt.Errorf("discipline: %w", err)
	}
	if a.Mastery, err = mastery(ctx, c.st, asOf, lifts, &b); err != nil {
		return nil, fmt.Errorf("mastery: %w", err)
	}

	w := p.GoalWeights()
	rs := w.MIGHT*a.Might + w.DOMINION*a.Dominion + w.FRAME*a.Frame +
		w.VIGOR*a.Vigor + w.DISCIPLINE*a.Discipline + w.MASTERY*a.Mastery
	// Scores above 100 are allowed on the attributes and feed the post-Berserk
	// economy, but RS caps at 100 for ladder purposes (v1.0 1).
	rs = clamp(rs, 0, 100)

	berserk := EvaluateBerserk(a, b, scores)
	eligible := EligibleRung(rs, scores, berserk)

	current, firstEver, err := c.currentRung(ctx, asOf)
	if err != nil {
		return nil, err
	}
	held, err := c.qualifyingDays(ctx, asOf, eligible.Index)
	if err != nil {
		return nil, err
	}
	granted := ApplyHysteresis(eligible, rs, current, held, firstEver)

	r := &Rank{
		RS:            round1(rs),
		Rank:          granted.Name,
		RankIndex:     granted.Index,
		EligibleIndex: eligible.Index,
		Attributes: Attributes{
			Might: round1(a.Might), Dominion: round1(a.Dominion), Frame: round1(a.Frame),
			Vigor: round1(a.Vigor), Discipline: round1(a.Discipline), Mastery: round1(a.Mastery),
		},
		Breakdown: roundBreakdown(b),
		Weights:   w,
		Patterns:  scores,
		Berserk:   berserk,
		Version:   Version,
	}

	// Erratum 1: show the composite below Berserk, the gates at the boundary.
	r.ShowGates = granted.Index >= blackSwordsmanIndex
	if granted.Index < berserkIndex {
		next := RungByIndex(granted.Index + 1)
		r.NextRank = next.Name
		r.ToNext = round1(math.Max(next.Floor-rs, 0))
		if granted.Ceil > granted.Floor && !math.IsInf(granted.Ceil, 1) {
			r.BandProgress = clamp((rs-granted.Floor)/(granted.Ceil+1-granted.Floor), 0, 1)
		}
	}

	r.ThreatLevel, err = c.threatLevel(ctx, asOf)
	if err != nil {
		return nil, err
	}
	r.Confidence = confidence(p, scores)
	if r.Journey, err = c.journey(ctx, asOf, rs); err != nil {
		return nil, err
	}
	r.WeakLink = weakLink(p, scores, a, w, granted, eligible)
	r.Notes = notes(p, scores, berserk)

	if err := c.awardBlood(ctx, asOf, p, b, scores, lifts); err != nil {
		return nil, fmt.Errorf("blood: %w", err)
	}
	if r.Blood, err = c.Ledger.Total(ctx, asOf); err != nil {
		return nil, err
	}
	return r, nil
}

// awardBlood settles everything the ledger can decide from state alone. PR
// awards are not here: they are events, and the pipeline hands them over as
// they happen.
func (c *Calculator) awardBlood(ctx context.Context, asOf time.Time, p Profile, b Breakdown,
	scores []PatternScore, lifts map[string]*liftScore) error {

	if err := c.Ledger.AwardWeeks(ctx, asOf, 8); err != nil {
		return err
	}
	if err := c.Ledger.AwardGateMonth(ctx, asOf); err != nil {
		return err
	}
	if err := c.Ledger.AwardVerification(ctx, asOf, scores); err != nil {
		return err
	}

	in := MilestoneInputs{Profile: p, Bd: b, Scores: scores, StrictPU: b.DReps * 15 / 100}
	if p.BodyweightKg > 0 {
		for name, ls := range lifts {
			switch name {
			case "squat", "back squat":
				in.SquatBW = math.Max(in.SquatBW, ls.e1rm/p.BodyweightKg)
			case "deadlift", "conventional deadlift", "sumo deadlift":
				in.DeadBW = math.Max(in.DeadBW, ls.e1rm/p.BodyweightKg)
			case "bench press", "paused bench press":
				in.BenchBW = math.Max(in.BenchBW, ls.e1rm/p.BodyweightKg)
			case "overhead press", "shoulder press":
				in.OHPBW = math.Max(in.OHPBW, ls.e1rm/p.BodyweightKg)
			}
		}
	}
	return c.Ledger.AwardMilestones(ctx, asOf, in)
}

// threatLevel is v1.1 29, which replaced v1.0's dormancy flag outright.
//
// The rank a lifter earned is not confiscated for missing three weeks; the
// Threat Level is what decays. Taking rewards away from people is how you get
// uninstalls -- marking them as diminished is how you get them back.
func (c *Calculator) threatLevel(ctx context.Context, asOf time.Time) (float64, error) {
	var last sql.NullString
	if err := c.st.DB().QueryRowContext(ctx, `
		SELECT MAX(local_date) FROM v_sets
		WHERE set_type IN ('working','backoff','drop')`).Scan(&last); err != nil {
		return 0, err
	}
	if !last.Valid || last.String == "" {
		return 100, nil
	}
	d, err := time.Parse("2006-01-02", last.String)
	if err != nil {
		return 100, nil
	}
	idle := asOf.Sub(d).Hours() / 24
	// Twenty-one days of grace, then 1.5 points a month, floored at 60. The
	// floor matters: a level that can reach zero stops carrying information
	// about how far someone has fallen.
	if idle <= 21 {
		return 100, nil
	}
	return round1(clamp(100-1.5*(idle-21)/30, 60, 100)), nil
}

// confidence is the v1.1 21 bar that replaced the hard onboarding lockout.
//
// A lockout tells a strong newcomer the app does not believe them. A bar tells
// them what the app has actually seen, which is the honest statement and the
// one that suggests an action.
func confidence(p Profile, scores []PatternScore) float64 {
	var verified, proven int
	for _, ps := range scores {
		switch ps.Status {
		case Verified:
			verified++
			proven++
		case Proven, Provisional:
			proven++
		}
	}
	evidence := (float64(verified)*1.0 + float64(proven-verified)*0.5) / float64(len(Patterns))

	// A fallback body-fat estimate widens the band (v1.0 2.1), because LBM
	// sets every strength reference in the system.
	body := 1.0
	switch p.BFSource {
	case "dexa":
		body = 1.0
	case "caliper", "navy":
		body = 0.85
	default:
		body = 0.6
	}
	if p.Frozen {
		body *= 0.7
	}
	return round2(clamp(0.7*evidence+0.3*body, 0, 1))
}

// journey counts only what happened inside the app.
func (c *Calculator) journey(ctx context.Context, asOf time.Time, rs float64) (Journey, error) {
	var j Journey
	var first sql.NullString
	var sessions int
	if err := c.st.DB().QueryRowContext(ctx, `
		SELECT MIN(local_date), COUNT(DISTINCT local_date) FROM v_sets
		WHERE set_type IN ('working','backoff','drop')`).Scan(&first, &sessions); err != nil {
		return j, err
	}
	j.Sessions = sessions
	if first.Valid && first.String != "" {
		if d, err := time.Parse("2006-01-02", first.String); err == nil {
			j.Days = int(asOf.Sub(d).Hours() / 24)
		}
	}

	var startRS sql.NullFloat64
	if err := c.st.DB().QueryRowContext(ctx, `
		SELECT rs FROM berserk_snapshots ORDER BY local_date ASC LIMIT 1`).Scan(&startRS); err != nil &&
		err != sql.ErrNoRows {
		return j, err
	}
	if startRS.Valid {
		j.StartRS = round1(startRS.Float64)
		j.RSGain = round1(rs - startRS.Float64)
	}
	return j, nil
}

// weakLink is v1.0 14.3, the single highest-value line in the system: name the
// pattern that is holding the rank and the number that moves it.
func weakLink(p Profile, scores []PatternScore, a Attributes, w Weights, granted, eligible Rung) string {
	if len(scores) == 0 || eligible.Index >= berserkIndex {
		return ""
	}
	// Aim at the rank the numbers could next reach, not the one hysteresis is
	// currently granting. A user mid-promotion already knows they are climbing;
	// what they need is the next thing that is actually blocking them, and
	// measuring the gap from the granted rank makes the line vanish exactly
	// when someone is making progress.
	next := RungByIndex(eligible.Index + 1)

	weakest := scores[0]
	for _, s := range scores {
		if s.Score < weakest.Score {
			weakest = s
		}
	}

	// A structural block is more actionable than an RS gap, so it is reported
	// first: no amount of load fixes an unproven pattern.
	if weakest.Status == Unproven {
		return fmt.Sprintf("%s is unproven and imputed. Logging one working set of 8 reps or fewer proves it and moves %s off an estimate.",
			weakest.Pattern.Display(), weakest.Pattern.Short())
	}
	if next.PatternFloor > 0 && weakest.Score < next.PatternFloor {
		if kg, ok := loadForScore(p, weakest, next.PatternFloor); ok {
			return fmt.Sprintf("%s at %.0f is holding you at %s. %s needs %.0f (%.0f kg) for %s.",
				weakest.Pattern.Short(), weakest.Score, granted.Name,
				weakest.Pattern.Short(), next.PatternFloor, kg, next.Name)
		}
	}

	// Otherwise translate the RS gap into a target on the weakest pattern.
	gap := next.Floor - (w.MIGHT*a.Might + w.DOMINION*a.Dominion + w.FRAME*a.Frame +
		w.VIGOR*a.Vigor + w.DISCIPLINE*a.Discipline + w.MASTERY*a.Mastery)
	if gap <= 0 || w.MIGHT <= 0 {
		return ""
	}
	target := a.Might + gap/w.MIGHT
	pat, need, ok := solveWeakestForMight(scores, target)
	if !ok {
		return fmt.Sprintf("%s is your weakest pattern; %s needs gains across more than one pattern.",
			weakest.Pattern.Short(), next.Name)
	}
	ps := weakest
	for _, s := range scores {
		if s.Pattern == pat {
			ps = s
		}
	}
	if kg, ok := loadForScore(p, ps, need); ok {
		return fmt.Sprintf("%s is holding you at %s. %.0f kg on %s (%.0f now) moves you to %s.",
			pat.Short(), granted.Name, kg, displayLift(ps), ps.E1RMKg, next.Name)
	}
	return fmt.Sprintf("%s is holding you at %s. %s at %.0f moves you to %s.",
		pat.Short(), granted.Name, pat.Short(), need, next.Name)
}

// loadForScore inverts the scoring chain to a kilogram figure on the lift the
// user is actually training, which is the difference between a number they can
// load on a bar and an abstraction.
func loadForScore(p Profile, ps PatternScore, target float64) (float64, bool) {
	ref := Ref(p, ps.Pattern)
	if ref <= 0 {
		return 0, false
	}
	sub, ok := substitutes[ps.SourceLift]
	if !ok {
		// Fall back to the anchor: the user has no logged lift here yet.
		a := anchors[ps.Pattern]
		sub = substitutes[a.liftName]
	}
	if sub.coef <= 0 || sub.conf <= 0 {
		return 0, false
	}
	e1rm := target / 100 * ref / (sub.coef * sub.conf)
	if sub.bodyweightBased {
		// Report added load, not total, because that is what goes on the belt.
		return round1(e1rm - p.BodyweightKg), true
	}
	return round1(e1rm), true
}

func displayLift(ps PatternScore) string {
	if ps.SourceLift != "" {
		return ps.SourceLift
	}
	return anchors[ps.Pattern].liftName
}

// notes collects the things a user must be told before they conclude the app
// is broken. v1.0 14.5: never let a rank move without an explanation.
func notes(p Profile, scores []PatternScore, b BerserkStatus) []string {
	var out []string
	if p.Estimated {
		out = append(out, "Body fat is a BMI-based estimate, so every strength reference is approximate. A tape or caliper measurement tightens the whole score.")
	}
	if p.Frozen {
		out = append(out, "Reported body fat moved more than 4 points in 30 days, so reference loads are frozen until a re-measure.")
	}
	var imputed, provisional int
	for _, ps := range scores {
		if ps.Imputed {
			imputed++
		}
		if ps.Status == Provisional {
			provisional++
		}
	}
	if imputed > 0 {
		out = append(out, fmt.Sprintf("%d pattern(s) are untested and imputed at 75%% of your tested mean. Imputation always drags the harmonic mean down.", imputed))
	}
	if provisional > 0 {
		out = append(out, fmt.Sprintf("%d pattern(s) are self-reported. Provisional patterns carry you to DREADBORN and no further.", provisional))
	}
	if b.Note != "" {
		out = append(out, b.Note)
	}
	return out
}

// currentRung reads the last granted rank, and reports whether this is the
// first computation the account has ever had -- which is the one case where
// hysteresis must not apply.
func (c *Calculator) currentRung(ctx context.Context, asOf time.Time) (Rung, bool, error) {
	var idx int
	err := c.st.DB().QueryRowContext(ctx, `
		SELECT rank_index FROM berserk_snapshots
		WHERE local_date < ? ORDER BY local_date DESC LIMIT 1`,
		c.st.LocalDate(asOf)).Scan(&idx)
	if err == sql.ErrNoRows {
		return Ladder[0], true, nil
	}
	if err != nil {
		return Ladder[0], false, err
	}
	return RungByIndex(idx), false, nil
}

// qualifyingDays counts how many consecutive days, ending today, the user has
// been eligible for at least this rank. Ten of them is a promotion.
func (c *Calculator) qualifyingDays(ctx context.Context, asOf time.Time, eligible int) (int, error) {
	rows, err := c.st.DB().QueryContext(ctx, `
		SELECT local_date, eligible_index FROM berserk_snapshots
		WHERE local_date < ? ORDER BY local_date DESC LIMIT ?`,
		c.st.LocalDate(asOf), promotionHoldDays)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	// Today counts, since it is being computed right now.
	streak := 1
	expect, err := time.Parse("2006-01-02", c.st.LocalDate(asOf))
	if err != nil {
		return streak, nil
	}
	for rows.Next() {
		var ds string
		var idx int
		if err := rows.Scan(&ds, &idx); err != nil {
			return streak, err
		}
		d, err := time.Parse("2006-01-02", ds)
		if err != nil {
			break
		}
		expect = expect.AddDate(0, 0, -1)
		// A missing day breaks the streak. It has to: otherwise a user could
		// qualify on ten scattered days across three months and be promoted
		// for a consistency they never had.
		if !d.Equal(expect) || idx < eligible {
			break
		}
		streak++
	}
	return streak, rows.Err()
}

// ---------- persistence ----------

// Save writes the daily snapshot. Hysteresis, the gate-month Blood award and
// the journey all read this table, so a missed write is not cosmetic.
func (c *Calculator) Save(ctx context.Context, asOf time.Time, r *Rank) error {
	detail := struct {
		Patterns  []PatternScore `json:"patterns"`
		Breakdown Breakdown      `json:"breakdown"`
		Berserk   BerserkStatus  `json:"berserk"`
		Weights   Weights        `json:"weights"`
		WeakLink  string         `json:"weak_link,omitempty"`
	}{r.Patterns, r.Breakdown, r.Berserk, r.Weights, r.WeakLink}

	blob, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	berserkInt := 0
	if r.Berserk.Qualified {
		berserkInt = 1
	}
	_, err = c.st.DB().ExecContext(ctx, `
		INSERT INTO berserk_snapshots
		  (local_date, rs, rank_index, rank_name, eligible_index,
		   might, dominion, frame, vigor, discipline, mastery,
		   berserk, threat_level, confidence, blood_total,
		   detail_json, system_version, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(local_date) DO UPDATE SET
		  rs = excluded.rs, rank_index = excluded.rank_index,
		  rank_name = excluded.rank_name, eligible_index = excluded.eligible_index,
		  might = excluded.might, dominion = excluded.dominion, frame = excluded.frame,
		  vigor = excluded.vigor, discipline = excluded.discipline, mastery = excluded.mastery,
		  berserk = excluded.berserk, threat_level = excluded.threat_level,
		  confidence = excluded.confidence, blood_total = excluded.blood_total,
		  detail_json = excluded.detail_json, system_version = excluded.system_version`,
		c.st.LocalDate(asOf), r.RS, r.RankIndex, r.Rank, r.EligibleIndex,
		r.Attributes.Might, r.Attributes.Dominion, r.Attributes.Frame,
		r.Attributes.Vigor, r.Attributes.Discipline, r.Attributes.Mastery,
		berserkInt, r.ThreatLevel, r.Confidence, r.Blood.Total,
		string(blob), Version, time.Now().UTC().Format(time.RFC3339))
	return err
}

// Delta renders a one-line rank change for the coach context, or "" when
// nothing worth mentioning happened. v1.0 14.5: a rank never moves without a
// reason attached.
func (c *Calculator) Delta(ctx context.Context, asOf time.Time, cur *Rank) (string, error) {
	var prevName string
	var prevIdx int
	var prevVersion string
	err := c.st.DB().QueryRowContext(ctx, `
		SELECT rank_name, rank_index, system_version FROM berserk_snapshots
		WHERE local_date < ? ORDER BY local_date DESC LIMIT 1`,
		c.st.LocalDate(asOf)).Scan(&prevName, &prevIdx, &prevVersion)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	if prevVersion != Version {
		return fmt.Sprintf("rank recalibrated to system v%s, now %s", Version, cur.Rank), nil
	}
	switch {
	case cur.RankIndex > prevIdx:
		return fmt.Sprintf("promoted from %s to %s", prevName, cur.Rank), nil
	case cur.RankIndex < prevIdx:
		return fmt.Sprintf("dropped from %s to %s", prevName, cur.Rank), nil
	case cur.EligibleIndex > cur.RankIndex:
		return fmt.Sprintf("qualifying for %s, holding for promotion", RungByIndex(cur.EligibleIndex).Name), nil
	case cur.ToNext > 0 && cur.ToNext <= 2:
		return fmt.Sprintf("%.1f RS from %s", cur.ToNext, cur.NextRank), nil
	}
	return "", nil
}

// ---------- helpers ----------

func roundBreakdown(b Breakdown) Breakdown {
	return Breakdown{
		HM: round1(b.HM), LOW: round1(b.LOW),
		DPullup: round1(b.DPullup), DDip: round1(b.DDip), DReps: round1(b.DReps),
		DPress: round1(b.DPress), DSquat: round1(b.DSquat),
		SFFMI: round1(b.SFFMI), BFMod: round2(b.BFMod), FFMIAd: round1(b.FFMIAd),
		VVolume: round1(b.VVolume), VCardio: round1(b.VCardio),
		VDensity: round1(b.VDensity), VRecover: round1(b.VRecover),
		A30: round2(b.A30), A365: round2(b.A365), TA: round2(b.TA),
		MBreadth: round1(b.MBreadth), MQuality: round1(b.MQuality),
		MSkill: round1(b.MSkill), MLongevity: round1(b.MLongevity),
	}
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	return math.Max(lo, math.Min(hi, v))
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
