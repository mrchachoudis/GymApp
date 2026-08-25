package berserk

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/mrcha/gymlogger/internal/model"
	"github.com/mrcha/gymlogger/internal/store"
)

// Pattern is one of the six movement patterns. Every scored lift maps to
// exactly one (v1.0 4.1), and a pattern score is its best qualifying lift
// after conversion -- not an average, because averaging lets a lifter dilute a
// weak pattern by logging many mediocre variants of it.
type Pattern string

const (
	HPress Pattern = "h_press"
	VPress Pattern = "v_press"
	Squat  Pattern = "squat"
	Hinge  Pattern = "hinge"
	VPull  Pattern = "v_pull"
	HPull  Pattern = "h_pull"
)

// Patterns is the canonical order. Everything user-facing iterates this rather
// than ranging a map, so the six rows never shuffle between screens.
var Patterns = []Pattern{HPress, VPress, Squat, Hinge, VPull, HPull}

func (p Pattern) Display() string {
	switch p {
	case HPress:
		return "Horizontal Press"
	case VPress:
		return "Vertical Press"
	case Squat:
		return "Squat"
	case Hinge:
		return "Hinge"
	case VPull:
		return "Vertical Pull"
	case HPull:
		return "Horizontal Pull"
	}
	return string(p)
}

// Short is the compact label used in the gate readout, where six rows have to
// line up in a phone-width column.
func (p Pattern) Short() string {
	switch p {
	case HPress:
		return "H-PRESS"
	case VPress:
		return "V-PRESS"
	case Squat:
		return "SQUAT"
	case Hinge:
		return "HINGE"
	case VPull:
		return "V-PULL"
	case HPull:
		return "H-PULL"
	}
	return string(p)
}

// anchor holds the v1.0 3.3 calibration constants and the 2.2 height
// exponent. k is calibrated against the reference lifter: 178 cm, 80 kg,
// 12.5% BF, LBM 70 kg, for whom LBM^0.70 = 19.57.
type anchor struct {
	k        float64
	heightP  float64
	liftName string
}

var anchors = map[Pattern]anchor{
	HPress: {6.64, 0.50, "bench press"},
	VPress: {4.19, 0.50, "overhead press"},
	Squat:  {9.20, 0.40, "squat"},
	Hinge:  {11.24, 0.15, "deadlift"},
	VPull:  {6.39, 0.20, "pull-up"},
	HPull:  {5.62, 0.20, "barbell row"},
}

// allometricExponent is v1.0 3.2's second load-bearing decision and the
// system's single tuning knob. Lower it and light lifters get harder targets;
// raise it and heavy lifters do. The defensible band is 0.67 to 0.75.
const allometricExponent = 0.70

// referenceHeightCm is the height at which H_adj is exactly 1.
const referenceHeightCm = 178.0

// Ref returns the load that scores exactly 100 on a pattern, for this profile.
//
// Ref = k * LBM^0.70 * H_adj, with H_adj = (178/H)^p. Note the direction: the
// height term LOWERS the reference for tall lifters, partially offsetting the
// higher reference their greater lean mass already earned them. That is
// deliberate and it is not double-counting (v1.0 2.2).
func Ref(p Profile, pat Pattern) float64 {
	a, ok := anchors[pat]
	if !ok {
		return 0
	}
	hAdj := math.Pow(referenceHeightCm/p.HeightCm, a.heightP)
	return a.k * math.Pow(p.LBMKg, allometricExponent) * hAdj
}

