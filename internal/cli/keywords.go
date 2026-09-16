package cli

import (
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/bwt"
	"github.com/trip-clear/bing-webmaster-cli/internal/output"
)

// Keyword research is not scoped to a site: it asks Bing about a market, so it
// needs a country and a language instead of --site. These defaults target Japan.
const (
	defaultCountry  = "jp"
	defaultLanguage = "ja-JP"
)

func newKeywordsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "keywords",
		Aliases: []string{"keyword", "kw"},
		Short:   "キーワードリサーチ（表示回数・関連キーワード）",
		Long: `Bing のキーワードリサーチ。自サイトの実績ではなく、Bing 全体でその語が
どれだけ検索されたか（表示回数）を返す。

--site ではなく --country / --language で対象市場を指定する（既定は日本語・日本）。

「広義」の列は、その語だけでなく表記ゆれ・類似表現まで含めた表示回数。語そのものの
検索数より広く、需要の大きさを見るのに向く。`,
	}
	cmd.AddCommand(newKeywordsStatsCmd(), newKeywordsRelatedCmd())
	return cmd
}

func marketFlags(cmd *cobra.Command, country, language *string) {
	cmd.Flags().StringVar(country, "country", defaultCountry, "国コード（例: jp, us）")
	cmd.Flags().StringVar(language, "language", defaultLanguage, "言語コード（例: ja-JP, en-US）")
}

func newKeywordsStatsCmd() *cobra.Command {
	var country, language string

	cmd := &cobra.Command{
		Use:     "stats <keyword>",
		Short:   "キーワードの表示回数の推移を取得する",
		Args:    cobra.ExactArgs(1),
		Example: `  bwt keywords stats 温泉 旅館 --country jp --language ja-JP`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			stats, err := client.KeywordStats(ctx, args[0], country, language)
			if err != nil {
				return err
			}
			sort.Slice(stats, func(i, j int) bool { return stats[i].Date.Day() < stats[j].Date.Day() })

			table := &output.Table{
				Columns: []output.Column{
					{Name: "日付", Key: "date"},
					{Name: "表示回数", Key: "impressions", Right: true},
					{Name: "広義", Key: "broad_impressions", Right: true},
				},
				Note: "キーワード: " + args[0] + "  市場: " + country + " / " + language,
			}
			jsonRows := make([]map[string]any, 0, len(stats))
			for _, s := range stats {
				table.Rows = append(table.Rows, []string{
					s.Date.Day(), strconv.Itoa(s.Impressions), strconv.Itoa(s.BroadImpressions),
				})
				jsonRows = append(jsonRows, map[string]any{
					"date":              s.Date.Day(),
					"impressions":       s.Impressions,
					"broad_impressions": s.BroadImpressions,
				})
			}
			return emit(cmd, output.Result{
				Table: table,
				JSON: map[string]any{
					"query": args[0], "country": country, "language": language, "rows": jsonRows,
				},
			})
		},
	}
	marketFlags(cmd, &country, &language)
	return cmd
}

func newKeywordsRelatedCmd() *cobra.Command {
	var (
		country, language string
		start, end        string
		preset            string
		days, lag         int
		limit             int
	)

	cmd := &cobra.Command{
		Use:   "related <keyword>",
		Short: "関連キーワードを表示回数付きで取得する",
		Long: `指定した語に関連するキーワードを、期間内の表示回数付きで返す。

先頭行には指定した語そのものの実績を出す（Bing が返さない場合は省略される）。`,
		Args:    cobra.ExactArgs(1),
		Example: `  bwt keywords related 温泉 --preset last_3m -l 50`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rng, err := bwt.Resolve(bwt.RangeSpec{Start: start, End: end, Days: days, Preset: preset, LagDays: lag})
			if err != nil {
				return usageErrorf("%v", err)
			}
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}

			related, err := client.RelatedKeywords(ctx, args[0], country, language, rng)
			if err != nil {
				return err
			}
			// The seed term's own volume is the baseline every related term is
			// read against, so it is fetched too. Bing returns nothing for
			// terms it has no data on, which is not an error.
			seed, err := client.Keyword(ctx, args[0], country, language, rng)
			if err != nil {
				return err
			}

			rows := make([]bwt.Keyword, 0, len(related)+1)
			if seed != nil {
				rows = append(rows, *seed)
			}
			sort.SliceStable(related, func(i, j int) bool { return related[i].Impressions > related[j].Impressions })
			rows = append(rows, related...)
			if limit > 0 && len(rows) > limit {
				rows = rows[:limit]
			}

			table := &output.Table{
				Columns: []output.Column{
					{Name: "キーワード", Key: "query"},
					{Name: "表示回数", Key: "impressions", Right: true},
					{Name: "広義", Key: "broad_impressions", Right: true},
				},
				Note: strings.Join([]string{
					"起点: " + args[0],
					"市場: " + country + " / " + language,
					"期間: " + rng.String(),
				}, "  "),
			}
			jsonRows := make([]map[string]any, 0, len(rows))
			for _, k := range rows {
				table.Rows = append(table.Rows, []string{
					k.Query, strconv.Itoa(k.Impressions), strconv.Itoa(k.BroadImpressions),
				})
				jsonRows = append(jsonRows, map[string]any{
					"query":             k.Query,
					"impressions":       k.Impressions,
					"broad_impressions": k.BroadImpressions,
				})
			}
			return emit(cmd, output.Result{
				Table: table,
				JSON: map[string]any{
					"query":    args[0],
					"country":  country,
					"language": language,
					"range":    rangeJSON(rng),
					"keywords": jsonRows,
				},
			})
		},
	}

	marketFlags(cmd, &country, &language)
	f := cmd.Flags()
	f.StringVar(&start, "start", "", "開始日 YYYY-MM-DD")
	f.StringVar(&end, "end", "", "終了日 YYYY-MM-DD")
	f.IntVar(&days, "days", 0, "終了日から遡る日数")
	f.StringVarP(&preset, "preset", "p", "last_3m", "期間プリセット: "+strings.Join(bwt.PresetNames(), ", "))
	f.IntVar(&lag, "lag", bwt.DefaultLagDays, "データ確定の遅延日数")
	f.IntVarP(&limit, "limit", "l", 100, "表示する最大行数（0 で全件）")
	return cmd
}
