# bwt — Bing Webmaster Tools CLI

Bing Webmaster Tools API をターミナルから叩く CLI（Go 製）。検索パフォーマンスの取得、URL のインデックス状況の確認、サイトマップ管理、URL のインデックス送信、クロールの問題、キーワードリサーチ、被リンクの確認に対応する。

同じ設計方針の Google 版が [`search-console-cli`](https://github.com/trip-clear/search-console-cli)（`gsc`）。フラグ名・出力形式・終了コードは可能な範囲で揃えてあるので、両方のデータを1本のスクリプトで扱える。

## インストール

必要なもの: Go 1.26.5 以上（これ未満でもツールチェーンが自動で落ちてくる）。

### go install（推奨）

```bash
go install github.com/trip-clear/bing-webmaster-cli/cmd/bwt@latest

bwt --version
```

- `~/go/bin`（正確には `go env GOPATH`/bin）が PATH に入っていること。入っていなければ `export PATH="$(go env GOPATH)/bin:$PATH"` を shell の rc に足す。
- 更新は同じコマンドを再実行するだけ。バージョン固定なら `@v0.1.0` のようにタグを指定する。

### ソースから

```bash
git clone https://github.com/trip-clear/bing-webmaster-cli.git
cd bing-webmaster-cli

make install          # ~/.local/bin/bwt に入る（PREFIX=/usr/local で変更可）
# または
make build            # ./bin/bwt に置くだけ
```

## セットアップ（認証）

Google と違って OAuth もサービスアカウントも要らない。**API キー1本**で、そのアカウントが確認済みのサイト全部に対する読み書きができる。

1. https://www.bing.com/webmasters にログイン
2. 右上の歯車 → **設定 > API アクセス > API キー** でキーを生成
3. 保存する

```bash
bwt auth set          # プロンプトに貼り付け → ~/.config/bwt/config.json (0600)
bwt auth status       # どのキーが使われるかを確認
bwt sites list        # 実際にサイトが見えるか確認
```

CI からは環境変数で渡す。

```bash
export BWT_API_KEY=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

優先順位は `--api-key` > `BWT_API_KEY` > `BING_WEBMASTER_API_KEY` > 設定ファイル。

> キーを引数で渡す（`bwt auth set <key>`）とシェルの履歴に残る。対話的に使うときは引数なしで実行する。

## 対象サイトの指定

`--site` / `-s`、または環境変数 `BWT_SITE`。

Bing のサイトは Search Console と違い **URL プレフィックスの1種類だけ**で、ドメインプロパティに相当するものはない。末尾スラッシュまで含めて一致している必要がある。

| 書き方 | 解釈 |
|---|---|
| `example.com` | `https://example.com/` |
| `https://example.com` | `https://example.com/` |
| `http://example.com/` | そのまま（http で登録したサイトは https とは別物） |
| `sc-domain:example.com` | `https://example.com/`（gsc の書き方を受け付けるだけ） |

正確な文字列は `bwt sites list` で確認する。

```bash
export BWT_SITE=https://example.com/   # 毎回 --site を書かなくて済む
```

## 知っておくべき Bing API の癖

このツールの設計はほぼ全部ここから来ている。

**1. 期間指定が無い。** `GetQueryStats` などの実績系メソッドには日付パラメータが1つも無く、**そのサイトで記録されている全期間のデータを毎回まるごと返す**。したがって期間の絞り込み・キー単位の合算・平均順位の加重平均は、すべて `bwt` 側でやっている。

その副作用として、**`--compare` を付けても API 呼び出しは1回のまま**（同じレスポンスから前期間を切り出す）。期間を変えて何度も見るのも、追加コストは無い。

**2. データは週次バケット。** Bing は日別ではなく週単位で記録する。`--days 7` のような短い期間はバケットの隙間に落ちて0件になることがある。これが「データが無い」のか「期間の取り方が悪い」のか分からないと事故るので、表の下に必ず **何バケット入ったか** を出す。

```
https://example.com/  期間: 2026-06-01 〜 2026-06-30（30日間）  データ: 3バケット（2026-06-06 〜 2026-06-20）
```

比較時は両期間のバケット数が出る。バケット数が違う期間同士の比較は、その時点で割り引いて読む必要がある。

既定のプリセットが `last_3m`（gsc は `last_28d`）なのも同じ理由。

**3. 順位が2種類ある。** `平均順位` は表示されたときの平均掲載順位（Search Console の「掲載順位」に相当）、`クリック順位` はクリックされたときの平均順位。集計時は前者を表示回数で、後者をクリック数で重み付けして平均する。クリックが0の行は「順位0位」ではないので `-` と表示する。

**4. サイト全体の日別データには順位が無い。** `--dim date` はクリック・表示回数だけを返す。

**5. フィルタはクライアント側。** サーバ側フィルタは存在しないので `--filter` は取得後に適用される。gsc と同じ演算子が使えるが、指定できるディメンションは集計単位と同じもの1つだけ。

## 使い方

### 検索パフォーマンス（query）

```bash
# 上位クエリ（既定は直近3か月）
bwt query

# 上位クエリ50件を前期間と比較
bwt query -l 50 --compare previous

# ページ別・先月分を CSV に（Excel 用に BOM 付き）
bwt query -d page --preset last_month -o csv --bom > pages.csv

# ブログ配下だけ、指名検索を除外して
bwt query -d page -f page~~/blog/ -l 200
bwt query -f 'query!~ブランド名'

# 日別（バケット別）推移
bwt query -d date --days 90

# 特定ページを呼び出したクエリ / 特定クエリで表示されたページ
bwt query -d query --page https://example.com/blog/post-1
bwt query -d page --query '箱根 温泉'
```

**期間**（優先順位: `--start`/`--end` > `--days` > `--preset`）

- プリセット: `today` `yesterday` `latest` `last_7d` `last_28d` `last_30d` `last_90d` `last_3m`（既定）`last_6m` `last_12m` `this_month` `last_month` `all`
- 既定の終了日は **PT の今日 − 2日**（`--lag` で変更可）。Bing はデータを PT で記録する。
- `--preset all` は API が持っている全期間。Bing は数年ぶん保持していることがある。

**ディメンション**: `query`（既定） `page` `date`。Bing の API は複数ディメンションの組み合わせに対応していないので、掛け合わせが要るときは `--page` / `--query` のドリルダウンを使う。

**フィルタ**（`-f` を繰り返し可・`--filter-group-type or` で OR に）

| 演算子 | 意味 | 例 |
|---|---|---|
| `==` | 一致（大文字小文字は無視） | `query==onsen` |
| `!=` | 不一致 | `query!=onsen` |
| `~~` | 含む | `page~~/blog/` |
| `!~` | 含まない | `query!~ブランド名` |
| `~*` | 正規表現に一致（RE2） | `page~*^https://example\.jp/(a\|b)` |
| `!*` | 正規表現を除外 | `page!*/tag/` |

`==` `!=` `~~` `!~` は大文字小文字を無視する。`~*` `!*` は書いたとおりにコンパイルされるので、無視させたいなら `(?i)` を自分で付ける。

**比較**: `--compare previous`（直前の同じ長さの期間）/ `--compare year`（364日前 = 曜日が揃う）。掲載順位の「改善」列は**順位が上がった分**（`+3.0` = 3位上昇）。前期間に表示回数が無い行は CTR と順位の差を `-` にする（新規クエリを「0位からの急落」と誤報しないため）。日付ディメンションとは併用不可。

### URL のインデックス状況（inspect）

```bash
bwt inspect https://example.com/blog/post-1
bwt inspect -o json https://example.com/a https://example.com/b
bwt inspect --children https://example.com/blog/    # ディレクトリ配下を一覧
```

Search Console の URL 検査と違い、Bing には「なぜインデックスされないか」を返す API が無い。返るのは**インデックスにあればその情報**（HTTP ステータス・最終クロール日・発見日・被リンク数・累計クリック/表示回数）だけで、Bing がまだ知らない URL はエラーになる。その場合は `bwt submit` で送る。

クリック・表示回数は**期間を問わない累計**。期間別は `bwt query` を使う。

### URL のインデックス送信（submit）

```bash
bwt submit quota                                # 残り枠を確認
bwt submit https://example.com/new-post
bwt submit --file urls.txt                      # 1行1URL、# はコメント
cat urls.txt | bwt submit -                     # 標準入力
bwt submit --file urls.txt --dry-run            # 件数と枠だけ見る
```

- 500件ずつに分割して送る。重複 URL は落とす。
- 送る前に残り枠を確認し、**枠を超えるなら1件も送らずに止める**（`--force` で枠ぶんだけ送る）。枠は送信ごとに減るので、途中まで送って失敗するのが一番損。
- 途中で失敗した場合は「何件まで送信済みか」を出す。再実行時はそこから先だけを渡すこと。

> Microsoft は現在 [IndexNow](https://www.indexnow.org/) を推奨している。API キー不要で Bing 以外のエンジンにも同時に通知できるため、CI から自動で叩くならそちらのほうが素直なことが多い。このコマンドは、枠を見ながら手動でまとめて送りたい場合向け。

### サイトマップ（sitemaps）

Bing の API はサイトマップを「フィード（feed）」と呼ぶが、扱うものは `sitemap.xml` で同じ。

```bash
bwt sitemaps list
bwt sitemaps submit https://example.com/sitemap.xml
bwt sitemaps get https://example.com/sitemap-index.xml    # インデックスの子を一覧
bwt sitemaps remove https://example.com/old-sitemap.xml --yes
```

### クロール（crawl）

```bash
bwt crawl stats --preset last_28d    # 日別のクロール数・ステータス別内訳
bwt crawl issues                     # 取得に失敗した URL（被リンクの多い順）
```

`crawl issues` の「問題」列は Bing のビットフラグを展開したもので、1つの URL に複数付くことがある。被リンクの多い順に並ぶので、上から直すのが効率がいい。

`crawl stats` の「インデックス」列は累計ではなく**その時点の件数**なので、合計行には期間末の値を出している（`*` 付き）。

### キーワードリサーチ（keywords）

自サイトの実績ではなく、**Bing 全体でその語がどれだけ検索されたか**を返す。`--site` ではなく `--country` / `--language` で市場を指定する（既定は `jp` / `ja-JP`）。

```bash
bwt keywords related 温泉 --preset last_3m -l 50
bwt keywords stats '箱根 温泉'
```

「広義」の列は、その語だけでなく表記ゆれ・類似表現まで含めた表示回数。需要の大きさを見るのに向く。

### 被リンク（links）

```bash
bwt links list                                  # 被リンクを受けている自サイトのページ
bwt links get https://example.com/blog/post-1   # そのページへのリンク元とアンカーテキスト
bwt links list --page 1                         # 次のページ（0始まり）
```

件数はサーバ側で決まっていて指定できない。`--page` で送る。

### サイト（sites）

```bash
bwt sites list
bwt sites add https://example.com/
bwt sites verify https://example.com/    # 設置済みの確認コードを再チェックさせる
bwt sites remove https://example.com/ --yes
```

`add` は「追加」であって「確認」ではない。確認コード（meta タグ / `BingSiteAuth.xml` / DNS TXT）の値は `bwt sites list -o json` の `authentication_code` / `dns_verification_code` にある。

## 出力

`-o table`（既定・日本語幅を考慮して整列）/ `-o json`（機械処理用・メタ情報と合計を含む）/ `-o csv`（表計算用。`--bom` で Excel 対策）。

- table のヘッダは日本語、**CSV のヘッダは機械可読キー**（`query,clicks,impressions,ctr,position,click_position`）で JSON のフィールド名と一致する。
- table 出力ではセルを60文字で切り詰める（`--max-width 0` で無効化）。CSV / JSON は常に全文。
- CTR は `7.62%`、順位は `8.4` と整形して出す。生の数値で計算したいときは `-o json`。
- JSON には、表に出ないメタ情報（`buckets` = 期間に入ったバケット日の一覧、行ごとの `buckets` 件数、`click_position`）も入る。

## 終了コード

| コード | 意味 |
|---|---|
| 0 | 成功 |
| 1 | API・実行時エラー |
| 2 | 引数・フラグの誤り |
| 3 | API キーが見つからない |

## 環境変数

| 変数 | 用途 |
|---|---|
| `BWT_API_KEY` / `BING_WEBMASTER_API_KEY` | API キー |
| `BWT_SITE` | 既定の対象サイト |
| `BWT_CONFIG_DIR` | 設定の保存先（既定 `~/.config/bwt`） |
| `BWT_ENDPOINT` | API ホストの上書き（テスト・デバッグ用） |

## スロットリング

Bing はスロットリングを HTTP 429 ではなく **HTTP 4xx + `ErrorCode: 4/5`** で返してくる。`bwt` は既定でリクエスト間隔を 200ms 空け（約5req/秒）、スロットリングとサーバエラーは 1s / 2s / 4s のバックオフで3回まで自動リトライする。

それでも詰まるときは `--throttle 500ms` のように広げる。ローカルのスタブに向けるときは `--throttle 0`。

## 開発

```bash
make test    # ユニット + スタブサーバに対する E2E
make lint    # gofmt + go vet
```

`BWT_ENDPOINT` でスタブサーバに向けられるため、テストは実 API を叩かない。

```
cmd/bwt/          エントリポイント
internal/cli/     コマンド定義と表示（cobra）
internal/bwt/     API クライアント・日付・集計・フィルタ
internal/config/  API キーと既定サイトの解決
internal/output/  table / json / csv のレンダリング
```
