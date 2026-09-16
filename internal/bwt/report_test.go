package bwt

import (
	"testing"
	"time"
)

func day(s string) Date {
	t, err := time.ParseInLocation(DateFormat, s, Pacific)
	if err != nil {
		panic(err)
	}
	return Date{t}
}

func stat(query, d string, clicks, impressions int, clickPos, impPos float64) Stats {
	return Stats{
		Query:                 query,
		Date:                  day(d),
		Clicks:                clicks,
		Impressions:           impressions,
		AvgClickPosition:      clickPos,
		AvgImpressionPosition: impPos,
	}
}

func rng(t *testing.T, start, end string) DateRange {
	t.Helper()
	r, err := Resolve(RangeSpec{Start: start, End: end})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The API hands back every bucket it has ever recorded, so the range has to be
// applied here or a "last month" report would silently be an all-time one.
func TestAggregateKeepsOnlyBucketsInRange(t *testing.T) {
	raw := []Stats{
		stat("温泉 旅館", "2026-05-30", 10, 100, 3, 8),
		stat("温泉 旅館", "2026-06-06", 20, 200, 2, 4),
		stat("温泉 旅館", "2026-07-04", 99, 999, 1, 1),
	}

	rep := Aggregate(raw, rng(t, "2026-06-01", "2026-06-30"))

	if len(rep.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rep.Rows))
	}
	if rep.Rows[0].Clicks != 20 || rep.Rows[0].Impressions != 200 {
		t.Errorf("row = %+v, want only the June bucket", rep.Rows[0])
	}
	if rep.Skipped != 2 {
		t.Errorf("skipped = %d, want 2", rep.Skipped)
	}
	if len(rep.Days) != 1 || rep.Days[0] != "2026-06-06" {
		t.Errorf("days = %v, want [2026-06-06]", rep.Days)
	}
}

// Position is an average per bucket. Summing buckets means re-averaging by
// impressions: a week seen 10 times must not move the number as much as one
// seen 1,000 times.
func TestAggregateWeightsPositionsByVolume(t *testing.T) {
	raw := []Stats{
		stat("q", "2026-06-06", 10, 1000, 2, 2),
		stat("q", "2026-06-13", 1, 10, 50, 90),
	}

	rep := Aggregate(raw, rng(t, "2026-06-01", "2026-06-30"))
	row := rep.Rows[0]

	if row.Clicks != 11 || row.Impressions != 1010 || row.Buckets != 2 {
		t.Fatalf("row = %+v, want 11 clicks over 1010 impressions in 2 buckets", row)
	}
	wantPos := (2*1000.0 + 90*10.0) / 1010.0 // ≈ 2.87, not the naive mean of 46
	if diff := row.Position - wantPos; diff > 0.001 || diff < -0.001 {
		t.Errorf("position = %v, want %v (impression-weighted)", row.Position, wantPos)
	}
	wantClickPos := (2*10.0 + 50*1.0) / 11.0 // click position weights by clicks
	if diff := row.ClickPosition - wantClickPos; diff > 0.001 || diff < -0.001 {
		t.Errorf("click position = %v, want %v (click-weighted)", row.ClickPosition, wantClickPos)
	}
	if diff := row.CTR() - 11/1010.0; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("ctr = %v, want it recomputed from the totals", row.CTR())
	}
}

// A bucket with impressions but no clicks reports AvgClickPosition 0. Letting
// that into the click-position average would drag a rank-3 term toward 0.
func TestAggregateIgnoresPositionsWithNoVolume(t *testing.T) {
	raw := []Stats{
		stat("q", "2026-06-06", 5, 100, 3, 3),
		stat("q", "2026-06-13", 0, 100, 0, 9),
	}

	row := Aggregate(raw, rng(t, "2026-06-01", "2026-06-30")).Rows[0]

	if row.ClickPosition != 3 {
		t.Errorf("click position = %v, want 3 (the clickless bucket contributes nothing)", row.ClickPosition)
	}
	if diff := row.Position - 6.0; diff > 0.001 || diff < -0.001 {
		t.Errorf("position = %v, want 6 (both buckets had impressions)", row.Position)
	}
}

func TestAggregateSortsByClicksThenImpressions(t *testing.T) {
	raw := []Stats{
		stat("b", "2026-06-06", 5, 500, 1, 1),
		stat("a", "2026-06-06", 5, 900, 1, 1),
		stat("c", "2026-06-06", 9, 10, 1, 1),
	}

	rows := Aggregate(raw, rng(t, "2026-06-01", "2026-06-30")).Rows

	for i, want := range []string{"c", "a", "b"} {
		if rows[i].Key != want {
			t.Errorf("row %d = %q, want %q", i, rows[i].Key, want)
		}
	}
}

func TestAggregateOfNothingDoesNotDivideByZero(t *testing.T) {
	rep := Aggregate(nil, rng(t, "2026-06-01", "2026-06-30"))
	total := rep.Total()
	if len(rep.Rows) != 0 || total.Clicks != 0 || total.CTR() != 0 || total.Position != 0 {
		t.Fatalf("got %+v / %+v, want empty", rep.Rows, total)
	}
}

func TestAggregateByDayOrdersOldestFirst(t *testing.T) {
	raw := []TrafficStats{
		{Date: day("2026-06-13"), Clicks: 2, Impressions: 20},
		{Date: day("2026-06-06"), Clicks: 1, Impressions: 10},
		{Date: day("2026-05-30"), Clicks: 9, Impressions: 90},
	}

	rows := AggregateByDay(raw, rng(t, "2026-06-01", "2026-06-30")).Rows

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (May is out of range)", len(rows))
	}
	if rows[0].Key != "2026-06-06" || rows[1].Key != "2026-06-13" {
		t.Errorf("rows = %q, %q, want chronological order", rows[0].Key, rows[1].Key)
	}
}

func TestTotalCountsEachBucketDateOnce(t *testing.T) {
	raw := []Stats{
		stat("a", "2026-06-06", 1, 10, 1, 1),
		stat("b", "2026-06-06", 1, 10, 1, 1),
		stat("a", "2026-06-13", 1, 10, 1, 1),
	}

	total := Aggregate(raw, rng(t, "2026-06-01", "2026-06-30")).Total()

	if total.Buckets != 2 {
		t.Errorf("buckets = %d, want 2 distinct dates (not 3 rows)", total.Buckets)
	}
	if total.Clicks != 3 {
		t.Errorf("clicks = %d, want 3", total.Clicks)
	}
}
