# 現行HTTP API

APIのURL・認証・要求/応答形は [`contracts/openapi.json`](../contracts/openapi.json) に整理しています。Report/Eventの任意項目 `hypocenter` に、仮定値や低精度の注記を伴う数値を保持します。既存のURL・認証・同期cursorは維持します。

電文の構造化した値は、Reportの任意項目として返します。旧版のクライアントはこれらを無視でき、表示用の `bulletin` と `affected_areas` は従来どおり返します。

| 項目 | 内容 |
|---|---|
| `hypocenter` の追加項目 | 震央地名コード、詳細な震央地名、震央の補足、決定機関、陸域・海域、震源の記述、Mの種別・条件・記述、発現時刻、精度ランク。警報（VXSE43）ではM・深さに関わる項目（震源の記述、Mの種別・条件・記述、深さ・Mの精度ランク）を返しません |
| `eew` | EEWの見出し、予想最大震度・予想最大長周期地震動階級の範囲、最大予想震度の変化と理由、警報の対象地域（地方・府県予報区・細分区域）、地域ごとの予想（範囲、警報/予報、長周期地震動階級、到達予想時刻、状況）、付加文。M・深さは含みません |
| `intensity_observation` | 観測した震度・長周期地震動階級の木構造（都道府県・細分区域・市町村・観測点）。コード、震度5弱以上未入電、震度の修正、絶対速度応答、周期帯別の階級と絶対速度応答を含みます |
| `related_earthquakes` | 津波・関連情報に付く地震の震源（座標・震央地名コード・Mの記述など） |

重複判定の指紋はこれらの項目を除いて計算するため、旧版で保存した電文との重複判定は変わりません。保存済みの電文は再解析しないので、更新前に受信した報にはこれらの項目がありません。

iOSは `https://対象ドメイン/api/v1` を接続先にします。端末別Bearer tokenはペアリングで取得し、APNs device tokenと区別します。Agent ingest・heartbeat・Web Push購読APIはありません。

報・イベント・ページは[共通JSON Schema](../contracts/api.schema.json)、通常通知は[APNs Schema](../contracts/push-payload.schema.json)を参照してください。`apns-send` の接続テスト通知は通常の報通知と別形式です。

同期cursorと報数の違いは[イベント仕様](event-semantics.md)、具体的なコマンドとendpoint表は[サーバーREADME](../server/README.md)を参照してください。

## 観測地点の地図パラメータ

`GET /api/v1/map/stations` は端末認証付きで位置情報を返します。応答と取得制限は [履歴・差分・地図](history-and-maps.md) および OpenAPI を参照してください。
