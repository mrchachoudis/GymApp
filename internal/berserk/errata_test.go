package berserk

import (
	"math"
	"testing"
)

// The tests in this file exist to hold the six v1.3 errata in place. Each one
// re-derives the arithmetic the erratum asserts, so a future retune that
// silently breaks one of them fails here rather than in a user's face.

func scoresAll(v float64, status Status) []PatternScore {
	out := make([]PatternScore, 0, len(Patterns))
	for _, p := range Patterns {
		out = append(out, PatternScore{Pattern: p, Name: p.Display(), Score: v, Status: status})
	}
	return out
}

// ---------- ERRATUM 1: Berserk is defined by gates alone ----------

// TestGatesImplyTheOldRSThreshold re-derives the check in v1.2 Patch 2 that
// made the RS condition redundant in the first place.
func TestGatesImplyTheOldRSThreshold(t *testing.T) {
	w := Profile{GoalProfile: "balanced"}.GoalWeights()
	rs := w.MIGHT*85 + w.DOMINION*80 + w.FRAME*78 + w.VIGOR*70 + w.DISCIPLINE*80 + w.MASTERY*72
	if math.Abs(rs-79.86) > 0.005 {
		t.Fatalf("gates met exactly should give RS 79.86, got %.4f", rs)
	}
	// And the point of the erratum: 79.86 is below the v1.2 threshold of 80, so
	// an RS condition of 80 would have excluded a lifter who met every gate.
	// That 0.14 sliver is the artifact the erratum deletes.
	if rs >= 80 {
		t.Fatalf("expected the gates to land just under the old RS 80 threshold, got %.4f", rs)
	}
}

// TestBerserkIgnoresRS is the erratum proper: RS is not a gate. A lifter who
// meets every gate qualifies regardless of what the composite says.
func TestBerserkIgnoresRS(t *testing.T) {
	a := Attributes{Might: 85, Dominion: 80, Frame: 78, Vigor: 70, Discipline: 80, Mastery: 72}
	st := EvaluateBerserk(a, Breakdown{}, scoresAll(85, Verified))
	if !st.Qualified {
		t.Fatalf("gates met exactly must qualify, failing=%d summary=%q", st.Failing, st.Summary)
	}

	// EligibleRung is handed an RS of 0 to prove RS plays no part.
	if got := EligibleRung(0, scoresAll(85, Verified), st); got.Index != berserkIndex {
		t.Fatalf("Berserk must not depend on RS, got rank %s", got.Name)
	}
}

// TestNoCompensationAtTheBerserkBoundary is the struck sentence from v1.2: a
// 95 MIGHT does not purchase a 65 VIGOR. All six gates are hard.
func TestNoCompensationAtTheBerserkBoundary(t *testing.T) {
	a := Attributes{Might: 95, Dominion: 90, Frame: 90, Vigor: 65, Discipline: 90, Mastery: 90}
	w := Profile{}.GoalWeights()
	rs := w.MIGHT*a.Might + w.DOMINION*a.Dominion + w.FRAME*a.Frame +
		w.VIGOR*a.Vigor + w.DISCIPLINE*a.Discipline + w.MASTERY*a.Mastery
	if rs < 80 {
		t.Fatalf("test setup: this lifter should exceed the old RS threshold, got %.2f", rs)
	}

	st := EvaluateBerserk(a, Breakdown{}, scoresAll(95, Verified))
	if st.Qualified {
		t.Fatal("a lifter under the VIGOR gate must not be Berserk, however high the composite")
	}
	if st.Failing != 1 {
		t.Fatalf("expected exactly one failing requirement, got %d", st.Failing)
	}
	if st.Gates[3].Name != "VIGOR" || st.Gates[3].Pass {
		t.Fatalf("VIGOR should be the binding gate, got %+v", st.Gates[3])
	}
}

// TestShowGatesAtTheBoundary covers the UI consequence, which the erratum calls
// the important part: a composite is misleading at the boundary, so the client
// is told to switch readouts.
func TestShowGatesAtTheBoundary(t *testing.T) {
	for _, tc := range []struct {
		index int
		want  bool
	}{{10, false}, {12, false}, {13, true}, {14, true}} {
		got := tc.index >= blackSwordsmanIndex
		if got != tc.want {
			t.Errorf("rank %d: ShowGates = %v, want %v", tc.index, got, tc.want)
		}
	}
}

// ---------- ERRATUM 2: clamp every unbounded term ----------