// substitute converts a logged lift into its pattern anchor.
//
// Convention, stated once here because v1.2 Patch 8 exists precisely because
// v1.1 left it ambiguous and someone was going to implement it backwards:
//
//	anchor_equivalent = substitute_e1RM * coef
//
// So a coefficient above 1 means the substitute is EASIER than the anchor at
// the same load (incline bench at 100 kg implies a bigger flat bench), and a
// coefficient below 1 means it is harder. Then the confidence discount applies.
type substitute struct {
	pattern Pattern
	coef    float64
	// conf is the substitution confidence discount (v1.0 4.2): 1.00 for an
	// anchor, 0.95 for a free-weight substitute, 0.90 for a machine whose load
	// number means nothing without a machine-specific calibration.
	conf float64
	// cap ceilings the score this lift may produce. Leg press is capped at 85
	// because you can leg press a car through a quarter of the range of motion
	// and it tells nobody anything. To exceed a cap you must log the anchor.
	cap float64
	// bodyweightBased means the scored load is total system load, BW + added.
	// This is the mechanism that makes a heavier lifter's pull-up numbers
	// legitimately harder to hit, and it is why a cut pays out immediately.
	bodyweightBased bool
}

// substitutes is v1.0 4.2. Names are the canonical lowercase forms the parser
// emits; unmapped lifts simply do not score, which is correct -- a lateral
// raise is not evidence about any pattern.
//
// CONSTRUCTED: v1.0 lists "machine chest press" and "hack squat / pendulum"
// with a confidence but no coefficient (machine-calibrated). They are given
// coef 1.00 and the machine cap of 85 here, on the same reasoning that caps
// leg press: without a per-machine calibration the number is unanchored, so it
// may support a mid-range score and no more.
var substitutes = map[string]substitute{
	// Horizontal press
	"bench press":          {HPress, 1.00, 1.00, 0, false},
	"paused bench press":   {HPress, 1.06, 0.95, 0, false},
	"incline bench press":  {HPress, 1.22, 0.95, 0, false},
	"dumbbell bench press": {HPress, 1.08, 0.95, 0, false},
	"dumbbell press":       {HPress, 1.08, 0.95, 0, false},
	"dips":                 {HPress, 0.92, 0.95, 0, true},
	"weighted dip":         {HPress, 0.92, 0.95, 0, true},
	"machine chest press":  {HPress, 1.00, 0.90, 85, false},

	// Vertical press
	"overhead press":          {VPress, 1.00, 1.00, 0, false},
	"shoulder press":          {VPress, 1.00, 1.00, 0, false},
	"seated overhead press":   {VPress, 0.93, 0.95, 0, false},
	"push press":              {VPress, 0.80, 0.95, 0, false},
	"dumbbell shoulder press": {VPress, 1.12, 0.95, 0, false},

	// Squat
	"squat":                 {Squat, 1.00, 1.00, 0, false},
	"back squat":            {Squat, 1.00, 1.00, 0, false},
	"front squat":           {Squat, 1.22, 0.95, 0, false},
	"hack squat":            {Squat, 1.00, 0.90, 85, false},
	"pendulum squat":        {Squat, 1.00, 0.90, 85, false},
	"leg press":             {Squat, 0.40, 0.85, 85, false},
	"bulgarian split squat": {Squat, 0.75, 0.95, 0, true},

	// Hinge
	"deadlift":              {Hinge, 1.00, 1.00, 0, false},
	"conventional deadlift": {Hinge, 1.00, 1.00, 0, false},
	"sumo deadlift":         {Hinge, 1.00, 1.00, 0, false},
	"rdl":                   {Hinge, 1.20, 0.95, 0, false},
	"romanian deadlift":     {Hinge, 1.20, 0.95, 0, false},
	"trap bar deadlift":     {Hinge, 0.92, 0.95, 0, false},
	"hip thrust":            {Hinge, 0.55, 0.95, 80, false},
	"good morning":          {Hinge, 1.65, 0.95, 0, false},

	// Vertical pull
	"pull-up":          {VPull, 1.00, 1.00, 0, true},
	"weighted pull-up": {VPull, 1.00, 1.00, 0, true},
	"chin-up":          {VPull, 0.95, 0.95, 0, true},
	"lat pulldown":     {VPull, 1.00, 0.88, 80, false},

	// Horizontal pull
	"barbell row":         {HPull, 1.00, 1.00, 0, false},
	"pendlay row":         {HPull, 1.05, 0.95, 0, false},
	"chest-supported row": {HPull, 0.90, 0.95, 0, false},
	"dumbbell row":        {HPull, 0.85, 0.95, 0, false},
	"seated cable row":    {HPull, 0.85, 0.90, 0, false},
}

