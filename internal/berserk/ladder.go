package berserk

import (
	"fmt"
	"math"
	"sort"
)

// Rung is one rank on the ladder.
type Rung struct {
	Index int
	Name  string
	// Floor and Ceil bound the RS band. Ceil is inclusive; BERSERK has no
	// ceiling and is not reached by RS at all (see BerserkGates).
	Floor, Ceil float64
	// PatternFloor is the minimum pattern score the rank tolerates, 0 for none.
	PatternFloor float64
	// ProvenNeeded is how many patterns must be at least PROVEN.
	ProvenNeeded int
	// VerifiedNeeded is how many must be VERIFIED. Only the top two ranks
	// require any, which is what makes them cost about a month of real logging.
	VerifiedNeeded int
}

// Ladder is v1.2 Patch 3.
//
// Band widths run 9, 8, 7, 7, 7, 6, 6, 6, 6, 5, 5, 4, 4: smooth compression
// with no thrash zones, and the interval never drops below four points, which
// is what makes the three-point demotion hysteresis coherent.
//
// v1.1's GODBREAKER / ASCENDANT / TRANSCENDENT / APEX are gone -- generic MMO
// escalation, and placing "Transcendent" below Berserk read as a mistake.
// STRUGGLER, APOSTLE and BLACK SWORDSMAN are the source material.
var Ladder = []Rung{
	{1, "COMMONER", 0, 8, 0, 0, 0},
	{2, "MERCENARY", 9, 16, 0, 0, 0},
	{3, "BLOODED", 17, 23, 0, 0, 0},
	{4, "RAIDER", 24, 30, 0, 2, 0},
	{5, "REAVER", 31, 37, 0, 3, 0},
	{6, "MARAUDER", 38, 43, 26, 4, 0},
	{7, "RAVAGER", 44, 49, 32, 4, 0},
	{8, "STRUGGLER", 50, 55, 40, 5, 0},
	{9, "EXECUTIONER", 56, 61, 50, 5, 0},
	{10, "APOSTLE", 62, 66, 58, 6, 0},
	{11, "WARLORD", 67, 71, 64, 6, 0},
	{12, "DREADBORN", 72, 75, 70, 6, 0},
	{13, "BLACK SWORDSMAN", 76, 79, 74, 6, 6},
	{14, "BERSERK", 80, math.Inf(1), 78, 6, 6},
}

const (
	// berserkIndex and blackSwordsmanIndex are named because three separate
	// rules key off them.
	berserkIndex        = 14
	blackSwordsmanIndex = 13
	// provisionalCeiling is v1.2 Patch 5: a self-reported best carries a user
	// to DREADBORN and no further. A strong newcomer reaching rank 12 of 14
	// inside a week is honest; the top two require the app to have seen it.
	provisionalCeiling = 12
	// promotionHoldDays and demotionSlack are the Patch 3 hysteresis. Rank
	// should feel like a slow tide, not a stock ticker.
	promotionHoldDays = 10
	demotionSlack     = 3.0
)

// Gate is one Berserk requirement and its current standing.
type Gate struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Pass      bool    `json:"pass"`
	// Fix is a computed, specific instruction: what number has to move and to
	// what. v1.0 14.3 -- the highest-value element in this whole system is the
	// line naming the weak link, and "train harder" is not that line.
	Fix string `json:"fix,omitempty"`
}

// berserkGates are the six hard attribute gates of v1.2 Patch 2, as amended by
// v1.3 Erratum 1.
//
// The RS condition is GONE. v1.2 stated RS >= 80 alongside these six gates,
// which collectively guarantee RS >= 79.86, so the RS condition carried no
// information -- it was satisfied by construction whenever the gates passed,
// except for a 0.14 sliver that was pure artifact.
//
// All six are HARD. There is no compensation between attributes at the Berserk
// boundary. v1.2's "exceed one, and you can run slightly under another" is
// struck. Compensation is fine on ranks 1-13, where the RS band IS the
// definition and the pattern floors are the only hard constraint; it is not
// fine here, because Berserk is specifically a claim about completeness. A 95
// MIGHT does not purchase a 65 VIGOR.
var berserkGates = []struct {
	name      string
	threshold float64
}{
	{"MIGHT", 85},
	{"DOMINION", 80},
	{"FRAME", 78},
	{"VIGOR", 70},
	{"DISCIPLINE", 80},
	{"MASTERY", 72},
}

