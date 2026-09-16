package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trip-clear/bing-webmaster-cli/internal/bwt"
)

// runCLI drives the real root command against a stub API, exactly as the binary
// would: argv in, rendered output out.
func runCLI(t *testing.T, handler http.HandlerFunc, args ...string) (string, error) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Setenv("BWT_CONFIG_DIR", t.TempDir()) // never touch the developer's real config
	t.Setenv("BWT_API_KEY", "test-key")
	t.Setenv("BWT_SITE", "")
	t.Setenv("BWT_ENDPOINT", srv.URL)

	// Cobra flag state is package-level; reset it between runs.
	global = struct {
		site     string
		output   string
		apiKey   string
		bom      bool
		maxWidth int
		timeout  time.Duration
		throttle time.Duration
		version  string
	}{}

	root := newRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	// Nothing to be polite to: the stub is local, and the real pacing would add
	// 200ms to every stubbed call.
	root.SetArgs(append(args, "--throttle=0"))

	err := root.Execute()
	return out.String(), err
}

// msDate renders a PT day the way Bing stamps its buckets.
func msDate(t *testing.T, day string) string {
	t.Helper()
	d, err := time.ParseInLocation(bwt.DateFormat, day, bwt.Pacific)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("/Date(%d)/", d.UnixMilli())
}

type statRow struct {
	Query                 string  `json:"Query"`
	Date                  string  `json:"Date"`
	Clicks                int     `json:"Clicks"`
	Impressions           int     `json:"Impressions"`
	AvgClickPosition      float64 `json:"AvgClickPosition"`
	AvgImpressionPosition float64 `json:"AvgImpressionPosition"`
}

func jsonHandler(payload any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"d": payload})
	}
}

// sampleStats spans two periods so a comparison has something to compare.
func sampleStats(t *testing.T) []statRow {
	t.Helper()
	return []statRow{
		// current period (June)
		{"温泉 旅館", msDate(t, "2026-06-06"), 120, 1000, 2, 4.6},
		{"onsen", msDate(t, "2026-06-13"), 30, 500, 5, 12.4},
		// previous period (May)
		{"温泉 旅館", msDate(t, "2026-05-09"), 80, 900, 3, 7.2},
		{"消えたクエリ", msDate(t, "2026-05-09"), 50, 500, 1, 3},
	}
}

func TestQueryAggregatesAndRendersTable(t *testing.T) {
	out, err := runCLI(t, jsonHandler(sampleStats(t)),
		"query", "--site", "example.com", "--start", "2026-06-01", "--end", "2026-06-30")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, want := range []string{
		"温泉 旅館", "120", "12.00%", "4.6", // the row itself
		"合計 (2行)", "150", "1500", // the totals footer
		"https://example.com/",        // the bare domain was normalised
		"期間: 2026-06-01 〜 2026-06-30", // the range is stated under the table
		"データ: 2バケット",                  // and so is how much data landed in it
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	// The May buckets belong to a different period and must not be summed in.
	if strings.Contains(out, "消えたクエリ") {
		t.Errorf("a bucket outside the range leaked into the report:\n%s", out)
	}
}

// Bing's buckets are weekly, so an empty result usually means the window fell
// between them. Saying "0 rows" without saying that would mislead.
func TestQueryExplainsAnEmptyWindow(t *testing.T) {
	out, err := runCLI(t, jsonHandler(sampleStats(t)),
		"query", "-s", "example.com", "--start", "2026-06-08", "--end", "2026-06-10")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "0バケット") || !strings.Contains(out, "週次バケット") {
		t.Errorf("an empty window should explain Bing's weekly bucketing:\n%s", out)
	}
}

