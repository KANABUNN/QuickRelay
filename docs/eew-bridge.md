# EEW ブリッジの状態管理と WSS 配信

読み上げ用の別プログラム（QuickRelay Voice、以下 QRV）へ配信する緊急地震速報（EEW）の状態を、QuickRelay とは別のサービス（`eew-bridge`）とデータベース（`bridge.db`）で管理し、WSS で配信します。このページでは次の内容を説明します。

- 電文の解析と系列の状態遷移
- 配信ログ
- 受け渡しの取込
- WSS の契約
- QRV との分担と調整する場所
- 運用コマンド
- 複製 DB を使った無音検証

QuickRelay 側の受け渡しは [EEW ブリッジへの受け渡し](eew-handoff.md)、systemd・Caddy・負荷測定・切り戻しは [EEW ブリッジの配備と運用](eew-bridge-deployment.md) を参照してください。

| 場所 | 担当 |
| --- | --- |
| `server/internal/dmdata/eew.go` | `ParseEEW`。VXSE45・VXSE43 の XML 原電文を解析する。上限と XML の制限は既存の `Normalize` と同じ。 |
| `server/internal/eew` | 状態の型と状態遷移（`Apply`）。入出力を持たない。 |
| `server/internal/eewbridge` | `bridge.db`、受け渡しの取込（`Feeder`）、WSS 配信（`Server`）、端末トークン、ヘルス、無音検証。 |
| `server/cmd/eew-bridge` | `serve`、`check`、`token`、`backup`、`rotate-epoch`、`verify` コマンド。 |
| `server/cmd/eew-loadtest` | 隔離した DB で受領から WSS 端末までを測る負荷試験。 |
| `contracts/eew-wss.schema.json` | WSS メッセージの JSON Schema。例は `contracts/examples/eew-*.valid.json`。 |

## 対象の電文

- VXSE45 を予報系列（`forecast`）、VXSE43 を警報系列（`warning`）とする。系列は電文種別で決め、VXSE45 の警報区分は警報系列に使わない。
- XML 原電文（DMDATA の `format=xml`）だけを扱う。JSON 変換の電文は隔離する。
- 訓練・試験、DMDATA の試験配信、VXSE44、その他の電文は無視する（`ignored`）。
- イベントのキーは `(environment, Head/EventID)`。v1 の `environment` は `production` だけ。

## 状態遷移

系列ごとに独立して更新します。片方の系列の更新は、もう片方の状態・改訂番号・出典を変えません。

| 入力 | 結果 |
| --- | --- |
| 通常報（発表）で、報数が適用済みより大きい | 適用する。予報の最終報（`NextAdvisory`）なら `final`。報数の飛びは待たない。 |
| 報数が適用済みより小さい | `stale`。状態は変えない。 |
| 報数が同じで内容も同じ | `duplicate`。 |
| 報数が同じで内容が違う | 隔離（`same_serial_conflict`）。 |
| 取消済みの系列への通常報 | 取消の報数以下なら `stale`、それより大きければ隔離（`after_cancel`）。 |
| 最終報後の予報の通常報 | 報数が大きければ隔離（`after_final`）。 |
| 明示取消 | QuickRelay が確定した取消（受け渡しの `applied.is_cancelled=true`）だけを適用する。報数の大小や最終報の有無は問わない。先着の取消も記録する。 |
| 取消済みの系列への取消 | `duplicate`（`already_canceled`）。 |
| 訂正報、報数のない通常報 | 隔離。初版では置換規則を適用しない。 |
| 同じ電文 ID で本文が違う | 隔離（`telegram_id_conflict`）。 |
| 発表から 1 時間を超えて届いたライブの報 | 状態を変えない（`too_old`）。無音検証では適用する。 |

予報の最終報で、警報やイベントは終わりません。時間の経過や地域の脱落から取消を推定することもありません。

## 警報地域の累積

VXSE43 の `Head/Headline/Information` のうち、`Kind=31` の地域を `(codeType, code)` で管理します。

- `reportRegions` は当該報に載った地域。`addedRegions` は当該報で追加された地域（`LastKind=00`）。
- `activeRegions` はこれまでの累積。地域を減らすことはない。
- 累積の各地域には、追加した報の電文 ID（`sourceRef`）と音声開始期限（`speechUntil`）を付ける。期限は続報で延長しない。
- `regionSources` は `sourceRef` が指す過去の報の出典。
- 次の場合は `coverage=partial` にし、以後 `complete` に戻さない。
  - ブリッジが受けていない報で警報済みの地域（`LastKind=31`）が載っている。その地域の `sourceRef` と `speechUntil` は `null`。
  - 警報の続報との間で、上流の接続世代が変わった。

## 音声開始期限

期限は報の `Head/ReportDateTime` から計算します。

