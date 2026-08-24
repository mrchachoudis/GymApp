package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mrcha/gymlogger/internal/store"
)

// Recommendation is what the scheduler thinks should happen next.
type Recommendation struct {
	Kind          string   `json:"kind"` // "train", "rest", "nudge", "none"
	SessionName   string   `json:"session_name,omitempty"`
	Groups        []string `json:"groups,omitempty"`
	Reason        string   `json:"reason"`
	DaysSinceLast int      `json:"days_since_last"`
	Readiness     float64  `json:"readiness"`
}

// Notification is a push payload ready to hand to FCM.
type Notification struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	DedupKey string `json:"dedup_key"`
}

type Scheduler struct {
	st  *store.Store
	loc *time.Location

	// NudgeAfterDays is how long without a logged session before the app says
	// something unprompted.
	NudgeAfterDays int
}

func New(st *store.Store) *Scheduler {
	return &Scheduler{st: st, loc: st.Location(), NudgeAfterDays: 2}
}

func (s *Scheduler) split(ctx context.Context) Split {
	sp, err := ParseSplit(s.st.Setting(ctx, "split_json", ""))
	if err != nil {
		return DefaultSplit()
	}
	return sp
}

// ---------- availability ----------

// Available reports whether it is reasonable to buzz Mario's phone right now.
//
// He works nights Friday through Sunday, 23:00 to 07:00, then sleeps into the
// afternoon. A reminder at 04:00 on a Saturday is not a reminder, it is a
// reason to uninstall the app. The windows are settings so they survive a
// change of job without a redeploy.
func (s *Scheduler) Available(ctx context.Context, t time.Time) bool {
	local := t.In(s.loc)
	hour := local.Hour()

	quietStart := s.settingInt(ctx, "quiet_start_hour", 22)
	quietEnd := s.settingInt(ctx, "quiet_end_hour", 9)

	if s.onShift(ctx, local) {
		return false
	}
	if s.postShiftSleep(ctx, local) {
		return false
	}

	// Quiet hours wrap midnight, so the comparison flips when start > end.
	if quietStart > quietEnd {
		if hour >= quietStart || hour < quietEnd {
			return false
		}
	} else if hour >= quietStart && hour < quietEnd {
		return false
	}
	return true
}

// onShift reports whether the given local time falls inside a night shift.
// Shifts start on the configured days at shift_start_hour and run past
// midnight into the next calendar day, so both ends are checked.
func (s *Scheduler) onShift(ctx context.Context, local time.Time) bool {
	days := s.shiftDays(ctx)
	startH := s.settingInt(ctx, "shift_start_hour", 23)
	endH := s.settingInt(ctx, "shift_end_hour", 7)
	hour := local.Hour()

	// Late-evening portion: today is a shift day and it is past the start.
	if days[local.Weekday()] && hour >= startH {
		return true
	}
	// Early-morning portion: yesterday was a shift day and it is before the end.
	yesterday := local.AddDate(0, 0, -1).Weekday()
	if days[yesterday] && hour < endH {
		return true
	}
	return false
}

// postShiftSleep covers the hours after a shift ends, when he is asleep rather
// than available.
func (s *Scheduler) postShiftSleep(ctx context.Context, local time.Time) bool {
	days := s.shiftDays(ctx)
	endH := s.settingInt(ctx, "shift_end_hour", 7)
	sleepH := s.settingInt(ctx, "post_shift_sleep_hours", 8)

	yesterday := local.AddDate(0, 0, -1).Weekday()
	if !days[yesterday] {
		return false
	}
	hour := local.Hour()
	return hour >= endH && hour < endH+sleepH
}

func (s *Scheduler) shiftDays(ctx context.Context) map[time.Weekday]bool {
	raw := s.st.Setting(ctx, "shift_days", "friday,saturday,sunday")
	out := map[time.Weekday]bool{}
	names := map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
		"wednesday": time.Wednesday, "thursday": time.Thursday,
		"friday": time.Friday, "saturday": time.Saturday,
	}
	for _, part := range strings.Split(raw, ",") {
		if wd, ok := names[strings.ToLower(strings.TrimSpace(part))]; ok {
			out[wd] = true
		}
	}
	return out
}

func (s *Scheduler) settingInt(ctx context.Context, key string, def int) int {
	var v int
	if _, err := fmt.Sscanf(s.st.Setting(ctx, key, ""), "%d", &v); err != nil {
		return def
	}
	return v
}

// ---------- recommendation ----------

