# Quick Relay Server

Ubuntu 24.04 LTS + Go + SQLite WAL + systemd + Caddy。DMDATAを直接受信してAPNsへ通知し、既存Swiftアプリと互換の認証・履歴APIを提供します。個人利用を想定した単一プロセス構成です。

## 準備とビルド

Go 1.26以降が必要です。今回の検証は公式Go 1.27.1を使用しました。Ubuntu標準の古いGoパッケージでビルドできない場合は[Go公式配布](https://go.dev/dl/)を使ってください。SQLite開発パッケージやCコンパイラは本番ビルドに不要です。

```sh
cd server
go mod download
go test ./...
go vet ./...
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -o bin/quakerelay ./cmd/quakerelay
CGO_ENABLED=0 go build -trimpath -o bin/apns-send ./cmd/apns-send
```

Windows用検証: `scripts/verify.ps1 -Go PATH_TO_GO`。Linux用検証: `sh scripts/verify.sh`。いずれも実プロセス・HTTP・SQLiteを使った `scripts/smoke.py` を実行します。スモークテストは一時DBだけを使い、外部通知を送信しません。

## 1. APNs単体送信

Apple DeveloperでPush Notifications対応App IDとAPNs用.p8鍵を用意します。Bundle IDはiOSの署名対象と一致させます。Debug実機のtokenはdevelopment、TestFlight / 配布版はproductionを使います。

`.env.example` を非公開の設定ファイルへコピーし、次を埋めます。

| 変数 | 用途 |
|---|---|
| APNS_TEAM_ID | Apple Team ID |
| APNS_KEY_ID | .p8のKey ID |
| APNS_BUNDLE_ID | iOSのBundle ID |
| APNS_KEY_PATH | 非公開.p8の絶対パス。旧APNS_KEY_FILEも対応 |
| APNS_ENVIRONMENT | sandbox/development、production、明示的なboth |
| APNS_DEVICE_TOKEN | 単体送信専用。登録済み端末へは -installation-id を推奨 |

```sh
./bin/apns-send -env-file /etc/quick-relay/quick-relay.env
```

地震と区別した接続テスト通知を1件送信します。成功はAppleの受理までで、iPhone表示を示すものではありません。tokenや鍵をソース・スクリーンショット・共有ログへ入れないでください。単体確認後は設定のAPNS_DEVICE_TOKENを空にできます。

`APNS_ENVIRONMENT` はサーバーで有効にする環境も制限します。新しい環境限定鍵は対象環境だけで使用してください。両方を扱う場合は `both` と、`APNS_SANDBOX_KEY_ID/PATH`・`APNS_PRODUCTION_KEY_ID/PATH` を設定します。旧来の両環境共通鍵だけは共通ID/PATHを `both` で使用できます。Topic Specific keyの対象も `jp.kb-dev.quickrelay` にします。

実装はP-256 / ES256 JWTを50分間再利用し、HTTP/2・TLSを必須とします。[Appleのtoken認証](https://developer.apple.com/documentation/usernotifications/establishing-a-token-based-connection-to-apns)と[APNs Provider API](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns)に基づきます。

## 2. APIと端末登録

`PAIRING_SECRET` は `openssl rand -hex 32` 等で生成し、安定して保存します。設定ファイルは単純な `KEY=value` 形式で、シェル展開は行いません。既存プロセス環境変数が優先されます。

```sh
# 設定例のRELAY_MODE=offlineでは外部接続なしでAPIを確認できます。
./bin/quakerelay -env-file /etc/quick-relay/quick-relay.env check
./bin/quakerelay -env-file /etc/quick-relay/quick-relay.env serve
# 別の端末。同じ設定ファイル / DBを指定します。
./bin/quakerelay -env-file /etc/quick-relay/quick-relay.env pair
```

発行した8桁コードをiOSアプリへ入力します。コードは10分、未使用コードは新規発行で置換、失敗5回で失効します。公開pairing APIには全体で毎分10回の制限があります。同一コード・同一installation IDの応答喪失後の再試行は、期限内なら同じ認証tokenを返します。

端末のAPI認証tokenとAPNs device tokenは別物です。API認証tokenはDBにSHA-256のみ、APNs tokenは送信のため非公開DBに保持します。認証済み端末は自分のtokenと通知設定のみを更新できます。古いAPNs応答が更新済みtokenを無効化しないよう、token値・環境・登録時刻を比較します。

## 3. DMDATA接続

- `DMDATA_AUTH_MODE=api_key`: APIキーをBasic認証のユーザー名、空パスワードとして使用。
- `DMDATA_AUTH_MODE=oauth`: Bearer access token。OAuth更新フローは実装していないため、常駐用途はAPIキーを推奨。
- 必要scope: `socket.start`, `socket.close`, `eew.get.forecast`, `eew.get.warning`, `telegram.get.earthquake`。
- `DMDATA_CLASSIFICATIONS` へ契約済み区分をカンマ区切りで指定します。既定は3区分です。scopeだけでは購読できません。
- APIキーは `DMDATA_API_KEY`、旧 `DMDATA_TOKEN` も対応。appNameは `Quick Relay` です。
- 契約済み3区分の20種を処理します。[受信対象と表示](../docs/earthquake-tsunami-products.md)を参照。JSON以外の2種は原文を保存し、試験識別の制約から通知しません。

`POST /v2/socket` に `formatMode=json` と `test=no` を指定し、返されたwss URLへ `dmdata.v2` で接続します。JSON pingへのpong、100秒の無通信監視、指数バックオフ＋jitter、再接続時の新規ticket、終了時の自分のsocketのcloseに対応します。他のアプリのsocketは閉じません。

UTF-8 / base64、非圧縮 / gzip / 単一ファイルzipを扱い、展開後サイズを制限します。訓練・試験報は本文statusでも拒否します。未知schemaや壊れた電文は拒否件数に記録します。

公式仕様: [Socket Start](https://dmdata.jp/docs/reference/api/v2/socket.start/)、[WebSocket](https://dmdata.jp/docs/reference/api/v2/websocket/)、[共通JSONヘッダー](https://dmdata.jp/docs/reference/conversion/json/)、[EEW JSON](https://dmdata.jp/docs/reference/conversion/json/schema/eew-information/)、[地震情報JSON](https://dmdata.jp/docs/reference/conversion/json/schema/earthquake-information/)。

## 4. イベント管理とAPNs

- 同じeventIdの履歴をまとめ、最新判定は `eventId + telegram_type` 単位でserialを比較します。予報と警報の報数を相互比較しません。
- 同じserialの取消・最終報を反映し、取消済みの同じ電文系列を古い報で復活させません。
- 古い報も履歴には保存しますが、新たに通知を予約しません。message IDと正規化内容の二重重複防止があります。
- 種別をまたぐ画面の最新状態は発表時刻と状態で選びます。APIの `latest_revision` はサーバー側の単調な状態更新番号、`source_serial` とreportの `revision` はDMDATAの報数です。
- 履歴、最新状態、端末別送信キューは同じDBトランザクションで保存します。APNs送信はその後です。
- 送信claimの30秒leaseにより、プロセス停止後の処理を再開できます。同一端末への送信は直列、全体では最大4並列です。
- EEWのローカル有効期限は発表から60秒、通常地震情報は10分。再起動後に古い速報を送りません。EEWはAPNs expiration=0でオフライン保存を要求しません。
- 各報は独立したAPNs requestとし、event IDでthreadをまとめます。collapse IDを共用しません。
- 429 / transport失敗は期限内で再試行。5xxはAppleの推奨に従い15分後以降とするため、本版の短い有効期限では期限切れになります。
- 400の無効tokenと410の失効tokenは送信対象から外します。認証鍵の403では端末API認証を失効させません。
- 予報・警報・取消はユーザー設定に応じTime Sensitive、通常情報はactive。Critical Alertは使用しません。
- 警報の通知には震央地名・予想最大震度・対象地域を表示します。マグニチュードと深さは追加の `hypocenter` を含め警報のAPIへ出しません。仮定震源は `hypocenter` に値と注記を保持し、従来の数値項目はnullのままにします。予報のM・深さや仮定値は出所と注記を付けて表示します。[数値表示の仕様](../docs/event-semantics.md)と[DMDATAのEEW取扱条件](https://dmdata.jp/docs/eew/)を参照してください。

APNs受理直後・DB更新直前に停止した場合、再送で通知が重複する可能性があります。APNsの受理やapns-idは端末到達・exactly-onceを保証しません。

受信切断中の電文をDMDATAから自動取得し直す機能は未実装です。再接続で受信は再開しますが、切断区間は履歴にも欠落し得ます。独立した公式警報経路を併用してください。読み取れない電文は自動推測せず、拒否件数とログから調査します。

## 5. iOSとHTTP API

iOSアプリのServer URLは `https://あなたのドメイン/api/v1` です。[iOS手順](../ios/README.md)を参照してください。

| Method / path | 認証 | 内容 |
|---|---|---|
| GET /health, /api/v1/health, /healthz | 不要 | プロセス・DB・設定。Appleへの実疎通確認ではない |
| GET /readyz | 不要 | DB、APNs設定、DMDATA接続・heartbeatの確認 |
| POST /api/v1/pair/complete | 8桁code | installation IDにAPI tokenを発行 |
| POST /api/v1/devices/register | Bearer | iOS互換device token登録 |
| PUT /api/v1/devices/me | Bearer | 同じ登録処理 |
| GET /api/v1/devices/me | Bearer | 自端末・通知設定。tokenは返さない |
| PATCH /api/v1/devices/me/preferences | Bearer | 通知ON/OFF、Time Sensitive、音、event_types |
| DELETE /api/v1/devices/me | Bearer | API認証とPush対象を失効 |
| GET /api/v1/sync?after_sequence=0&limit=200 | Bearer | 報の連番順ページと最新event状態 |
| GET /api/v1/events?limit=50&cursor=N | Bearer | 最新event一覧、次ページcursor |
| GET /api/v1/events/current | Bearer | 最新event一覧の別名（活動中のみを意味しない） |
| GET /api/v1/events/{eventId} | Bearer | 最新状態と全報 |
| GET /api/v1/status | Bearer | DMDATA接続状態、拒否件数、送信状態集計 |
| GET /metrics | Bearer | Prometheus用メトリクス |

登録例（snake_case）:

```json
{"installation_id":"YOUR_INSTALLATION_ID","device_token":"APNS_HEX_TOKEN","environment":"development","app_version":"1.0","os_version":"17.0","device_name":"My iPhone"}
```

報APIの `occurred_at` は電文発表時刻、`origin_time` は地震発生時刻です。Push内の `server_sequence` は永続化した報の連番、`event_id/report_id` はiOS deep link用、`eventId/serial/kind/cancel/final/warning` は正規化した速報情報です。

## 6. Ubuntu / systemd / Caddy

[Ubuntu 24.04をゼロから構築する手順](../docs/deployment.md)を使用してください。`deploy/install.sh` が専用ユーザー・ディレクトリ・秘密設定・unitを配置し、`deploy/update.sh` がbackupと更新を行います。

サービス名は `quick-relay`、配置は `/opt/quick-relay/`、`/etc/quick-relay/`、`/var/lib/quick-relay/` です。既存の `quakerelay` 配備からの手動移行方法も同ガイドに記載しています。
