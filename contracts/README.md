# Quick Relayの共通契約

旧Agent ingest / HMAC / Talker IPC / Web Push専用契約は保全ブランチへ残し、mainはGo APIとAPNsの形に揃えています。数値表示の拡張ではReport/Eventに任意の `hypocenter` を追加し、旧クライアント向けの項目を維持しています。

| ファイル | 対象 |
|---|---|
| `openapi.json` | OpenAPI 3.1形式の現行HTTP API（URLはorigin基準） |
| `api.schema.json` | `$defs/Report`, `Event`, `SyncPage`, `Device`, `Preferences` |
| `push-payload.schema.json` | APNsの通常報通知。単体接続テストCLIの通知は対象外 |
| `live-activity-payload.schema.json` | ActivityKitの開始・無音更新・終了。既定のCodableキーを共有 |
| `notification-test-payload.schema.json` | 操作した端末だけへの明示的な通知テスト |
| `examples/*.valid.json` | 秘密情報を含まない合成データ（実際の災害電文ではない） |
| `fixtures/text-normalization/` | 旧版から抽出した地名辞書・文字正規化の参考fixture |

JSON Schemaはdraft 2020-12です。`api.schema.json` のrootは定義集なので、具体的なモデルを検証する際は `$defs` の該当定義を選択してください。APNsの4096バイト上限、ID一致、順序比較、警報時の表示制限はSchemaだけでは表現せず、既存のGoテストで確認します。

[`server/internal/model`](../server/internal/model/)と[`relay.Payload`](../server/internal/relay/worker.go)が実装です。Goのfixtureテストで報のJSON往復、Event変換、APNs出力を共通exampleと照合します。HTTP・SQLiteの境界は既存のAPIテストとスモークテストで確認します。

認証token、APNs device token、raw電文、秘密鍵はこれらのfixtureへ記録しません。[イベント仕様](../docs/event-semantics.md)も参照してください。
