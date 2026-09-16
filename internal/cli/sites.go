package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/trip-clear/bing-webmaster-cli/internal/bwt"
	"github.com/trip-clear/bing-webmaster-cli/internal/output"
)

func newSitesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sites",
		Aliases: []string{"site"},
		Short:   "サイトの一覧・追加・所有権確認・削除",
	}
	cmd.AddCommand(newSitesListCmd(), newSitesAddCmd(), newSitesVerifyCmd(), newSitesRemoveCmd())
	return cmd
}

func newSitesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "API キーのアカウントに登録されたサイトを一覧する",
		Long: `API キーの持ち主が Bing Webmaster Tools に登録しているサイトを一覧する。

--site に渡すべき正確な文字列はここで確認する。Bing のサイトは末尾スラッシュまで
含めて1つなので、"https://example.com" と "https://example.com/" は別物として
扱われる（bwt 側では自動で補完する）。

「確認済み」が「いいえ」のサイトは、所有権の確認（meta タグ / XML ファイル / DNS）が
済んでいない。実績データは取得できない。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			sites, err := client.Sites(ctx)
			if err != nil {
				return err
			}
			return emit(cmd, sitesResult(sites))
		},
	}
}

func newSitesAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <site>",
		Short: "サイトを追加する（所有権の確認は別途必要）",
		Long: `サイトをアカウントに追加する。

これは「追加」であって「確認（verification）」ではない。追加したあと meta タグ /
XML ファイル / DNS レコードのいずれかを設置し、bwt sites verify で確認させる。`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			site := bwt.NormalizeSite(args[0])
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			if err := client.AddSite(ctx, site); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"追加しました: %s\n所有権の確認コードは `bwt sites list -o json` で確認できます。設置後に `bwt sites verify %s` を実行してください。\n",
				site, site)
			return nil
		},
	}
}

func newSitesVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <site>",
		Short: "所有権の確認を実行する",
		Long: `設置済みの確認コード（meta タグ / BingSiteAuth.xml / DNS TXT）を Bing に
再チェックさせる。確認コードの値は bwt sites list -o json の
AuthenticationCode / DnsVerificationCode にある。`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			site := bwt.NormalizeSite(args[0])
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			ok, err := client.VerifySite(ctx, site)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("所有権を確認できませんでした: %s\n\n確認コードが設置されているか、URL が完全に一致しているかを確認してください。", site)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "確認できました: %s\n", site)
			return nil
		},
	}
}

func newSitesRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <site>",
		Aliases: []string{"delete"},
		Short:   "サイトをアカウントから削除する",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			site := bwt.NormalizeSite(args[0])
			if !yes {
				return usageErrorf("%s を削除します。実行するには --yes を付けてください", site)
			}
			ctx := cmd.Context()
			client, err := newClient(ctx)
			if err != nil {
				return err
			}
			if err := client.RemoveSite(ctx, site); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "削除しました: %s\n", site)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "確認なしで削除する")
	return cmd
}

func sitesResult(sites []bwt.Site) output.Result {
	table := &output.Table{
		Columns: []output.Column{
			{Name: "サイト", Key: "url"},
			{Name: "確認済み", Key: "is_verified"},
		},
		Note: "--site にはこの「サイト」列の文字列をそのまま渡す（末尾スラッシュまで含めて一致する必要がある）",
	}
	jsonRows := make([]map[string]any, 0, len(sites))
	for _, s := range sites {
		table.Rows = append(table.Rows, []string{s.URL, yesNo(s.IsVerified)})
		jsonRows = append(jsonRows, map[string]any{
			"url":                   s.URL,
			"is_verified":           s.IsVerified,
			"authentication_code":   s.AuthenticationCode,
			"dns_verification_code": s.DnsVerificationCode,
		})
	}
	return output.Result{Table: table, JSON: map[string]any{"sites": jsonRows}}
}

func yesNo(b bool) string {
	if b {
		return "はい"
	}
	return "いいえ"
}
