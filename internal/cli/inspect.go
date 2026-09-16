package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/bwt"
	"github.com/trip-clear/bing-webmaster-cli/internal/output"
)

func newInspectCmd() *cobra.Command {
	var (
		children bool
		page     int
	)

	cmd := &cobra.Command{
		Use:   "inspect <url> [url...]",
		Short: "URL のインデックス状況を確認する",
		Long: `Bing がその URL について持っている情報を返す。

インデックス済みか（HTTP ステータス）、最後にクロールしたのはいつか、いつ発見したか、
被リンクのアンカー数、そして期間を問わない累計のクリック・表示回数を1行にまとめる。

Search Console の URL 検査とは違い、Bing には「なぜインデックスされないか」を返す API は
無い。返ってくるのは「インデックスにあるならその情報」だけで、まだ知られていない URL は
エラーになる（その場合は bwt submit で送信する）。

--children を付けると、ディレクトリ配下でインデックスされている URL を一覧する。`,
		Args: cobra.MinimumNArgs(1),
		Example: `  bwt inspect https://example.com/blog/post-1
  bwt inspect -o json https://example.com/a https://example.com/b
  bwt inspect --children https://example.com/blog/`,
		RunE: func(cmd *cobra.Command, args []string) error {
			site, err := resolveSite()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}

			if children {
				if len(args) != 1 {
					return usageErrorf("--children はディレクトリ URL を1つだけ指定してください")
				}
				return runChildren(ctx, cmd, client, site, args[0], page)
			}
			return emit(cmd, inspectResult(site, inspectAll(ctx, client, site, args)))
		},
	}

	f := cmd.Flags()
	f.BoolVar(&children, "children", false, "ディレクトリ配下のインデックス済み URL を一覧する")
	f.IntVar(&page, "page", 0, "--children のページ番号（0 始まり）")
	return cmd
}

type inspection struct {
	url     string
	info    *bwt.URLInfo
	traffic *bwt.URLTrafficInfo
	err     error
}

// inspectAll walks the URLs one at a time. It is deliberately sequential: the
// client already paces requests, and two calls per URL against an API that
// throttles on its own schedule is not worth parallelising.
func inspectAll(ctx context.Context, client *bwt.Client, site string, urls []string) []inspection {
	out := make([]inspection, 0, len(urls))
	for _, u := range urls {
		in := inspection{url: u}
		// One bad URL must not sink the whole batch: record the error in its
		// row and keep going.
		in.info, in.err = client.URLInfo(ctx, site, u)
		if in.err == nil {
			// Traffic is a nice-to-have; a URL with no traffic record should
			// still report its index status.
			in.traffic, _ = client.URLTrafficInfo(ctx, site, u)
		}
		out = append(out, in)
	}
	return out
}

func inspectResult(site string, results []inspection) output.Result {
	table := &output.Table{
		Columns: []output.Column{
			{Name: "URL", Key: "url"},
			{Name: "HTTP", Key: "http_status", Right: true},
			{Name: "最終クロール", Key: "last_crawled_date"},
			{Name: "発見日", Key: "discovery_date"},
			{Name: "被リンク", Key: "anchor_count", Right: true},
			{Name: "クリック", Key: "clicks", Right: true},
			{Name: "表示回数", Key: "impressions", Right: true},
		},
		Note: site + "  ※クリック・表示回数は期間を問わない累計（期間別は bwt query）",
	}
	jsonRows := make([]map[string]any, 0, len(results))

	for _, r := range results {
		if r.err != nil {
			table.Rows = append(table.Rows, []string{r.url, "エラー", firstLine(r.err.Error()), "", "", "", ""})
			jsonRows = append(jsonRows, map[string]any{"url": r.url, "error": r.err.Error()})
			continue
		}

		clicks, impressions := "-", "-"
		if r.traffic != nil {
			clicks = strconv.Itoa(r.traffic.Clicks)
			impressions = strconv.Itoa(r.traffic.Impressions)
		}
		table.Rows = append(table.Rows, []string{
			r.info.URL,
			strconv.Itoa(r.info.HttpStatus),
			r.info.LastCrawledDate.Minute(),
			r.info.DiscoveryDate.Minute(),
			strconv.Itoa(r.info.AnchorCount),
			clicks,
			impressions,
		})

		obj := map[string]any{
			"url":                   r.info.URL,
			"http_status":           r.info.HttpStatus,
			"last_crawled_date":     r.info.LastCrawledDate,
			"discovery_date":        r.info.DiscoveryDate,
			"anchor_count":          r.info.AnchorCount,
			"document_size":         r.info.DocumentSize,
			"is_page":               r.info.IsPage,
			"total_child_url_count": r.info.TotalChildUrlCount,
		}
		if r.traffic != nil {
			obj["clicks"] = r.traffic.Clicks
			obj["impressions"] = r.traffic.Impressions
		}
		jsonRows = append(jsonRows, obj)
	}

	return output.Result{Table: table, JSON: map[string]any{"site": site, "results": jsonRows}}
}

func runChildren(ctx context.Context, cmd *cobra.Command, client *bwt.Client, site, dir string, page int) error {
	infos, err := client.ChildrenURLInfo(ctx, site, dir, page)
	if err != nil {
		return err
	}
	traffic, err := client.ChildrenURLTrafficInfo(ctx, site, dir, page)
	if err != nil {
		// The index listing is the point; traffic is extra.
		traffic = nil
	}
	byURL := map[string]bwt.URLTrafficInfo{}
	for _, t := range traffic {
		byURL[t.URL] = t
	}

	table := &output.Table{
		Columns: []output.Column{
			{Name: "URL", Key: "url"},
			{Name: "HTTP", Key: "http_status", Right: true},
			{Name: "最終クロール", Key: "last_crawled_date"},
			{Name: "被リンク", Key: "anchor_count", Right: true},
			{Name: "クリック", Key: "clicks", Right: true},
			{Name: "表示回数", Key: "impressions", Right: true},
		},
		Note: fmt.Sprintf("%s  配下: %s  ページ %d（次のページは --page %d）", site, dir, page, page+1),
	}
	jsonRows := make([]map[string]any, 0, len(infos))

	for _, i := range infos {
		t := byURL[i.URL]
		table.Rows = append(table.Rows, []string{
			i.URL,
			strconv.Itoa(i.HttpStatus),
			i.LastCrawledDate.Minute(),
			strconv.Itoa(i.AnchorCount),
			strconv.Itoa(t.Clicks),
			strconv.Itoa(t.Impressions),
		})
		jsonRows = append(jsonRows, map[string]any{
			"url":               i.URL,
			"http_status":       i.HttpStatus,
			"last_crawled_date": i.LastCrawledDate,
			"discovery_date":    i.DiscoveryDate,
			"anchor_count":      i.AnchorCount,
			"document_size":     i.DocumentSize,
			"clicks":            t.Clicks,
			"impressions":       t.Impressions,
		})
	}

	return emit(cmd, output.Result{
		Table: table,
		JSON:  map[string]any{"site": site, "directory": dir, "page": page, "urls": jsonRows},
	})
}
