// Package schedule works out when a cron schedule fires, in its own timezone.
package schedule

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// CatchUp is how late a firing may still run. A firing missed by more (Bob was down) is
// recorded as skipped instead.
const CatchUp = 6 * time.Hour

// Spec is a parsed 5-field cron expression in a timezone.
type Spec struct {
	sched cron.Schedule
	loc   *time.Location
}

// Parse checks a 5-field cron expression ("0 6 * * 1-5") and an IANA timezone ("Europe/London").
func Parse(expr, timezone string) (Spec, error) {
	if timezone == "" {
		timezone = "UTC"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return Spec{}, fmt.Errorf("timezone %q: %w", timezone, err)
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return Spec{}, fmt.Errorf("cron %q: %w", expr, err)
	}
	if _, ok := sched.(*cron.SpecSchedule); !ok {
		return Spec{}, fmt.Errorf("cron %q: use five fields (minute hour day month weekday)", expr)
	}
	return Spec{sched: sched, loc: loc}, nil
}

// Next is the first firing strictly after t. When clocks go back and a local time happens
// twice, it fires at the first only.
func (s Spec) Next(t time.Time) time.Time {
	n := s.sched.Next(t.In(s.loc))
	for !n.IsZero() && wall(n.Add(-time.Hour), s.loc) == wall(n, s.loc) {
		n = s.sched.Next(n)
	}
	return n
}

func wall(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02 15:04") }

// Due returns the latest firing in (last, now], if there is one. last is when the schedule was
// last fired by cron (or created). Several missed firings collapse into the latest: a schedule
// never runs twice to catch up.
func (s Spec) Due(last, now time.Time) (time.Time, bool) {
	next := s.Next(last)
	if next.IsZero() || next.After(now) {
		return time.Time{}, false
	}
	// Walk forward to the latest firing not after now. Starting CatchUp before now bounds the
	// walk: anything older than that is only ever reported as missed.
	if from := now.Add(-CatchUp - time.Minute); next.Before(from) {
		if n := s.Next(from); !n.After(now) {
			next = n
		}
	}
	for n := s.Next(next); !n.IsZero() && !n.After(now); n = s.Next(n) {
		next = n
	}
	return next, true
}