// PatternScore is one pattern's standing, with everything the UI needs to
// explain it. v1.0 14.3: the single highest-value element in this system is
// the line naming the weak link, and it cannot be written without the source
// lift and the reference beside the score.
type PatternScore struct {
	Pattern    Pattern `json:"pattern"`
	Name       string  `json:"name"`
	Score      float64 `json:"score"` // 0..120, 0 when untested
	Status     Status  `json:"status"`
	SourceLift string  `json:"source_lift,omitempty"`
	E1RMKg     float64 `json:"e1rm_kg,omitempty"`
	RefKg      float64 `json:"ref_kg"`
	Imputed    bool    `json:"imputed"`
	Capped     bool    `json:"capped,omitempty"`
	// Qualifying counts in-app sets that could support verification. Two of
	// them, fourteen days apart, is what VERIFIED means.
	Qualifying int `json:"qualifying_sets"`
}

// Status is the evidence standing of a pattern (v1.2 Patch 5). It is
// evidence-based, not account-age-based: the question is whether the app has
// seen the lift, not how long the user has had an account.
type Status string

const (
	// Unproven: no qualifying data. The score is imputed and flagged.
	Unproven Status = "UNPROVEN"
	// Provisional: self-reported at onboarding, scored at 0.93 confidence.
	// Carries a user to DREADBORN and no further.
	Provisional Status = "PROVISIONAL"
	// Proven: one qualifying set logged in-app within 180 days.
	Proven Status = "PROVEN"
	// Verified: two qualifying sets, at least fourteen days apart, inside the
	// same 180 days. Required by BLACK SWORDSMAN and BERSERK.
	Verified Status = "VERIFIED"
)

// IsProven reports whether the pattern counts toward a "N proven" structural
// requirement. A verified pattern is proven; an imputed one is not.
func (s Status) IsProven() bool {
	return s == Provisional || s == Proven || s == Verified
}

const (
	// provenWindowDays is how long a qualifying set stays evidence (v1.0 4.3).
	provenWindowDays = 180
	// verifySpacingDays is the gap two sets must straddle to verify a pattern.
	// It is what makes verification cost a month rather than an afternoon.
	verifySpacingDays = 14
	// provisionalConfidence discounts a self-reported best (v1.2 Patch 5).
	provisionalConfidence = 0.93
	// patternScoreCap is v1.1 10's ceiling. Erratum 6 confirms it does not
	// obstruct the MIGHT gate: all six at 120 yields MIGHT 120.
	patternScoreCap = 120
	// patternScoreFloor is Erratum 3. Applied at aggregation only -- the true
	// zero stays in the database and stays visible in the UI as "untested" --
	// because 6/Sum(1/S) divides by zero on any pattern of 0, and a user with
	// no tested patterns has nothing to impute from, so every score is 0 and
	// MIGHT comes out NaN. That failure surfaces to the user, not to you.
	patternScoreFloor = 5.0
	// imputationFactor is v1.0 4.3. An untested pattern is worth three
	// quarters of the mean of the tested ones: enough that a beginner without
	// a full gym can still rank, low enough that the harmonic mean always
	// leaves a reason to go prove it.
	imputationFactor = 0.75
)

// liftScore is one scored movement, kept separately from pattern scores
// because the MASTERY breadth term counts movements, not patterns.
type liftScore struct {
	name    string
	pattern Pattern
	score   float64
	e1rm    float64
	capped  bool
	dates   []time.Time
}

