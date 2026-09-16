// Package cli wires the bwt commands together.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/bwt"
	"github.com/trip-clear/bing-webmaster-cli/internal/config"
	"github.com/trip-clear/bing-webmaster-cli/internal/output"
)

// Exit codes, so scripts can tell a bad invocation from a missing credential.
const (
	exitOK      = 0
	exitError   = 1 // API or runtime error
	exitUsage   = 2 // bad flags or arguments
	exitNoCreds = 3 // no usable API key
)

// global holds the persistent flags.
var global struct {
	site     string
	output   string
	apiKey   string
	bom      bool
	maxWidth int
	timeout  time.Duration
	throttle time.Duration
	version  string
}

// Execute runs the root command and returns the process exit code.
func Execute(version string) int {
	global.version = version

	if err := newRootCmd(version).Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "エラー: %v\n", err)
		return exitCode(err)
	}
	return exitOK
}

func exitCode(err error) int {
	switch {
	case errors.Is(err, config.ErrNoAPIKey):
		return exitNoCreds
	case errors.Is(err, errUsage):
		return exitUsage
	default:
		return exitError
	}
}

// errUsage marks an error as "the command was called wrong", so it maps to exit
// code 2 rather than being confused with an API failure. It is a matching
// target only -- the text the user sees is the one passed to usageErrorf.
var errUsage = errors.New("usage")

type usageError struct{ msg string }

func (e *usageError) Error() string        { return e.msg }
func (e *usageError) Is(target error) bool { return target == errUsage }

func usageErrorf(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "bwt",
		Short: "Bing Webmaster Tools CLI",
		Long: `Bing Webmaster Tools API をターミナルから叩く CLI。

検索パフォーマンス（クリック・表示回数・CTR・平均掲載順位）の取得、URL のインデックス
状況の確認、サイトマップ管理、URL のインデックス送信、クロールの問題、キーワード
リサーチ、被リンクの確認を行う。

対象サイトは --site か環境変数 BWT_SITE で指定する。Bing のサイトは Search Console と
違って URL プレフィックスの1種類だけで、末尾スラッシュまで含めて一致する必要がある:
  https://example.com/   （example.com と書いても補完される）

認証は Bing Webmaster Tools で発行する API キー:
  bwt auth set             ~/.config/bwt/config.json に保存
  export BWT_API_KEY=...   環境変数（CI 向け）

Bing の実績データは「週次バケット」で返る。期間を絞っても、その期間に含まれるバケット
だけが集計対象になる（何バケット入ったかは表の下に出る）。`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&global.site, "site", "s", "", "対象サイト（env: BWT_SITE）")
	pf.StringVarP(&global.output, "output", "o", output.FormatTable, "出力形式: table | json | csv")
	pf.StringVar(&global.apiKey, "api-key", "", "API キー（env: BWT_API_KEY / BING_WEBMASTER_API_KEY）")
	pf.BoolVar(&global.bom, "bom", false, "CSV に UTF-8 BOM を付ける（Excel で文字化けさせないため）")
	pf.IntVar(&global.maxWidth, "max-width", 60, "table 出力でセルを切り詰める幅（0 で切り詰めない。csv/json は常に全文）")
	pf.DurationVar(&global.timeout, "timeout", bwt.DefaultTimeout, "1リクエストのタイムアウト")
	pf.DurationVar(&global.throttle, "throttle", bwt.DefaultThrottle, "リクエスト間の最小間隔（スロットリングされるなら大きくする。0 で無効）")

	root.AddCommand(
		newAuthCmd(),
		newSitesCmd(),
		newQueryCmd(),
		newInspectCmd(),
		newSitemapsCmd(),
		newSubmitCmd(),
		newCrawlCmd(),
		newKeywordsCmd(),
		newLinksCmd(),
	)
	return root
}

// newClient builds an authenticated client. It is where a missing API key turns
// into the exit code 3 that scripts look for.
func newClient(_ context.Context) (*bwt.Client, error) {
	key, _, err := config.APIKey(global.apiKey)
	if err != nil {
		return nil, err
	}

	opts := []bwt.Option{
		bwt.WithUserAgent("bwt/" + global.version),
		bwt.WithTimeout(global.timeout),
		bwt.WithThrottle(global.throttle),
	}
	// BWT_ENDPOINT points the client at a different API host. It exists for
	// tests and debugging; leave it unset in normal use.
	if endpoint := os.Getenv("BWT_ENDPOINT"); endpoint != "" {
		opts = append(opts, bwt.WithEndpoint(endpoint))
	}
	return bwt.New(key, opts...), nil
}

// resolveSite returns the normalised property from --site, BWT_SITE or the
// config file.
func resolveSite() (string, error) {
	site := config.Site(global.site)
	if site == "" {
		return "", usageErrorf("対象サイトが未指定です。--site を渡すか BWT_SITE を設定してください（候補は `bwt sites list` で確認）")
	}
	return bwt.NormalizeSite(site), nil
}

func emit(cmd *cobra.Command, r output.Result) error {
	return output.Emit(cmd.OutOrStdout(), r, output.Options{
		Format:       global.output,
		BOM:          global.bom,
		MaxCellWidth: global.maxWidth,
	})
}