- 予報：10 秒
- 警報：15 秒。警報地域は、その地域を追加した報から 15 秒。
- 明示取消：30 秒
- 予報の最終報：`null`

発表時刻が VPS の受信時刻より 1 分以上先なら、時計の異常として期限を `null` にします。

期限は読み上げを始めてよい上限です。QRV は同じ秒数の上限を持ち、ブリッジの期限と QRV の上限の早いほうまでに始められない報は読みません。

- 期限を縮める変更は、ブリッジだけで効きます。
- 期限を延ばす変更は、QRV を先に変えます。QRV は上の秒数を超える期限を契約違反として扱い、その電文を受け付けません。
- 最終報を読み上げるかどうかは受信側が決めます。QRV は最終報も発表から 10 秒以内なら読み上げ、その期限を自分で計算します。現行の QRV は最終報に期限が入っていると電文を受け付けないため、ブリッジは最終報の期限を `null` のままにします。

## イベントの JSON

配信する状態は `server/internal/eew/types.go` の JSON タグに従います。改訂番号と報数は 10 進文字列、時刻は UTC の RFC 3339 です。

```json
{
  "eventId": "20240417231454", "environment": "production", "revision": "3",
  "forecast": {
    "status": "canceled", "revision": "2",
    "source": {"issuer": "JMA", "provider": "DMDATA", "code": "VXSE45", "telegramId": "…", "serial": "5",
               "infoType": "cancel", "issuedAt": "…", "sentAt": null, "receivedAt": "…", "controlAt": "…", "origin": "live"},
    "data": null, "speechUntil": "…"
  },
  "warning": {
    "status": "active", "revision": "1", "source": {"…": "…"},
    "data": {
      "reportRegions": [{"codeType": "地震情報／細分区域", "code": "622", "name": "愛媛県南予", "level": "region"}],
      "addedRegions": [{"codeType": "地震情報／細分区域", "code": "622", "name": "愛媛県南予", "level": "region"}],
      "activeRegions": [{"codeType": "地震情報／細分区域", "code": "622", "name": "愛媛県南予", "level": "region",
                         "sourceRef": "…", "speechUntil": "…"}],
      "regionSources": [], "coverage": "complete"
    },
    "speechUntil": "…"
  }
}
```

予報の `data` は次の項目を持ちます。

- `originTime`、`magnitude`、`depthKm`
- `hypocenter`：`code`、`name`、緯度経度、`quality`
- `maxIntensity`、`maxLongPeriodClass`
- `regionForecasts`：地域ごとの予想震度、長周期地震動階級、到達情報

震度の `kind` は `exact`、`range`、`lowerBound`、`unknown` のいずれかで、不明を 0 として扱いません。到達情報は `condition`（`predicted`、`arrived`、`noPrediction`、`unknown`）と時刻を分けて持ちます。警報の `data` には規模・深さ・予想震度を含めません。

### 震源の精度

`hypocenter.quality.kind` は、`Body/Earthquake` の値から上の行を優先して決めます。QuickRelay の通知文と同じ判定です。

| `kind` | 条件 | QRV の読み上げ |
| --- | --- | --- |
| `assumed` | `Condition`（仮定震源要素）がある。または `MagnitudeCalculation` の `rank` が 8（P 波／S 波レベル越え、仮定震源要素）。 | 「震源は仮定」 |
| `lowAccuracy` | `Epicenter` の `rank` が 1（IPF 法 1 点）。 | 「1点観測」 |
| `estimated` | 上のどちらでもない。 | なし |

`assumed` のとき、`originTime`・`magnitude`・`depthKm` は仮の値なので `null` にします。

`epicenterRank`・`epicenterRank2`・`depthRank`・`magnitudeRank` は、気象庁のコードを文字列のまま送ります。

`magnitudePoints` は `NumberOfMagnitudeCalculation` のコードで、観測点の数ではありません。

| 値 | 意味 |
| --- | --- |
| 1 | 1 点、P 波／S 波レベル越え、または仮定震源要素 |
| 2〜4 | 観測点の数 |
| 5 | 5 点以上 |

気象庁のコード 0（不明）と未定義のコードは送りません。防災科研のシステムで計算したマグニチュードの報は 0 なので、`magnitudePoints` がありません。観測点の数で読み上げを絞る場合も、項目がないことを観測点 0 と解釈しないでください。

## bridge.db

| テーブル | 内容 |
| --- | --- |
| `events` | イベントごとの配信状態（上の JSON）と、適用済みの報数などの管理情報。 |
| `inputs` | 入力フレームごとの結果（`applied`、`duplicate`、`stale`、`not_applied`、`ignored`、`quarantined`）と理由。重複判定と隔離の記録を兼ねる。 |
| `streams` / `stream_log` | 配信ストリームの `epoch`・最終連番と、状態が変わるたびに追記する配信ログ。v1 のストリームは `eew-production`。 |