// scoreLifts reads every qualifying working set in the proven window and
// reduces it to a best score per movement.
func scoreLifts(ctx context.Context, st *store.Store, p Profile, asOf time.Time) (map[string]*liftScore, error) {
	end := st.LocalDate(asOf)
	start := asOf.AddDate(0, 0, -provenWindowDays).Format("2006-01-02")

	rows, err := st.DB().QueryContext(ctx, `
		SELECT local_date, name, load_basis, COALESCE(weight_kg, 0),
		       COALESCE(reps, reps_low, 0)
		FROM v_sets
		WHERE set_type = 'working'
		  AND local_date >= ? AND local_date <= ?
		  AND COALESCE(reps, reps_low, 0) BETWEEN 1 AND 8`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]*liftScore{}
	for rows.Next() {
		var ds, name, basis string
		var w float64
		var reps int
		if err := rows.Scan(&ds, &name, &basis, &w, &reps); err != nil {
			return nil, err
		}
		sub, ok := substitutes[name]
		if !ok {
			continue // not evidence about any pattern
		}
		day, err := time.Parse("2006-01-02", ds)
		if err != nil {
			continue
		}

		score, e1rm, capped := scoreSet(p, sub, basis, w, reps)
		if e1rm <= 0 {
			continue
		}
		ls, seen := out[name]
		if !seen {
			ls = &liftScore{name: name, pattern: sub.pattern}
			out[name] = ls
		}
		ls.dates = append(ls.dates, day)
		if score > ls.score {
			ls.score, ls.e1rm, ls.capped = score, e1rm, capped
		}
	}
	return out, rows.Err()
}

// scoreSet turns one set into a pattern-anchor score.
func scoreSet(p Profile, sub substitute, basis string, w float64, reps int) (score, e1rm float64, capped bool) {
	load := w
	switch {
	case sub.bodyweightBased:
		// Total system load. Erratum 3: added may be negative for band or
		// machine assistance, which is exactly the case that matters -- a user
		// who cannot yet do one bodyweight pull-up still gets a real,
		// non-zero, monotonically improving score instead of falling off the
		// bottom of the system.
		load = p.BodyweightKg + w
	case model.LoadBasis(basis) == model.BasisPerSide, model.LoadBasis(basis) == model.BasisPerLimb:
		// A 30 kg per-side dumbbell press is 60 kg of load. The conversion
		// coefficients in the table are stated against totals.
		load = w * 2
	}
	if load <= 0 {
		return 0, 0, false
	}

	raw, ok := model.Epley1RM(load, reps)
	if !ok {
		return 0, 0, false
	}
	// v1.0 3.1 validity: full confidence to five reps, a 3% haircut through
	// eight, and nothing above that. Epley drifts badly past eight reps, and
	// half the sets in a typical log are twelve to twenty.
	if reps >= 6 {
		raw *= 0.97
	}

	adj := raw * sub.coef * sub.conf
	ref := Ref(p, sub.pattern)
	if ref <= 0 {
		return 0, 0, false
	}
	score = clamp(100*adj/ref, 0, patternScoreCap)
	if sub.cap > 0 && score > sub.cap {
		return sub.cap, raw, true
	}
	return score, raw, false
}

