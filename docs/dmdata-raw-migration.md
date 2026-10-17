# DMDATA 原電文受信への移行

## 実装前調査と方針（2026-10-17）

基点は main の `2b88128aab6c59f99934296d618868ec571b8e4b`、作業ブランチは
`codex/dmdata-raw-ingestion`。本番VPSへのデプロイ・外部設定変更は実施していない。

実装前の経路は `dmdata.Client` → `Normalize` → `model.Report` → `store.Ingest`
（reports / streams / events / deliveries / live_activity_jobs）→ `relay.Worker` → APNs。
Socket Start は `formats=[json,a/n,binary]`、区分は設定された
`eew.forecast,eew.warning,telegram.earthquake`、`test=no`、typesフィルターなし。
APIキー/OAuth、dmdata.v2、ping/pong、新ticketでの再接続、Socket Closeは実装済みだった。

Base64、gzip、単一ファイルzipの展開とサイズ制限は存在したが、XML本文解析はなかった。
主経路はDMDATA変換JSON。WEPA60（英数字）とIXAC41（BUFR）は履歴のみ。
reports.rawには解析成功時のWebSocketフレームが入り、失敗・未知形式は保存されなかった。
APNsは正規化済みReportを使い、購読条件・TTL・重複抑止を送信前にも判定していた。

この調査と公式仕様の照合結果をチャットで報告した後、rawを既定にしてJSONへ戻せる設定、
解析前の保存、既存内部構造へのXML正規化を実装した。APNs側は既存実装を使う方針とした。

## 照合した公式仕様