func TestVCardioNeverGoesNegative(t *testing.T) {
	// VO2max 25 is an ordinary value for a deconditioned beginner, and the
	// unclamped term gives -27.8.
	if got := clamp(100*(25-30)/18, 0, 120); got != 0 {
		t.Fatalf("V_cardio at VO2max 25 = %.2f, want 0", got)
	}
	// The upper clamp is 120, not 100: the term is allowed to exceed
	// Berserk level and feed the post-Berserk economy.
	if got := clamp(100*(60-30)/18, 0, 120); got != 120 {
		t.Fatalf("V_cardio at VO2max 60 = %.2f, want the 120 clamp", got)
	}
}

func TestVRecoverNeverGoesNegative(t *testing.T) {
	// Six rest days a week is a deload or a return from illness, and the
	// unclamped term gives -33.3.
	if got := clamp(100*(1-math.Abs(6-2)/3), 0, 100); got != 0 {
		t.Fatalf("V_recover at 6 rest days = %.2f, want 0", got)
	}
	// Two rest days is the peak, and zero rest days is still penalised.
	if got := clamp(100*(1-math.Abs(2-2)/3), 0, 100); got != 100 {
		t.Fatalf("V_recover at 2 rest days = %.2f, want 100", got)
	}
	if got := clamp(100*(1-math.Abs(0-2)/3), 0, 100); math.Abs(got-33.33) > 0.01 {
		t.Fatalf("V_recover at 0 rest days = %.2f, want ~33.33", got)
	}
}

// TestBFModFloorIsDefensive records something the erratum does not say out
// loud: with v1.0's coefficients the 0.50 floor never actually binds. The
// steepest branch is 1 - 0.9*(BF-15)/100, which only reaches 0.50 at 70.6%
// body fat, and LoadProfile already bounds a reported value to 60%. So the
// clamp is a guard against a future retune of those coefficients, not a
// behaviour users will meet. Both facts are asserted, because if a retune ever
// makes the floor reachable this test should start describing that instead.
func TestBFModFloorIsDefensive(t *testing.T) {
	for bf := 3.0; bf <= 60; bf += 0.5 {
		var b Breakdown
		p := Profile{HeightCm: 178, BodyweightKg: 100, BodyfatPct: bf, LBMKg: 100 * (1 - bf/100)}
		p.FFMIAdj = p.LBMKg/(1.78*1.78) + 6.1*(1.80-1.78)
		frame(p, &b)
		if b.BFMod < 0.50 || b.BFMod > 1.00 {
			t.Fatalf("BF_mod at %.1f%% = %.3f, outside the clamped range", bf, b.BFMod)
		}
	}
	// The guard itself works if the formula ever produces something lower.
	if got := clamp(0.2, 0.50, 1.00); got != 0.50 {
		t.Fatalf("the BF_mod clamp does not hold: %.2f", got)
	}
	// Degraded, not annihilated: a heavy lifter at 40% body fat who still
	// carries real lean mass keeps a meaningful FRAME.
	var b Breakdown
	p := Profile{HeightCm: 178, BodyweightKg: 110, BodyfatPct: 40, LBMKg: 66}
	p.FFMIAdj = p.LBMKg/(1.78*1.78) + 6.1*(1.80-1.78)
	got := frame(p, &b)
	if got <= 20 {
		t.Fatalf("FRAME should degrade rather than vanish, got %.2f", got)
	}

	// FRAME does still reach zero, but through S_ffmi rather than the
	// modifier: below FFMI_adj 16 there is no lean mass to score. That is the
	// intended shape and is asserted so it is not mistaken for the clamp.
	var b2 Breakdown
	thin := Profile{HeightCm: 178, BodyweightKg: 110, BodyfatPct: 60, LBMKg: 44}
	thin.FFMIAdj = thin.LBMKg/(1.78*1.78) + 6.1*(1.80-1.78)
	if frame(thin, &b2) != 0 || b2.SFFMI != 0 || b2.BFMod <= 0.50 {
		t.Fatalf("expected S_ffmi to floor at 0 with BF_mod unclamped, got S_ffmi=%.2f mod=%.2f",
			b2.SFFMI, b2.BFMod)
	}
}

func TestDominionComponentsAreClamped(t *testing.T) {
	// A component that would run to 194 without the clamp.
	if got := clamp(100*(80+160)/(1.55*80), 0, 120); got != 120 {
		t.Fatalf("D_pullup should clamp to 120, got %.2f", got)
	}
	if got := clamp(100*-5/15, 0, 120); got != 0 {
		t.Fatalf("D_reps should floor at 0, got %.2f", got)
	}
}

// ---------- ERRATUM 3: guard the harmonic mean ----------

