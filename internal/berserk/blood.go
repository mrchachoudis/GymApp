package berserk

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/mrcha/gymlogger/internal/store"
)

// Blood is the post-Berserk currency (v1.2 Patch 7, rebalanced by v1.3
// Erratum 5).
//
// The design constraint that produced these numbers: a tier system must be
// reachable by consistency alone, with PRs as an accelerant. v1.1 shipped
// requirement checklists in parallel with a currency, one of which demanded
// twenty meaningful PR events per tier -- an advanced natural lifter produces
// six to ten real PRs a year, so that was a two-to-three year hard gate that
// got worse each tier and would have stalled exactly the users it was built
// for. Blood replaced it, and the rates below are what keep it a grind rather
// than a wall.
type Blood struct {
	Total float64 `json:"total"`
	// Tier is the Berserk numeral: 1 for BERSERK I, 2 for II, and so on.
	Tier      int     `json:"tier"`
	TierName  string  `json:"tier_name"`
	NextTier  string  `json:"next_tier,omitempty"`
	NextAt    float64 `json:"next_at,omitempty"`
	ToNext    float64 `json:"to_next,omitempty"`
	Progress  float64 `json:"progress"` // 0..1 within the current tier
	Last30Day float64 `json:"last_30d"`
}

// Award values, v1.3 Erratum 5.
//
// v1.2's figures were rebalanced downward here because its own arithmetic did
// not hold: the table gave 25*4.33 + 100 = 208/month against a claimed 125,
// and 8357 / 125 is 5.6 years, not the nine it reported. Both errors, in
// opposite directions. These values are set so that consistency-only
// progression is slow but never stalled.
const (
	BloodQualifyingWeek = 20.0 // week with >=3 qualifying sessions
	BloodGatesMonth     = 60.0 // month holding all Berserk gates
	BloodStrengthPRBase = 20.0 // plus 15 * delta-S
	BloodStrengthPRRate = 15.0
	BloodRepPR          = 12.0
	BloodVolumePR       = 6.0
	BloodMilestone      = 60.0
	BloodStagnantBroken = 50.0 // no PR in 180 days, then a PR
	BloodVerification   = 40.0 // new pattern verified or skill unlocked
)

// qualifyingSessionsPerWeek is the bar for a week to pay out.
const qualifyingSessionsPerWeek = 3

// BloodRequired is the cumulative Blood needed to reach tier n.
//
//	Blood_required(n) = 1200 * (n-1)^1.4
//
// NOTE ON THE PUBLISHED TABLE: this formula reproduces v1.2 Patch 7 and v1.3
// Erratum 5 exactly for tiers II through V (1200, 3167, 5587, 8357) and then
// diverges from the two illustrative rows at the bottom of both tables --- it
// gives ~18,282 for VIII against a printed 18,600, and ~34,437 for XII against
// a printed 38,900. The formula is the generative rule and the one both
// documents state, so it wins; the divergent rows are treated as approximations
// in the prose. If the printed numbers were intended as authoritative, the
// exponent is closer to 1.44 and this constant is where to change it.
func BloodRequired(n int) float64 {
	if n <= 1 {
		return 0
	}
	return 1200 * math.Pow(float64(n-1), 1.4)
}

// bloodTier inverts BloodRequired.
func bloodTier(total float64) int {
	n := 1
	for BloodRequired(n+1) <= total {
		n++
		if n > 999 { // a lifetime of Blood still terminates
			break
		}
	}
	return n
}

// Roman renders the Berserk numeral. Berserk tiers are open-ended by design,
// so this has to keep working past the dozen the specs tabulate.
func Roman(n int) string {
	if n <= 0 {
		return ""
	}
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	out := ""
	for i, v := range vals {
		for n >= v {
			out += syms[i]
			n -= v
		}
	}
	return out
}

// Ledger is the append-only Blood record. Every award goes through Grant, and
// Grant is idempotent on dedup_key -- which is the whole reason the ledger is a
// table rather than a running total. A nightly recompute must be able to run
// twice without paying the same week twice.
type Ledger struct{ st *store.Store }

func NewLedger(st *store.Store) *Ledger { return &Ledger{st: st} }