func TestQueryJSONIsMachineReadable(t *testing.T) {
	out, err := runCLI(t, jsonHandler(sampleStats(t)),
		"query", "-s", "example.com", "-o", "json", "--start", "2026-06-01", "--end", "2026-06-30")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	var got struct {
		Site    string   `json:"site"`
		Buckets []string `json:"buckets"`
		Range   struct{ Start, End string }
		Rows    []struct {
			Query       string  `json:"query"`
			Clicks      float64 `json:"clicks"`
			Impressions float64 `json:"impressions"`
			Position    float64 `json:"position"`
		} `json:"rows"`
		Totals struct {
			Clicks   float64 `json:"clicks"`
			RowCount int     `json:"row_count"`
		} `json:"totals"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	if got.Site != "https://example.com/" || got.Range.Start != "2026-06-01" {
		t.Errorf("meta = %+v, want the normalised site and the requested range", got)
	}
	if len(got.Rows) != 2 || got.Rows[0].Query != "温泉 旅館" || got.Rows[0].Clicks != 120 {
		t.Fatalf("rows = %+v", got.Rows)
	}
	if got.Totals.Clicks != 150 || got.Totals.RowCount != 2 {
		t.Errorf("totals = %+v, want 150 clicks over 2 rows", got.Totals)
	}
	// The buckets are what tells a reader whether the window had data at all.
	if len(got.Buckets) != 2 || got.Buckets[0] != "2026-06-06" {
		t.Errorf("buckets = %v, want the two June dates", got.Buckets)
	}
}

func TestQueryCSVHasMachineHeaderAndNoFooter(t *testing.T) {
	out, err := runCLI(t, jsonHandler(sampleStats(t)),
		"query", "-s", "example.com", "-o", "csv", "--start", "2026-06-01", "--end", "2026-06-30")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + 2 rows, no totals):\n%s", len(lines), out)
	}
	if lines[0] != "query,clicks,impressions,ctr,position,click_position" {
		t.Errorf("header = %q, want machine-readable keys matching the JSON fields", lines[0])
	}
	if strings.Contains(out, "合計") {
		t.Error("the totals footer must not leak into CSV")
	}
}

// The comparison period is carved out of the same payload, so it must not cost
// a second request.
func TestQueryCompareUsesOneRequest(t *testing.T) {
	calls := 0
	out, err := runCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		jsonHandler(sampleStats(t))(w, r)
	}, "query", "-s", "example.com", "--compare", "previous", "--start", "2026-06-01", "--end", "2026-06-30")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if calls != 1 {
		t.Errorf("made %d API calls, want 1 (the API returns the full history anyway)", calls)
	}
	// 120 clicks now vs 80 before.
	if !strings.Contains(out, "+40 (+50.0%)") {
		t.Errorf("expected the click delta for 温泉 旅館:\n%s", out)
	}
	// Position 7.2 -> 4.6 is a 2.6-place improvement, shown as positive.
	if !strings.Contains(out, "+2.6") {
		t.Errorf("expected the position improvement to read as +2.6:\n%s", out)
	}
	// A query that lost all its traffic is the point of running a comparison.
	if !strings.Contains(out, "消えたクエリ") {
		t.Errorf("a query present only in the previous period must be kept:\n%s", out)
	}
	if !strings.Contains(out, "比較: 2026-05-02 〜 2026-05-31") {
		t.Errorf("expected the comparison window to be stated:\n%s", out)
	}
}

func TestQueryFilterIsApplied(t *testing.T) {
	out, err := runCLI(t, jsonHandler(sampleStats(t)),
		"query", "-s", "example.com", "-f", "query~~温泉", "--start", "2026-06-01", "--end", "2026-06-30")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "温泉 旅館") || strings.Contains(out, "onsen") {
		t.Errorf("the filter was not applied:\n%s", out)
	}
}

func TestQueryRejectsMismatchedFilterDimension(t *testing.T) {
	_, err := runCLI(t, jsonHandler(sampleStats(t)),
		"query", "-s", "example.com", "-d", "query", "-f", "page~~/blog/")
	if err == nil {
		t.Fatal("expected an error: a page filter cannot apply to a query report")
	}
	if exitCode(err) != exitUsage {
		t.Errorf("exit code = %d, want %d (a bad flag, not an API failure)", exitCode(err), exitUsage)
	}
}

func TestQueryRejectsCompareWithDateDimension(t *testing.T) {
	_, err := runCLI(t, jsonHandler([]any{}), "query", "-s", "example.com", "-d", "date", "--compare", "previous")
	if err == nil {
		t.Fatal("expected an error: comparing a time series on a date key is meaningless")
	}
	if !strings.Contains(err.Error(), "date") {
		t.Errorf("error should explain the date/compare conflict, got: %v", err)
	}
}

func TestQueryDrillDownRequiresMatchingDimension(t *testing.T) {
	_, err := runCLI(t, jsonHandler([]any{}),
		"query", "-s", "example.com", "-d", "page", "--page", "https://example.com/a")
	if err == nil || exitCode(err) != exitUsage {
		t.Fatalf("--page belongs with --dim query, got: %v", err)
	}
}

func TestQueryByDateHasNoPositionColumn(t *testing.T) {
	traffic := []map[string]any{
		{"Date": msDate(t, "2026-06-06"), "Clicks": 10, "Impressions": 100},
		{"Date": msDate(t, "2026-06-13"), "Clicks": 20, "Impressions": 150},
	}
	out, err := runCLI(t, jsonHandler(traffic),
		"query", "-s", "example.com", "-d", "date", "--start", "2026-06-01", "--end", "2026-06-30")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if !strings.Contains(out, "2026-06-06") || !strings.Contains(out, "2026-06-13") {
		t.Errorf("both buckets should be listed:\n%s", out)
	}
	if strings.Contains(out, "平均順位") {
		t.Errorf("Bing returns no position at the site level; the column must not be faked:\n%s", out)
	}
}

func TestSitesListRendersVerification(t *testing.T) {
	out, err := runCLI(t, jsonHandler([]map[string]any{
		{"Url": "https://example.com/", "IsVerified": true, "AuthenticationCode": "ABC"},
		{"Url": "https://example.jp/", "IsVerified": false, "AuthenticationCode": "DEF"},
	}), "sites", "list")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, want := range []string{"https://example.com/", "はい", "https://example.jp/", "いいえ"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestSitemapsListRendersCounts(t *testing.T) {
	out, err := runCLI(t, jsonHandler([]map[string]any{{
		"Url":         "https://example.com/sitemap.xml",
		"Type":        "Sitemap",
		"Status":      "Success",
		"Submitted":   msDate(t, "2026-06-01"),
		"LastCrawled": msDate(t, "2026-06-13"),
		"UrlCount":    120,
		"FileSize":    2048,
	}}), "sitemaps", "list", "-s", "example.com")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, want := range []string{"https://example.com/sitemap.xml", "Success", "2026-06-13", "120", "2.0KB"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestInspectReportsIndexStatus(t *testing.T) {
	out, err := runCLI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "GetUrlTrafficInfo") {
			_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{
				"Url": "https://example.com/a", "Clicks": 12, "Impressions": 340, "IsPage": true,
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{
			"Url":             "https://example.com/a",
			"HttpStatus":      200,
			"AnchorCount":     7,
			"LastCrawledDate": msDate(t, "2026-06-10"),
			"DiscoveryDate":   msDate(t, "2026-01-05"),
		}})
	}, "inspect", "-s", "example.com", "https://example.com/a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	for _, want := range []string{"https://example.com/a", "200", "2026-06-10", "2026-01-05", "12", "340"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// One dead URL must not take the rest of the batch down with it.
func TestInspectKeepsGoingAfterOneFailure(t *testing.T) {
	out, err := runCLI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Query().Get("url"), "/bad") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ErrorCode":11,"Message":"Url not found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{
			"Url": "https://example.com/good", "HttpStatus": 200,
		}})
	}, "inspect", "-s", "example.com", "https://example.com/bad", "https://example.com/good")
	if err != nil {
		t.Fatalf("the batch should succeed even when one URL fails: %v\n%s", err, out)
	}

	if !strings.Contains(out, "エラー") {
		t.Errorf("the failing URL should be marked as an error:\n%s", out)
	}
	if !strings.Contains(out, "https://example.com/good") {
		t.Errorf("the healthy URL should still be reported:\n%s", out)
	}
}

// Quota is counted per submission, so overshooting it silently would burn a
// day's allowance on URLs that never get sent.
func TestSubmitStopsBeforeExceedingQuota(t *testing.T) {
	submitted := 0
	out, err := runCLI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "GetUrlSubmissionQuota") {
			_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{"DailyQuota": 2, "MonthlyQuota": 100}})
			return
		}
		submitted++
		_, _ = w.Write([]byte(`{"d":null}`))
	}, "submit", "-s", "example.com",
		"https://example.com/1", "https://example.com/2", "https://example.com/3")

	if err == nil {
		t.Fatalf("expected the command to stop: 3 URLs against a quota of 2\n%s", out)
	}
	if submitted != 0 {
		t.Errorf("made %d submissions, want 0 (nothing should be sent before stopping)", submitted)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("the error should name the escape hatch: %v", err)
	}
}

func TestSubmitDeduplicatesAndReportsProgress(t *testing.T) {
	var sent []string
	out, err := runCLI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "GetUrlSubmissionQuota") {
			_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{"DailyQuota": 10, "MonthlyQuota": 100}})
			return
		}
		var body struct {
			URLList []string `json:"urlList"`
			URL     string   `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.URL != "" {
			sent = append(sent, body.URL)
		}
		sent = append(sent, body.URLList...)
		_, _ = w.Write([]byte(`{"d":null}`))
	}, "submit", "-s", "example.com",
		"https://example.com/1", "https://example.com/1", "https://example.com/2")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if len(sent) != 2 {
		t.Errorf("sent %v, want the duplicate dropped", sent)
	}
	if !strings.Contains(out, "2件を送信しました") {
		t.Errorf("expected a completion line:\n%s", out)
	}
}

