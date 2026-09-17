package schedule

import (
	"testing"
	"time"
)

func at(t *testing.T, zone, value string) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	v, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParse(t *testing.T) {
	for _, bad := range [][2]string{{"0 6 * *", "UTC"}, {"61 * * * *", "UTC"}, {"0 6 * * *", "Mars/Olympus"}, {"@every 1m", "UTC"}, {"0 0 6 * * *", "UTC"}} {
		if _, err := Parse(bad[0], bad[1]); err == nil {
			t.Errorf("Parse(%q, %q) accepted", bad[0], bad[1])
		}
	}
	if _, err := Parse("@daily", ""); err != nil {
		t.Errorf("@daily in the default timezone: %v", err)
	}
}

func TestDue(t *testing.T) {
	cases := []struct {
		name, cron, zone string
		last, now        string // in zone
		want             string // "" = not due
	}{
		{"not yet", "0 6 * * *", "UTC", "2026-09-17 05:00", "2026-09-17 05:59", ""},
		{"on the minute", "0 6 * * *", "UTC", "2026-09-17 05:00", "2026-09-17 06:00", "2026-09-17 06:00"},
		{"a little late", "0 6 * * *", "UTC", "2026-09-17 05:00", "2026-09-17 06:03", "2026-09-17 06:00"},
		{"already ran", "0 6 * * *", "UTC", "2026-09-17 06:00", "2026-09-17 12:00", ""},
		{"weekdays skip the weekend", "0 6 * * 1-5", "UTC", "2026-09-18 06:00", "2026-09-20 23:59", ""}, // Fri → Sun
		{"several missed collapse to the latest", "*/15 * * * *", "UTC", "2026-09-17 06:00", "2026-09-17 07:20", "2026-09-17 07:15"},
		{"missed by days, still reports the latest", "0 6 * * *", "UTC", "2026-09-01 06:00", "2026-09-17 05:00", "2026-09-16 06:00"},

		// The schedule's own timezone decides the hour.
		{"new york morning", "0 6 * * *", "America/New_York", "2026-09-17 05:00", "2026-09-17 06:00", "2026-09-17 06:00"},
		{"tokyo is not utc", "0 6 * * *", "Asia/Tokyo", "2026-09-17 05:30", "2026-09-17 05:59", ""},

		// Europe/London: clocks go forward 2026-03-29 01:00 → 02:00, back 2026-10-25 02:00 → 01:00.
		{"06:00 the day clocks go forward", "0 6 * * *", "Europe/London", "2026-03-28 06:00", "2026-03-29 06:00", "2026-03-29 06:00"},
		{"06:00 the day clocks go back", "0 6 * * *", "Europe/London", "2026-10-24 06:00", "2026-10-25 06:00", "2026-10-25 06:00"},
		{"01:30 is skipped when it does not exist", "30 1 * * *", "Europe/London", "2026-03-28 01:30", "2026-03-29 23:00", ""},
	}
	for _, c := range cases {
		spec, err := Parse(c.cron, c.zone)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, ok := spec.Due(at(t, c.zone, c.last), at(t, c.zone, c.now))
		switch {
		case c.want == "" && ok:
			t.Errorf("%s: due at %s, want not due", c.name, got.In(spec.loc).Format("2006-01-02 15:04 MST"))
		case c.want != "" && !ok:
			t.Errorf("%s: not due, want %s", c.name, c.want)
		case c.want != "" && !got.Equal(at(t, c.zone, c.want)):
			t.Errorf("%s: due at %s, want %s", c.name, got.In(spec.loc).Format("2006-01-02 15:04 MST"), c.want)
		}
	}

	// 01:30 happens twice when London's clocks go back: 00:30 and 01:30 UTC. It fires at the first,
	// and having fired there (00:30 UTC) it does not fire again at the second (01:30 UTC).
	if got, ok := func() (time.Time, bool) {
		spec, _ := Parse("30 1 * * *", "Europe/London")
		return spec.Due(at(t, "UTC", "2026-10-24 00:31"), at(t, "UTC", "2026-10-25 03:00"))
	}(); !ok || !got.Equal(at(t, "UTC", "2026-10-25 00:30")) {
		t.Errorf("01:30 London on the autumn change: due %v at %s UTC, want 00:30 UTC", ok, got.UTC().Format("15:04"))
	}
	// Having fired at the first 01:30 (00:30 UTC) it does not fire again at the second (01:30 UTC).
	twice, _ := Parse("30 1 * * *", "Europe/London")
	if got, ok := twice.Due(at(t, "UTC", "2026-10-25 00:31"), at(t, "UTC", "2026-10-25 03:00")); ok {
		t.Errorf("01:30 London fired twice on the autumn change: again at %s UTC", got.UTC().Format("15:04"))
	}

	// A firing one hour apart in UTC on the day London's clocks go forward.
	spec, _ := Parse("0 6 * * *", "Europe/London")
	before := spec.Next(at(t, "UTC", "2026-03-28 00:00"))
	after := spec.Next(before)
	if got := after.Sub(before); got != 23*time.Hour {
		t.Errorf("06:00 London across the spring change is %v apart, want 23h", got)
	}
}
