package muscle

import (
	"context"
	"sort"
	"time"

	"github.com/mrcha/gymlogger/internal/store"
)

// Volume is one group's work over a window.
type Volume struct {
	Group Group   `json:"group"`
	Name  string  `json:"name"`
	Sets  float64 `json:"sets"` // weighted: primary 1.0, secondary 0.5
	// Raw counts every set that touched the group at all, which is the honest
	// number to show a human next to a body diagram. Sets is the number the
	// volume maths uses.
	Raw         int    `json:"raw_sets"`
	LastTrained string `json:"last_trained,omitempty"`
	DaysSince   int    `json:"days_since"`
}

// Report is a window of training reduced to per-group volume.
type Report struct {
	From    string   `json:"from"`
	To      string   `json:"to"`
	Volumes []Volume `json:"volumes"`
	// TotalSets counts logged working sets in the window, whether or not they
	// resolved to a group.
	TotalSets int `json:"total_sets"`
	// Unmatched names the exercises that could not be resolved. Surfacing them
	// is the difference between a mapping gap you can fix and volume that
	// quietly disappears: anything listed here is work the user did that no
	// group was credited for.
	Unmatched []string `json:"unmatched,omitempty"`
	// Neglected is a major group with NO work in this window, longest gap
	// first. It is the line the muscle map puts under the figures, and it is
	// empty when every major group was trained.
	Neglected string `json:"neglected,omitempty"`
}

// Window computes per-group volume between two dates inclusive.
//
// Only working, backoff and drop sets count, on the same reasoning that keeps
// warmups out of every other volume figure in the app: warmup count varies with
// sleep and temperature, so including it makes week-over-week comparison noise.
func Window(ctx context.Context, st *store.Store, from, to time.Time) (Report, error) {
	rep := Report{
		From: from.Format("2006-01-02"),
		To:   to.Format("2006-01-02"),
	}

	rows, err := st.DB().QueryContext(ctx, `
		SELECT local_date, name, equipment, COUNT(*)
		FROM v_sets
		WHERE set_type IN ('working','backoff','drop')
		  AND local_date >= ? AND local_date <= ?
		GROUP BY local_date, name, equipment
		ORDER BY local_date`, rep.From, rep.To)
	if err != nil {
		return rep, err
	}
	defer rows.Close()

	weighted := map[Group]float64{}
	raw := map[Group]int{}
	last := map[Group]string{}
	missing := map[string]bool{}

	for rows.Next() {
		var date, name, equip string
		var n int
		if err := rows.Scan(&date, &name, &equip, &n); err != nil {
			return rep, err
		}
		rep.TotalSets += n

		w, ok := Of(name, equip)
		if !ok {
			missing[name] = true
			continue
		}
		for g, factor := range w {
			weighted[g] += factor * float64(n)
			raw[g] += n
			if date > last[g] {
				last[g] = date
			}
		}
	}
	if err := rows.Err(); err != nil {
		return rep, err
	}

	// Every group is reported, including the ones at zero. A muscle map that
	// only lists what you trained cannot tell you what you did not.
	for _, g := range Groups {
		v := Volume{Group: g, Name: g.Display(), Sets: round1(weighted[g]), Raw: raw[g]}
		if d, ok := last[g]; ok {
			v.LastTrained = d
			if parsed, err := time.Parse("2006-01-02", d); err == nil {
				v.DaysSince = int(to.Sub(parsed).Hours() / 24)
			}
		}
		rep.Volumes = append(rep.Volumes, v)
	}

	for n := range missing {
		rep.Unmatched = append(rep.Unmatched, n)
	}
	sort.Strings(rep.Unmatched)
	rep.Neglected = neglected(ctx, st, to, weighted)
	return rep, nil
}

// neglected names a major group that got NO work in the window.
//
// The distinction matters because the UI renders this as "CALVES - 0 SETS IN
// 28 D". An earlier version picked the group with the longest gap regardless of
// whether it had been trained, which produced lines like "CHEST - 0 SETS IN 5
// D" for a chest that had seen seven sets that week. A neglect callout that can
// be factually wrong is worse than no callout.
//
// Among untrained groups, the one with the longest gap wins, and one never
// trained at all beats any gap. If every major group was trained in the window,
// there is nothing to report and the line is omitted.
func neglected(ctx context.Context, st *store.Store, asOf time.Time, worked map[Group]float64) string {
	var idle []Group
	for _, g := range Major {
		if worked[g] == 0 {
			idle = append(idle, g)
		}
	}
	if len(idle) == 0 {
		return ""
	}

	const lookbackDays = 365
	gaps, err := windowRaw(ctx, st, asOf.AddDate(0, 0, -lookbackDays), asOf)
	if err != nil {
		return idle[0].Display()
	}

	worst, worstDays := Group(""), -1
	for _, g := range idle {
		days, trained := gaps[g]
		if !trained {
			return g.Display() // never trained in a year beats any gap
		}
		if days > worstDays {
			worst, worstDays = g, days
		}
	}
	if worst == "" {
		return ""
	}
	return worst.Display()
}

// windowRaw returns days-since-last-set per group, or absence when a group was
// not trained in the window at all.
func windowRaw(ctx context.Context, st *store.Store, from, to time.Time) (map[Group]int, error) {
	rows, err := st.DB().QueryContext(ctx, `
		SELECT local_date, name, equipment
		FROM v_sets
		WHERE set_type IN ('working','backoff','drop')
		  AND local_date >= ? AND local_date <= ?
		GROUP BY local_date, name, equipment`,
		from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	last := map[Group]string{}
	for rows.Next() {
		var date, name, equip string
		if err := rows.Scan(&date, &name, &equip); err != nil {
			return nil, err
		}
		w, ok := Of(name, equip)
		if !ok {
			continue
		}
		for g := range w {
			if date > last[g] {
				last[g] = date
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make(map[Group]int, len(last))
	for g, d := range last {
		if parsed, err := time.Parse("2006-01-02", d); err == nil {
			out[g] = int(to.Sub(parsed).Hours() / 24)
		}
	}
	return out, nil
}

// WeeklyMedian is the input VIGOR's V_volume term needs: the median weekly hard
// sets across the major groups, averaged over the trailing weeks.
//
// A median rather than a mean, because one group trained into the ground should
// not read as high overall work capacity -- which is exactly the pattern
// V_volume exists to not reward. Major rather than all eleven groups, because
// calves and forearms carry different volume norms and folding them in would
// make VIGOR a measure of whether someone does direct calf work.
func WeeklyMedian(ctx context.Context, st *store.Store, asOf time.Time, weeks int) (float64, error) {
	if weeks <= 0 {
		return 0, nil
	}
	var total float64
	for w := 0; w < weeks; w++ {
		end := asOf.AddDate(0, 0, -7*w)
		start := end.AddDate(0, 0, -6)
		rep, err := Window(ctx, st, start, end)
		if err != nil {
			return 0, err
		}
		byGroup := make(map[Group]float64, len(rep.Volumes))
		for _, v := range rep.Volumes {
			byGroup[v.Group] = v.Sets
		}
		counts := make([]float64, 0, len(Major))
		for _, g := range Major {
			counts = append(counts, byGroup[g])
		}
		sort.Float64s(counts)
		total += median(counts)
	}
	return total / float64(weeks), nil
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
