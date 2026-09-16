package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/output"
)

func newLinksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "links",
		Aliases: []string{"link"},
		Short:   "被リンクの確認",
		Long: `Bing が把握している被リンクを見る。

結果はページ送りで返る。件数はサーバ側で決まっていて指定できないので、
--page で次のページに進む（0 始まり）。`,
	}
	cmd.AddCommand(newLinksListCmd(), newLinksGetCmd())
	return cmd
}

// pageNote states where in the paging this result sits, and only offers the
// next page when there is one -- Bing decides the page size, so the reader has
// no other way to tell whether they have seen everything.
func pageNote(page, totalPages int) string {
	if totalPages <= 0 {
		return fmt.Sprintf("ページ %d", page+1)
	}
	if page+1 >= totalPages {
		return fmt.Sprintf("ページ %d / %d（最後のページ）", page+1, totalPages)
	}
	return fmt.Sprintf("ページ %d / %d（次は --page %d）", page+1, totalPages, page+1)
}

func newLinksListCmd() *cobra.Command {
	var page int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "被リンクを受けている自サイトのページを一覧する",
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
			counts, err := client.LinkCounts(ctx, site, page)
			if err != nil {
				return err
			}

			table := &output.Table{
				Columns: []output.Column{
					{Name: "ページ", Key: "url"},
					{Name: "被リンク数", Key: "count", Right: true},
				},
				Note: site + "  " + pageNote(page, counts.TotalPages),
			}
			jsonRows := make([]map[string]any, 0, len(counts.Links))
			for _, l := range counts.Links {
				table.Rows = append(table.Rows, []string{l.URL, strconv.Itoa(l.Count)})
				jsonRows = append(jsonRows, map[string]any{"url": l.URL, "count": l.Count})
			}
			return emit(cmd, output.Result{
				Table: table,
				JSON: map[string]any{
					"site": site, "page": page, "total_pages": counts.TotalPages, "links": jsonRows,
				},
			})
		},
	}
	cmd.Flags().IntVar(&page, "page", 0, "ページ番号（0 始まり）")
	return cmd
}

func newLinksGetCmd() *cobra.Command {
	var page int

	cmd := &cobra.Command{
		Use:     "get <page-url>",
		Short:   "自サイトの1ページに対する被リンク元を一覧する",
		Args:    cobra.ExactArgs(1),
		Example: `  bwt links get https://example.com/blog/post-1`,
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
			details, err := client.URLLinks(ctx, site, args[0], page)
			if err != nil {
				return err
			}

			table := &output.Table{
				Columns: []output.Column{
					{Name: "リンク元", Key: "url"},
					{Name: "アンカーテキスト", Key: "anchor_text"},
				},
				Note: fmt.Sprintf("%s  対象: %s  %s", site, args[0], pageNote(page, details.TotalPages)),
			}
			jsonRows := make([]map[string]any, 0, len(details.Details))
			for _, d := range details.Details {
				table.Rows = append(table.Rows, []string{d.URL, d.AnchorText})
				jsonRows = append(jsonRows, map[string]any{"url": d.URL, "anchor_text": d.AnchorText})
			}
			return emit(cmd, output.Result{
				Table: table,
				JSON: map[string]any{
					"site": site, "url": args[0], "page": page,
					"total_pages": details.TotalPages, "links": jsonRows,
				},
			})
		},
	}
	cmd.Flags().IntVar(&page, "page", 0, "ページ番号（0 始まり）")
	return cmd
}
