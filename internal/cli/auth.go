package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/config"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "API キーの設定・確認・削除",
		Long: `Bing Webmaster Tools の API キーを管理する。

キーは Bing Webmaster Tools（https://www.bing.com/webmasters）にログインして
「設定 > API アクセス > API キー」で発行する。1アカウントに1つで、そのアカウントが
確認済みのサイトすべてに対する読み書き権限を持つ。`,
	}
	cmd.AddCommand(newAuthSetCmd(), newAuthStatusCmd(), newAuthLogoutCmd())
	return cmd
}

func newAuthSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set [api-key]",
		Short: "API キーを ~/.config/bwt/config.json に保存する",
		Long: `API キーを保存する（パーミッション 0600）。

引数を省略すると標準入力から読む。引数で渡すとシェルの履歴に残るため、
対話的に使うときは引数なしで実行するのが安全:

  bwt auth set          # プロンプトが出るので貼り付ける
  pbpaste | bwt auth set`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := ""
			if len(args) > 0 {
				key = strings.TrimSpace(args[0])
			} else {
				var err error
				if key, err = promptKey(cmd); err != nil {
					return err
				}
			}
			if key == "" {
				return usageErrorf("API キーが空です")
			}

			f, err := config.Load()
			if err != nil {
				return err
			}
			f.APIKey = key
			if err := config.Save(f); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "保存しました: %s（%s）\n次: bwt sites list\n",
				config.Path(), config.Redact(key))
			return nil
		},
	}
}

func promptKey(cmd *cobra.Command) (string, error) {
	fmt.Fprint(cmd.ErrOrStderr(), "API キー: ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "どの API キーが使われるかを表示する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, src, err := config.APIKey(global.apiKey)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "APIキー: %s\n", config.Redact(key))
			fmt.Fprintf(w, "参照元 : %s（%s）\n", src.Detail, src.Kind)
			if site := config.Site(global.site); site != "" {
				fmt.Fprintf(w, "既定サイト: %s\n", site)
			}
			fmt.Fprintf(w, "\nキーは見つかりました。実際にサイトが見えるかは `bwt sites list` で確認してください。\n")
			return nil
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "保存済みの API キーを削除する",
		Long: `~/.config/bwt/config.json から API キーを消す。

環境変数（BWT_API_KEY / BING_WEBMASTER_API_KEY）で渡している場合はそちらが
残るため、あわせて unset してください。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, had, err := config.Logout()
			if err != nil {
				return err
			}
			if !had {
				fmt.Fprintf(cmd.OutOrStdout(), "保存済みの API キーはありませんでした: %s\n", path)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "削除しました: %s\n", path)
			return nil
		},
	}
}
