package berserk

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/mrcha/gymlogger/internal/model"
	"github.com/mrcha/gymlogger/internal/muscle"
	"github.com/mrcha/gymlogger/internal/store"
)

// Attributes are the six axes of v1.0 1, each 0..120 where 100 is
// Berserk-level in that attribute.
type Attributes struct {
	Might      float64 `json:"might"`
	Dominion   float64 `json:"dominion"`
	Frame      float64 `json:"frame"`
	Vigor      float64 `json:"vigor"`
	Discipline float64 `json:"discipline"`
	Mastery    float64 `json:"mastery"`
}

// Get reads an attribute by gate name, so the gate loop does not have to be a
// six-armed switch that someone will eventually get wrong.
func (a Attributes) Get(name string) float64 {
	switch name {
	case "MIGHT":
		return a.Might
	case "DOMINION":
		return a.Dominion
	case "FRAME":
		return a.Frame
	case "VIGOR":
		return a.Vigor
	case "DISCIPLINE":
		return a.Discipline
	case "MASTERY":
		return a.Mastery
	}
	return 0
}

// Breakdown carries the component terms behind each attribute. They are stored
// and displayed because v1.0 14.2 requires every input to every formula to be
// recoverable: when the allometric exponent is retuned, history has to be
// recomputable rather than orphaned.
type Breakdown struct {
	// MIGHT
	HM  float64 `json:"hm"`
	LOW float64 `json:"low"`

	// DOMINION
	DPullup float64 `json:"d_pullup"`
	DDip    float64 `json:"d_dip"`
	DReps   float64 `json:"d_reps"`
	DPress  float64 `json:"d_press"`
	DSquat  float64 `json:"d_squat"`

	// FRAME
	SFFMI  float64 `json:"s_ffmi"`
	BFMod  float64 `json:"bf_mod"`
	FFMIAd float64 `json:"ffmi_adj"`

	// VIGOR
	VVolume  float64 `json:"v_volume"`
	VCardio  float64 `json:"v_cardio"`
	VDensity float64 `json:"v_density"`
	VRecover float64 `json:"v_recover"`

	// DISCIPLINE
	A30  float64 `json:"a30"`
	A365 float64 `json:"a365"`
	TA   float64 `json:"ta"`

	// MASTERY
	MBreadth   float64 `json:"m_breadth"`
	MQuality   float64 `json:"m_quality"`
	MSkill     float64 `json:"m_skill"`
	MLongevity float64 `json:"m_longevity"`
}

// ---------- MIGHT ----------

const (
	// mightHMWeight and mightLOWWeight are v1.1's 0.70/0.30 split, kept by
	// v1.2 Patch 4. It is marginally softer on specialists than v1.0's
	// 0.65/0.35, which is acceptable because the Patch 3 pattern floors now do
	// most of that work.
	mightHMWeight  = 0.70
	mightLOWWeight = 0.30
)

// might aggregates the six pattern scores.
//
// An arithmetic mean is fatal here: it lets a 200 kg bench paper over a 60 kg
// squat. The harmonic mean punishes a weak link automatically, and the
// explicit LOW term -- the mean of the two lowest patterns -- punishes it
// again. A bench specialist at 100/55/55/55/55/55 scores below a completely
// unremarkable lifter at a flat 65, which is the correct outcome.
func might(scores []PatternScore) (value, hm, low float64) {
	eff := make([]float64, 0, len(scores))
	for _, s := range scores {
		// Erratum 3: floor at aggregation, never at storage. A pattern of 0 is
		// reachable -- no vertical pull data before imputation resolves, or no
		// tested patterns at all -- and 1/0 makes MIGHT NaN and the rank
		// undefined.
		eff = append(eff, math.Max(s.Score, patternScoreFloor))
	}
	if len(eff) == 0 {
		return 0, 0, 0
	}

	var recip float64
	for _, s := range eff {
		recip += 1 / s
	}
	hm = float64(len(eff)) / recip

	sorted := append([]float64(nil), eff...)
	sort.Float64s(sorted)
	n := 2
	if len(sorted) < n {
		n = len(sorted)
	}
	var lowSum float64
	for _, s := range sorted[:n] {
		lowSum += s
	}
	low = lowSum / float64(n)

	return clamp(mightHMWeight*hm+mightLOWWeight*low, 0, patternScoreCap), hm, low
}

