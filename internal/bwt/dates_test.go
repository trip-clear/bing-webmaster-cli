package bwt

import (
	"testing"
	"time"
)

func TestResolvePrecedence(t *testing.T) {
	// Explicit dates beat everything else.
	r, err := Resolve(RangeSpec{Start: "2026-06-01", End: "2026-06-30", Days: 7, Preset: "last_7d"})
	if err != nil {
		t.Fatal(err)
	}
	if r.StartString() != "2026-06-01" || r.EndString() != "2026-06-30" || r.Days() != 30 {
		t.Errorf("got %s (%d days), want the explicit range", r, r.Days())
	}

	// --days beats --preset and counts back from --end inclusively.
	r, err = Resolve(RangeSpec{End: "2026-06-30", Days: 7, Preset: "last_28d"})
	if err != nil {
		t.Fatal(err)
	}
	if r.StartString() != "2026-06-24" || r.Days() != 7 {
		t.Errorf("got %s (%d days), want 7 days ending 2026-06-30", r, r.Days())
	}
}

func TestResolveDefaultsToConfiguredPresetAndLag(t *testing.T) {
	r, err := Resolve(RangeSpec{LagDays: DefaultLagDays})
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := TodayPT().AddDate(0, 0, -DefaultLagDays)
	if r.EndString() != wantEnd.Format(DateFormat) {
		t.Errorf("end = %s, want today in PT minus %d days", r.EndString(), DefaultLagDays)
	}
	// last_3m, not last_28d: Bing's weekly buckets make a 28-day window thin.
	if r.Days() < 80 {
		t.Errorf("default range is %d days, want roughly three months", r.Days())
	}
}

func TestResolveRejectsBadInput(t *testing.T) {
	for _, spec := range []RangeSpec{
		{Start: "2026/06/01"},
		{End: "yesterday"},
		{Preset: "last_decade"},
		{Start: "2026-07-01", End: "2026-06-01"},
		{LagDays: -1},
	} {
		if _, err := Resolve(spec); err == nil {
			t.Errorf("%+v should have been rejected", spec)
		}
	}
}

func TestComparison(t *testing.T) {
	r, err := Resolve(RangeSpec{Start: "2026-06-01", End: "2026-06-30"})
	if err != nil {
		t.Fatal(err)
	}

	prev, err := Comparison(r, "previous")
	if err != nil {
		t.Fatal(err)
	}
	if prev.EndString() != "2026-05-31" || prev.Days() != r.Days() {
		t.Errorf("previous = %s (%d days), want the 30 days ending 2026-05-31", prev, prev.Days())
	}

	// 364 days keeps the weekday alignment that weekly search traffic has.
	yoy, err := Comparison(r, "year")
	if err != nil {
		t.Fatal(err)
	}
	if yoy.Start.Weekday() != r.Start.Weekday() {
		t.Errorf("year-over-year start is a %s, want a %s", yoy.Start.Weekday(), r.Start.Weekday())
	}
	if yoy.StartString() != "2025-06-02" {
		t.Errorf("year start = %s, want 2025-06-02", yoy.StartString())
	}

	if _, err := Comparison(r, "quarter"); err == nil {
		t.Error("expected an error for an unknown comparison mode")
	}
}

// A bucket stamped at midnight PT is 07:00 or 08:00 UTC. Comparing in UTC would
// put it on the previous day and drop the first bucket of every range.
func TestContainsUsesPacificDays(t *testing.T) {
	r, err := Resolve(RangeSpec{Start: "2026-06-06", End: "2026-06-06"})
	if err != nil {
		t.Fatal(err)
	}

	midnightPT := Date{time.Date(2026, 6, 6, 7, 0, 0, 0, time.UTC)} // 00:00 PDT
	if !r.Contains(midnightPT) {
		t.Error("a bucket at midnight PT must fall inside that PT day")
	}
	if r.Contains(Date{}) {
		t.Error("a zero date is not inside any range")
	}
	if r.Contains(Date{time.Date(2026, 6, 7, 7, 0, 0, 0, time.UTC)}) {
		t.Error("the next day must fall outside")
	}
}
