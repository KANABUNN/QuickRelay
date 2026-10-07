# 現行HTTP API

APIのURL・認証・要求/応答形は [`contracts/openapi.json`](../contracts/openapi.json) に整理しています。Report/Eventの任意項目 `hypocenter` に、仮定値や低精度の注記を伴う数値を保持します。既存のURL・認証・同期cursorは維持します。

iOSは `https://対象ドメイン/api/v1` を接続先にします。端末別Bearer tokenはペアリングで取得し、APNs device tokenと区別します。Agent ingest・heartbeat・Web Push購読APIはありません。

報・イベント・ページは[共通JSON Schema](../contracts/api.schema.json)、通常通知は[APNs Schema](../contracts/push-payload.schema.json)を参照してください。`apns-send` の接続テスト通知は通常の報通知と別形式です。

同期cursorと報数の違いは[イベント仕様](event-semantics.md)、具体的なコマンドとendpoint表は[サーバーREADME](../server/README.md)を参照してください。
