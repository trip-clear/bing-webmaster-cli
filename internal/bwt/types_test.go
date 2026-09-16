package bwt

import (
	"encoding/json"
	"testing"
)

// Bing stamps every date in .NET's JSON format. The milliseconds are UTC and the
// trailing offset, when present, is decoration: both spellings of one instant
// must land on the same reporting day.
func TestDateParsesDotNetFormat(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{`"/Date(1399100400000)/"`, "2014-05-03"},
		{`"/Date(1399100400000-0700)/"`, "2014-05-03"},
		{`"/Date(1399100400000+0000)/"`, "2014-05-03"},
		// 07:00 UTC is midnight PDT: the bucket belongs to the 3rd, not the 2nd.
		{`"2014-05-03T07:00:00Z"`, "2014-05-03"},
	} {
		var d Date
		if err := json.Unmarshal([]byte(tc.in), &d); err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if got := d.Day(); got != tc.want {
			t.Errorf("%s -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDateHandlesNullAndEmpty(t *testing.T) {
	for _, in := range []string{`null`, `""`} {
		var d Date
		if err := json.Unmarshal([]byte(in), &d); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if !d.IsZero() {
			t.Errorf("%s should leave the date zero", in)
		}
		if d.Minute() != "-" {
			t.Errorf("%s should render as a dash, got %q", in, d.Minute())
		}
	}
}

// A zero date must not serialise as year 1: a report that says a sitemap was
// last crawled in 0001-01-01 is worse than one that says nothing.
func TestDateMarshalsZeroAsNull(t *testing.T) {
	data, err := json.Marshal(struct{ D Date }{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"D":null}` {
		t.Errorf("got %s, want {\"D\":null}", data)
	}
}

func TestDateRejectsGarbage(t *testing.T) {
	var d Date
	if err := json.Unmarshal([]byte(`"yesterday"`), &d); err == nil {
		t.Fatal("expected an error for an unparseable date")
	}
}

func TestNormalizeSite(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"example.com", "https://example.com/"},
		{"example.com/", "https://example.com/"},
		{"https://example.com", "https://example.com/"},
		{"https://example.com/", "https://example.com/"},
		{"http://example.com/", "http://example.com/"},
		{"https://example.com/shop", "https://example.com/shop/"},
		// A Search Console property string pasted by habit should still work.
		{"sc-domain:example.com", "https://example.com/"},
		{"  example.com  ", "https://example.com/"},
		{"", ""},
	} {
		if got := NormalizeSite(tc.in); got != tc.want {
			t.Errorf("NormalizeSite(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCrawlIssueLabelsDecodeBitField(t *testing.T) {
	// 4 (4xx) | 16 (robots.txt)
	got := CrawlIssue{Issues: 20}.IssueLabels()
	if len(got) != 2 || got[0] != "4xx" || got[1] != "robots.txt でブロック" {
		t.Errorf("got %v, want [4xx, robots.txt でブロック]", got)
	}
	none := CrawlIssue{Issues: 0}.IssueLabels()
	if len(none) != 1 || none[0] != "なし" {
		t.Errorf("got %v, want [なし]", none)
	}
}
