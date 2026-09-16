package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/bwt"
	"github.com/trip-clear/bing-webmaster-cli/internal/output"
)

func newQueryCmd() *cobra.Command {
	var (
		dim        string
		filters    []string
		groupType  string
		start, end string
		preset     string
		days, lag  int
		limit      int
		compare    string
		forPage    string
		forQuery   string
	)

	cmd := &cobra.Command{
		Use:   "query",
		Short: "検索パフォーマンス（クリック・表示回数・CTR・平均掲載順位）を取得する",
		Long: `Bing の実績データを取得し、期間で絞って集計する。

Bing の API には期間指定も集計もない。GetQueryStats は「そのサイトで記録されている
全バケット」を毎回そのまま返すだけなので、期間の絞り込み・キー単位の合算・平均順位の
加重平均はすべてこの CLI 側で行っている。つまり 1回の API 呼び出しで、期間を変えても
--compare を付けても追加のリクエストは発生しない。

Bing の実績は「週次バケット」で記録される。期間を絞ると、その期間に開始日が含まれる
バケットだけが集計対象になる。表の下に「データ: Nバケット」と出るので、意図した
期間ぶんのデータが入っているかは必ずそこで確認する（0バケットならデータが無いのでは
なく、期間がバケットの隙間に落ちている可能性が高い）。

ディメンション:
  --dim query           クエリ別（既定）
  --dim page            ページ別
  --dim date            日別（バケット別）の推移。Bing はこの単位では順位を返さない

ドリルダウン:
  --dim query --page <url>     そのページを呼び出したクエリ
  --dim page  --query <クエリ> そのクエリで表示されたページ

期間の指定（優先順位: --start/--end > --days > --preset）:
  --preset last_3m           既定。プリセット一覧は下の Flags を参照
  --days 30                  --end から遡って30日間
  --start 2026-06-01 --end 2026-06-30

フィルタ（--filter は繰り返し可。すべてクライアント側で適用される）:
  query~~温泉            query が「温泉」を含む
  page!~/tag/            page が /tag/ を含まない
  query==onsen           完全一致（大文字小文字は無視）
  page~*/blog/\d+$       正規表現（RE2）

期間比較（--compare）:
  --compare previous     直前の同じ長さの期間と比較
  --compare year         364日前（曜日が揃う）と比較`,
		Args: cobra.NoArgs,
		Example: `  # 上位クエリ（直近3か月）
  bwt query --site https://example.com/

  # 上位クエリ50件を前期間と比較
  bwt query -d query -l 50 --compare previous

  # ブログ配下のページ別実績を先月分、CSVで
  bwt query -d page -f page~~/blog/ --preset last_month -o csv --bom > blog.csv

  # 日別（バケット別）の推移
  bwt query -d date --days 90

  # 特定ページを呼び出したクエリ
  bwt query -d query --page https://example.com/blog/post-1`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			site, err := resolveSite()
			if err != nil {
				return err
			}
			o, err := buildQueryOptions(dim, filters, groupType, forPage, forQuery,
				bwt.RangeSpec{Start: start, End: end, Days: days, Preset: preset, LagDays: lag}, limit, compare)
			if err != nil {
				return err
			}
			o.Site = site

			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			return runQuery(ctx, cmd, client, o)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&dim, "dim", "d", "query", "集計単位: query | page | date")
	f.StringArrayVarP(&filters, "filter", "f", nil, "フィルタ式（例: page~~/blog/）。繰り返し可")
	f.StringVar(&groupType, "filter-group-type", "and", "複数フィルタの結合: and | or")
	f.StringVar(&start, "start", "", "開始日 YYYY-MM-DD")
	f.StringVar(&end, "end", "", "終了日 YYYY-MM-DD（既定: PT の今日 - --lag 日）")
	f.IntVar(&days, "days", 0, "終了日から遡る日数")
	f.StringVarP(&preset, "preset", "p", "", "期間プリセット: "+strings.Join(bwt.PresetNames(), ", ")+"（既定: "+bwt.DefaultPreset+"）")
	f.IntVar(&lag, "lag", bwt.DefaultLagDays, "データ確定の遅延日数（既定の終了日を今日から何日戻すか）")
	f.IntVarP(&limit, "limit", "l", 1000, "表示する最大行数（0 で全件）")
	f.StringVar(&compare, "compare", "", "比較期間: previous（直前の同期間） | year（364日前）")
	f.StringVar(&forPage, "page", "", "このページを呼び出したクエリに絞る（--dim query と併用）")
	f.StringVar(&forQuery, "query", "", "このクエリで表示されたページに絞る（--dim page と併用）")

	return cmd
}

