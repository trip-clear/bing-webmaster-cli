package bwt

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Date is a timestamp in the ".NET JSON" format Bing returns: /Date(1757980800000)/,
// sometimes with a trailing offset like /Date(1757980800000-0700)/.
//
// The number is always epoch milliseconds in UTC, and the offset -- when present --
// only says how the server would have displayed it, so it is ignored. Bing reports
// in Pacific Time, which is what Time() converts to.
type Date struct{ time.Time }

var dotNetDate = regexp.MustCompile(`^/Date\((-?\d+)(?:[+-]\d{4})?\)/$`)

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// Some fields come back as null rather than a date.
		if string(b) == "null" {
			return nil
		}
		return err
	}
	if s == "" {
		return nil
	}
	m := dotNetDate.FindStringSubmatch(s)
	if m == nil {
		// Tolerate a plain ISO timestamp in case the API ever emits one.
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			d.Time = t
			return nil
		}
		return fmt.Errorf("日付として解釈できません: %q", s)
	}
	ms, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return err
	}
	d.Time = time.UnixMilli(ms).UTC()
	return nil
}

// MarshalJSON emits an ISO-8601 timestamp, so `-o json` is usable by anything
// that is not a .NET client. A zero date becomes null rather than year 1.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.Pacific().Format(time.RFC3339))
}

// Pacific renders the instant in the timezone Bing reports in.
func (d Date) Pacific() time.Time { return d.In(Pacific) }

// Day is the report bucket this timestamp belongs to, as YYYY-MM-DD in PT.
func (d Date) Day() string {
	if d.IsZero() {
		return ""
	}
	return d.Pacific().Format(DateFormat)
}

// Minute is a compact timestamp for tables ("2026-09-05 04:12").
func (d Date) Minute() string {
	if d.IsZero() {
		return "-"
	}
	return d.Pacific().Format("2006-01-02 15:04")
}

// Site is one property in Bing Webmaster Tools.
type Site struct {
	URL                 string `json:"Url"`
	IsVerified          bool
	AuthenticationCode  string
	DnsVerificationCode string
}

// Stats is the row type Bing uses for every traffic report. The field named
// "Query" carries a search query for GetQueryStats/GetPageQueryStats and a page
// URL for GetPageStats/GetQueryPageStats -- the API reuses one struct for both.
type Stats struct {
	Query                 string
	Date                  Date
	Clicks                int
	Impressions           int
	AvgClickPosition      float64
	AvgImpressionPosition float64
}

// TrafficStats is a site-wide daily total (GetRankAndTrafficStats). Bing gives
// no position at this level.
type TrafficStats struct {
	Date        Date
	Clicks      int
	Impressions int
}

// Feed is a submitted sitemap. Bing calls sitemaps "feeds" throughout the API.
type Feed struct {
	URL         string `json:"Url"`
	Type        string
	Status      string
	Submitted   Date
	LastCrawled Date
	URLCount    int `json:"UrlCount"`
	FileSize    int
	Compressed  bool
}

// URLInfo is what Bing knows about one indexed URL (GetUrlInfo).
type URLInfo struct {
	URL                string `json:"Url"`
	AnchorCount        int
	DiscoveryDate      Date
	DocumentSize       int
	HttpStatus         int
	IsPage             bool
	LastCrawledDate    Date
	TotalChildUrlCount int
}

// URLTrafficInfo is the traffic half of a URL's record (GetUrlTrafficInfo).
type URLTrafficInfo struct {
	URL         string `json:"Url"`
	Clicks      int
	Impressions int
	IsPage      bool
}

// Quota is the remaining URL or content submission allowance.
type Quota struct {
	DailyQuota   int
	MonthlyQuota int
}

// CrawlStats is one day of bingbot activity.
type CrawlStats struct {
	Date               Date
	CrawledPages       int
	InIndex            int
	InLinks            int
	CrawlErrors        int
	Code2xx            int
	Code301            int
	Code302            int
	Code4xx            int
	Code5xx            int
	AllOtherCodes      int
	BlockedByRobotsTxt int
	ContainsMalware    int
}

// CrawlIssue is one URL bingbot had trouble with. Issues is a bit field.
type CrawlIssue struct {
	URL      string `json:"Url"`
	HttpCode int
	Issues   int
	InLinks  int
}

// crawlIssueFlags decodes the Issues bit field into readable names.
var crawlIssueFlags = []struct {
	bit   int
	label string
}{
	{1, "301"},
	{2, "302"},
	{4, "4xx"},
	{8, "5xx"},
	{16, "robots.txt でブロック"},
	{32, "マルウェア"},
	{64, "重要URLが robots.txt でブロック"},
	{128, "DNS エラー"},
	{256, "タイムアウト"},
}

// IssueLabels lists the problems encoded in the Issues bit field.
func (c CrawlIssue) IssueLabels() []string {
	var out []string
	for _, f := range crawlIssueFlags {
		if c.Issues&f.bit != 0 {
			out = append(out, f.label)
		}
	}
	if len(out) == 0 {
		return []string{"なし"}
	}
	return out
}

// Keyword is a keyword-research result (GetKeyword / GetRelatedKeywords).
// BroadImpressions counts impressions for the term and its variants.
type Keyword struct {
	Query            string
	Impressions      int
	BroadImpressions int
}

// KeywordStats is the historical series behind a keyword (GetKeywordStats).
type KeywordStats struct {
	Query            string
	Date             Date
	Impressions      int
	BroadImpressions int
}

// LinkCount is one page of the site and how many inbound links point at it.
type LinkCount struct {
	URL   string `json:"Url"`
	Count int
}

// LinkCounts is one page of the GetLinkCounts result.
type LinkCounts struct {
	Links      []LinkCount
	TotalPages int
}

// LinkDetail is one inbound link, with the anchor text it was linked with.
type LinkDetail struct {
	URL        string `json:"Url"`
	AnchorText string
}

// LinkDetails is one page of the GetUrlLinks result.
type LinkDetails struct {
	Details    []LinkDetail
	TotalPages int
}

// NormalizeSite returns the exact string the API expects for a property.
//
// Unlike Search Console, Bing has only one kind of property: a URL prefix with
// a scheme and a trailing slash. A bare domain is completed to https, because
// that is what a site registered today will be.
//
//	example.com          -> https://example.com/
//	https://example.com  -> https://example.com/
//	http://example.com/  -> unchanged
func NormalizeSite(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// sc-domain: is the Search Console spelling; accept it so a GSC_SITE value
	// pasted by muscle memory does something sensible instead of 400ing.
	s = strings.TrimPrefix(s, "sc-domain:")
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	if !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s
}
