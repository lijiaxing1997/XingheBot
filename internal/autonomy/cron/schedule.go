package cron

import (
	"errors"
	"fmt"
	"strings"
	"time"

	robcron "github.com/robfig/cron/v3"
)

func ValidateJob(job Job) error {
	id := strings.TrimSpace(job.ID)
	if id == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(job.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(job.Schedule.Type) == "" {
		return errors.New("schedule.type is required")
	}
	if strings.TrimSpace(job.Task.Type) == "" {
		return errors.New("task.type is required")
	}
	if strings.TrimSpace(job.Task.Prompt) == "" {
		return errors.New("task.prompt is required")
	}
	return nil
}

func ComputeNextRunAt(job Job, now time.Time, defaultTimezone string, minRefireGap time.Duration) (time.Time, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	if err := ValidateJob(job); err != nil {
		return time.Time{}, err
	}

	schedType := strings.ToLower(strings.TrimSpace(job.Schedule.Type))
	tz := strings.TrimSpace(job.Schedule.Timezone)
	if tz == "" {
		tz = strings.TrimSpace(defaultTimezone)
	}
	loc, err := loadLocation(tz)
	if err != nil {
		return time.Time{}, err
	}

	switch schedType {
	case "cron":
		expr := strings.TrimSpace(job.Schedule.Expr)
		if expr == "" {
			return time.Time{}, errors.New("schedule.expr is required for cron")
		}
		parser := robcron.NewParser(robcron.Minute | robcron.Hour | robcron.Dom | robcron.Month | robcron.Dow | robcron.Descriptor)
		schedule, err := parser.Parse(expr)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse cron expr: %w", err)
		}
		base := now.In(loc)
		next := schedule.Next(base)
		if minRefireGap > 0 {
			gapUntil := now.Add(minRefireGap).In(loc)
			if !next.After(gapUntil) {
				next = schedule.Next(gapUntil)
			}
		}
		return next.UTC(), nil
	case "every":
		raw := strings.TrimSpace(job.Schedule.Every)
		if raw == "" {
			return time.Time{}, errors.New("schedule.every is required for every")
		}
		d, err := time.ParseDuration(raw)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse schedule.every: %w", err)
		}
		if d <= 0 {
			return time.Time{}, errors.New("schedule.every must be > 0")
		}
		return now.Add(d).UTC(), nil
	case "at":
		raw := strings.TrimSpace(job.Schedule.At)
		if raw == "" {
			return time.Time{}, errors.New("schedule.at is required for at")
		}
		at, err := parseTimeInLocation(raw, loc)
		if err != nil {
			return time.Time{}, err
		}
		return at.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unknown schedule.type: %s", schedType)
	}
}

func loadLocation(raw string) (*time.Location, error) {
	name := strings.TrimSpace(raw)
	if name == "" || strings.EqualFold(name, "local") {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("load location %q: %w", name, err)
	}
	return loc, nil
}

func parseTimeInLocation(raw string, loc *time.Location) (time.Time, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, errors.New("time is empty")
	}
	if loc == nil {
		loc = time.Local
	}
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t, nil
	}
	layouts := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, text, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse time %q: expected RFC3339 or local formats like 2006-01-02 15:04", text)
}