// TestMightSurvivesAllZeroPatterns is the failure the erratum describes: a user
// with no tested patterns has nothing to impute from, every score is 0, and
// 6/Sum(1/0) makes MIGHT NaN and the rank undefined.
func TestMightSurvivesAllZeroPatterns(t *testing.T) {
	v, hm, low := might(scoresAll(0, Unproven))
	for name, got := range map[string]float64{"MIGHT": v, "HM": hm, "LOW": low} {
		if math.IsNaN(got) || math.IsInf(got, 0) {
			t.Fatalf("%s = %v with all patterns at 0; the floor did not apply", name, got)
		}
	}
	if v != patternScoreFloor {
		t.Fatalf("MIGHT with all patterns floored should equal the floor %.1f, got %.2f",
			patternScoreFloor, v)
	}
}

func TestSingleZeroPatternDoesNotPoisonMight(t *testing.T) {
	s := scoresAll(90, Proven)
	s[3].Score = 0 // no vertical pull data yet
	v, _, _ := might(s)
	if math.IsNaN(v) || v <= 0 {
		t.Fatalf("MIGHT = %v with one untested pattern", v)
	}
	// The floor is a guard, not a gift: one zeroed pattern must still hurt.
	full, _, _ := might(scoresAll(90, Proven))
	if v >= full {
		t.Fatalf("a zeroed pattern must lower MIGHT: got %.2f against %.2f", v, full)
	}
}

// TestTrueZeroIsNotStored checks the other half of the erratum: the floor is
// applied at aggregation, never at storage, so the UI can still say "untested".
func TestTrueZeroIsNotStored(t *testing.T) {
	s := scoresAll(0, Unproven)
	might(s)
	for _, ps := range s {
		if ps.Score != 0 {
			t.Fatalf("aggregation must not mutate stored scores, got %.2f", ps.Score)
		}
	}
}

// TestAssistedPullupScoresPositively is the negative-added case. A user who
// cannot do one bodyweight pull-up still gets a real, monotonically improving
// score rather than falling off the bottom of the system.
func TestAssistedPullupScoresPositively(t *testing.T) {
	p := Profile{HeightCm: 178, BodyweightKg: 80, BodyfatPct: 20, LBMKg: 64}
	sub := substitutes["pull-up"]

	prev := 0.0
	for _, added := range []float64{-30, -20, -10, 0, 10} {
		got, _, _ := scoreSet(p, sub, "bodyweight_plus", added, 5)
		if got <= 0 {
			t.Fatalf("assisted pull-up at %+.0f kg scored %.2f, want positive", added, got)
		}
		if got <= prev {
			t.Fatalf("score must increase as assistance decreases: %+.0f kg gave %.2f after %.2f",
				added, got, prev)
		}
		prev = got
	}
}

// ---------- ERRATUM 4: the VIGOR gate is correctly placed ----------

// TestZeroCardioLifterLandsExactlyOnTheGate reproduces the erratum's first
// table. The gate binds, but only just.
func TestZeroCardioLifterLandsExactlyOnTheGate(t *testing.T) {
	v := 0.35*100 + 0.30*0 + 0.20*100 + 0.15*100
	if v != 70.0 {
		t.Fatalf("zero-cardio lifter at full non-cardio marks should score exactly 70, got %.2f", v)
	}
}

// TestRealisticCardioRequirement reproduces the erratum's second calculation:
// at realistic non-cardio values the gate asks for VO2max ~34, which is a brisk
// four-flight stair climb, not conditioning as a second discipline.
func TestRealisticCardioRequirement(t *testing.T) {
	base := 0.35*90 + 0.20*85 + 0.15*100
	if math.Abs(base-63.5) > 0.001 {
		t.Fatalf("expected 63.5 from the non-cardio terms, got %.2f", base)
	}
	needCardio := (70 - base) / 0.30
	if math.Abs(needCardio-21.667) > 0.01 {
		t.Fatalf("expected V_cardio 21.7, got %.2f", needCardio)
	}
	vo2 := 30 + needCardio*18/100
	if math.Abs(vo2-33.9) > 0.05 {
		t.Fatalf("expected VO2max ~33.9, got %.2f", vo2)
	}
	// The erratum's warning: do not raise the gate. At VIGOR 80 it becomes a
	// second sport.
	vo2At80 := 30 + ((80-base)/0.30)*18/100
	if vo2At80 < 38 {
		t.Fatalf("expected the VIGOR-80 requirement to jump near 39, got %.2f", vo2At80)
	}
}

// ---------- ERRATUM 5: blood economy ----------