// ---------- DOMINION ----------

// dominion scores relative strength against RAW bodyweight, which is the whole
// point of the attribute: this is the third of the score that a misreported
// body fat percentage cannot touch, because the scale is verifiable (v1.0 2.1).
//
// A lifter at Berserk-level DOMINION does a pull-up with +55% bodyweight, a dip
// with +62%, fifteen strict pull-ups, presses bodyweight overhead and squats
// double bodyweight. That is a coherent, strong, non-mythical human.
func dominion(ctx context.Context, st *store.Store, p Profile, asOf time.Time, b *Breakdown) (float64, error) {
	bw := p.BodyweightKg
	if bw <= 0 {
		return 0, nil
	}
	end := st.LocalDate(asOf)
	start := asOf.AddDate(0, 0, -provenWindowDays).Format("2006-01-02")

	rows, err := st.DB().QueryContext(ctx, `
		SELECT name, load_basis, COALESCE(weight_kg, 0), COALESCE(reps, reps_low, 0)
		FROM v_sets
		WHERE set_type = 'working' AND local_date >= ? AND local_date <= ?`, start, end)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var addedPullup, addedDip, strictReps, ohp, squat float64
	haveAdded := map[string]bool{}

	for rows.Next() {
		var name, basis string
		var w float64
		var reps int
		if err := rows.Scan(&name, &basis, &w, &reps); err != nil {
			return 0, err
		}
		switch name {
		case "pull-up", "weighted pull-up", "chin-up":
			// Erratum 3: added is negative under band or machine assistance,
			// and that has to survive into the score rather than being clamped
			// away at the input.
			if !haveAdded["pullup"] || w > addedPullup {
				addedPullup, haveAdded["pullup"] = w, true
			}
			if reps > int(strictReps) && w <= 0 {
				// Strict rep count is a bodyweight statistic; a weighted set
				// says nothing about how many unloaded reps are available.
				strictReps = float64(reps)
			}
		case "dips", "weighted dip":
			if !haveAdded["dip"] || w > addedDip {
				addedDip, haveAdded["dip"] = w, true
			}
		case "overhead press", "shoulder press":
			if e, ok := epleyTotal(basis, w, reps); ok && e > ohp {
				ohp = e
			}
		case "squat", "back squat":
			if e, ok := epleyTotal(basis, w, reps); ok && e > squat {
				squat = e
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// Erratum 2 clamps every component. Without the floor an assisted pull-up
	// produced a negative contribution; without the ceiling a single outlier
	// could carry the attribute.
	b.DPullup = clampIf(haveAdded["pullup"], 100*(bw+addedPullup)/(1.55*bw))
	b.DDip = clampIf(haveAdded["dip"], 100*(bw+addedDip)/(1.62*bw))
	b.DReps = clamp(100*strictReps/15, 0, 120)
	b.DPress = clamp(100*ohp/bw, 0, 120)
	b.DSquat = clamp(100*squat/(2*bw), 0, 120)

	return clamp(0.30*b.DPullup+0.20*b.DDip+0.15*b.DReps+0.15*b.DPress+0.20*b.DSquat, 0, 120), nil
}

// clampIf returns 0 when the evidence is absent, rather than computing a score
// from a missing measurement. A user who has never logged a dip should read as
// untested, not as "dip with zero added weight".
func clampIf(have bool, v float64) float64 {
	if !have {
		return 0
	}
	return clamp(v, 0, 120)
}

func epleyTotal(basis string, w float64, reps int) (float64, bool) {
	load := w
	if model.LoadBasis(basis) == model.BasisPerSide || model.LoadBasis(basis) == model.BasisPerLimb {
		load = w * 2
	}
	return model.Epley1RM(load, reps)
}

// ---------- FRAME ----------

// frame scores physique from FFMI and body composition.
//
// The body-fat modifier is a plateau, not a cliff, and that single shape
// satisfies both stated requirements at once: it does not punish a healthy
// bulk or cut anywhere inside 8-15%, and it never rewards dangerous leanness,
// because below 8% the modifier turns back down. Sub-8% is not health, it is
// a photoshoot.
func frame(p Profile, b *Breakdown) float64 {
	// FFMI 23.5 as the 100-point mark is deliberate: the natural ceiling sits
	// around 25-26, so 23.5 is "obviously a serious lifter" without requiring
	// the top 0.1% of genetics.
	b.FFMIAd = p.FFMIAdj
	b.SFFMI = clamp(100*(p.FFMIAdj-16.0)/(23.5-16.0), 0, 120)

	bf := p.BodyfatPct
	mod := 1.0
	switch {
	case bf > 15:
		mod = 1.00 - 0.9*(bf-15)/100
	case bf < 8:
		mod = 1.00 - 1.5*(8-bf)/100
	}
	// Erratum 2: floor at 0.50 so extreme body composition DEGRADES frame
	// rather than annihilating it. At 60% body fat the unclamped modifier is
	// 0.595; at the clamp boundary the attribute still carries information.
	b.BFMod = clamp(mod, 0.50, 1.00)

	return clamp(b.SFFMI*b.BFMod, 0, 120)
}

// ---------- VIGOR ----------

// vigor scores work capacity and conditioning.
//
// On whether the VIGOR gate excludes physique- or strength-focused users
// (Erratum 4): a zero-cardio lifter at full marks on everything else scores
// exactly 70.0, so the gate binds -- but at realistic values the cardio
// contribution required is V_cardio >= 21.7, i.e. VO2max ~34, which is roughly
// a brisk four-flight stair climb. Anyone training three days a week clears it
// incidentally. The gate demands a floor of general physical function, not
// conditioning as a second discipline. Do not raise it: at VIGOR 80 the
// required VO2max jumps to ~39 and it becomes a second sport.
func vigor(ctx context.Context, st *store.Store, p Profile, asOf time.Time, b *Breakdown) (float64, error) {
	sets, rest, err := volumeAndRest(ctx, st, asOf)
	if err != nil {
		return 0, err
	}

	b.VVolume = 100 * math.Min(1, sets.weeklyHardSets/16)
	// Erratum 2: V_cardio went negative below VO2max 30, which is an ordinary
	// value for a deconditioned beginner, and a negative attribute
	// contribution is not a thing.
	b.VCardio = clamp(100*(p.VO2maxEst-30)/18, 0, 120)
	b.VDensity = 100 * math.Min(1, sets.setsPerHour/14)
	// Erratum 2: V_recover went negative above five rest days a week -- anyone
	// on a deload or coming back from illness. Zero rest days is not
	// discipline either, it is a pending deload, so both extremes are penalised
	// and the clamp only removes the part that was never meant to be signed.
	b.VRecover = clamp(100*(1-math.Abs(rest-2)/3), 0, 100)

	return clamp(0.35*b.VVolume+0.30*b.VCardio+0.20*b.VDensity+0.15*b.VRecover, 0, 120), nil
}

type volumeStats struct {
	weeklyHardSets float64
	setsPerHour    float64
}

// volumeAndRest derives the VIGOR inputs from the session log.
//
// v1.0 6.3 asks for weekly hard sets as a per-muscle-group median. That used to
// be approximated with the six movement patterns standing in as volume units,
// because no exercise-to-muscle-group mapping existed anywhere in the app. It
// now comes from internal/muscle, so the term means what the spec said, and the
// CONSTRUCTED note that stood here is retired.
//
// One approximation remains and is still marked: sessions carry no duration, so
// sets-per-hour uses the configurable avg_session_minutes setting rather than a
// measured elapsed time.
func volumeAndRest(ctx context.Context, st *store.Store, asOf time.Time) (volumeStats, float64, error) {
	const weeks = 4
	var vs volumeStats

	weekly, err := muscle.WeeklyMedian(ctx, st, asOf, weeks)
	if err != nil {
		return vs, 0, err
	}
	vs.weeklyHardSets = weekly

	end := st.LocalDate(asOf)
	start := asOf.AddDate(0, 0, -weeks*7+1)

	rows, err := st.DB().QueryContext(ctx, `
		SELECT local_date, COUNT(*)
		FROM v_sets
		WHERE set_type IN ('working','backoff','drop')
		  AND local_date >= ? AND local_date <= ?
		GROUP BY local_date`, start.Format("2006-01-02"), end)
	if err != nil {
		return vs, 0, err
	}
	defer rows.Close()

	var perDay []float64
	trainingDays := map[int]map[string]bool{}
	for rows.Next() {
		var ds string
		var n int
		if err := rows.Scan(&ds, &n); err != nil {
			return vs, 0, err
		}
		day, err := time.Parse("2006-01-02", ds)
		if err != nil {
			continue
		}
		week := int(day.Sub(start).Hours() / 24 / 7)
		if trainingDays[week] == nil {
			trainingDays[week] = map[string]bool{}
		}
		trainingDays[week][ds] = true
		perDay = append(perDay, float64(n))
	}
	if err := rows.Err(); err != nil {
		return vs, 0, err
	}

	// Sets per hour, from the median training day.
	sort.Float64s(perDay)
	if len(perDay) > 0 {
		minutes := settingFloat(ctx, st, "avg_session_minutes", defaultSessionMinutes)
		if minutes > 0 {
			vs.setsPerHour = perDay[len(perDay)/2] / (minutes / 60)
		}
	}

	// Rest days per week, averaged. Weeks with no training at all are counted:
	// seven rest days is exactly the signal V_recover exists to catch.
	var restTotal float64
	for w := 0; w < weeks; w++ {
		restTotal += 7 - float64(len(trainingDays[w]))
	}
	return vs, restTotal / float64(weeks), nil
}

// ---------- DISCIPLINE ----------

// discipline scores consistency and training age.
//
// Training age is logarithmic on purpose: year one is worth far more than year
// eight, which is how adaptation actually works. It saturates at ten years.
func discipline(ctx context.Context, st *store.Store, p Profile, asOf time.Time, b *Breakdown) (float64, error) {
	n30, err := sessionCount(ctx, st, asOf, 30)
	if err != nil {
		return 0, err
	}
	n365, err := sessionCount(ctx, st, asOf, 365)
	if err != nil {
		return 0, err
	}

	b.A30 = math.Min(1, float64(n30)/13)
	b.A365 = math.Min(1, float64(n365)/156)
	b.TA = clamp(math.Log(1+p.TrainingMonths/6)/math.Log(1+120.0/6), 0, 1)

	return clamp(100*(0.40*b.A30+0.35*b.A365+0.25*b.TA), 0, 120), nil
}

// sessionCount counts distinct days that contained real work. A day with only
// warmups logged is not a session, on the same reasoning that keeps warmups
// out of volume totals.
func sessionCount(ctx context.Context, st *store.Store, asOf time.Time, days int) (int, error) {
	end := st.LocalDate(asOf)
	start := asOf.AddDate(0, 0, -days+1).Format("2006-01-02")
	var n int
	err := st.DB().QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT local_date) FROM v_sets
		WHERE set_type IN ('working','backoff','drop')
		  AND local_date >= ? AND local_date <= ?`, start, end).Scan(&n)
	return n, err
}

// ---------- MASTERY ----------

// mastery scores breadth, technical quality, skill and longevity of gains.
func mastery(ctx context.Context, st *store.Store, asOf time.Time,
	lifts map[string]*liftScore, b *Breakdown) (float64, error) {

	breadth := 0
	for _, ls := range lifts {
		if ls.score >= 60 {
			breadth++
		}
	}
	b.MBreadth = 100 * math.Min(1, float64(breadth)/14)

	quality, err := qualityRatio(ctx, st, asOf)
	if err != nil {
		return 0, err
	}
	b.MQuality = 100 * quality

	var skills int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM skill_unlocks WHERE skill IN (`+skillPlaceholders+`)`,
		skillArgs()...).Scan(&skills); err != nil {
		return 0, err
	}
	b.MSkill = 100 * math.Min(1, float64(skills)/12)

	held, err := liftsHeldWithoutRegression(ctx, st, asOf)
	if err != nil {
		return 0, err
	}
	b.MLongevity = 100 * math.Min(1, float64(held)/6)

	return clamp(0.30*b.MBreadth+0.30*b.MQuality+0.20*b.MSkill+0.20*b.MLongevity, 0, 120), nil
}

// qualityRatio is the ROM/tempo-verified share of logged sets.
//
// CONSTRUCTED: the schema has no explicit ROM or tempo flag. clean_reps is the
// closest honest signal it does carry -- the user stating how many reps were
// actually clean -- so a set counts as verified when clean_reps is present and
// accounts for every rep. Sets where the user reported partials therefore
// correctly fail to count.
func qualityRatio(ctx context.Context, st *store.Store, asOf time.Time) (float64, error) {
	end := st.LocalDate(asOf)
	start := asOf.AddDate(0, 0, -provenWindowDays).Format("2006-01-02")
	var total, verified int
	err := st.DB().QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN clean_reps IS NOT NULL
		                          AND clean_reps >= COALESCE(reps, reps_low, 0)
		                         THEN 1 ELSE 0 END), 0)
		FROM v_sets
		WHERE set_type IN ('working','backoff','drop')
		  AND local_date >= ? AND local_date <= ?`, start, end).Scan(&total, &verified)
	if err != nil {
		return 0, err
	}
	if total == 0 {
		return 0, nil
	}
	return float64(verified) / float64(total), nil
}

// liftsHeldWithoutRegression counts movements whose recent best is at least
// their best from a year ago. Holding a lift for twelve months is the thing
// this term is trying to reward, and it is invisible to every other attribute.
func liftsHeldWithoutRegression(ctx context.Context, st *store.Store, asOf time.Time) (int, error) {
	recentStart := asOf.AddDate(0, 0, -90).Format("2006-01-02")
	oldStart := asOf.AddDate(0, 0, -365).Format("2006-01-02")
	oldEnd := asOf.AddDate(0, 0, -275).Format("2006-01-02")

	rows, err := st.DB().QueryContext(ctx, `
		SELECT name,
		       MAX(CASE WHEN local_date >= ? THEN weight_kg END) AS recent_best,
		       MAX(CASE WHEN local_date >= ? AND local_date <= ? THEN weight_kg END) AS old_best
		FROM v_sets
		WHERE set_type = 'working' AND weight_kg IS NOT NULL
		  AND COALESCE(reps, reps_low, 0) BETWEEN 1 AND 8
		GROUP BY name`, recentStart, oldStart, oldEnd)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	held := 0
	for rows.Next() {
		var name string
		var recent, old *float64
		if err := rows.Scan(&name, &recent, &old); err != nil {
			return 0, err
		}
		if recent != nil && old != nil && *recent >= *old {
			held++
		}
	}
	return held, rows.Err()
}

// Skills are the twelve binary, verifiable, load-free unlocks of v1.0 6.5.
var Skills = []string{
	"strict pause bench",
	"atg back squat",
	"deficit deadlift",
	"strict ohp no leg drive",
	"strict pull-up x10",
	"weighted dip",
	"front squat x5",
	"single-leg rdl",
	"hanging leg raise x10",
	"two-minute hang",
	"pistol squat",
	"60s plank at bw+20%",
}

// skillPlaceholders and skillArgs constrain the count to the known twelve, so
// a stray row cannot inflate M_skill past its denominator.
var skillPlaceholders = func() string {
	s := ""
	for i := range Skills {
		if i > 0 {
			s += ","
		}
		s += "?"
	}
	return s
}()

func skillArgs() []any {
	out := make([]any, len(Skills))
	for i, s := range Skills {
		out[i] = s
	}
	return out
}
