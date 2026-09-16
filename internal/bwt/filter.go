package bwt

import (
	"fmt"
	"regexp"
	"strings"
)

// Dimension is what a report is grouped by.
type Dimension string

const (
	DimQuery Dimension = "query"
	DimPage  Dimension = "page"
	DimDate  Dimension = "date"
)

// ParseDimension maps a user-supplied dimension name onto the three the API can
// actually produce.
func ParseDimension(s string) (Dimension, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "query", "queries", "keyword":
		return DimQuery, nil
	case "page", "pages", "url":
		return DimPage, nil
	case "date", "day":
		return DimDate, nil
	default:
		return "", fmt.Errorf("不明なディメンション %q（有効: query, page, date）", s)
	}
}

// filterOps are the operators of the --filter mini-language.
//
// They match the Search Console CLI's on purpose, even though Bing does no
// server-side filtering and every one of these is applied here, after the rows
// arrive: the same expression should mean the same thing in both tools.
var filterOps = []struct {
	token string
	kind  string
}{
	{"==", "equals"},
	{"!=", "not_equals"},
	{"~~", "contains"},
	{"!~", "not_contains"},
	{"~*", "regex"},
	{"!*", "not_regex"},
}

// Filter is one parsed --filter expression.
type Filter struct {
	Dim   Dimension
	Kind  string
	Value string
	re    *regexp.Regexp
}

// Match reports whether a row key satisfies the filter. Literal comparisons are
// case-insensitive, which is how both search consoles treat queries; regexes are
// compiled exactly as written, so case folding there is the caller's `(?i)`.
func (f Filter) Match(key string) bool {
	switch f.Kind {
	case "equals":
		return strings.EqualFold(key, f.Value)
	case "not_equals":
		return !strings.EqualFold(key, f.Value)
	case "contains":
		return strings.Contains(strings.ToLower(key), strings.ToLower(f.Value))
	case "not_contains":
		return !strings.Contains(strings.ToLower(key), strings.ToLower(f.Value))
	case "regex":
		return f.re.MatchString(key)
	case "not_regex":
		return !f.re.MatchString(key)
	}
	return true
}

// Filters is a set of filters combined with AND or OR.
type Filters struct {
	list []Filter
	or   bool
}

// Match reports whether a row key passes the set.
func (fs Filters) Match(key string) bool {
	if len(fs.list) == 0 {
		return true
	}
	if fs.or {
		for _, f := range fs.list {
			if f.Match(key) {
				return true
			}
		}
		return false
	}
	for _, f := range fs.list {
		if !f.Match(key) {
			return false
		}
	}
	return true
}

// Empty reports whether there is nothing to filter on.
func (fs Filters) Empty() bool { return len(fs.list) == 0 }

// Len is how many filters were given.
func (fs Filters) Len() int { return len(fs.list) }

// ParseFilter parses one expression: <dimension><op><value>.
//
//	query~~温泉        query が「温泉」を含む
//	page!~/tag/        page が /tag/ を含まない
//	query==onsen       完全一致（大文字小文字は無視）
//	page~*/blog/\d+$   正規表現（RE2）
//
// dim is the dimension the report is grouped by; a filter naming anything else
// is rejected rather than silently ignored, because there is only one column of
// keys to match against.
func ParseFilter(expr string, dim Dimension) (Filter, error) {
	best, bestIdx := -1, len(expr)
	for i, o := range filterOps {
		if idx := strings.Index(expr, o.token); idx >= 0 && idx < bestIdx {
			best, bestIdx = i, idx
		}
	}
	if best < 0 {
		return Filter{}, fmt.Errorf("フィルタ %q に演算子がありません（== 一致 / != 不一致 / ~~ 含む / !~ 含まない / ~* 正規表現 / !* 正規表現で除外）", expr)
	}

	op := filterOps[best]
	name := strings.TrimSpace(expr[:bestIdx])
	value := strings.TrimSpace(expr[bestIdx+len(op.token):])

	if name == "" {
		return Filter{}, fmt.Errorf("フィルタ %q の %q の前にディメンションがありません", expr, op.token)
	}
	if value == "" {
		return Filter{}, fmt.Errorf("フィルタ %q の %q の後に値がありません", expr, op.token)
	}

	fdim, err := ParseDimension(name)
	if err != nil {
		return Filter{}, fmt.Errorf("フィルタ %q: %w", expr, err)
	}
	if fdim == DimDate {
		return Filter{}, fmt.Errorf("フィルタ %q: date ではフィルタできません（日付の絞り込みは --start/--end/--preset を使う）", expr)
	}
	if fdim != dim {
		return Filter{}, fmt.Errorf("フィルタ %q: 集計は %s 単位なので %s ではフィルタできません（--dim %s にするか、フィルタ側を %s に直してください）",
			expr, dim, fdim, fdim, dim)
	}

	f := Filter{Dim: fdim, Kind: op.kind, Value: value}
	if op.kind == "regex" || op.kind == "not_regex" {
		re, err := regexp.Compile(value)
		if err != nil {
			return Filter{}, fmt.Errorf("フィルタ %q の正規表現が不正です: %w", expr, err)
		}
		f.re = re
	}
	return f, nil
}

// ParseFilters parses every --filter into one set. groupType is "and" or "or".
func ParseFilters(exprs []string, dim Dimension, groupType string) (Filters, error) {
	var fs Filters
	switch strings.ToLower(groupType) {
	case "", "and":
	case "or":
		fs.or = true
	default:
		return fs, fmt.Errorf("不明な --filter-group-type %q（有効: and, or）", groupType)
	}

	for _, e := range exprs {
		if e == "" {
			continue
		}
		f, err := ParseFilter(e, dim)
		if err != nil {
			return Filters{}, err
		}
		fs.list = append(fs.list, f)
	}
	return fs, nil
}

// Apply keeps the rows whose key passes the filters.
func (fs Filters) Apply(rows []Row) []Row {
	if fs.Empty() {
		return rows
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if fs.Match(r.Key) {
			out = append(out, r)
		}
	}
	return out
}