1 件の入力の適用、記録、配信ログへの追記は、同じトランザクションで確定します。同じフレームを再処理しても状態は変わりません。保持期間は配信ログが 15 分または 10,000 件、イベントと入力の記録が 24 時間です。

## 受け渡しの取込

`eew-bridge serve` は QuickRelay の受け渡しソケットを long-poll で読み（待機 4 秒）、行ごとに適用します。受け渡し位置は、入力の記録と同じトランザクションで `bridge.db` に保存します。

| 状況 | 動作 |
| --- | --- |
| 起動時・再接続時 | 最初に読んだ時点の最新位置までの行を `origin=recovered`、それより後を `origin=live` とする。 |
| QuickRelay の DMDATA 接続世代が変わった | 進行中の警報の累積を `partial` にし、`cause=reconcile` で配信する。 |
| 読む前に行が削除された（`gap`） | 同じく `partial` にし、配信ログの `epoch` を新しくする。端末はスナップショットで状態を置き換える。 |
| QuickRelay の DB が復元された（409） | 位置を先頭に戻して残っている行を読み直し、`epoch` を新しくする。処理済みのフレームは重複として扱う。 |
| ソケットに接続できない | 0.5〜5 秒の間隔で再試行する。その間 `capture` は `degraded` になる。 |

## WSS の契約

`wss://<ホスト>/v1/eew`。Caddy が TLS を終端し、`127.0.0.1:9082` に転送します。メッセージの形は [contracts/eew-wss.schema.json](../contracts/eew-wss.schema.json) が正です。

1. 接続時に `Authorization: Bearer <端末トークン>` を送る。トークンが無い・違う・失効している場合は 401。
2. 接続後 5 秒以内に `subscribe` を送る。`resume` には前回保存したカーソルを入れ、初回は `null`。
3. サーバーは `sync_begin` を送る。カーソルが今の `epoch` の保持範囲内なら `mode=resume`、それ以外は `mode=snapshot`。
4. resume では、カーソルの次から `asOfSeq` までの `eew.state` を `delivery=replay` で送る。snapshot では、直近 10 分に更新されたイベントを `snapshot` の各パートで送る（状態が空でも 1 パート送る）。
5. サーバーは `sync_complete` を送る。`cursor.seq` は `asOfSeq` と同じ。端末はここで状態とカーソルを一緒に確定する。
6. その後は、新しい確定ごとに `eew.state` を `delivery=live` で送る。`health` は 5 秒ごとと、状態が変わったときに送る。

`health` の `upstream` は QuickRelay の DMDATA 受信、`capture` はブリッジの受け渡しの取込を表します。最後に確認してから 10 秒を過ぎた情報は `degraded` とします。

上限は、1 フレーム 256 KiB、同期の合計 4 MiB、同期の所要時間 10 秒です。resume が 4 MiB を超える場合は snapshot に切り替えます。接続数は全体で 32、1 トークンあたり 4 です。認証の失敗が 1 分間に 20 回に達すると、それ以降の接続を 429 で断ります。

| クローズコード | 意味と端末の対応 |
| --- | --- |
| 1000 | 正常終了。 |
| 1001 | サービスの停止。再接続する。 |
| 4400 | `subscribe` がない、形が違う、または対応していない `schemaVersion`。 |
| 4403 | トークンが許可していない `environment`。設定を直す。 |
| 4408 | `subscribe` または同期が時間内に終わらなかった。再接続する。 |
| 4409 | `epoch` が変わった、またはログの保持範囲から外れた。再接続して同期し直す。 |
| 4410 | トークンが失効した。新しいトークンが必要。 |
| 4413 | フレームまたは同期が上限を超えた。 |
| 4429 | 送信が詰まった（1 フレームの書き込みが 5 秒を超えた）。再接続する。 |

## QRV との分担

ブリッジは気象庁の電文を、意味を変えずに状態として配信します。読み上げの文、続報で伝える項目、地名の読み方、対象地域、最低震度は QRV が決めます。

QRV（main の `0a47fb1` 時点）は、`delivery=live`・`cause=telegram` で届いた `origin=live` の報だけを読み上げ、次の項目を使います。

| 項目 | QRV での使い方 |
| --- | --- |
| `source.serial`、予報の `status=final` | 「第N報」「最終報」 |
| `hypocenter.name`、`quality.kind` | 震源の名前、「震源は仮定」「1点観測」 |
| `maxIntensity`、`magnitude`、`depthKm`、`maxLongPeriodClass` | 値と、前に読み上げた値からの「上昇」「低下」「変更」 |
| `regionForecasts` の `codeType`・`code`・`name`・`maxIntensity` | 地域を絞ったときの地域の予想震度と、最低震度の判定 |
| 警報の `addedRegions`・`activeRegions`（`level`・`sourceRef`・`speechUntil`）、`regionSources` | 警報地域。府県予報区（`level=prefecture`）の名前を優先して読む |
| 各系列の `speechUntil`、`source.issuedAt` | 読み上げを始めてよい期限 |