// BerserkStatus is the full gate readout.
//
// Erratum 1, and this is the important part: below Berserk the UI shows a
// composite ("RS 61 / 62 -> APOSTLE"). At the Berserk boundary it must stop
// showing a composite and show the binding gate instead. A composite number
// here is actively misleading -- a lifter can sit above the old RS threshold
// and still be one point of MASTERY from the rank.
type BerserkStatus struct {
	Qualified bool   `json:"qualified"`
	Gates     []Gate `json:"gates"`
	// PatternsVerified and MinPattern are the two structural requirements.
	PatternsVerified int     `json:"patterns_verified"`
	MinPattern       float64 `json:"min_pattern"`
	MinPatternName   string  `json:"min_pattern_name"`
	// Failing counts unmet requirements, so the UI can say "One attribute
	// from BERSERK" rather than making the user count rows.
	Failing int    `json:"failing"`
	Summary string `json:"summary"`
	// Note carries the Erratum 6 explanation when the pattern floor passes but
	// MIGHT does not, which otherwise reads as a bug.
	Note string `json:"note,omitempty"`
}

// EvaluateBerserk applies the Erratum 1 definition: gates alone, no RS term.
func EvaluateBerserk(a Attributes, b Breakdown, scores []PatternScore) BerserkStatus {
	st := BerserkStatus{MinPattern: math.Inf(1)}

	for _, g := range berserkGates {
		v := round1(a.Get(g.name))
		gate := Gate{Name: g.name, Value: v, Threshold: g.threshold, Pass: v >= g.threshold}
		if !gate.Pass {
			gate.Fix = fixFor(g.name, g.threshold, a, b, scores)
			st.Failing++
		}
		st.Gates = append(st.Gates, gate)
	}

	for _, ps := range scores {
		if ps.Status == Verified {
			st.PatternsVerified++
		}
		if ps.Score < st.MinPattern {
			st.MinPattern, st.MinPatternName = ps.Score, ps.Pattern.Short()
		}
	}
	if math.IsInf(st.MinPattern, 1) {
		st.MinPattern = 0
	}

	verifiedOK := st.PatternsVerified == len(Patterns)
	floorOK := st.MinPattern >= Ladder[berserkIndex-1].PatternFloor
	if !verifiedOK {
		st.Failing++
	}
	if !floorOK {
		st.Failing++
	}

	st.Qualified = st.Failing == 0

	// Erratum 6: the 78 pattern floor is NOT sufficient for the MIGHT gate. A
	// perfectly balanced lifter at exactly 78 across all six fails MIGHT by
	// seven points, because HM = LOW = MIGHT when all patterns are equal, so
	// the minimum BALANCED pattern score for MIGHT 85 is 85. The floor exists
	// to permit imbalance -- one pattern at 78 carried by others above 85 --
	// not to define the target. Say so, or the user assumes a bug.
	if floorOK && a.Might < 85 {
		st.Note = "The 78 pattern floor permits imbalance; it is not the target. " +
			"Six equal patterns must each reach 85 to satisfy MIGHT, because MIGHT equals the pattern score when all six are equal."
	}

	switch {
	case st.Qualified:
		st.Summary = "All Berserk requirements met."
	case st.Failing == 1:
		st.Summary = fmt.Sprintf("One requirement from BERSERK: %s.", firstFailing(st))
	default:
		st.Summary = fmt.Sprintf("%d requirements from BERSERK.", st.Failing)
	}
	return st
}

func firstFailing(st BerserkStatus) string {
	for _, g := range st.Gates {
		if !g.Pass {
			return g.Name
		}
	}
	if st.PatternsVerified < len(Patterns) {
		return fmt.Sprintf("%d of 6 patterns verified", st.PatternsVerified)
	}
	return fmt.Sprintf("%s at %.0f", st.MinPatternName, st.MinPattern)
}