// queryOptions is one resolved `bwt query` invocation.
type queryOptions struct {
	Site     string
	Dim      bwt.Dimension
	Filters  bwt.Filters
	Range    bwt.DateRange
	Compare  bwt.DateRange
	Mode     string // --compare, empty when not comparing
	Limit    int
	ForPage  string
	ForQuery string
}

func buildQueryOptions(dim string, filters []string, groupType, forPage, forQuery string,
	spec bwt.RangeSpec, limit int, compare string) (queryOptions, error) {

	if limit < 0 {
		return queryOptions{}, usageErrorf("--limit は 0 以上にしてください（0 = 全件）")
	}

	d, err := bwt.ParseDimension(dim)
	if err != nil {
		return queryOptions{}, usageErrorf("%v", err)
	}

	switch {
	case forPage != "" && forQuery != "":
		return queryOptions{}, usageErrorf("--page と --query は同時に使えません（Bing の API はどちらか一方のドリルダウンしかできません）")
	case forPage != "" && d != bwt.DimQuery:
		return queryOptions{}, usageErrorf("--page は --dim query と一緒に使ってください（そのページを呼び出したクエリの一覧になります）")
	case forQuery != "" && d != bwt.DimPage:
		return queryOptions{}, usageErrorf("--query は --dim page と一緒に使ってください（そのクエリで表示されたページの一覧になります）")
	}

	fs, err := bwt.ParseFilters(filters, d, groupType)
	if err != nil {
		return queryOptions{}, usageErrorf("%v", err)
	}
	if d == bwt.DimDate && !fs.Empty() {
		return queryOptions{}, usageErrorf("--dim date では --filter を使えません（日付の絞り込みは --start/--end/--preset で行います）")
	}

	rng, err := bwt.Resolve(spec)
	if err != nil {
		return queryOptions{}, usageErrorf("%v", err)
	}

	o := queryOptions{Site: "", Dim: d, Filters: fs, Range: rng, Limit: limit, ForPage: forPage, ForQuery: forQuery}

	if compare != "" {
		// Joining two periods on a date key is meaningless: the dates never
		// overlap, so every row would show up once with a zero on one side.
		if d == bwt.DimDate {
			return queryOptions{}, usageErrorf("--compare は --dim date と併用できません（推移はそのまま読むか、--dim query / page で比較してください）")
		}
		prev, err := bwt.Comparison(rng, compare)
		if err != nil {
			return queryOptions{}, usageErrorf("%v", err)
		}
		o.Compare, o.Mode = prev, compare
	}
	return o, nil
}

// runQuery fetches once and reports. Bing returns its full history in a single
// response, so the comparison period is carved out of the same payload rather
// than costing a second request.
func runQuery(ctx context.Context, cmd *cobra.Command, client *bwt.Client, o queryOptions) error {
	if o.Dim == bwt.DimDate {
		raw, err := client.RankAndTrafficStats(ctx, o.Site)
		if err != nil {
			return err
		}
		return emit(cmd, dateResult(o, bwt.AggregateByDay(raw, o.Range)))
	}

	raw, err := fetchStats(ctx, client, o)
	if err != nil {
		return err
	}

	current := bwt.Aggregate(raw, o.Range)
	current.Rows = o.Filters.Apply(current.Rows)

	if o.Mode == "" {
		return emit(cmd, plainResult(o, current))
	}

	previous := bwt.Aggregate(raw, o.Compare)
	previous.Rows = o.Filters.Apply(previous.Rows)
	return emit(cmd, compareResult(o, current, previous))
}