// Recommend picks the next session. The rule is: among templates whose muscle
// groups have recovered, choose the one trained longest ago. If nothing has
// recovered, or he has trained too many days straight, recommend rest.
func (s *Scheduler) Recommend(ctx context.Context, now time.Time) (*Recommendation, error) {
	sp := s.split(ctx)

	lastHit, err := s.lastHitByGroup(ctx, sp)
	if err != nil {
		return nil, err
	}
	lastSession, err := s.lastSessionByName(ctx)
	if err != nil {
		return nil, err
	}
	consecutive, err := s.consecutiveTrainingDays(ctx, now)
	if err != nil {
		return nil, err
	}
	daysSince, err := s.daysSinceLastSession(ctx, now)
	if err != nil {
		return nil, err
	}

	if consecutive >= sp.MaxConsecutiveDays {
		return &Recommendation{
			Kind:          "rest",
			Reason:        fmt.Sprintf("%d training days in a row, take one off", consecutive),
			DaysSinceLast: daysSince,
		}, nil
	}

	type scored struct {
		t         SessionTemplate
		readiness float64
		staleness float64
	}
	var cands []scored
	for _, t := range sp.Templates {
		cands = append(cands, scored{
			t:         t,
			readiness: t.Readiness(now, lastHit),
			staleness: t.Staleness(now, lastSession),
		})
	}

	// Fully recovered first, then longest since it was last trained. Sorting
	// on staleness alone would keep recommending a session whose muscles are
	// still cooked.
	sort.SliceStable(cands, func(i, j int) bool {
		ri, rj := cands[i].readiness >= 1, cands[j].readiness >= 1
		if ri != rj {
			return ri
		}
		if cands[i].readiness != cands[j].readiness && (!ri || !rj) {
			return cands[i].readiness > cands[j].readiness
		}
		return cands[i].staleness > cands[j].staleness
	})

	best := cands[0]
	if best.readiness < 1 {
		return &Recommendation{
			Kind:          "rest",
			Reason:        "nothing has fully recovered yet",
			DaysSinceLast: daysSince,
			Readiness:     best.readiness,
		}, nil
	}

	groups := make([]string, 0, len(best.t.Groups))
	for _, g := range best.t.Groups {
		groups = append(groups, string(g))
	}

	reason := fmt.Sprintf("recovered and %.0f days since the last one", best.staleness)
	if best.staleness >= 14 {
		reason = "not in the log yet"
	}

	return &Recommendation{
		Kind:          "train",
		SessionName:   best.t.Name,
		Groups:        groups,
		Reason:        reason,
		DaysSinceLast: daysSince,
		Readiness:     best.readiness,
	}, nil
}

// ---------- notifications ----------

// Tick is called on a timer. It returns the notifications that should be sent
// right now, already deduplicated against what has been sent before.
//
// Dedup keys are date-scoped, so a service restart mid-day cannot re-nag, and
// a new day naturally allows one fresh reminder.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) ([]Notification, error) {
	if !s.Available(ctx, now) {
		return nil, nil
	}

	local := now.In(s.loc)
	date := local.Format("2006-01-02")

	trainedToday, err := s.trainedOn(ctx, date)
	if err != nil {
		return nil, err
	}
	if trainedToday {
		return nil, nil // he already did the thing, do not nag about it
	}

	rec, err := s.Recommend(ctx, now)
	if err != nil {
		return nil, err
	}

	var out []Notification

	switch rec.Kind {
	case "train":
		reminderHour := s.settingInt(ctx, "reminder_hour", 17)
		if local.Hour() >= reminderHour {
			out = append(out, Notification{
				Kind:     "train",
				Title:    "Session due: " + rec.SessionName,
				Body:     fmt.Sprintf("%s is up. %s.", rec.SessionName, rec.Reason),
				DedupKey: "train:" + date,
			})
		}
	case "rest":
		// A rest recommendation is only worth a push when he might otherwise
		// train anyway, which is after several days on.
		if rec.DaysSinceLast == 0 && strings.Contains(rec.Reason, "in a row") {
			out = append(out, Notification{
				Kind:     "rest",
				Title:    "Rest day",
				Body:     rec.Reason + ".",
				DedupKey: "rest:" + date,
			})
		}
	}

	if rec.DaysSinceLast >= s.NudgeAfterDays {
		out = append(out, Notification{
			Kind:  "nudge",
			Title: "Nothing logged in a while",
			Body: fmt.Sprintf("%d days since the last session. %s is ready when you are.",
				rec.DaysSinceLast, rec.SessionName),
			DedupKey: "nudge:" + date,
		})
	}

	// Filter against the ledger so a restart does not resend.
	var fresh []Notification
	for _, n := range out {
		first, err := s.st.MarkNotified(ctx, n.DedupKey, n.Kind, n.Body)
		if err != nil {
			return nil, err
		}
		if first {
			fresh = append(fresh, n)
		}
	}
	return fresh, nil
}