// Grant records an award if its key has not been seen. It reports whether the
// award was new, so callers can surface "+40 Blood" only the first time.
func (l *Ledger) Grant(ctx context.Context, key, source string, amount float64, on, detail string) (bool, error) {
	if amount <= 0 {
		return false, nil
	}
	res, err := l.st.DB().ExecContext(ctx, `
		INSERT INTO blood_ledger(dedup_key, source, amount, awarded_on, detail, created_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(dedup_key) DO NOTHING`,
		key, source, amount, on, detail, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Total sums the ledger, with the trailing-30-day figure the UI uses to show
// an accrual rate rather than only a stock.
func (l *Ledger) Total(ctx context.Context, asOf time.Time) (Blood, error) {
	var b Blood
	cut := asOf.AddDate(0, 0, -30).Format("2006-01-02")
	err := l.st.DB().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0),
		       COALESCE(SUM(CASE WHEN awarded_on >= ? THEN amount ELSE 0 END), 0)
		FROM blood_ledger`, cut).Scan(&b.Total, &b.Last30Day)
	if err != nil && err != sql.ErrNoRows {
		return b, err
	}

	b.Tier = bloodTier(b.Total)
	b.TierName = "BERSERK " + Roman(b.Tier)
	floor := BloodRequired(b.Tier)
	next := BloodRequired(b.Tier + 1)
	b.NextTier = "BERSERK " + Roman(b.Tier+1)
	b.NextAt = round1(next)
	b.ToNext = round1(next - b.Total)
	if next > floor {
		b.Progress = clamp((b.Total-floor)/(next-floor), 0, 1)
	}
	b.Total = round1(b.Total)
	b.Last30Day = round1(b.Last30Day)
	return b, nil
}

// AwardWeeks pays every qualifying week in the trailing window.
//
// Erratum 5: weekly Blood is awarded PER QUALIFYING WEEK, evaluated on a
// rolling Monday boundary. Do not hardcode a monthly figure -- some months
// contain five qualifying weeks, and a hardcoded 4.33 quietly robs the user in
// those months and overpays in others.
func (l *Ledger) AwardWeeks(ctx context.Context, asOf time.Time, weeks int) error {
	monday := mondayOf(asOf)
	for i := 0; i < weeks; i++ {
		start := monday.AddDate(0, 0, -7*i)
		end := start.AddDate(0, 0, 6)
		// The current, incomplete week is skipped: paying it now and paying it
		// again when it completes is exactly what the dedup key cannot catch,
		// because it would be the same key for a different amount of work.
		if end.After(asOf) {
			continue
		}
		var n int
		if err := l.st.DB().QueryRowContext(ctx, `
			SELECT COUNT(DISTINCT local_date) FROM v_sets
			WHERE set_type IN ('working','backoff','drop')
			  AND local_date >= ? AND local_date <= ?`,
			start.Format("2006-01-02"), end.Format("2006-01-02")).Scan(&n); err != nil {
			return err
		}
		if n < qualifyingSessionsPerWeek {
			continue
		}
		key := "week:" + start.Format("2006-01-02")
		if _, err := l.Grant(ctx, key, "week", BloodQualifyingWeek,
			end.Format("2006-01-02"), fmt.Sprintf("%d qualifying sessions", n)); err != nil {
			return err
		}
	}
	return nil
}

// mondayOf returns the Monday on or before t, which is the rolling boundary
// Erratum 5 specifies.
func mondayOf(t time.Time) time.Time {
	d := int(t.Weekday()) - int(time.Monday)
	if d < 0 {
		d += 7
	}
	y, m, day := t.AddDate(0, 0, -d).Date()
	return time.Date(y, m, day, 0, 0, 0, 0, t.Location())
}

// AwardGateMonth pays for a completed calendar month spent holding every
// Berserk gate. It runs off the snapshot history rather than the live score,
// because "held for a month" is a claim about thirty days, not about today.
func (l *Ledger) AwardGateMonth(ctx context.Context, asOf time.Time) error {
	// Only settle a month once it is over.
	firstOfThis := time.Date(asOf.Year(), asOf.Month(), 1, 0, 0, 0, 0, asOf.Location())
	last := firstOfThis.AddDate(0, 0, -1)
	prefix := last.Format("2006-01")

	var days, held int
	if err := l.st.DB().QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(berserk), 0) FROM berserk_snapshots
		WHERE local_date LIKE ?`, prefix+"%").Scan(&days, &held); err != nil {
		return err
	}
	// A month with gaps in the snapshot history has not been shown to be held.
	if days < 28 || held < days {
		return nil
	}
	_, err := l.Grant(ctx, "gates_month:"+prefix, "gates_month", BloodGatesMonth,
		last.Format("2006-01-02"), "held all Berserk gates")
	return err
}