// fixFor computes what has to move. Erratum 6 is explicit that the MIGHT
// suggestion must come from solving the formula for the weakest pattern rather
// than from generic encouragement, and the same standard is applied to the
// other five gates: name the binding component and the number it must reach.
func fixFor(name string, threshold float64, a Attributes, b Breakdown, scores []PatternScore) string {
	switch name {
	case "MIGHT":
		pat, target, ok := solveWeakestForMight(scores, threshold)
		if !ok {
			return "raise the weakest patterns; the gate is out of reach from one pattern alone"
		}
		return fmt.Sprintf("raise %s to %.0f -> MIGHT %.0f", pat.Short(), target, threshold)

	case "MASTERY":
		// M_breadth is the term a user can act on directly and the one
		// Erratum 1's worked example points at. But it is worth at most 30
		// points of MASTERY, so the suggestion has to be bounded by what
		// breadth can actually deliver -- otherwise the gap divides out to
		// "develop 28 more movements" against a denominator of 14.
		const breadthPerMovement = 0.30 * 100 / 14
		remaining := int(math.Round((100 - b.MBreadth) / (100.0 / 14)))
		need := int(math.Ceil((threshold - a.Mastery) / breadthPerMovement))
		if need > 0 && remaining > 0 {
			if need <= remaining {
				return fmt.Sprintf("develop %d more movements to 60+", need)
			}
			return fmt.Sprintf("develop %d more movements to 60+, and %s",
				remaining, lowestComponent(map[string]float64{
					"technical quality": b.MQuality, "skill unlocks": b.MSkill,
					"held lifts": b.MLongevity,
				}))
		}
		return lowestComponent(map[string]float64{
			"technical quality": b.MQuality, "skill unlocks": b.MSkill, "held lifts": b.MLongevity,
		})

	case "DOMINION":
		return lowestComponent(map[string]float64{
			"weighted pull-up": b.DPullup, "weighted dip": b.DDip,
			"strict pull-up reps": b.DReps, "overhead press": b.DPress, "squat": b.DSquat,
		})

	case "VIGOR":
		// Erratum 4's arithmetic, run backwards for this specific user.
		if b.VCardio < 100 {
			deficit := threshold - a.Vigor
			needCardio := b.VCardio + deficit/0.30
			vo2 := 30 + needCardio*18/100
			return fmt.Sprintf("reach VO2max ~%.0f (%.0f now)", vo2, 30+b.VCardio*18/100)
		}
		return lowestComponent(map[string]float64{
			"weekly hard sets": b.VVolume, "session density": b.VDensity, "rest days": b.VRecover,
		})

	case "FRAME":
		if b.BFMod < 1.0 {
			return "body fat is outside the 8-15% plateau; FFMI alone would score higher"
		}
		return "raise FFMI; lean mass is the only input"

	case "DISCIPLINE":
		return lowestComponentPct(map[string]float64{
			"30-day adherence": b.A30, "365-day adherence": b.A365, "training age": b.TA,
		})
	}
	return ""
}

// lowestComponentPct is the same idea for terms that are ratios rather than
// scores, so the output reads "at 1%" instead of the ambiguous "at 1".
func lowestComponentPct(m map[string]float64) string {
	scaled := make(map[string]float64, len(m))
	for k, v := range m {
		scaled[k] = v * 100
	}
	s := lowestComponent(scaled)
	if s == "" {
		return ""
	}
	return s + "%"
}

func lowestComponent(m map[string]float64) string {
	type kv struct {
		k string
		v float64
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v == all[j].v {
			return all[i].k < all[j].k
		}
		return all[i].v < all[j].v
	})
	if len(all) == 0 {
		return ""
	}
	return fmt.Sprintf("weakest term is %s at %.0f", all[0].k, all[0].v)
}