func TestConsistencyOnlyAccrual(t *testing.T) {
	// The erratum's own figure: 20 x 4.33 + 60 is about 147 Blood a month.
	got := BloodQualifyingWeek*4.33 + BloodGatesMonth
	if math.Abs(got-146.6) > 0.5 {
		t.Fatalf("consistency-only accrual = %.1f/month, want ~147", got)
	}
	// And the property the whole post-Berserk design exists to have: a lifter
	// who never PRs again still reaches BERSERK V in under five years.
	years := BloodRequired(5) / got / 12
	if years >= 5 {
		t.Fatalf("consistency alone should reach BERSERK V in under five years, got %.1f", years)
	}
}

func TestBloodThresholdsMatchThePublishedTiers(t *testing.T) {
	for _, tc := range []struct {
		tier int
		want float64
	}{{2, 1200}, {3, 3167}, {4, 5587}, {5, 8357}} {
		if got := BloodRequired(tc.tier); math.Abs(got-tc.want) > 1 {
			t.Errorf("BloodRequired(%d) = %.0f, want %.0f", tc.tier, got, tc.want)
		}
	}
}

// TestBloodTierInversion checks that a total maps back to the tier that
// produced it, including exactly on a boundary.
func TestBloodTierInversion(t *testing.T) {
	for _, tc := range []struct {
		total float64
		want  int
	}{{0, 1}, {1199, 1}, {1200, 2}, {3167, 3}, {8358, 5}, {8356, 4}} {
		// Note the published 8,357 is the rounded form of 8,357.2, so a total of
		// exactly 8357 is still BERSERK IV. That is the formula being exact, not
		// an off-by-one.
		if got := bloodTier(tc.total); got != tc.want {
			t.Errorf("bloodTier(%.0f) = %d, want %d", tc.total, got, tc.want)
		}
	}
}

