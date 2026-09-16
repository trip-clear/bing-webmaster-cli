package cli

import (
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/bwt"
	"github.com/trip-clear/bing-webmaster-cli/internal/output"
)

func newCrawlCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "crawl",
		Short: "クロールの統計と問題",
	}
	cmd.AddCommand(newCrawlStatsCmd(), newCrawlIssuesCmd())
	return cmd
}

func newCrawlStatsCmd() *cobra.Command {
	var (
		start, end string
		preset     string
		days, lag  int
	)

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "bingbot のクロール統計を日別に表示する",
		Long: `bingbot が何ページ取得し、そのうち何件がエラーだったか、インデックスに何件
入っているかを日別に返す。

クロール統計は query と違って日別に記録されている（週次バケットではない）。
期間の絞り込みはこの CLI 側で行う。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			site, err := resolveSite()
			if err != nil {
				return err
			}
			rng, err := bwt.Resolve(bwt.RangeSpec{Start: start, End: end, Days: days, Preset: preset, LagDays: lag})
			if err != nil {
				return usageErrorf("%v", err)
			}
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			stats, err := client.CrawlStats(ctx, site)
			if err != nil {
				return err
			}
			return emit(cmd, crawlStatsResult(site, rng, stats))
		},
	}

	f := cmd.Flags()
	f.StringVar(&start, "start", "", "開始日 YYYY-MM-DD")
	f.StringVar(&end, "end", "", "終了日 YYYY-MM-DD")
	f.IntVar(&days, "days", 0, "終了日から遡る日数")
	f.StringVarP(&preset, "preset", "p", "last_28d", "期間プリセット: "+strings.Join(bwt.PresetNames(), ", "))
	f.IntVar(&lag, "lag", bwt.DefaultLagDays, "データ確定の遅延日数")
	return cmd
}

func crawlStatsResult(site string, rng bwt.DateRange, stats []bwt.CrawlStats) output.Result {
	kept := make([]bwt.CrawlStats, 0, len(stats))
	for _, s := range stats {
		if rng.Contains(s.Date) {
			kept = append(kept, s)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Date.Day() < kept[j].Date.Day() })

	table := &output.Table{
		Columns: []output.Column{
			{Name: "日付", Key: "date"},
			{Name: "クロール", Key: "crawled_pages", Right: true},
			{Name: "インデックス", Key: "in_index", Right: true},
			{Name: "2xx", Key: "code_2xx", Right: true},
			{Name: "301", Key: "code_301", Right: true},
			{Name: "302", Key: "code_302", Right: true},
			{Name: "4xx", Key: "code_4xx", Right: true},
			{Name: "5xx", Key: "code_5xx", Right: true},
			{Name: "robots", Key: "blocked_by_robots_txt", Right: true},
			{Name: "エラー", Key: "crawl_errors", Right: true},
		},
		Note: site + "  期間: " + rng.String(),
	}
	jsonRows := make([]map[string]any, 0, len(kept))
	var total bwt.CrawlStats

	for _, s := range kept {
		table.Rows = append(table.Rows, []string{
			s.Date.Day(),
			strconv.Itoa(s.CrawledPages),
			strconv.Itoa(s.InIndex),
			strconv.Itoa(s.Code2xx),
			strconv.Itoa(s.Code301),
			strconv.Itoa(s.Code302),
			strconv.Itoa(s.Code4xx),
			strconv.Itoa(s.Code5xx),
			strconv.Itoa(s.BlockedByRobotsTxt),
			strconv.Itoa(s.CrawlErrors),
		})
		jsonRows = append(jsonRows, map[string]any{
			"date":                  s.Date.Day(),
			"crawled_pages":         s.CrawledPages,
			"in_index":              s.InIndex,
			"in_links":              s.InLinks,
			"code_2xx":              s.Code2xx,
			"code_301":              s.Code301,
			"code_302":              s.Code302,
			"code_4xx":              s.Code4xx,
			"code_5xx":              s.Code5xx,
			"all_other_codes":       s.AllOtherCodes,
			"blocked_by_robots_txt": s.BlockedByRobotsTxt,
			"contains_malware":      s.ContainsMalware,
			"crawl_errors":          s.CrawlErrors,
		})

		total.CrawledPages += s.CrawledPages
		total.Code2xx += s.Code2xx
		total.Code301 += s.Code301
		total.Code302 += s.Code302
		total.Code4xx += s.Code4xx
		total.Code5xx += s.Code5xx
		total.BlockedByRobotsTxt += s.BlockedByRobotsTxt
		total.CrawlErrors += s.CrawlErrors
		// InIndex is a running level, not a daily count, so the footer shows
		// the latest value rather than a meaningless sum.
		total.InIndex = s.InIndex
	}

	if len(kept) > 0 {
		table.Footer = []string{
			"合計 (" + strconv.Itoa(len(kept)) + "日)",
			strconv.Itoa(total.CrawledPages),
			strconv.Itoa(total.InIndex) + "*",
			strconv.Itoa(total.Code2xx),
			strconv.Itoa(total.Code301),
			strconv.Itoa(total.Code302),
			strconv.Itoa(total.Code4xx),
			strconv.Itoa(total.Code5xx),
			strconv.Itoa(total.BlockedByRobotsTxt),
			strconv.Itoa(total.CrawlErrors),
		}
		table.Note += "\n※インデックス列は累計ではなく現在の件数なので、合計行には期間末の値を出している（*）"
	}

	return output.Result{
		Table: table,
		JSON:  map[string]any{"site": site, "range": rangeJSON(rng), "rows": jsonRows},
	}
}

func newCrawlIssuesCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "issues",
		Short: "bingbot が取得に失敗した URL を一覧する",
		Long: `クロールに問題があった URL を、被リンク数の多い順に一覧する。

被リンクが多い URL ほど、壊れたままにしておく損が大きい。「問題」の列は Bing の
ビットフラグを展開したもので、1つの URL に複数付くことがある。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			site, err := resolveSite()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			issues, err := client.CrawlIssues(ctx, site)
			if err != nil {
				return err
			}
			return emit(cmd, crawlIssuesResult(site, issues, limit))
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "l", 200, "表示する最大行数（0 で全件）")
	return cmd
}

func crawlIssuesResult(site string, issues []bwt.CrawlIssue, limit int) output.Result {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].InLinks != issues[j].InLinks {
			return issues[i].InLinks > issues[j].InLinks
		}
		return issues[i].URL < issues[j].URL
	})

	shown := issues
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}

	table := &output.Table{
		Columns: []output.Column{
			{Name: "URL", Key: "url"},
			{Name: "HTTP", Key: "http_code", Right: true},
			{Name: "被リンク", Key: "in_links", Right: true},
			{Name: "問題", Key: "issues"},
		},
		Note: site + "  被リンクの多い順",
	}
	jsonRows := make([]map[string]any, 0, len(shown))

	for _, i := range shown {
		labels := i.IssueLabels()
		table.Rows = append(table.Rows, []string{
			i.URL,
			strconv.Itoa(i.HttpCode),
			strconv.Itoa(i.InLinks),
			strings.Join(labels, ", "),
		})
		jsonRows = append(jsonRows, map[string]any{
			"url":         i.URL,
			"http_code":   i.HttpCode,
			"in_links":    i.InLinks,
			"issues":      labels,
			"issues_bits": i.Issues,
		})
	}

	return output.Result{
		Table: table,
		JSON:  map[string]any{"site": site, "issues": jsonRows, "total": len(issues)},
	}
}