func TestSubmitDryRunSendsNothing(t *testing.T) {
	submitted := 0
	out, err := runCLI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "GetUrlSubmissionQuota") {
			_ = json.NewEncoder(w).Encode(map[string]any{"d": map[string]any{"DailyQuota": 10, "MonthlyQuota": 100}})
			return
		}
		submitted++
		_, _ = w.Write([]byte(`{"d":null}`))
	}, "submit", "-s", "example.com", "--dry-run", "https://example.com/1")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if submitted != 0 {
		t.Error("--dry-run must not submit anything")
	}
	if !strings.Contains(out, "残り枠") {
		t.Errorf("--dry-run should still report the quota:\n%s", out)
	}
}

func TestCrawlIssuesDecodeBitFieldAndSortByLinks(t *testing.T) {
	out, err := runCLI(t, jsonHandler([]map[string]any{
		{"Url": "https://example.com/rare", "HttpCode": 404, "Issues": 4, "InLinks": 1},
		{"Url": "https://example.com/popular", "HttpCode": 404, "Issues": 20, "InLinks": 99},
	}), "crawl", "issues", "-s", "example.com")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if !strings.Contains(out, "robots.txt でブロック") {
		t.Errorf("the Issues bit field should be spelled out:\n%s", out)
	}
	// The most-linked broken URL is the one worth fixing first.
	if strings.Index(out, "/popular") > strings.Index(out, "/rare") {
		t.Errorf("issues should be ordered by inbound links:\n%s", out)
	}
}