- [DMDATA Socket Start v2](https://dmdata.jp/docs/reference/api/v2/socket.start/):
  formatMode=raw/json、formatsとの排他、非XMLの試験はtest=noでも配信される。
- [DMDATA WebSocket v2](https://dmdata.jp/docs/reference/api/v2/websocket/):
  外側はJSON。format=xml/json/a/n/binary、compression=gzip/zip/null、
  encoding=base64/utf-8/null。圧縮時はbase64。ticket使い捨て、ping/pong必須。
- [気象庁XML技術資料](https://xml.kishou.go.jp/tec_material.html):
  2024-10-31 XSD、2026-10-09解説資料、2026-09-17サンプルセットを確認。
  地震火山関連解説 II.21（PDF通算100～122ページ）の取消、ISO6709座標、
  精度rank、最終報のNextAdvisory（規定の固定文言を検証）を使用した。
- [DMDATA EEW JSON](https://dmdata.jp/docs/reference/conversion/json/schema/eew-information/)と
  [津波JSON](https://dmdata.jp/docs/reference/conversion/json/schema/tsunami-information/):
  既存内部構造への対応付けを確認。警報対象のHeadlineと震度予測・観測を区別した。

JMAのReport/Control、informationBasis1のHead、seismology1のBody、
elementBasis1のCoordinate/Magnitude/TsunamiHeightを名前空間付きで解釈する。
XMLの情報名・バージョンは確認した組合せだけを受け入れる。未知バージョンや
未知の津波種別を推測せず、原文を残して通知しない。XMLスキーマ全体を検証する
汎用XSDバリデーターではなく、対象情報を正規化するパーサーである。

## 実装したデータフロー

```text
Socket Start: formatMode=raw（jsonを設定すれば旧方式）
  → WebSocket受信 / ping-pong / 上限付き受信キュー
  → ingest.Processor
      → received_telegramsへ受信フレームをそのままcommit
      → Base64復号 → gzip/zip展開 → XMLまたは変換JSON解析
      → 共通のReport正規化
      → 既存の履歴・最新状態・通知キューを一つのtransactionでcommit
      → 受信記録のstateを更新
  → 既存relay.Worker → 既存APNs実装
```

DMDATA_FORMAT_MODEの既定はraw。rawではSocket StartへformatMode=rawを送り、formatsは送らない。
json設定では移行前と同じformats=[json,a/n,binary]を送り、formatModeは省略する。
これによりJSONへのロールバック時も非XMLの2種を明示的に購読する。両指定の併用はしない。
区分・types省略・test=no・認証・再接続方針は継続する。

受信記録はフレームのSHA-256を主キーに、元バイト列、最初/最後の受信処理時刻、
受信回数、`pending/normalized/ignored/rejected`、Report IDを保存する。
gzip/Base64状態の本文も受信どおり保存するので、展開後のXML原文を再現できる。
解析に成功したReportのraw保存も継続する。元XMLと内部変換用JSONを取り違えないよう、
SourceDocumentの形式・ハッシュ・サイズには復元したXMLを使う。

保存失敗は処理を停止させる。受信記録保存前にはReport・通知キューへ進まない。
Report保存失敗ではpendingが残る。受信記録の最終state更新だけが失敗した場合は、
直前のReport/通知キューcommitは有効であり、送信済み通知の取消は行わない。
再解析は履歴用replayを使えば通知を予約しない。自動pending再処理は追加していない。

フレーム2MiB、展開後4MiB、XML深さ64、要素数100,000の上限がある。
DTD、外部エンティティ、複数ルート、単一項目の重複を拒否する。
受信サイズ超過、キューあふれ、終了直前などで処理関数まで渡らないデータの保存や、
切断区間の自動backfillを保証するものではない。

## 既存機能への影響

| 対象 | 維持した内容 |
|---|---|
| EEW VXSE44/45、VXSE43 | 予報/警報、報数、警報対象、取消、訂正、最終報。VXSE44は従来どおり履歴のみ |
| 震源・震度 | メートルの深さをkmへ正規化。NaN・欠損を0にしない。仮定震源・1点観測の注記を保持 |
| EEW警報の公開項目 | M・深さを公開しない。EEW原文取得APIの拒否も維持 |
| 地震情報・長周期 | VXSE51/52/53/61/62、震度・長周期階級・観測地点・付記を既存構造へ渡す |
| 津波 | VTSE41/51/52、地域区分・到達状況・高さ・満潮・観測・沿岸推定。取消と解除を区別 |
| 高さの限定表現 | 巨大、高い、以上、超、未満、上昇中、欠測を保持。超/未満は原表現を保持 |
| その他 | 地震活動・回数、南海トラフ、後発地震注意、お知らせ、WEPA60、IXAC41 |
| APNs/iOS | 送信・ペイロード・端末設定・TTL・重複抑止・Live Activity・API契約の既存実装を使用 |

正規化後の公開型は変更していない。XMLアダプターは既存の詳細表示用構造へ
ローカルでJSONを組み立てるが、DMDATAのJSON変換には依存しない。
警報の地域抽出に渡す電文種別が未設定だった呼び出し順も修正し、
VXSE43ではHeadlineの警報対象を用いる既存分岐が正しく動くようにした。

非EEW原文の認証付きダウンロードにXML拡張子とMIMEを追加した。
未対応の詳細項目も元XMLには保持するが、すべてのXML要素の画面表示を新設したものではない。

## テストと実行結果

移行前に `go test ./...` が全パッケージで通過した。

- 配布バイト列を維持した公式XML21件をリポジトリに収録し、SHA-256も確認。
- 追加取得した公式セットの対象183件（18種）も解析成功。
  訓練/試験の原文除外を別に確認し、解析範囲の検証ではStatusだけを通常に置換している。
- gzip/Base64、単一ファイルzip、非圧縮、切断、欠損、未知形式・版・コード、
  空フレーム・不正XML・DTD・重複項目・展開サイズ上限、予報・警報・取消・訂正・最終報を検証。
- 受信記録の保存・重複回数・再オープン・履歴再解析、保存失敗、JSON切替時の同一ID重複を検証。
- 原文取得の認証・XMLのバイト一致・EEW原文非公開を検証。
- 原電文→受信記録→正規化→通知キュー→既存APNsペイロードをfake送信先で検証。
- XMLファズテストは10秒設定、1,037試行でpanicなし。
- 最終のリポジトリ検証、全Goテスト、go vet、依存検証、Windows実プロセス試験、Linux amd64/arm64向けビルドが通過。
- 秘密情報検査は228ファイルを確認し、検出0件。新規ファイルを含む差分検査も通過。macOS/iOSの実行結果は含まない。

通常の再実行（serverディレクトリ）:

```sh
go test ./...
go vet ./...
go test ./internal/dmdata -run '^$' -fuzz '^FuzzNormalizeXML$' -fuzztime=10s -parallel=2
go test ./internal/dmdata -run '^$' -bench '^BenchmarkNormalizeFormats$' -benchmem -count=5 -benchtime=500ms
```

公式セット全体の再検証は、技術資料ページのサンプルZIPを作業用フォルダーへ展開し、
`DMDATA_JMA_SAMPLES_DIR`をその絶対パスに設定して
`go test ./internal/dmdata -run '^TestPublishedXMLCorpus$' -v` を実行する。
未設定時もリポジトリ内21件の検証は通常テストで実行する。

## 性能測定

Windows amd64、Go 1.27.1、Intel Core i7-14700F。
内容を一致させたEEW初報のgzip/Base64フレームを使い、各方式5回、各500msで測定。
比較JSONは公式XMLの使用項目に合わせた参照fixtureで、実際のDMDATA受信JSONではない。
入力生成・圧縮時間は計測に含めず、復号・展開・解析・正規化を測る。

| 方式 | 時間中央値 | メモリ中央値 | 割り当て中央値 |
|---|---:|---:|---:|
| 変換JSON | 56.808 µs/op | 54,922 B/op | 187 |
| raw XML | 127.652 µs/op | 98,281 B/op | 987 |

この入力ではXML側のローカル処理が約2.25倍であり、処理時間の短縮は確認していない。
DMDATA側変換・ネットワーク・受信キュー待ち・SQLite・実APNsの時間はこの比較に含まれない。
大きな津波/観測電文や本番マシンに一般化しない。

通常のcommitログには `format_mode`、`archive_us`、`normalize_us`、`commit_us`を出す。
archiveは受信記録commit、normalizeは復号からReportまで、
commitは既存履歴/outbox保存と受信state更新の所要時間。どちらのモードでも同じ測り方をする。
ホストのタイマー分解能に依存し、短い区間は0µsになる場合がある。
DB書き込みが増えるため、実環境のI/Oと電文サイズ分布による影響は別途測定が必要。

## 主な変更ファイル

| ファイル | 内容 |
|---|---|
| server/internal/dmdata/client.go | raw既定、JSON切替、壊れたフレームも処理側へ渡して保存 |
| server/internal/dmdata/normalize.go | XML/JSONの共通Report正規化、圧縮と符号化の整合確認 |
| server/internal/dmdata/xml.go | JMA XMLの名前空間・版・内容検証と内部構造への対応付け |
| server/internal/ingest/processor.go | 保存→解析→履歴/outbox、履歴専用処理、所要時間 |
| server/internal/store/received.go / migrations/006_received_telegrams.sql | 解析前の受信記録（追加テーブルのみ） |
| server/cmd/quakerelay/main.go | live/replayから共通処理、保存失敗時停止、時間ログ |
| server/internal/config/config.go / server/.env.example | DMDATA_FORMAT_MODE |
| server/internal/api/history.go | XML原文の拡張子/MIME |
| server/scripts/smoke.py | rawのCLI取込、認証付き原文取得、失敗・未知形式の保存、通知なしの実プロセス検証 |
| dmdata・ingest・relay・api・config・storeのテスト | 正常/異常/互換/保存/通知の検証、比較ベンチマーク |
| server/internal/dmdata/testdata / .gitattributes | 公式原文と照合値、比較JSON、改行変換の抑止 |
| server/README.md、docs/architecture.md、docs/deployment.md、docs/earthquake-tsunami-products.md | 新経路・設定と関連手順 |

## ロールバックと受入範囲

この作業では本番を変更していないので、本番でのロールバック作業も行っていない。

設定だけ戻す場合は、実際に使用している設定の `DMDATA_FORMAT_MODE=json` を指定し再起動する。
環境変数はenvファイルより優先されるため、上書き元も確認する。
次のSocket Startから移行前と同じformats=[json,a/n,binary]による変換JSON方式になる。履歴・端末登録・通知設定はそのまま使う。
受信記録・時間計測はこのバイナリのまま継続する。これはXML解析を迂回する手順である。

バイナリも戻す場合は、停止後に現DBをバックアップ・検証してから、
事前に保存した基点commitのバイナリと元設定へ戻す。既存テーブル・列・Report/API型は
変更していない。006は追加テーブル/インデックスのみで、旧版は追加テーブルを使わない。
ロールバックのためにテーブルやschema_migrations行を削除しない。
古いDBスナップショットへの置換はその後の履歴・端末変更を失うため、通常手順にしない。
旧版ではraw XMLを再解析できないので、解析未完の受信記録は保持して対応版で履歴再解析する。

ローカルでは、このブランチの変更を保存した上で `git switch main` で基点へ戻せる。
`git reset --hard` や一括削除は使わない。

受信記録の自動削除は追加していない。DBとバックアップ容量は増えるため、
将来配備する際は既存の保管運用に含める。秘密情報・raw本文を通常ログへ出していない。
実DMDATA接続、実APNs/iPhone到達、VPS I/O、iOS/macOSでの実行は今回未検証。

## 全変更ファイル（48件）

公式サンプルXML21件を含む。APNs本体・iOSのソース変更はない。

- [.gitattributes](../.gitattributes)
- [docs/architecture.md](../docs/architecture.md)
- [docs/deployment.md](../docs/deployment.md)
- [docs/dmdata-raw-migration.md](../docs/dmdata-raw-migration.md)
- [docs/earthquake-tsunami-products.md](../docs/earthquake-tsunami-products.md)
- [server/.env.example](../server/.env.example)
- [server/README.md](../server/README.md)
- [server/cmd/quakerelay/main.go](../server/cmd/quakerelay/main.go)
- [server/internal/api/history.go](../server/internal/api/history.go)
- [server/internal/api/raw_source_test.go](../server/internal/api/raw_source_test.go)
- [server/internal/config/config.go](../server/internal/config/config.go)
- [server/internal/config/config_test.go](../server/internal/config/config_test.go)
- [server/internal/dmdata/client.go](../server/internal/dmdata/client.go)
- [server/internal/dmdata/client_test.go](../server/internal/dmdata/client_test.go)
- [server/internal/dmdata/normalize.go](../server/internal/dmdata/normalize.go)
- [server/internal/dmdata/testdata/eew-reference.json](../server/internal/dmdata/testdata/eew-reference.json)
- [server/internal/dmdata/testdata/jma/32-35_01_01_100806_VXSE51.xml](../server/internal/dmdata/testdata/jma/32-35_01_01_100806_VXSE51.xml)
- [server/internal/dmdata/testdata/jma/32-35_01_02_240613_VXSE52.xml](../server/internal/dmdata/testdata/jma/32-35_01_02_240613_VXSE52.xml)
- [server/internal/dmdata/testdata/jma/32-35_01_03_240613_VXSE53.xml](../server/internal/dmdata/testdata/jma/32-35_01_03_240613_VXSE53.xml)
- [server/internal/dmdata/testdata/jma/32-35_03_01_100514_VXSE60.xml](../server/internal/dmdata/testdata/jma/32-35_03_01_100514_VXSE60.xml)
- [server/internal/dmdata/testdata/jma/32-35_03_02_240613_VXSE61.xml](../server/internal/dmdata/testdata/jma/32-35_03_02_240613_VXSE61.xml)
- [server/internal/dmdata/testdata/jma/32-35_09_01_191111_VXSE56.xml](../server/internal/dmdata/testdata/jma/32-35_09_01_191111_VXSE56.xml)
- [server/internal/dmdata/testdata/jma/32-39_11_03_250206_VTSE51.xml](../server/internal/dmdata/testdata/jma/32-39_11_03_250206_VTSE51.xml)
- [server/internal/dmdata/testdata/jma/32-39_12_05_250206_VTSE52.xml](../server/internal/dmdata/testdata/jma/32-39_12_05_250206_VTSE52.xml)
- [server/internal/dmdata/testdata/jma/36_01_01_240613_VXSE44.xml](../server/internal/dmdata/testdata/jma/36_01_01_240613_VXSE44.xml)
- [server/internal/dmdata/testdata/jma/37_01_01_240613_VXSE43.xml](../server/internal/dmdata/testdata/jma/37_01_01_240613_VXSE43.xml)
- [server/internal/dmdata/testdata/jma/38-39_02_01_250206_VTSE41.xml](../server/internal/dmdata/testdata/jma/38-39_02_01_250206_VTSE41.xml)
- [server/internal/dmdata/testdata/jma/38-39_02_04_250206_VTSE41.xml](../server/internal/dmdata/testdata/jma/38-39_02_04_250206_VTSE41.xml)
- [server/internal/dmdata/testdata/jma/42_01_01_100514_VZSE40.xml](../server/internal/dmdata/testdata/jma/42_01_01_100514_VZSE40.xml)
- [server/internal/dmdata/testdata/jma/74_01_02_200512_VYSE50.xml](../server/internal/dmdata/testdata/jma/74_01_02_200512_VYSE50.xml)
- [server/internal/dmdata/testdata/jma/75_01_01_200512_VYSE51.xml](../server/internal/dmdata/testdata/jma/75_01_01_200512_VYSE51.xml)
- [server/internal/dmdata/testdata/jma/75_01_04_200512_VYSE52.xml](../server/internal/dmdata/testdata/jma/75_01_04_200512_VYSE52.xml)
- [server/internal/dmdata/testdata/jma/77_01_01_240613_VXSE45.xml](../server/internal/dmdata/testdata/jma/77_01_01_240613_VXSE45.xml)
- [server/internal/dmdata/testdata/jma/77_01_02_240613_VXSE45.xml](../server/internal/dmdata/testdata/jma/77_01_02_240613_VXSE45.xml)
- [server/internal/dmdata/testdata/jma/77_01_33_240613_VXSE45.xml](../server/internal/dmdata/testdata/jma/77_01_33_240613_VXSE45.xml)
- [server/internal/dmdata/testdata/jma/78_01_01_240613_VXSE62.xml](../server/internal/dmdata/testdata/jma/78_01_01_240613_VXSE62.xml)
- [server/internal/dmdata/testdata/jma/80_01_01_260917_VYSE60.xml](../server/internal/dmdata/testdata/jma/80_01_01_260917_VYSE60.xml)
- [server/internal/dmdata/testdata/jma/README.md](../server/internal/dmdata/testdata/jma/README.md)
- [server/internal/dmdata/testdata/jma/SHA256SUMS.json](../server/internal/dmdata/testdata/jma/SHA256SUMS.json)
- [server/internal/dmdata/xml.go](../server/internal/dmdata/xml.go)
- [server/internal/dmdata/xml_test.go](../server/internal/dmdata/xml_test.go)
- [server/internal/ingest/processor.go](../server/internal/ingest/processor.go)
- [server/internal/ingest/processor_test.go](../server/internal/ingest/processor_test.go)
- [server/internal/relay/raw_test.go](../server/internal/relay/raw_test.go)
- [server/internal/store/migrations/006_received_telegrams.sql](../server/internal/store/migrations/006_received_telegrams.sql)
- [server/internal/store/migrations_test.go](../server/internal/store/migrations_test.go)
- [server/internal/store/received.go](../server/internal/store/received.go)
- [server/scripts/smoke.py](../server/scripts/smoke.py)