`arrival`、`isWarning`、`kindCode`、`prefectureCode`、緯度経度、精度のコード、`magnitudePoints`、`coverage` は、配信しても QRV の読み上げには使いません。

### 調整する場所

| 変えたいこと | 場所 | 注意 |
| --- | --- | --- |
| 読み上げの文、続報で伝える項目、対象地域、最低震度 | QRV | ブリッジの変更は不要。 |
| 地名の読み方 | QRV の `readings.json` | ブリッジは気象庁の名称をそのまま送る。 |
| 音声開始期限 | `server/internal/eew/types.go` の `ForecastSpeechLimit` など | 延ばすときは QRV が先。[音声開始期限](#音声開始期限)を参照。 |
| 震源の精度の区分 | `server/internal/dmdata/eew.go` の `forecastData` | QuickRelay の通知文と同じ判定にそろえる。 |
| 配信する項目 | `server/internal/eew/types.go`、`contracts/eew-wss.schema.json`、`contracts/examples/eew-*.valid.json` | v1 で増やせるのは任意の項目だけ。QRV は知らない項目を無視する。項目を必須にする、型や意味を変える場合は `schemaVersion` を上げる。 |

ブリッジを変えたら、`server` で次を実行します。このテストは、サーバーの出力が JSON Schema に合うことも確かめます。

```sh
go test ./internal/dmdata ./internal/eew ./internal/eewbridge
```

状態遷移を変えた場合は、[複製 DB による無音検証](#複製-db-による無音検証)で過去の報の判定も比べます。`contracts/examples/eew-*.valid.json` を変えた場合は、QRV の `QuickRelay.Voice.Checks/ContractExamples` に同じファイルを入れ、QRV の確認を実行します。

## 運用

設定は環境変数、または `-env-file` で指定するファイル（`KEY=value` 形式）で渡します。

| キー | 既定値 |
| --- | --- |
| `EEW_BRIDGE_DATABASE` | `/var/lib/quick-relay-bridge/bridge.db` |
| `EEW_BRIDGE_LISTEN` | `127.0.0.1:9082`（ループバックのみ） |
| `EEW_HANDOFF_SOCKET` | `/run/quick-relay-handoff/handoff.sock` |

```sh
eew-bridge serve
eew-bridge check                           # 設定を検査する（待受はループバックだけ）
eew-bridge token issue -name "居間の PC"   # トークンは一度だけ表示する。DB には SHA-256 だけを保存する
eew-bridge token list
eew-bridge token revoke t_xxxxxxxxxxxx     # 接続中のセッションは 5 秒以内に 4410 で閉じる
eew-bridge backup /path/to/new.db          # 整合した snapshot。まだ DB がなければ何もしない
eew-bridge rotate-epoch                    # bridge.db をバックアップから戻した後、起動前に実行する
```

`GET /healthz`（ループバック）は `upstream`、`capture`、セッション数、`epoch`、最終連番を返します。両方が `healthy` のときだけ 200 です。

## 複製 DB による無音検証

QuickRelay のバックアップから作った複製 DB を使い、ブリッジの状態を QuickRelay の判定と比べます。音声も配信も出しません。

```sh
cd server
go build -o .local/eew-bridge ./cmd/eew-bridge
.local/eew-bridge verify -source-db /path/to/relay-copy.db
```

複製 DB は読み取り専用で開きます。稼働中の DB ファイルを直接指定せず、`quakerelay backup` や運用バックアップで作った複製を使ってください。`-state-db` を付けると、作ったブリッジ DB を残せます（既存のファイルは使いません）。`-json` で JSON を出力します。

検証では、`reports` の VXSE43・VXSE45 を確定順に `historical` として適用します。QuickRelay が各報を系列に確定したかどうかは、現在の `Report.NewerThan` で計算し直します。

| 出力 | 意味 |
| --- | --- |
| 結果の内訳 | `applied`、`stale:older_serial` など、結果と理由ごとの件数。 |
| Series mismatches | 系列の最終状態（状態と報数）がブリッジと QuickRelay で違うもの。 |
| Applied disagreements | QuickRelay が確定したのにブリッジが適用しなかった報、またはその逆。 |
| Stored relay streams decided differently today | 保存済みの `streams` が、現在の判定と違うもの。#15 より前に旧報扱いされた取消など。参考情報で、終了コードには影響しない。 |
| Quarantined | 隔離した報と理由。 |

終了コードは、一致すれば 0、mismatch か disagreement があれば 3、それ以外のエラーは 1 です。