func fetchStats(ctx context.Context, client *bwt.Client, o queryOptions) ([]bwt.Stats, error) {
	switch {
	case o.ForPage != "":
		return client.PageQueryStats(ctx, o.Site, o.ForPage)
	case o.ForQuery != "":
		return client.QueryPageStats(ctx, o.Site, o.ForQuery)
	case o.Dim == bwt.DimPage:
		return client.PageStats(ctx, o.Site)
	default:
		return client.QueryStats(ctx, o.Site)
	}
}

// ---- single-period output ----

func plainResult(o queryOptions, rep *bwt.Report) output.Result {
	rows := truncateRows(rep.Rows, o.Limit)

	table := &output.Table{
		Columns: []output.Column{
			{Name: dimLabel(o.Dim), Key: string(o.Dim)},
			{Name: "クリック", Key: "clicks", Right: true},
			{Name: "表示回数", Key: "impressions", Right: true},
			{Name: "CTR", Key: "ctr", Right: true},
			{Name: "平均順位", Key: "position", Right: true},
			{Name: "クリック順位", Key: "click_position", Right: true},
		},
		Note: rangeNote(o, rep),
	}
	jsonRows := make([]map[string]any, 0, len(rows))

	for _, r := range rows {
		table.Rows = append(table.Rows, []string{
			r.Key,
			strconv.Itoa(r.Clicks),
			strconv.Itoa(r.Impressions),
			output.Pct(r.CTR()),
			output.Pos(r.Position),
			clickPosCell(r),
		})
		jsonRows = append(jsonRows, rowJSON(o.Dim, r))
	}

	total := rep.Total()
	if len(rep.Rows) > 0 {
		table.Footer = []string{
			fmt.Sprintf("合計 (%d行)", len(rep.Rows)),
			strconv.Itoa(total.Clicks),
			strconv.Itoa(total.Impressions),
			output.Pct(total.CTR()),
			output.Pos(total.Position),
			clickPosCell(total),
		}
	}

	return output.Result{
		Table: table,
		JSON: map[string]any{
			"site":      o.Site,
			"dimension": string(o.Dim),
			"range":     rangeJSON(o.Range),
			"buckets":   rep.Days,
			"rows":      jsonRows,
			"totals":    totalsJSON(total, len(rep.Rows)),
		},
	}
}

// dateResult renders the time series, which has no position data.
func dateResult(o queryOptions, rep *bwt.Report) output.Result {
	rows := rep.Rows
	if o.Limit > 0 && len(rows) > o.Limit {
		// Keep the most recent buckets, not the oldest ones.
		rows = rows[len(rows)-o.Limit:]
	}

	table := &output.Table{
		Columns: []output.Column{
			{Name: "日付", Key: "date"},
			{Name: "クリック", Key: "clicks", Right: true},
			{Name: "表示回数", Key: "impressions", Right: true},
			{Name: "CTR", Key: "ctr", Right: true},
		},
		Note: rangeNote(o, rep) + "\n※Bing はサイト全体の日別データに掲載順位を返さない（順位が要るときは --dim query / page）",
	}
	jsonRows := make([]map[string]any, 0, len(rows))

	for _, r := range rows {
		table.Rows = append(table.Rows, []string{
			r.Key, strconv.Itoa(r.Clicks), strconv.Itoa(r.Impressions), output.Pct(r.CTR()),
		})
		jsonRows = append(jsonRows, map[string]any{
			"date":        r.Key,
			"clicks":      r.Clicks,
			"impressions": r.Impressions,
			"ctr":         r.CTR(),
		})
	}

	total := rep.Total()
	if len(rep.Rows) > 0 {
		table.Footer = []string{
			fmt.Sprintf("合計 (%d行)", len(rep.Rows)),
			strconv.Itoa(total.Clicks),
			strconv.Itoa(total.Impressions),
			output.Pct(total.CTR()),
		}
	}

	return output.Result{
		Table: table,
		JSON: map[string]any{
			"site":      o.Site,
			"dimension": "date",
			"range":     rangeJSON(o.Range),
			"rows":      jsonRows,
			"totals": map[string]any{
				"clicks":      total.Clicks,
				"impressions": total.Impressions,
				"ctr":         total.CTR(),
				"row_count":   len(rep.Rows),
			},
		},
	}
}