// ---------- queries ----------

// lastHitByGroup maps each muscle group to when it was last trained. Groups
// are inferred from the session_type recorded on each session, matched against
// the split templates.
func (s *Scheduler) lastHitByGroup(ctx context.Context, sp Split) (map[MuscleGroup]time.Time, error) {
	rows, err := s.st.DB().QueryContext(ctx, `
		SELECT session_type, logged_at FROM sessions
		WHERE is_rest_day = 0 AND session_type IS NOT NULL
		ORDER BY logged_at DESC LIMIT 40`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byName := map[string][]MuscleGroup{}
	for _, t := range sp.Templates {
		byName[strings.ToLower(t.Name)] = t.Groups
	}

	out := map[MuscleGroup]time.Time{}
	for rows.Next() {
		var st sql.NullString
		var ts string
		if err := rows.Scan(&st, &ts); err != nil {
			return nil, err
		}
		when, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			continue
		}
		groups := byName[strings.ToLower(st.String)]
		if groups == nil {
			groups = inferGroups(st.String)
		}
		for _, g := range groups {
			// Rows are newest first, so the first sighting of a group wins.
			if _, seen := out[g]; !seen {
				out[g] = when
			}
		}
	}
	return out, rows.Err()
}

// inferGroups handles session types that do not match a template name, which
// happens whenever Mario freestyles a session.
func inferGroups(sessionType string) []MuscleGroup {
	s := strings.ToLower(sessionType)
	var out []MuscleGroup
	for word, g := range map[string]MuscleGroup{
		"chest": Chest, "back": Back, "shoulder": Shoulders, "delt": Shoulders,
		"bicep": Biceps, "tricep": Triceps, "arm": Biceps,
		"leg": Quads, "quad": Quads, "hamstring": Hamstrings, "glute": Glutes,
		"calf": Calves, "calves": Calves, "forearm": Forearms, "core": Core, "abs": Core,
	} {
		if strings.Contains(s, word) {
			out = append(out, g)
		}
	}
	// "arms" and "legs" imply their partners; training one without the other
	// is not how anyone actually trains.
	if strings.Contains(s, "arm") {
		out = append(out, Triceps, Forearms)
	}
	if strings.Contains(s, "leg") {
		out = append(out, Hamstrings, Glutes)
	}
	return out
}

func (s *Scheduler) lastSessionByName(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.st.DB().QueryContext(ctx, `
		SELECT session_type, MAX(logged_at) FROM sessions
		WHERE is_rest_day = 0 AND session_type IS NOT NULL
		GROUP BY session_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]time.Time{}
	for rows.Next() {
		var name string
		var ts string
		if err := rows.Scan(&name, &ts); err != nil {
			return nil, err
		}
		if when, err := time.Parse(time.RFC3339, ts); err == nil {
			out[name] = when
		}
	}
	return out, rows.Err()
}

func (s *Scheduler) consecutiveTrainingDays(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.st.DB().QueryContext(ctx, `
		SELECT DISTINCT local_date FROM sessions
		WHERE is_rest_day = 0 AND local_date <= ?
		ORDER BY local_date DESC LIMIT 14`, s.st.LocalDate(now))
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	var prev time.Time
	for rows.Next() {
		var ds string
		if err := rows.Scan(&ds); err != nil {
			return 0, err
		}
		d, err := time.Parse("2006-01-02", ds)
		if err != nil {
			continue
		}
		if !prev.IsZero() && prev.Sub(d) > 24*time.Hour {
			break
		}
		prev = d
		count++
	}
	return count, rows.Err()
}

func (s *Scheduler) daysSinceLastSession(ctx context.Context, now time.Time) (int, error) {
	var ds sql.NullString
	err := s.st.DB().QueryRowContext(ctx, `
		SELECT MAX(local_date) FROM sessions WHERE is_rest_day = 0`).Scan(&ds)
	if err != nil || !ds.Valid {
		return 999, nil
	}
	last, err := time.Parse("2006-01-02", ds.String)
	if err != nil {
		return 999, nil
	}
	today, _ := time.Parse("2006-01-02", s.st.LocalDate(now))
	return int(today.Sub(last).Hours() / 24), nil
}

func (s *Scheduler) trainedOn(ctx context.Context, date string) (bool, error) {
	var n int
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE local_date = ?`, date).Scan(&n)
	return n > 0, err
}

// MarshalRecommendation is a convenience for the HTTP layer.
func MarshalRecommendation(r *Recommendation) []byte {
	b, _ := json.Marshal(r)
	return b
}