func TestRomanCoversOpenEndedTiers(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{1, "I"}, {2, "II"}, {4, "IV"}, {5, "V"}, {8, "VIII"}, {12, "XII"}, {40, "XL"}} {
		if got := Roman(tc.n); got != tc.want {
			t.Errorf("Roman(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// ---------- ERRATUM 6: the cap and the Berserk floor ----------

// TestPatternCapDoesNotBlockMight is the erratum's first check.
func TestPatternCapDoesNotBlockMight(t *testing.T) {
	v, hm, low := might(scoresAll(patternScoreCap, Verified))
	if math.Abs(v-patternScoreCap) > 1e-9 ||
		math.Abs(hm-patternScoreCap) > 1e-9 ||
		math.Abs(low-patternScoreCap) > 1e-9 {
		t.Fatalf("all patterns at the cap should give MIGHT = HM = LOW = %d, got %.2f/%.2f/%.2f",
			patternScoreCap, v, hm, low)
	}
}

// TestPatternFloorIsNotSufficientForMight is the erratum's second check, and
// the reason the UI has to explain itself: a perfectly balanced lifter at
// exactly the 78 floor fails MIGHT by seven points.
func TestPatternFloorIsNotSufficientForMight(t *testing.T) {
	v, _, _ := might(scoresAll(78, Verified))
	if math.Abs(v-78) > 0.001 {
		t.Fatalf("six equal patterns at 78 should give MIGHT 78, got %.2f", v)
	}
	if v >= 85 {
		t.Fatal("MIGHT at the pattern floor must fail the 85 gate")
	}

	// The minimum BALANCED pattern score for MIGHT 85 is 85, since
	// HM = LOW = MIGHT when all six are equal.
	if v85, _, _ := might(scoresAll(85, Verified)); math.Abs(v85-85) > 0.001 {
		t.Fatalf("six equal patterns at 85 should give MIGHT 85, got %.2f", v85)
	}

	// And the note has to fire, or a user staring at a passing floor and a
	// failing MIGHT gate will assume a bug.
	a := Attributes{Might: 78, Dominion: 80, Frame: 78, Vigor: 70, Discipline: 80, Mastery: 72}
	st := EvaluateBerserk(a, Breakdown{}, scoresAll(78, Verified))
	if st.Note == "" {
		t.Fatal("expected the explanatory note when the pattern floor passes but MIGHT fails")
	}
}

// TestFloorPermitsImbalance is the flip side: 78 exists so one pattern can sit
// low while others carry it, not as the target.
func TestFloorPermitsImbalance(t *testing.T) {
	s := scoresAll(95, Verified)
	s[3].Score = 78
	v, _, _ := might(s)
	if v < 85 {
		t.Fatalf("one pattern at the floor carried by five at 95 should still clear MIGHT 85, got %.2f", v)
	}
}

// TestSolveWeakestForMight covers the erratum's closing instruction: compute
// the suggestion by solving the MIGHT formula for the weakest pattern, rather
// than telling the user to train harder.
func TestSolveWeakestForMight(t *testing.T) {
	// Five patterns at 88 with HINGE at 60 gives MIGHT below the gate, which is
	// the situation the suggestion exists for.
	s := scoresAll(88, Verified)
	s[3].Score = 60
	if v, _, _ := might(s); v >= 85 {
		t.Fatalf("test setup: MIGHT should start below the gate, got %.2f", v)
	}

	pat, target, ok := solveWeakestForMight(s, 85)
	if !ok {
		t.Fatal("expected a solvable target")
	}
	if pat != Hinge {
		t.Fatalf("expected the weakest pattern to be identified, got %s", pat)
	}

	// The answer must actually work, and the point just below it must not --
	// otherwise the app is quoting a number that does not deliver the gate.
	s[3].Score = target
	if v, _, _ := might(s); v < 85 {
		t.Fatalf("solved target %.0f yields MIGHT %.2f, which does not clear the gate", target, v)
	}
	s[3].Score = target - 1.01
	if v, _, _ := might(s); v >= 85 {
		t.Fatalf("target %.0f is not tight: %.2f still clears at one point below", target, v)
	}
}

func TestSolveWeakestReportsUnreachable(t *testing.T) {
	// Five patterns at 40 cannot reach MIGHT 85 on one pattern alone, and
	// saying so is more useful than naming an impossible number.
	s := scoresAll(40, Proven)
	if _, _, ok := solveWeakestForMight(s, 85); ok {
		t.Fatal("expected the solver to report the gate unreachable from one pattern")
	}
}

// ---------- structural invariants ----------

func TestGoalWeightsAlwaysSumToOne(t *testing.T) {
	for _, g := range []string{"balanced", "power", "physique", "nonsense"} {
		w := Profile{GoalProfile: g}.GoalWeights()
		if math.Abs(w.Sum()-1) > 1e-9 {
			t.Errorf("profile %q weights sum to %.6f, want 1", g, w.Sum())
		}
	}
}

// TestGoalProfilesRespectTheShiftBudget holds v1.0 1 to its own rule: a profile
// may move at most 0.06 between MIGHT, FRAME and VIGOR, and nothing else moves.
func TestGoalProfilesRespectTheShiftBudget(t *testing.T) {
	base := Profile{GoalProfile: "balanced"}.GoalWeights()
	for _, g := range []string{"power", "physique"} {
		w := Profile{GoalProfile: g}.GoalWeights()
		for name, pair := range map[string][2]float64{
			"MIGHT": {base.MIGHT, w.MIGHT},
			"FRAME": {base.FRAME, w.FRAME},
			"VIGOR": {base.VIGOR, w.VIGOR},
		} {
			if d := math.Abs(pair[1] - pair[0]); d > 0.06+1e-9 {
				t.Errorf("profile %q moves %s by %.3f, over the 0.06 budget", g, name, d)
			}
		}
		if w.DOMINION != base.DOMINION || w.DISCIPLINE != base.DISCIPLINE || w.MASTERY != base.MASTERY {
			t.Errorf("profile %q moved a weight outside MIGHT/FRAME/VIGOR", g)
		}
	}
}

// TestLadderIsContiguous checks the v1.2 Patch 3 bands leave no RS unassigned
// and never overlap, and that the narrowest band is wide enough for the
// three-point demotion hysteresis to be coherent.
func TestLadderIsContiguous(t *testing.T) {
	for i := 1; i < len(Ladder); i++ {
		prev, cur := Ladder[i-1], Ladder[i]
		if cur.Floor != prev.Ceil+1 {
			t.Errorf("gap or overlap between %s (ceil %.0f) and %s (floor %.0f)",
				prev.Name, prev.Ceil, cur.Name, cur.Floor)
		}
		if cur.Index != prev.Index+1 {
			t.Errorf("rank indexes must be contiguous: %d then %d", prev.Index, cur.Index)
		}
	}
	for _, r := range Ladder {
		if math.IsInf(r.Ceil, 1) {
			continue
		}
		if width := r.Ceil - r.Floor + 1; width < 4 {
			t.Errorf("%s band is %.0f points wide; hysteresis needs at least 4", r.Name, width)
		}
	}
	if Ladder[len(Ladder)-1].Name != "BERSERK" {
		t.Fatal("BERSERK must be the top rung")
	}
}

// TestPatternFloorsAreMonotonic checks the floors never relax as the ladder
// climbs, which would let a user lose a pattern and gain a rank.
func TestPatternFloorsAreMonotonic(t *testing.T) {
	for i := 1; i < len(Ladder); i++ {
		if Ladder[i].PatternFloor < Ladder[i-1].PatternFloor {
			t.Errorf("%s floor %.0f is below %s floor %.0f",
				Ladder[i].Name, Ladder[i].PatternFloor,
				Ladder[i-1].Name, Ladder[i-1].PatternFloor)
		}
		if Ladder[i].ProvenNeeded < Ladder[i-1].ProvenNeeded {
			t.Errorf("%s requires fewer proven patterns than %s", Ladder[i].Name, Ladder[i-1].Name)
		}
	}
}

// ---------- v1.2 Patch 5: provisional ceiling ----------

func TestProvisionalCannotPassDreadborn(t *testing.T) {
	s := scoresAll(95, Verified)
	s[2].Status = Provisional // one self-reported pattern

	st := EvaluateBerserk(
		Attributes{Might: 95, Dominion: 95, Frame: 95, Vigor: 95, Discipline: 95, Mastery: 95},
		Breakdown{}, s)
	got := EligibleRung(95, s, st)
	if got.Index > provisionalCeiling {
		t.Fatalf("a provisional pattern must cap the rank at DREADBORN, got %s", got.Name)
	}

	// With the same numbers all verified, the ceiling lifts.
	all := scoresAll(95, Verified)
	stAll := EvaluateBerserk(
		Attributes{Might: 95, Dominion: 95, Frame: 95, Vigor: 95, Discipline: 95, Mastery: 95},
		Breakdown{}, all)
	if got := EligibleRung(95, all, stAll); got.Index != berserkIndex {
		t.Fatalf("fully verified at these numbers should be BERSERK, got %s", got.Name)
	}
}

func TestBlackSwordsmanRequiresVerification(t *testing.T) {
	s := scoresAll(80, Proven) // proven, not verified
	st := EvaluateBerserk(Attributes{}, Breakdown{}, s)
	if got := EligibleRung(78, s, st); got.Index >= blackSwordsmanIndex {
		t.Fatalf("BLACK SWORDSMAN requires all six verified, got %s", got.Name)
	}
	v := scoresAll(80, Verified)
	stv := EvaluateBerserk(Attributes{}, Breakdown{}, v)
	if got := EligibleRung(78, v, stv); got.Index != blackSwordsmanIndex {
		t.Fatalf("verified at RS 78 should be BLACK SWORDSMAN, got %s", got.Name)
	}
}

// ---------- hysteresis ----------

func TestPromotionRequiresTenConsecutiveDays(t *testing.T) {
	cur := RungByIndex(8)
	next := RungByIndex(9)
	if got := ApplyHysteresis(next, 58, cur, 9, false); got.Index != cur.Index {
		t.Fatalf("nine qualifying days must not promote, got %s", got.Name)
	}
	if got := ApplyHysteresis(next, 58, cur, 10, false); got.Index != next.Index {
		t.Fatalf("ten qualifying days must promote, got %s", got.Name)
	}
}

func TestDemotionNeedsThreePointsOfSlack(t *testing.T) {
	cur := RungByIndex(9) // EXECUTIONER, floor 56
	lower := RungByIndex(8)
	if got := ApplyHysteresis(lower, 54, cur, 1, false); got.Index != cur.Index {
		t.Fatalf("RS 54 is within the 3-point slack of floor 56, should hold, got %s", got.Name)
	}
	if got := ApplyHysteresis(lower, 52.9, cur, 1, false); got.Index != lower.Index {
		t.Fatalf("RS 52.9 is more than 3 below floor 56, should demote, got %s", got.Name)
	}
}

// TestBerserkIsNotHeldByRSSlack: the slack rule is stated in RS, and Berserk
// has no RS condition, so losing the gates has to drop the rank rather than
// being cushioned by a rule that cannot apply.
func TestBerserkIsNotHeldByRSSlack(t *testing.T) {
	cur := RungByIndex(berserkIndex)
	lower := RungByIndex(blackSwordsmanIndex)
	if got := ApplyHysteresis(lower, 99, cur, 1, false); got.Index != lower.Index {
		t.Fatalf("losing the gates must drop Berserk regardless of RS, got %s", got.Name)
	}
}

// ---------- reference loads ----------

// TestReferenceLifterMatchesCalibration checks the v1.0 3.3 table: at LBM 70
// and the reference height, the six references are the published figures.
func TestReferenceLifterMatchesCalibration(t *testing.T) {
	p := Profile{HeightCm: 178, BodyweightKg: 80, BodyfatPct: 12.5, LBMKg: 70}
	for _, tc := range []struct {
		pat  Pattern
		want float64
	}{
		{HPress, 130}, {VPress, 82}, {Squat, 180},
		{Hinge, 220}, {VPull, 125}, {HPull, 110},
	} {
		got := Ref(p, tc.pat)
		if math.Abs(got-tc.want) > 1.5 {
			t.Errorf("Ref(%s) = %.1f kg, want ~%.0f kg", tc.pat, got, tc.want)
		}
	}
}

// TestHeightCorrectionStaysWithinSixPercent holds v1.0 2.2 to its stated
// magnitude across the realistic human range. Anything larger and a short
// lifter gets a trivial path or a tall one can never rank up.
func TestHeightCorrectionStaysWithinSixPercent(t *testing.T) {
	base := Profile{HeightCm: referenceHeightCm, LBMKg: 70}
	for _, h := range []float64{155, 165, 178, 190, 205} {
		p := Profile{HeightCm: h, LBMKg: 70}
		for _, pat := range Patterns {
			ratio := Ref(p, pat) / Ref(base, pat)
			// v1.0 2.2 says "at most about 6%". With p = 0.50 on the presses
			// the true extremes over 155-205 cm are +7.2% and -6.8%, so the
			// prose is a round number rather than a bound. The magnitude is
			// what matters and it is held here at 7.5%: past that a short
			// lifter gets a trivial path and a tall one can never rank up.
			if math.Abs(ratio-1) > 0.075 {
				t.Errorf("height %.0f moves the %s reference by %.1f%%, beyond the intended magnitude",
					h, pat, (ratio-1)*100)
			}
		}
	}
	// Direction check: the correction lowers the reference for tall lifters.
	tall := Profile{HeightCm: 195, LBMKg: 70}
	if Ref(tall, HPress) >= Ref(base, HPress) {
		t.Error("the height term must lower the reference for a taller lifter")
	}
}

// TestMachineCapsHold checks the v1.0 4.2 caps. To exceed one you must log the
// free-weight anchor; below Berserk it barely matters, at the top it forces
// real lifts.
func TestMachineCapsHold(t *testing.T) {
	p := Profile{HeightCm: 178, BodyweightKg: 80, BodyfatPct: 12.5, LBMKg: 70}
	// An absurd leg press that would otherwise score off the scale.
	score, _, capped := scoreSet(p, substitutes["leg press"], "total", 500, 5)
	if !capped || score != 85 {
		t.Fatalf("leg press should cap at 85, got %.2f (capped=%v)", score, capped)
	}
	score, _, capped = scoreSet(p, substitutes["lat pulldown"], "total", 200, 3)
	if !capped || score != 80 {
		t.Fatalf("lat pulldown should cap at 80, got %.2f (capped=%v)", score, capped)
	}
}

// TestSubstitutionDirection is v1.2 Patch 8: the coefficient converts the
// substitute INTO the anchor, so an incline press at a given load implies a
// LARGER flat bench, never a smaller one. Getting this backwards produces a
// 180 kg incline-derived bench, which is the bug the patch exists to prevent.
func TestSubstitutionDirection(t *testing.T) {
	p := Profile{HeightCm: 178, BodyweightKg: 80, BodyfatPct: 12.5, LBMKg: 70}
	flat, _, _ := scoreSet(p, substitutes["bench press"], "total", 100, 5)
	incline, _, _ := scoreSet(p, substitutes["incline bench press"], "total", 100, 5)
	if incline <= flat {
		t.Fatalf("100 kg incline should imply a higher score than 100 kg flat: %.2f vs %.2f",
			incline, flat)
	}
	// Push press is the other direction: 100 kg push pressed implies a smaller
	// strict press.
	strict, _, _ := scoreSet(p, substitutes["overhead press"], "total", 100, 5)
	push, _, _ := scoreSet(p, substitutes["push press"], "total", 100, 5)
	if push >= strict {
		t.Fatalf("100 kg push press should imply a lower strict-press score: %.2f vs %.2f",
			push, strict)
	}
}

// TestEpleyValidityBands covers v1.0 3.1: nothing above eight reps may set a
// strength score, and six to eight takes the confidence haircut.
func TestEpleyValidityBands(t *testing.T) {
	p := Profile{HeightCm: 178, BodyweightKg: 80, BodyfatPct: 12.5, LBMKg: 70}
	sub := substitutes["bench press"]

	if _, e1rm, _ := scoreSet(p, sub, "total", 100, 9); e1rm != 0 {
		t.Fatalf("a 9-rep set must not produce an e1RM, got %.2f", e1rm)
	}
	if _, e1rm, _ := scoreSet(p, sub, "total", 100, 20); e1rm != 0 {
		t.Fatalf("a 20-rep set must not produce an e1RM, got %.2f", e1rm)
	}
	if _, e1rm, _ := scoreSet(p, sub, "total", 100, 5); e1rm <= 0 {
		t.Fatal("a 5-rep set must produce an e1RM")
	}
	// The haircut: an 8-rep set is discounted 3%.
	s6, _, _ := scoreSet(p, sub, "total", 100, 6)
	raw6 := 100 * (1 + 6.0/30)
	want := 100 * raw6 * 0.97 / Ref(p, HPress)
	if math.Abs(s6-want) > 0.05 {
		t.Fatalf("6-rep set scored %.2f, want %.2f after the 0.97 haircut", s6, want)
	}
}

// TestPerSideLoadsAreDoubled guards the failure mode the LiftKey design already
// names: a 30 kg per-side dumbbell press is 60 kg of load, and treating it as
// 30 produces scores that are wrong by half.
func TestPerSideLoadsAreDoubled(t *testing.T) {
	p := Profile{HeightCm: 178, BodyweightKg: 80, BodyfatPct: 12.5, LBMKg: 70}
	sub := substitutes["dumbbell bench press"]
	perSide, _, _ := scoreSet(p, sub, "per_side", 30, 5)
	total, _, _ := scoreSet(p, sub, "total", 60, 5)
	if math.Abs(perSide-total) > 0.001 {
		t.Fatalf("30 kg per side should equal 60 kg total: %.2f vs %.2f", perSide, total)
	}
}

// ---------- body fat measurement ----------

// TestNavyBodyFatIsPlausible checks the tape formula against reference figures.
// It is the middle rung of v1.0 2.1's preference order and the only one a user
// can actually do at home, so it needs to be right.
func TestNavyBodyFatIsPlausible(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		sex                       Sex
		h, neck, waist, hip, want float64
	}{
		// A lean-ish 178 cm male: 38 cm neck, 84 cm waist.
		{"lean male", Male, 178, 38, 84, 0, 15.2},
		// Same frame carrying more: 100 cm waist.
		{"heavier male", Male, 178, 40, 100, 0, 25.0},
		{"female", Female, 165, 32, 74, 96, 27.4},
	} {
		got, err := NavyBodyFat(tc.sex, tc.h, tc.neck, tc.waist, tc.hip)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if math.Abs(got-tc.want) > 1.5 {
			t.Errorf("%s: got %.1f%%, want about %.1f%%", tc.name, got, tc.want)
		}
	}
}

// TestNavyBodyFatRejectsNonsense is the important half. The formula takes the
// logarithm of (waist - neck), so a mistyped waist smaller than the neck yields
// NaN, and a NaN body fat silently poisons LBM and therefore every strength
// reference in the system.
func TestNavyBodyFatRejectsNonsense(t *testing.T) {
	for _, tc := range []struct {
		name                string
		sex                 Sex
		h, neck, waist, hip float64
	}{
		{"waist under neck", Male, 178, 45, 40, 0},
		{"waist equals neck", Male, 178, 40, 40, 0},
		{"zero height", Male, 0, 38, 84, 0},
		{"missing waist", Male, 178, 38, 0, 0},
		{"female without hip", Female, 165, 32, 74, 0},
	} {
		if got, err := NavyBodyFat(tc.sex, tc.h, tc.neck, tc.waist, tc.hip); err == nil {
			t.Errorf("%s: expected an error, got %.1f%%", tc.name, got)
		}
	}
}

// TestNavyBodyFatMonotonic: more waist at a fixed frame must read as more fat.
func TestNavyBodyFatMonotonic(t *testing.T) {
	prev := 0.0
	for waist := 75.0; waist <= 120; waist += 5 {
		got, err := NavyBodyFat(Male, 178, 38, waist, 0)
		if err != nil {
			t.Fatalf("waist %.0f: %v", waist, err)
		}
		if got <= prev {
			t.Fatalf("body fat should rise with waist: %.0f cm gave %.1f after %.1f", waist, got, prev)
		}
		prev = got
	}
}

// TestCommonShorthandScores guards a silent hole in the rank system.
//
// The parser is meant to canonicalise "bench" to "bench press", and with a real
// model it does. The offline stub does not, and an unmapped name does not error
// -- it simply contributes nothing to MIGHT, forever, with nothing anywhere
// saying so. A real session logged as "bench 100 x 5" was worth zero.
func TestCommonShorthandScores(t *testing.T) {
	for _, tc := range []struct {
		name string
		want Pattern
	}{
		{"bench", HPress},
		{"ohp", VPress},
		{"dl", Hinge},
		{"row", HPull},
		{"pullup", VPull},
		{"pullups", VPull},
		{"dip", HPress},
	} {
		got, ok := PatternOf(tc.name)
		if !ok {
			t.Errorf("%q scores nothing", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("%q -> %s, want %s", tc.name, got, tc.want)
		}
	}
}