// ---- comparison output ----

// pair is one row of the joined result: the same key in both periods.
type pair struct {
	key  string
	cur  bwt.Row
	prev bwt.Row
}

func compareResult(o queryOptions, current, previous *bwt.Report) output.Result {
	byKey := map[string]*pair{}
	order := []string{}
	get := func(key string) *pair {
		p, ok := byKey[key]
		if !ok {
			p = &pair{key: key}
			byKey[key] = p
			order = append(order, key)
		}
		return p
	}
	for _, r := range current.Rows {
		get(r.Key).cur = r
	}
	// Rows that exist only in the previous period are kept on purpose: a query
	// that lost all its traffic is exactly what a report needs to surface.
	for _, r := range previous.Rows {
		get(r.Key).prev = r
	}

	pairs := make([]*pair, 0, len(order))
	for _, k := range order {
		pairs = append(pairs, byKey[k])
	}
	sortPairs(pairs)

	shown := pairs
	if o.Limit > 0 && len(shown) > o.Limit {
		shown = shown[:o.Limit]
	}

	table := &output.Table{
		Columns: []output.Column{
			{Name: dimLabel(o.Dim), Key: string(o.Dim)},
			{Name: "クリック", Key: "clicks", Right: true},
			{Name: "前期比", Key: "clicks_delta", Right: true},
			{Name: "表示回数", Key: "impressions", Right: true},
			{Name: "前期比", Key: "impressions_delta", Right: true},
			{Name: "CTR", Key: "ctr", Right: true},
			{Name: "差", Key: "ctr_delta", Right: true},
			{Name: "平均順位", Key: "position", Right: true},
			{Name: "改善", Key: "position_gain", Right: true},
		},
		Note: compareNote(o, current, previous),
	}
	jsonRows := make([]map[string]any, 0, len(shown))

	for _, p := range shown {
		ctrDelta, posDelta := ratioDeltas(p.cur, p.prev)
		table.Rows = append(table.Rows, []string{
			p.key,
			strconv.Itoa(p.cur.Clicks),
			deltaCell(float64(p.cur.Clicks), float64(p.prev.Clicks)),
			strconv.Itoa(p.cur.Impressions),
			deltaCell(float64(p.cur.Impressions), float64(p.prev.Impressions)),
			output.Pct(p.cur.CTR()),
			ctrDelta,
			output.Pos(p.cur.Position),
			posDelta,
		})

		obj := rowJSON(o.Dim, p.cur)
		obj[string(o.Dim)] = p.key
		obj["clicks_prev"] = p.prev.Clicks
		obj["clicks_delta"] = p.cur.Clicks - p.prev.Clicks
		obj["impressions_prev"] = p.prev.Impressions
		obj["impressions_delta"] = p.cur.Impressions - p.prev.Impressions
		obj["ctr_prev"] = p.prev.CTR()
		obj["position_prev"] = p.prev.Position
		jsonRows = append(jsonRows, obj)
	}

	curTotal, prevTotal := current.Total(), previous.Total()
	if len(pairs) > 0 {
		ctrDelta, posDelta := ratioDeltas(curTotal, prevTotal)
		table.Footer = []string{
			fmt.Sprintf("合計 (%d行)", len(pairs)),
			strconv.Itoa(curTotal.Clicks),
			deltaCell(float64(curTotal.Clicks), float64(prevTotal.Clicks)),
			strconv.Itoa(curTotal.Impressions),
			deltaCell(float64(curTotal.Impressions), float64(prevTotal.Impressions)),
			output.Pct(curTotal.CTR()),
			ctrDelta,
			output.Pos(curTotal.Position),
			posDelta,
		}
	}

	return output.Result{
		Table: table,
		JSON: map[string]any{
			"site":            o.Site,
			"dimension":       string(o.Dim),
			"range":           rangeJSON(o.Range),
			"compare_range":   rangeJSON(o.Compare),
			"buckets":         current.Days,
			"compare_buckets": previous.Days,
			"rows":            jsonRows,
			"totals":          totalsJSON(curTotal, len(current.Rows)),
			"totals_previous": totalsJSON(prevTotal, len(previous.Rows)),
		},
	}
}