// AwardVerification pays for each pattern the app has newly verified and each
// skill newly unlocked. The dedup key is the pattern or skill itself, so it
// pays exactly once in a lifetime.
func (l *Ledger) AwardVerification(ctx context.Context, asOf time.Time, scores []PatternScore) error {
	on := asOf.Format("2006-01-02")
	for _, ps := range scores {
		if ps.Status != Verified {
			continue
		}
		if _, err := l.Grant(ctx, "verified:"+string(ps.Pattern), "verification",
			BloodVerification, on, ps.Pattern.Display()+" verified"); err != nil {
			return err
		}
	}

	rows, err := l.st.DB().QueryContext(ctx, `SELECT skill FROM skill_unlocks`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		if _, err := l.Grant(ctx, "skill:"+s, "skill", BloodVerification, on, s+" unlocked"); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Milestone is a named, once-per-lifetime crossing.
//
// CONSTRUCTED: v1.2 Patch 7 prices a milestone at 60 Blood and gives two
// examples ("2x BW squat, 15 strict pull-ups, etc.") without enumerating the
// set. This list is built from the numbers the specs already treat as
// landmarks -- the v1.0 6.1 DOMINION targets, the v1.0 6.2 FFMI 100-point
// mark, and pattern scores at each multiple of ten above 60, which replaces
// v1.0 13.1's "pattern score crossing a multiple of 5" at the new price.
type Milestone struct {
	Key     string
	Label   string
	Reached func(m MilestoneInputs) bool
}

// MilestoneInputs is everything the milestone predicates read.
type MilestoneInputs struct {
	Profile  Profile
	Bd       Breakdown
	Scores   []PatternScore
	SquatBW  float64 // squat e1RM / bodyweight
	DeadBW   float64
	BenchBW  float64
	OHPBW    float64
	StrictPU float64
}

var milestones = []Milestone{
	{"squat_2bw", "2x bodyweight squat", func(m MilestoneInputs) bool { return m.SquatBW >= 2.0 }},
	{"deadlift_25bw", "2.5x bodyweight deadlift", func(m MilestoneInputs) bool { return m.DeadBW >= 2.5 }},
	{"bench_15bw", "1.5x bodyweight bench", func(m MilestoneInputs) bool { return m.BenchBW >= 1.5 }},
	{"ohp_1bw", "bodyweight overhead press", func(m MilestoneInputs) bool { return m.OHPBW >= 1.0 }},
	{"pullups_15", "15 strict pull-ups", func(m MilestoneInputs) bool { return m.StrictPU >= 15 }},
	{"pullup_plus50", "weighted pull-up at +50% bodyweight", func(m MilestoneInputs) bool {
		return m.Bd.DPullup >= 100*1.50/1.55
	}},
	{"ffmi_235", "FFMI 23.5", func(m MilestoneInputs) bool { return m.Profile.FFMIAdj >= 23.5 }},
}

// AwardMilestones grants any newly crossed milestone, plus the per-pattern
// decade crossings.
func (l *Ledger) AwardMilestones(ctx context.Context, asOf time.Time, in MilestoneInputs) error {
	on := asOf.Format("2006-01-02")
	for _, ms := range milestones {
		if !ms.Reached(in) {
			continue
		}
		if _, err := l.Grant(ctx, "milestone:"+ms.Key, "milestone", BloodMilestone, on, ms.Label); err != nil {
			return err
		}
	}
	for _, ps := range in.Scores {
		// Imputed scores are not achievements. Paying for one would let a user
		// earn Blood for a pattern they have never performed.
		if ps.Imputed || !ps.Status.IsProven() {
			continue
		}
		for mark := 60; mark <= patternScoreCap; mark += 10 {
			if ps.Score < float64(mark) {
				break
			}
			key := fmt.Sprintf("pattern:%s:%d", ps.Pattern, mark)
			label := fmt.Sprintf("%s reached %d", ps.Pattern.Display(), mark)
			if _, err := l.Grant(ctx, key, "milestone", BloodMilestone, on, label); err != nil {
				return err
			}
		}
	}
	return nil
}

// PRAward describes a personal record worth Blood. The pipeline already
// decides what counts as a PR; this only prices it.
type PRAward struct {
	SessionID int64
	Lift      string
	Kind      string  // strength|rep|volume
	DeltaS    float64 // score points gained, for a strength PR
	// Stagnant marks a lift that had gone 180 days without a PR. Breaking a
	// plateau is the single most motivating event in a long training life and
	// it is priced accordingly.
	Stagnant bool
}

// AwardPRs prices a session's records.
//
// PR value is denominated in rank-score points, not kilograms (v1.0 7.2). A
// beginner adding 10 kg to a 60 kg bench gains ~7.3 score points; an advanced
// lifter adding 2.5 kg to a 130 kg bench gains ~1.8. Different kilograms,
// comparable difficulty, and the system values them proportionally rather than
// pretending the beginner did something four times more impressive.
func (l *Ledger) AwardPRs(ctx context.Context, asOf time.Time, prs []PRAward) (float64, error) {
	on := asOf.Format("2006-01-02")
	var gained float64
	for _, pr := range prs {
		var amount float64
		switch pr.Kind {
		case "strength":
			amount = BloodStrengthPRBase + BloodStrengthPRRate*math.Max(pr.DeltaS, 0)
		case "rep":
			amount = BloodRepPR
		case "volume":
			amount = BloodVolumePR
		default:
			continue
		}
		key := fmt.Sprintf("pr:%d:%s:%s", pr.SessionID, pr.Lift, pr.Kind)
		ok, err := l.Grant(ctx, key, "pr_"+pr.Kind, amount, on, pr.Lift)
		if err != nil {
			return gained, err
		}
		if ok {
			gained += amount
		}

		if pr.Stagnant && pr.Kind == "strength" {
			key := fmt.Sprintf("stagnant:%d:%s", pr.SessionID, pr.Lift)
			ok, err := l.Grant(ctx, key, "stagnant", BloodStagnantBroken, on,
				pr.Lift+" broken after 180 days")
			if err != nil {
				return gained, err
			}
			if ok {
				gained += BloodStagnantBroken
			}
		}
	}
	return gained, nil
}
