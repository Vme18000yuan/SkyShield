package application

import (
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/Autumn-27/artex/db"
)

type scheduleWindow struct {
	active    bool
	complete  bool
	key       string
	start     time.Time
	nextStart *time.Time
	nextEnd   *time.Time
}

func scheduleLocation(mode string) *time.Location {
	if mode == "system" {
		return time.Local
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return loc
}

func scheduleClock(day time.Time, clock string, loc *time.Location) time.Time {
	parts := strings.Split(clock, ":")
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
}

// end before start means next-day end. Equal midnight means a full day;
// 24:00 is accepted for the end and normalised by PostgreSQL.
func windowBounds(day time.Time, s *db.TaskSchedule, loc *time.Location) (time.Time, *time.Time) {
	start := scheduleClock(day, s.StartTime, loc)
	if s.EndTime == "" {
		return start, nil
	}
	end := scheduleClock(day, s.EndTime, loc)
	if !end.After(start) {
		end = scheduleClock(day.AddDate(0, 0, 1), s.EndTime, loc)
	}
	return start, &end
}

func evaluateScheduleWindow(s *db.TaskSchedule, now time.Time) scheduleWindow {
	loc := scheduleLocation(s.TimezoneMode)
	now = now.In(loc)
	if s.ScheduleType == "once" {
		day, err := time.ParseInLocation("2006-01-02", s.RunDate, loc)
		if err != nil {
			return scheduleWindow{}
		}
		var start time.Time
		var end *time.Time
		if s.EndDate != "" {
			endDay, parseErr := time.ParseInLocation("2006-01-02", s.EndDate, loc)
			if parseErr != nil {
				return scheduleWindow{}
			}
			start = scheduleClock(day, s.StartTime, loc)
			endClock := s.EndTime
			if endClock == "" {
				endClock = "24:00"
			}
			endValue := scheduleClock(endDay, endClock, loc)
			end = &endValue
		} else {
			start, end = windowBounds(day, s, loc)
		}
		active := !now.Before(start) && (end == nil || now.Before(*end))
		complete := end != nil && !now.Before(*end)
		// An open-ended one-shot remains active after its start until manually
		// paused; there is no end boundary at which it could be considered missed.
		key := "once:" + s.RunDate
		if s.EndDate != "" {
			key += ":" + s.EndDate
		}
		w := scheduleWindow{active: active, complete: complete, key: key, start: start, nextEnd: end}
		if now.Before(start) {
			w.key = ""
			w.nextStart = &start
		}
		return w
	}
	// Once an unbounded weekly plan has started, it remains active.
	if s.EndTime == "" && strings.HasPrefix(s.LastWindowKey, "active:") {
		return scheduleWindow{active: true, key: s.LastWindowKey}
	}
	days := map[int]bool{}
	for _, d := range s.Weekdays {
		days[d] = true
	}
	w := scheduleWindow{}
	for offset := -1; offset <= 7; offset++ {
		day := now.AddDate(0, 0, offset)
		weekday := int(day.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		if !days[weekday] {
			continue
		}
		start, end := windowBounds(day, s, loc)
		if !now.Before(start) && (end == nil || now.Before(*end)) {
			w.active = true
			w.start = start
			w.key = "active:" + start.Format("2006-01-02T15:04")
			w.nextEnd = end
			// Do not pause between adjacent windows (e.g. Saturday + Sunday full days).
			continue
		}
		if start.After(now) && (w.nextStart == nil || start.Before(*w.nextStart)) {
			w.nextStart = &start
			if !w.active {
				w.nextEnd = end
			}
		}
	}
	return w
}

// evaluateRuntimeScheduleWindow applies startup/manual state to a calendar
// window. A plan's creation date says nothing about when its next window opens.
func evaluateRuntimeScheduleWindow(item *db.TaskSchedule, now, startedAt time.Time) scheduleWindow {
	w := evaluateScheduleWindow(item, now)
	if !item.Enabled || item.Status == "completed" {
		w.active = false
		return w
	}
	// Only suppress an unrecorded window that was already open at startup.
	// An explicit configuration edit clears LastWindowKey and advances UpdatedAt;
	// allow such edits (as well as newly-created plans) to enter the current window.
	if w.active && item.LastWindowKey == "" && item.CreatedAt.Before(startedAt) &&
		!item.UpdatedAt.After(startedAt) && !w.start.IsZero() && w.start.Before(startedAt) {
		w.active = false
		w.key = "missed:" + w.key
	}
	if strings.HasPrefix(item.LastWindowKey, "missed:") && item.LastWindowKey == "missed:"+w.key {
		w.active = false
		w.key = item.LastWindowKey
	}
	if item.LastWindowKey == "manual" && !w.active {
		w.active = true
		w.key = "manual"
		w.complete = false
	}
	return w
}

// ScheduleWindow is the public evaluation of an ARTEX calendar window.
type ScheduleWindow struct {
	Active    bool
	Complete  bool
	Key       string
	Start     time.Time
	NextStart *time.Time
	NextEnd   *time.Time
}

func EvaluateScheduleWindow(item *db.TaskSchedule, now time.Time) ScheduleWindow {
	return exportWindow(evaluateScheduleWindow(item, now))
}
func EvaluateRuntimeScheduleWindow(item *db.TaskSchedule, now, startedAt time.Time) ScheduleWindow {
	return exportWindow(evaluateRuntimeScheduleWindow(item, now, startedAt))
}
func exportWindow(w scheduleWindow) ScheduleWindow {
	return ScheduleWindow{Active: w.active, Complete: w.complete, Key: w.key, Start: w.start, NextStart: w.nextStart, NextEnd: w.nextEnd}
}
