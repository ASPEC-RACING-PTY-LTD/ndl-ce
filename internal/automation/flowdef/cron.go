package flowdef

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	// Timezones resolve even on hosts without a zoneinfo database.
	_ "time/tzdata"
)

// Schedule presets and the cron line each one stands for. daily and
// weekly run at the trigger's "at" time (HH:MM, default 03:00); weekly runs
// on "weekday" (0 Sunday to 6 Saturday, default 1 Monday).
var SchedulePresets = []string{"every_5_minutes", "hourly", "daily", "weekly", "cron"}

// Cron is a parsed five-field cron line: minute hour day-of-month month
// day-of-week.
type Cron struct {
	src                          string
	minute, hour, dom, month, dw []bool
	domStar, dowStar             bool
}

// String returns the line the schedule was parsed from.
func (c *Cron) String() string { return c.src }

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// ParseCron reads a standard five-field cron line. It accepts *, numbers,
// ranges (1-5), lists (1,15), steps (*/10, 0-30/5) and month and day names.
func ParseCron(line string) (*Cron, error) {
	fields := strings.Fields(line)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron needs 5 fields (minute hour day month weekday), got %d", len(fields))
	}
	c := &Cron{src: strings.Join(fields, " ")}
	var err error
	if c.minute, err = cronField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %v", err)
	}
	if c.hour, err = cronField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %v", err)
	}
	if c.dom, err = cronField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day of month: %v", err)
	}
	if c.month, err = cronField(fields[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month: %v", err)
	}
	if c.dw, err = cronField(fields[4], 0, 7, dayNames); err != nil {
		return nil, fmt.Errorf("weekday: %v", err)
	}
	if c.dw[7] {
		c.dw[0] = true
	}
	c.domStar = fields[2] == "*" || fields[2] == "?"
	c.dowStar = fields[4] == "*" || fields[4] == "?"
	return c, nil
}

func cronValue(s string, names map[string]int) (int, error) {
	if names != nil {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	return strconv.Atoi(s)
}

func cronField(s string, lo, hi int, names map[string]int) ([]bool, error) {
	set := make([]bool, hi+1)
	for _, part := range strings.Split(s, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n < 1 {
				return nil, fmt.Errorf("bad step in %q", part)
			}
			step = n
			part = part[:i]
		}
		from, to := lo, hi
		switch {
		case part == "*" || part == "?":
		case strings.Contains(part, "-"):
			ab := strings.SplitN(part, "-", 2)
			a, err1 := cronValue(ab[0], names)
			b, err2 := cronValue(ab[1], names)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("bad range %q", part)
			}
			from, to = a, b
		default:
			v, err := cronValue(part, names)
			if err != nil {
				return nil, fmt.Errorf("bad value %q", part)
			}
			from, to = v, v
			if step > 1 {
				to = hi
			}
		}
		if from < lo || to > hi || from > to {
			return nil, fmt.Errorf("%q is outside %d-%d", part, lo, hi)
		}
		for v := from; v <= to; v += step {
			set[v] = true
		}
	}
	return set, nil
}

func (c *Cron) dayMatches(t time.Time) bool {
	dom := c.dom[t.Day()]
	dow := c.dw[int(t.Weekday())]
	// Standard cron: when both day fields are restricted, either matches.
	if !c.domStar && !c.dowStar {
		return dom || dow
	}
	return dom && dow
}

// Next returns the first matching minute strictly after t, in t's location.
func (c *Cron) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if !c.month[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !c.minute[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// ScheduleSpec is the config of a schedule trigger.
type ScheduleSpec struct {
	Preset   string `json:"preset"`
	Cron     string `json:"cron,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	At       string `json:"at,omitempty"`
	Weekday  int    `json:"weekday,omitempty"`
}

// ScheduleFromConfig reads a schedule trigger's config.
func ScheduleFromConfig(cfg map[string]any) ScheduleSpec {
	s := ScheduleSpec{Weekday: 1}
	s.Preset, _ = cfg["preset"].(string)
	s.Cron, _ = cfg["cron"].(string)
	s.Timezone, _ = cfg["timezone"].(string)
	s.At, _ = cfg["at"].(string)
	switch v := cfg["weekday"].(type) {
	case float64:
		s.Weekday = int(v)
	case int:
		s.Weekday = v
	case string:
		s.Weekday, _ = strconv.Atoi(v)
	}
	if s.Preset == "" && s.Cron != "" {
		s.Preset = "cron"
	}
	return s
}

// CronLine turns a preset into its cron line.
func (s ScheduleSpec) CronLine() (string, error) {
	hh, mm := 3, 0
	if strings.TrimSpace(s.At) != "" {
		t, err := time.Parse("15:04", strings.TrimSpace(s.At))
		if err != nil {
			return "", fmt.Errorf("at must be HH:MM")
		}
		hh, mm = t.Hour(), t.Minute()
	}
	switch s.Preset {
	case "every_5_minutes":
		return "*/5 * * * *", nil
	case "hourly":
		return "0 * * * *", nil
	case "daily":
		return fmt.Sprintf("%d %d * * *", mm, hh), nil
	case "weekly":
		wd := s.Weekday
		if wd < 0 || wd > 6 {
			return "", fmt.Errorf("weekday must be 0 (Sunday) to 6 (Saturday)")
		}
		return fmt.Sprintf("%d %d * * %d", mm, hh, wd), nil
	case "cron":
		if strings.TrimSpace(s.Cron) == "" {
			return "", fmt.Errorf("cron is required")
		}
		return s.Cron, nil
	}
	return "", fmt.Errorf("preset must be one of %s", strings.Join(SchedulePresets, ", "))
}

// Location resolves the schedule's timezone (UTC when empty).
func (s ScheduleSpec) Location() (*time.Location, error) {
	tz := strings.TrimSpace(s.Timezone)
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q", tz)
	}
	return loc, nil
}

// Next is the first run time after t.
func (s ScheduleSpec) Next(t time.Time) (time.Time, error) {
	line, err := s.CronLine()
	if err != nil {
		return time.Time{}, err
	}
	c, err := ParseCron(line)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := s.Location()
	if err != nil {
		return time.Time{}, err
	}
	next := c.Next(t.In(loc))
	if next.IsZero() {
		return next, fmt.Errorf("the schedule never runs")
	}
	return next.UTC(), nil
}
