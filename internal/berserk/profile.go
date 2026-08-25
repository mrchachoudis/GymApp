package berserk

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/mrcha/gymlogger/internal/store"
)

// Profile is the body-composition and history input to every formula. It is
// resolved once per computation because half the attributes need LBM and the
// other half need raw bodyweight, and reading them separately invites the two
// to drift apart mid-calculation.
type Profile struct {
	BodyweightKg float64 // raw scale weight, smoothed
	BodyfatPct   float64 // 0..100
	BFSource     string  // dexa|caliper|navy|estimate
	HeightCm     float64
	LBMKg        float64 // smoothed, guardrailed
	FFMIAdj      float64

	TrainingMonths float64
	VO2maxEst      float64
	SessionMinutes float64 // nominal session length, for V_density
	GoalProfile    string  // balanced|power|physique

	// Frozen reports that reference recalculation is suspended because the
	// reported body fat moved more than four points in thirty days (v1.0 §9).
	// A number that jumps that far is a re-measure or a lie, and either way
	// the references should not chase it.
	Frozen bool

	// Estimated is true when body fat came from a fallback rather than a
	// measurement. It widens the confidence band (v1.0 §2.1).
	Estimated bool
}

// defaults exist so a brand-new database produces a real score instead of a
// division by zero. They are the v1.0 §3.3 reference lifter, which is also the
// height at which H_adj is exactly 1.
const (
	defaultHeightCm       = 178.0
	defaultBodyweightKg   = 80.0
	defaultSessionMinutes = 75.0
)

// LoadProfile assembles the profile from the body_metrics series, falling back
// to settings and then to the reference lifter.
func LoadProfile(ctx context.Context, st *store.Store, asOf time.Time) (Profile, error) {
	p := Profile{
		HeightCm:       settingFloat(ctx, st, "height_cm", defaultHeightCm),
		TrainingMonths: settingFloat(ctx, st, "training_months", 0),
		VO2maxEst:      settingFloat(ctx, st, "vo2max_est", 0),
		SessionMinutes: settingFloat(ctx, st, "avg_session_minutes", defaultSessionMinutes),
		GoalProfile:    st.Setting(ctx, "goal_profile", "balanced"),
	}
	if p.HeightCm <= 0 {
		p.HeightCm = defaultHeightCm
	}
	if p.SessionMinutes <= 0 {
		p.SessionMinutes = defaultSessionMinutes
	}

	series, err := bodySeries(ctx, st, asOf)
	if err != nil {
		return p, err
	}

	switch {
	case len(series) > 0:
		p.BodyweightKg = ewma(series, func(m bodyPoint) float64 { return m.bw }, asOf)
		p.BodyfatPct = ewma(series, func(m bodyPoint) float64 { return m.bf }, asOf)
		p.BFSource = series[len(series)-1].source
		p.Frozen = bfJumped(series, asOf)
	default:
		p.BodyweightKg = st.BodyweightKg(ctx)
		p.BodyfatPct = settingFloat(ctx, st, "bodyfat_pct", 0)
		p.BFSource = st.Setting(ctx, "bodyfat_source", "estimate")
	}
	if p.BodyweightKg <= 0 {
		p.BodyweightKg = defaultBodyweightKg
	}
	if p.BodyfatPct <= 0 {
		p.BodyfatPct = estimateBodyfat(p)
		p.BFSource = "estimate"
	}
	// Bound the reported value before it reaches LBM. A 3% or 60% entry is a
	// typo, and a typo must not be allowed to move a reference load.
	p.BodyfatPct = clamp(p.BodyfatPct, 3, 60)
	p.Estimated = p.BFSource == "estimate"

	p.LBMKg = p.BodyweightKg * (1 - p.BodyfatPct/100)
	hm := p.HeightCm / 100
	// v1.0 §6.2, the standard height normalization.
	p.FFMIAdj = p.LBMKg/(hm*hm) + 6.1*(1.80-hm)
	return p, nil
}

// estimateBodyfat is the last fallback in v1.0 §2.1's preference order. It is
// a BMI-derived approximation for an adult male and it is deliberately crude:
// its only job is to keep the engine running until a real measurement exists,
// and it tags itself "estimate" so the confidence bar reflects that.
//
// CONSTRUCTED: v1.0 §2.1 names "a fallback estimate from BMI and age" without
// giving the function. Deurenberg is the conventional choice; age is fixed at
// 30 because the app does not collect it.
func estimateBodyfat(p Profile) float64 {
	hm := p.HeightCm / 100
	if hm <= 0 {
		return 20
	}
	bmi := p.BodyweightKg / (hm * hm)
	const age, male = 30.0, 1.0
	return clamp(1.20*bmi+0.23*age-10.8*male-5.4, 5, 55)
}

type bodyPoint struct {
	date   time.Time
	bw, bf float64
	source string
}

