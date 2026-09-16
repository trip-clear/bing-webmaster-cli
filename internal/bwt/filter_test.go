package bwt

import "testing"

func mustFilters(t *testing.T, exprs []string, dim Dimension, group string) Filters {
	t.Helper()
	fs, err := ParseFilters(exprs, dim, group)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestFilterOperators(t *testing.T) {
	for _, tc := range []struct {
		expr string
		key  string
		want bool
	}{
		{"query~~温泉", "箱根 温泉 旅館", true},
		{"query~~温泉", "箱根 旅館", false},
		{"query!~ブランド", "箱根 旅館", true},
		{"query!~ブランド", "ブランド名 予約", false},
		{"query==onsen", "ONSEN", true}, // literal comparisons ignore case
		{"query!=onsen", "ONSEN", false},
		{"query~*^箱根.*旅館$", "箱根 温泉 旅館", true},
		{"query~*^箱根", "熱海 旅館", false},
		{"query!*^箱根", "熱海 旅館", true},
	} {
		f, err := ParseFilter(tc.expr, DimQuery)
		if err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		if got := f.Match(tc.key); got != tc.want {
			t.Errorf("%s vs %q = %v, want %v", tc.expr, tc.key, got, tc.want)
		}
	}
}

func TestFilterContainsIgnoresCase(t *testing.T) {
	f, err := ParseFilter("page~~/BLOG/", DimPage)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Match("https://example.com/blog/post") {
		t.Error("contains should be case-insensitive, matching how search consoles filter")
	}
}

// Only one column of keys exists, so a filter naming the other dimension cannot
// be honoured. Silently ignoring it would quietly return the unfiltered report.
func TestFilterRejectsMismatchedDimension(t *testing.T) {
	_, err := ParseFilter("page~~/blog/", DimQuery)
	if err == nil {
		t.Fatal("expected an error: a page filter cannot apply to a query report")
	}
	if _, err := ParseFilter("date==2026-06-01", DimQuery); err == nil {
		t.Fatal("expected an error for a date filter")
	}
}

func TestFilterRejectsMalformedExpressions(t *testing.T) {
	for _, expr := range []string{"query", "~~温泉", "query~~", "query~*[", "bogus~~x"} {
		if _, err := ParseFilter(expr, DimQuery); err == nil {
			t.Errorf("%q should have been rejected", expr)
		}
	}
}

func TestFilterGroupAndOr(t *testing.T) {
	rows := []Row{{Key: "箱根 温泉"}, {Key: "熱海 旅館"}, {Key: "箱根 旅館"}}

	and := mustFilters(t, []string{"query~~箱根", "query~~旅館"}, DimQuery, "and")
	if got := and.Apply(rows); len(got) != 1 || got[0].Key != "箱根 旅館" {
		t.Errorf("AND = %v, want just 箱根 旅館", got)
	}

	or := mustFilters(t, []string{"query~~温泉", "query~~熱海"}, DimQuery, "or")
	if got := or.Apply(rows); len(got) != 2 {
		t.Errorf("OR = %v, want 2 rows", got)
	}

	if _, err := ParseFilters([]string{"query~~x"}, DimQuery, "xor"); err == nil {
		t.Error("expected an error for an unknown group type")
	}
}

func TestNoFiltersKeepsEverything(t *testing.T) {
	rows := []Row{{Key: "a"}, {Key: "b"}}
	fs := mustFilters(t, nil, DimQuery, "and")
	if !fs.Empty() || len(fs.Apply(rows)) != 2 {
		t.Error("an empty filter set must be a no-op")
	}
}

func TestParseDimension(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Dimension
	}{
		{"", DimQuery}, {"query", DimQuery}, {"QUERY", DimQuery}, {"keyword", DimQuery},
		{"page", DimPage}, {"url", DimPage}, {"date", DimDate}, {"day", DimDate},
	} {
		got, err := ParseDimension(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseDimension(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := ParseDimension("device"); err == nil {
		t.Error("Bing has no device dimension; it should be rejected rather than sent")
	}
}