// patternScores reduces movement scores to the six pattern scores, resolves
// evidence status, and imputes the untested ones.
func patternScores(ctx context.Context, st *store.Store, p Profile, lifts map[string]*liftScore) ([]PatternScore, error) {
	claims, err := loadClaims(ctx, st)
	if err != nil {
		return nil, err
	}

	byPattern := map[Pattern]*PatternScore{}
	for _, pat := range Patterns {
		byPattern[pat] = &PatternScore{
			Pattern: pat,
			Name:    pat.Display(),
			RefKg:   round1(Ref(p, pat)),
			Status:  Unproven,
		}
	}

	// Logged evidence first. It always outranks a self-report, even when the
	// self-report is the higher number: v1.2 Patch 5 exists because a text
	// field must never be the thing that mints a rank.
	for _, ls := range lifts {
		ps := byPattern[ls.pattern]
		if ls.score > ps.Score {
			ps.Score = ls.score
			ps.SourceLift = ls.name
			ps.E1RMKg = round1(ls.e1rm)
			ps.Capped = ls.capped
		}
		ps.Qualifying += len(ls.dates)
		if got := statusFor(ls.dates); got > statusRank(ps.Status) {
			ps.Status = rankStatus(got)
		}
	}

	// Then claims, but only where nothing was logged.
	for pat, c := range claims {
		ps := byPattern[pat]
		if ps.Status.IsProven() {
			continue
		}
		ref := Ref(p, pat)
		if ref <= 0 {
			continue
		}
		ps.Score = clamp(100*c.e1rm*provisionalConfidence/ref, 0, patternScoreCap)
		ps.SourceLift = c.lift
		ps.E1RMKg = round1(c.e1rm)
		ps.Status = Provisional
	}

	// Imputation. v1.0 4.3: an untested pattern is worth 0.75 * the mean of
	// the tested ones. With nothing tested there is nothing to impute from and
	// the score stays 0, which the Erratum 3 aggregation floor then handles.
	var sum float64
	var n int
	for _, pat := range Patterns {
		if ps := byPattern[pat]; ps.Status.IsProven() {
			sum += ps.Score
			n++
		}
	}
	if n > 0 {
		mean := sum / float64(n)
		for _, pat := range Patterns {
			ps := byPattern[pat]
			if !ps.Status.IsProven() {
				ps.Score = clamp(imputationFactor*mean, 0, patternScoreCap)
				ps.Imputed = true
			}
		}
	}

	out := make([]PatternScore, 0, len(Patterns))
	for _, pat := range Patterns {
		ps := byPattern[pat]
		ps.Score = round1(ps.Score)
		out = append(out, *ps)
	}
	return out, nil
}

// statusFor decides PROVEN vs VERIFIED from a lift's qualifying dates. Two
// sets fourteen days apart is the bar: it cannot be cleared in one session,
// which is the entire point.
func statusFor(dates []time.Time) int {
	if len(dates) == 0 {
		return 0
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	span := dates[len(dates)-1].Sub(dates[0]).Hours() / 24
	if len(dates) >= 2 && span >= verifySpacingDays {
		return 3
	}
	return 2
}

func statusRank(s Status) int {
	switch s {
	case Verified:
		return 3
	case Proven:
		return 2
	case Provisional:
		return 1
	}
	return 0
}

func rankStatus(n int) Status {
	switch n {
	case 3:
		return Verified
	case 2:
		return Proven
	case 1:
		return Provisional
	}
	return Unproven
}

type claim struct {
	e1rm float64
	lift string
}

func loadClaims(ctx context.Context, st *store.Store) (map[Pattern]claim, error) {
	rows, err := st.DB().QueryContext(ctx,
		`SELECT pattern, e1rm_kg, lift FROM pattern_claims`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[Pattern]claim{}
	for rows.Next() {
		var pat string
		var c claim
		if err := rows.Scan(&pat, &c.e1rm, &c.lift); err != nil {
			return nil, err
		}
		if _, ok := anchors[Pattern(pat)]; ok {
			out[Pattern(pat)] = c
		}
	}
	return out, rows.Err()
}

// ScoreDelta converts an e1RM improvement on a named lift into pattern-score
// points, which is the unit Blood is denominated in.
//
// v1.0 7.2 is the reason this is not measured in kilograms: a beginner adding
// 10 kg to a 60 kg bench and an advanced lifter adding 2.5 kg to a 130 kg
// bench have done comparably hard things, and pricing them by load would value
// the beginner at four times the veteran.
func ScoreDelta(p Profile, liftName string, newE1RM, oldE1RM float64) float64 {
	sub, ok := substitutes[liftName]
	if !ok || newE1RM <= oldE1RM {
		return 0
	}
	ref := Ref(p, sub.pattern)
	if ref <= 0 {
		return 0
	}
	scale := 100 * sub.coef * sub.conf / ref
	return round2(clamp((newE1RM-oldE1RM)*scale, 0, patternScoreCap))
}

// IsScored reports whether a lift feeds any pattern. The pipeline uses it to
// avoid paying Blood for a PR on a movement the rank system does not score.
func IsScored(liftName string) bool {
	_, ok := substitutes[liftName]
	return ok
}
