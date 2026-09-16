package bwt

import (
	"sort"
)

// Row is one aggregated line of a traffic report: every bucket that shared a
// key, summed.
type Row struct {
	Key         string
	Clicks      int
	Impressions int
	// ClickPosition and Position are impression- and click-weighted averages of
	// the per-bucket averages Bing reports. Position (the average position at
	// which the page was *shown*) is the one comparable to Search Console's.
	ClickPosition float64
	Position      float64
	// Buckets is how many report buckets contributed to this row. Bing groups
	// traffic into weekly buckets, so a "30 day" report is usually built from
	// four or five of them, and a row with one bucket is one week of data.
	Buckets int
}

// CTR is clicks over impressions.
func (r Row) CTR() float64 {
	if r.Impressions == 0 {
		return 0
	}
	return float64(r.Clicks) / float64(r.Impressions)
}

// Report is an aggregated traffic report over a date range.
type Report struct {
	Rows []Row
	// Days are the distinct bucket dates that fell inside the range, sorted.
	// They are surfaced to the reader because Bing's buckets are weekly and
	// coarse: a range that happens to contain no bucket returns nothing at all,
	// which is not the same thing as "no traffic".
	Days []string
	// Skipped is how many raw buckets fell outside the range.
	Skipped int
}

// Total sums the whole report the same way a row is summed.
func (rep *Report) Total() Row {
	var (
		t         Row
		impWeight float64
		clkWeight float64
	)
	seen := map[string]bool{}
	for _, r := range rep.Rows {
		t.Clicks += r.Clicks
		t.Impressions += r.Impressions
		impWeight += r.Position * float64(r.Impressions)
		clkWeight += r.ClickPosition * float64(r.Clicks)
	}
	for _, d := range rep.Days {
		seen[d] = true
	}
	t.Buckets = len(seen)
	if t.Impressions > 0 {
		t.Position = impWeight / float64(t.Impressions)
	}
	if t.Clicks > 0 {
		t.ClickPosition = clkWeight / float64(t.Clicks)
	}
	return t
}

// accumulator sums buckets into one Row, keeping the weights it needs to turn
// per-bucket averages back into an overall average.
type accumulator struct {
	row       Row
	impWeight float64
	clkWeight float64
	days      map[string]bool
}

func (a *accumulator) add(s Stats) {
	a.row.Clicks += s.Clicks
	a.row.Impressions += s.Impressions
	a.row.Buckets++
	// A position is only meaningful where there was something to average: a
	// bucket with no impressions reports position 0, and letting that into the
	// mean would drag every average toward zero.
	a.impWeight += s.AvgImpressionPosition * float64(s.Impressions)
	a.clkWeight += s.AvgClickPosition * float64(s.Clicks)
}

func (a *accumulator) finish() Row {
	r := a.row
	if r.Impressions > 0 {
		r.Position = a.impWeight / float64(r.Impressions)
	}
	if r.Clicks > 0 {
		r.ClickPosition = a.clkWeight / float64(r.Clicks)
	}
	return r
}

// Aggregate turns the raw buckets Bing returns into one row per key, keeping
// only the buckets inside r.
//
// This is where the CLI earns its keep: the API has no date parameters and no
// aggregation at all -- GetQueryStats hands back every bucket it has ever
// recorded, one row per query per week -- so the period, the grouping and the
// weighted averages all have to happen here.
func Aggregate(raw []Stats, r DateRange) *Report {
	accs := map[string]*accumulator{}
	order := []string{}
	days := map[string]bool{}
	skipped := 0

	for _, s := range raw {
		if !r.Contains(s.Date) {
			skipped++
			continue
		}
		days[s.Date.Day()] = true

		a, ok := accs[s.Query]
		if !ok {
			a = &accumulator{}
			accs[s.Query] = a
			order = append(order, s.Query)
		}
		a.add(s)
	}

	rows := make([]Row, 0, len(order))
	for _, k := range order {
		row := accs[k].finish()
		row.Key = k
		rows = append(rows, row)
	}
	SortRows(rows)

	return &Report{Rows: rows, Days: sortedKeys(days), Skipped: skipped}
}

// AggregateByDay groups buckets by their date instead of by key, for a time
// series. Rows come back oldest first.
func AggregateByDay(raw []TrafficStats, r DateRange) *Report {
	accs := map[string]*accumulator{}
	order := []string{}

	for _, s := range raw {
		if !r.Contains(s.Date) {
			continue
		}
		day := s.Date.Day()
		a, ok := accs[day]
		if !ok {
			a = &accumulator{}
			accs[day] = a
			order = append(order, day)
		}
		a.add(Stats{Clicks: s.Clicks, Impressions: s.Impressions})
	}

	sort.Strings(order)
	rows := make([]Row, 0, len(order))
	for _, d := range order {
		row := accs[d].finish()
		row.Key = d
		rows = append(rows, row)
	}
	return &Report{Rows: rows, Days: order}
}

// SortRows orders rows the way a report is read: most clicks first, then most
// impressions, then by key so the order is stable between runs.
func SortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Clicks != rows[j].Clicks {
			return rows[i].Clicks > rows[j].Clicks
		}
		if rows[i].Impressions != rows[j].Impressions {
			return rows[i].Impressions > rows[j].Impressions
		}
		return rows[i].Key < rows[j].Key
	})
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
