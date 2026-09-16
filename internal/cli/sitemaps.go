package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/bwt"
	"github.com/trip-clear/bing-webmaster-cli/internal/output"
)

func newSitemapsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sitemaps",
		Aliases: []string{"sitemap", "feeds"},
		Short:   "サイトマップの一覧・詳細・送信・削除",
		Long:    "Bing の API はサイトマップを「フィード（feed）」と呼ぶが、扱うものは sitemap.xml で同じ。",
	}
	cmd.AddCommand(newSitemapsListCmd(), newSitemapsGetCmd(), newSitemapsSubmitCmd(), newSitemapsRemoveCmd())
	return cmd
}

func newSitemapsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "送信済みサイトマップを一覧する",
		Args:  cobra.NoArgs,
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
			feeds, err := client.Feeds(ctx, site)
			if err != nil {
				return err
			}
			return emit(cmd, feedsResult(site, feeds, ""))
		},
	}
}

func newSitemapsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <sitemap-url>",
		Short: "サイトマップインデックスの子サイトマップを一覧する",
		Long: `サイトマップインデックスに含まれる個々のサイトマップを一覧する。

普通のサイトマップ（インデックスでないもの）を渡した場合は、Bing は何も返さない。
その場合は bwt sitemaps list で状態を確認する。`,
		Args: cobra.ExactArgs(1),
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
			feeds, err := client.FeedDetails(ctx, site, args[0])
			if err != nil {
				return err
			}
			return emit(cmd, feedsResult(site, feeds, args[0]))
		},
	}
}

func newSitemapsSubmitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "submit <sitemap-url>",
		Short: "サイトマップを送信（再送信）する",
		Long: `サイトマップを Bing に送信する。URL は完全な URL で渡す
（例: https://example.com/sitemap.xml）。

成功は「受理された」という意味で、クロール結果はすぐには反映されない。
しばらく経ってから bwt sitemaps list で 状態 / 最終クロール / URL数 を確認する。`,
		Args:    cobra.ExactArgs(1),
		Example: `  bwt sitemaps submit https://example.com/sitemap.xml`,
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
			if err := client.SubmitFeed(ctx, site, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "送信しました: %s\n取得結果は少し待ってから `bwt sitemaps list` で確認してください。\n", args[0])
			return nil
		},
	}
}

func newSitemapsRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <sitemap-url>",
		Aliases: []string{"delete"},
		Short:   "サイトマップの登録を削除する",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			site, err := resolveSite()
			if err != nil {
				return err
			}
			if !yes {
				return usageErrorf("%s（%s）の登録を削除します。実行するには --yes を付けてください", args[0], site)
			}
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			if err := client.RemoveFeed(ctx, site, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "削除しました: %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "確認なしで削除する")
	return cmd
}

func feedsResult(site string, feeds []bwt.Feed, parent string) output.Result {
	note := site
	if parent != "" {
		note += "  親サイトマップ: " + parent
	}
	table := &output.Table{
		Columns: []output.Column{
			{Name: "サイトマップ", Key: "url"},
			{Name: "種別", Key: "type"},
			{Name: "状態", Key: "status"},
			{Name: "最終送信", Key: "submitted"},
			{Name: "最終クロール", Key: "last_crawled"},
			{Name: "URL数", Key: "url_count", Right: true},
			{Name: "サイズ", Key: "file_size", Right: true},
		},
		Note: note,
	}
	jsonRows := make([]map[string]any, 0, len(feeds))

	for _, f := range feeds {
		table.Rows = append(table.Rows, []string{
			f.URL,
			dash(f.Type),
			dash(f.Status),
			f.Submitted.Minute(),
			f.LastCrawled.Minute(),
			strconv.Itoa(f.URLCount),
			humanBytes(f.FileSize),
		})
		jsonRows = append(jsonRows, map[string]any{
			"url":          f.URL,
			"type":         f.Type,
			"status":       f.Status,
			"submitted":    f.Submitted,
			"last_crawled": f.LastCrawled,
			"url_count":    f.URLCount,
			"file_size":    f.FileSize,
			"compressed":   f.Compressed,
		})
	}

	return output.Result{Table: table, JSON: map[string]any{"site": site, "sitemaps": jsonRows}}
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func humanBytes(n int) string {
	switch {
	case n <= 0:
		return "-"
	case n < 1024:
		return strconv.Itoa(n) + "B"
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
