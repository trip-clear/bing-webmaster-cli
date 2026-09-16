package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/bwt"
	"github.com/trip-clear/bing-webmaster-cli/internal/output"
)

func newSubmitCmd() *cobra.Command {
	var (
		file   string
		dryRun bool
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "submit [url...]",
		Short: "URL をインデックス送信する（Adaptive URL Submission）",
		Long: `URL を Bing に送信し、クロールを促す。

URL は引数、--file で渡したファイル、または標準入力（1行1URL、# で始まる行と
空行は無視）から読む。500件ずつに分けて送信する。

送信枠は1日と1か月で決まっている（bwt submit quota で確認できる）。残り枠を超える
数を渡した場合は、送る前に止める。--force を付けると残り枠ぶんだけ送信する。

Microsoft は現在 IndexNow を推奨している。IndexNow は API キー不要で、Bing にも
他の対応エンジンにも同時に通知できるため、CI から自動で叩くならそちらのほうが
素直なことが多い。このコマンドは、既に Webmaster API を使っている場合や、送信枠を
確認しながら手動でまとめて送りたい場合のためのもの。`,
		Args: cobra.ArbitraryArgs,
		Example: `  bwt submit https://example.com/new-post
  bwt submit --file urls.txt
  sitemap-urls | bwt submit -
  bwt submit quota`,
		RunE: func(cmd *cobra.Command, args []string) error {
			site, err := resolveSite()
			if err != nil {
				return err
			}
			urls, err := collectURLs(cmd, args, file)
			if err != nil {
				return err
			}
			if len(urls) == 0 {
				return usageErrorf("送信する URL がありません（引数・--file・標準入力のいずれかで渡してください）")
			}

			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			return runSubmit(ctx, cmd, client, site, urls, dryRun, force)
		},
	}

	f := cmd.Flags()
	f.StringVar(&file, "file", "", "URL を1行1件で並べたファイル（- で標準入力）")
	f.BoolVar(&dryRun, "dry-run", false, "送信せず、対象件数と残り枠だけ表示する")
	f.BoolVar(&force, "force", false, "残り枠を超える場合、枠ぶんだけ送信する（既定は中止）")

	cmd.AddCommand(newSubmitQuotaCmd())
	return cmd
}

// collectURLs reads URLs from the arguments, a file, or stdin. "-" as either an
// argument or --file means stdin, so the command composes with a pipeline.
func collectURLs(cmd *cobra.Command, args []string, file string) ([]string, error) {
	var urls []string
	readStdin := file == "-"

	for _, a := range args {
		if a == "-" {
			readStdin = true
			continue
		}
		urls = append(urls, a)
	}

	if file != "" && file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		lines, err := readURLLines(f)
		if err != nil {
			return nil, err
		}
		urls = append(urls, lines...)
	}

	if readStdin {
		lines, err := readURLLines(cmd.InOrStdin())
		if err != nil {
			return nil, err
		}
		urls = append(urls, lines...)
	}

	return dedupe(urls), nil
}

func readURLLines(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // sitemaps produce long lines
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// dedupe drops repeats while keeping the given order: a duplicate would spend
// a second slot of a quota that is counted per submission, not per URL.
func dedupe(urls []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func runSubmit(ctx context.Context, cmd *cobra.Command, client *bwt.Client, site string,
	urls []string, dryRun, force bool) error {

	w := cmd.OutOrStdout()

	quota, err := client.URLSubmissionQuota(ctx, site)
	if err != nil {
		return err
	}
	allowed := min(quota.DailyQuota, quota.MonthlyQuota)

	fmt.Fprintf(w, "対象: %d件  残り枠: 日 %d件 / 月 %d件\n", len(urls), quota.DailyQuota, quota.MonthlyQuota)

	if len(urls) > allowed {
		if !force {
			return fmt.Errorf("送信件数 %d 件が残り枠 %d 件を超えています。件数を減らすか、枠ぶんだけ送るなら --force を付けてください",
				len(urls), allowed)
		}
		fmt.Fprintf(w, "残り枠に合わせて %d件に切り詰めます。\n", allowed)
		urls = urls[:allowed]
	}

	if dryRun {
		fmt.Fprintf(w, "--dry-run のため送信しませんでした。\n")
		return nil
	}

	sent := 0
	for start := 0; start < len(urls); start += bwt.MaxSubmitBatch {
		end := min(start+bwt.MaxSubmitBatch, len(urls))
		batch := urls[start:end]

		var err error
		if len(batch) == 1 {
			err = client.SubmitURL(ctx, site, batch[0])
		} else {
			err = client.SubmitURLBatch(ctx, site, batch)
		}
		if err != nil {
			// Say how far it got: the URLs already sent have spent quota and
			// must not be re-sent on a retry.
			return fmt.Errorf("%d件目以降の送信に失敗しました（%d件は送信済み）: %w", start+1, sent, err)
		}
		sent += len(batch)
		fmt.Fprintf(w, "送信: %d/%d\n", sent, len(urls))
	}

	fmt.Fprintf(w, "%d件を送信しました。反映には時間がかかります（状況は bwt inspect <url> で確認）。\n", sent)
	return nil
}

func newSubmitQuotaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "quota",
		Short: "URL 送信の残り枠を表示する",
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
			urlQuota, err := client.URLSubmissionQuota(ctx, site)
			if err != nil {
				return err
			}
			// Content submission is a separate allowance and not every account
			// has it; a failure here must not hide the URL quota.
			contentQuota, contentErr := client.ContentSubmissionQuota(ctx, site)

			table := &output.Table{
				Columns: []output.Column{
					{Name: "種別", Key: "kind"},
					{Name: "残り（日）", Key: "daily_quota", Right: true},
					{Name: "残り（月）", Key: "monthly_quota", Right: true},
				},
				Note: site,
				Rows: [][]string{{
					"URL送信",
					strconv.Itoa(urlQuota.DailyQuota),
					strconv.Itoa(urlQuota.MonthlyQuota),
				}},
			}
			payload := map[string]any{
				"site": site,
				"url_submission": map[string]any{
					"daily_quota":   urlQuota.DailyQuota,
					"monthly_quota": urlQuota.MonthlyQuota,
				},
			}
			if contentErr == nil {
				table.Rows = append(table.Rows, []string{
					"コンテンツ送信",
					strconv.Itoa(contentQuota.DailyQuota),
					strconv.Itoa(contentQuota.MonthlyQuota),
				})
				payload["content_submission"] = map[string]any{
					"daily_quota":   contentQuota.DailyQuota,
					"monthly_quota": contentQuota.MonthlyQuota,
				}
			}
			return emit(cmd, output.Result{Table: table, JSON: payload})
		},
	}
}