func bodySeries(ctx context.Context, st *store.Store, asOf time.Time) ([]bodyPoint, error) {
	end := st.LocalDate(asOf)
	start := asOf.AddDate(0, 0, -smoothingDays).Format("2006-01-02")
	rows, err := st.DB().QueryContext(ctx, `
		SELECT local_date, bodyweight_kg, COALESCE(bodyfat_pct, 0), bf_source
		FROM body_metrics
		WHERE local_date >= ? AND local_date <= ?
		ORDER BY local_date`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []bodyPoint
	for rows.Next() {
		var ds string
		var b bodyPoint
		if err := rows.Scan(&ds, &b.bw, &b.bf, &b.source); err != nil {
			return nil, err
		}
		d, err := time.Parse("2006-01-02", ds)
		if err != nil {
			continue
		}
		b.date = d
		out = append(out, b)
	}
	return out, rows.Err()
}

// smoothingDays is v1.0 §2.1's eight-week window.
const smoothingDays = 56

// ewma weights recent readings more heavily on a half-life of two weeks, which
// tracks a real cut without chasing a salty dinner.
func ewma(pts []bodyPoint, pick func(bodyPoint) float64, asOf time.Time) float64 {
	const halfLifeDays = 14.0
	var num, den float64
	for _, p := range pts {
		v := pick(p)
		if v <= 0 {
			continue
		}
		age := asOf.Sub(p.date).Hours() / 24
		if age < 0 {
			age = 0
		}
		w := math.Pow(0.5, age/halfLifeDays)
		num += w * v
		den += w
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// bfJumped implements the v1.0 §9 guardrail: more than four points of body-fat
// movement inside thirty days is measurement error or a lie, and the correct
// response is to stop moving the references and ask for a re-measure.
func bfJumped(pts []bodyPoint, asOf time.Time) bool {
	cutoff := asOf.AddDate(0, 0, -30)
	lo, hi := math.Inf(1), math.Inf(-1)
	n := 0
	for _, p := range pts {
		if p.bf <= 0 || p.date.Before(cutoff) {
			continue
		}
		lo, hi, n = math.Min(lo, p.bf), math.Max(hi, p.bf), n+1
	}
	return n >= 2 && hi-lo > 4
}

// GoalWeights returns the RS attribute weights for the profile.
//
// v1.0 §1: the user may shift up to ±0.06 between MIGHT, FRAME and VIGOR. The
// gates never move, which is what keeps a rank comparable across users — a
// powerlifting profile changes how fast you climb, never what Berserk means.
func (p Profile) GoalWeights() Weights {
	w := Weights{MIGHT: 0.40, DOMINION: 0.15, FRAME: 0.15, VIGOR: 0.12, DISCIPLINE: 0.10, MASTERY: 0.08}
	switch p.GoalProfile {
	case "power":
		w.MIGHT, w.VIGOR = 0.46, 0.06
	case "physique":
		w.FRAME, w.VIGOR = 0.21, 0.06
	}
	return w
}

// Weights are the RS coefficients. They sum to 1 by construction; the test
// asserts it for every profile because a profile that does not sum to 1
// silently rescales the whole ladder.
type Weights struct {
	MIGHT, DOMINION, FRAME, VIGOR, DISCIPLINE, MASTERY float64
}

func (w Weights) Sum() float64 {
	return w.MIGHT + w.DOMINION + w.FRAME + w.VIGOR + w.DISCIPLINE + w.MASTERY
}

func settingFloat(ctx context.Context, st *store.Store, key string, def float64) float64 {
	v := st.Setting(ctx, key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// ---------- body fat measurement ----------

// Sex affects the tape formula and nothing else in this package. The strength
// standards are calibrated against a trained male lifter (v1.0 3.3), which is a
// limitation of the source data rather than a design choice, and it is recorded
// here so the gap is visible rather than implied.
type Sex string

const (
	Male   Sex = "male"
	Female Sex = "female"
)

// NavyBodyFat estimates body fat from tape measurements, which is the middle
// option in v1.0 2.1's preference order: better than a BMI guess, worse than a
// DEXA scan, and the only one of the three a user can do at home in a minute.
//
// All measurements are centimetres. hip is required for female and ignored for
// male, which is the formula's own asymmetry, not ours.
//
// The result is clamped to a plausible human range. The formula involves a
// logarithm of (waist - neck), so a mistyped waist smaller than the neck would
// otherwise produce NaN and quietly poison every strength reference downstream.
func NavyBodyFat(sex Sex, heightCm, neckCm, waistCm, hipCm float64) (float64, error) {
	if heightCm <= 0 || neckCm <= 0 || waistCm <= 0 {
		return 0, fmt.Errorf("height, neck and waist are required")
	}
	var bf float64
	switch sex {
	case Female:
		if hipCm <= 0 {
			return 0, fmt.Errorf("hip measurement is required for the female formula")
		}
		d := waistCm + hipCm - neckCm
		if d <= 0 {
			return 0, fmt.Errorf("waist plus hip must exceed neck; check the measurements")
		}
		bf = 495/(1.29579-0.35004*math.Log10(d)+0.22100*math.Log10(heightCm)) - 450
	default:
		d := waistCm - neckCm
		if d <= 0 {
			return 0, fmt.Errorf("waist must exceed neck; check the measurements")
		}
		bf = 495/(1.0324-0.19077*math.Log10(d)+0.15456*math.Log10(heightCm)) - 450
	}
	if math.IsNaN(bf) || math.IsInf(bf, 0) {
		return 0, fmt.Errorf("measurements do not produce a usable estimate")
	}
	return round1(clamp(bf, 3, 60)), nil
}