func sortPairs(pairs []*pair) {
	rows := make([]bwt.Row, len(pairs))
	index := map[string]*pair{}
	for i, p := range pairs {
		rows[i] = p.cur
		rows[i].Key = p.key
		index[p.key] = p
	}
	bwt.SortRows(rows)
	for i, r := range rows {
		pairs[i] = index[r.Key]
	}
}

// deltaCell renders an absolute change with its relative change: "+120 (+15.4%)".
func deltaCell(cur, prev float64) string {
	return fmt.Sprintf("%s (%s)", output.DeltaInt(cur, prev), output.Growth(cur, prev))
}

// ratioDeltas renders the CTR and position changes, but only when the row was
// actually shown in both periods. A query that is new this month has no previous
// position, and subtracting from a zero would report it as a catastrophic drop
// from rank 0 rather than as new.
func ratioDeltas(cur, prev bwt.Row) (ctr, position string) {
	if cur.Impressions == 0 || prev.Impressions == 0 {
		return "-", "-"
	}
	return output.DeltaPct(cur.CTR(), prev.CTR()), output.DeltaPos(cur.Position, prev.Position)
}

// ---- helpers ----

func truncateRows(rows []bwt.Row, limit int) []bwt.Row {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func rowJSON(dim bwt.Dimension, r bwt.Row) map[string]any {
	return map[string]any{
		string(dim):      r.Key,
		"clicks":         r.Clicks,
		"impressions":    r.Impressions,
		"ctr":            r.CTR(),
		"position":       r.Position,
		"click_position": r.ClickPosition,
		"buckets":        r.Buckets,
	}
}

func totalsJSON(t bwt.Row, rowCount int) map[string]any {
	return map[string]any{
		"clicks":         t.Clicks,
		"impressions":    t.Impressions,
		"ctr":            t.CTR(),
		"position":       t.Position,
		"click_position": t.ClickPosition,
		"row_count":      rowCount,
		"buckets":        t.Buckets,
	}
}

func rangeJSON(r bwt.DateRange) map[string]string {
	return map[string]string{"start": r.StartString(), "end": r.EndString()}
}

// clickPosCell blanks the click position when nothing was clicked: an average
// over zero clicks is 0, which would read as "rank 0".
func clickPosCell(r bwt.Row) string {
	if r.Clicks == 0 {
		return "-"
	}
	return output.Pos(r.ClickPosition)
}

func dimLabel(d bwt.Dimension) string {
	switch d {
	case bwt.DimPage:
		return "page"
	case bwt.DimDate:
		return "date"
	default:
		return "query"
	}
}

func rangeNote(o queryOptions, rep *bwt.Report) string {
	note := fmt.Sprintf("%s  期間: %s（%d日間）  %s", o.Site, o.Range, o.Range.Days(), bucketNote(rep))
	if o.ForPage != "" {
		note += "\n対象ページ: " + o.ForPage
	}
	if o.ForQuery != "" {
		note += "\n対象クエリ: " + o.ForQuery
	}
	if o.Filters.Len() > 0 {
		note += fmt.Sprintf("\nフィルタ %d 件を適用（クライアント側で絞り込み）", o.Filters.Len())
	}
	return note
}

func compareNote(o queryOptions, current, previous *bwt.Report) string {
	return fmt.Sprintf("%s\n比較: %s  %s\n掲載順位の「改善」は順位が上がった分（+3.0 = 3位上昇）。",
		rangeNote(o, current), o.Compare, bucketNote(previous))
}

// bucketNote states how much data actually landed in the range. Bing's buckets
// are weekly, so an empty result usually means the window fell between them
// rather than that the site had no traffic.
func bucketNote(rep *bwt.Report) string {
	if len(rep.Days) == 0 {
		return "データ: 0バケット ※この期間に Bing のデータがありません（Bing は週次バケット。期間を広げてください）"
	}
	if len(rep.Days) == 1 {
		return fmt.Sprintf("データ: 1バケット（%s）", rep.Days[0])
	}
	return fmt.Sprintf("データ: %dバケット（%s 〜 %s）", len(rep.Days), rep.Days[0], rep.Days[len(rep.Days)-1])
}