// solveWeakestForMight finds the score the weakest pattern must reach for MIGHT
// to hit the target, holding the other five fixed.
//
// Solved numerically rather than algebraically because LOW changes composition
// partway up: once the weakest pattern rises past the second lowest, a
// different pair forms the mean, so the closed form has a kink in it. MIGHT is
// monotonically increasing in any single pattern score, so bisection is exact
// to the displayed precision and cannot pick the wrong root.
func solveWeakestForMight(scores []PatternScore, target float64) (Pattern, float64, bool) {
	if len(scores) == 0 {
		return "", 0, false
	}
	idx := 0
	for i, s := range scores {
		if s.Score < scores[idx].Score {
			idx = i
		}
	}

	trial := append([]PatternScore(nil), scores...)
	evaluate := func(x float64) float64 {
		trial[idx].Score = x
		v, _, _ := might(trial)
		return v
	}

	// Even at the pattern cap the gate may be unreachable from one pattern, in
	// which case saying so is more useful than naming an impossible number.
	if evaluate(patternScoreCap) < target {
		return scores[idx].Pattern, 0, false
	}

	lo, hi := scores[idx].Score, float64(patternScoreCap)
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if evaluate(mid) < target {
			lo = mid
		} else {
			hi = mid
		}
	}
	return scores[idx].Pattern, math.Ceil(hi), true
}

// EligibleRung returns the highest rank the current numbers justify, before
// hysteresis. Structural requirements are checked at every rung, not just the
// one the RS band lands in, because a user with RS 70 and three proven
// patterns is a REAVER regardless of what the band says.
func EligibleRung(rs float64, scores []PatternScore, berserk BerserkStatus) Rung {
	var proven, verified int
	minScore := math.Inf(1)
	anyProvisional := false
	for _, ps := range scores {
		if ps.Status.IsProven() {
			proven++
		}
		if ps.Status == Verified {
			verified++
		}
		if ps.Status == Provisional {
			anyProvisional = true
		}
		minScore = math.Min(minScore, ps.Score)
	}
	if math.IsInf(minScore, 1) {
		minScore = 0
	}

	best := Ladder[0]
	for _, r := range Ladder {
		switch {
		case r.Index == berserkIndex:
			// Erratum 1: Berserk is defined by gates alone. RS never enters.
			if berserk.Qualified {
				best = r
			}
			continue
		case rs < r.Floor:
			continue
		case minScore < r.PatternFloor:
			continue
		case proven < r.ProvenNeeded:
			continue
		case verified < r.VerifiedNeeded:
			continue
		}
		// v1.2 Patch 5: a provisional pattern cannot carry anyone past
		// DREADBORN. The top two ranks require the app to have seen the lift.
		if anyProvisional && r.Index > provisionalCeiling {
			continue
		}
		best = r
	}
	return best
}

// ApplyHysteresis turns the eligible rank into the granted rank.
//
// v1.2 Patch 3: promote when the threshold has held for ten consecutive days,
// demote only when RS falls three points below the band floor. The asymmetry
// is the point -- a rank should be slow to arrive and slower to leave, because
// a rank that flickers on a single good session means nothing.
// The first grant is exempt. v1.1 21 is explicit that rank is current
// capability granted IMMEDIATELY from real numbers, and holding a lifter who
// walks in benching 140 kg at COMMONER for ten days is exactly the onboarding
// insult that section exists to prevent. The hold is an anti-flicker rule for
// rank CHANGES; there is nothing to flicker away from on day one.
func ApplyHysteresis(eligible Rung, rs float64, current Rung, qualifyingDays int, firstEver bool) Rung {
	if firstEver {
		return eligible
	}
	switch {
	case eligible.Index > current.Index:
		if qualifyingDays >= promotionHoldDays {
			return eligible
		}
		return current
	case eligible.Index < current.Index:
		if rs < current.Floor-demotionSlack {
			return eligible
		}
		// BERSERK is not held by RS, so it cannot be retained by the slack
		// rule; losing the gates drops the rank and the Threat Level carries
		// the decay story instead.
		if current.Index == berserkIndex {
			return eligible
		}
		return current
	}
	return current
}

// RungByIndex looks up a rung, defaulting to COMMONER for an unknown index so
// a corrupt snapshot degrades to the bottom of the ladder rather than panicking.
func RungByIndex(i int) Rung {
	for _, r := range Ladder {
		if r.Index == i {
			return r
		}
	}
	return Ladder[0]
}