// A missing API key is a different problem from a failing API, and scripts
// distinguish them by exit code.
func TestMissingAPIKeyExitsWithItsOwnCode(t *testing.T) {
	t.Setenv("BWT_CONFIG_DIR", t.TempDir())
	t.Setenv("BWT_API_KEY", "")
	t.Setenv("BING_WEBMASTER_API_KEY", "")

	global = struct {
		site     string
		output   string
		apiKey   string
		bom      bool
		maxWidth int
		timeout  time.Duration
		throttle time.Duration
		version  string
	}{}

	root := newRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"sites", "list"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := exitCode(err); got != exitNoCreds {
		t.Errorf("exit code = %d, want %d", got, exitNoCreds)
	}
	if !strings.Contains(err.Error(), "bwt auth set") {
		t.Errorf("the error should say how to fix it: %v", err)
	}
}

// The site is required, and forgetting it is a usage error rather than a
// request that goes out and fails.
func TestMissingSiteIsAUsageError(t *testing.T) {
	calls := 0
	_, err := runCLI(t, func(w http.ResponseWriter, r *http.Request) { calls++ }, "query")
	if err == nil || exitCode(err) != exitUsage {
		t.Fatalf("got %v, want a usage error", err)
	}
	if calls != 0 {
		t.Error("no request should be made without a site")
	}
}
