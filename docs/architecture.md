# Quick Relayの現行構成

```text
DMDATA API v2 / WebSocket
  → server/internal/dmdata   電文検証・正規化・通知タイトル/本文生成
  → server/internal/model    Report / Event / Device / Preferences
  → server/internal/store    SQLite: reports / streams / events / deliveries
  → server/internal/relay    有効期限・lease・再試行・端末別送信
  → server/internal/apns     HTTP/2 / ES256
  → ios/                    APNs登録・Keychain・SwiftData・通知タップ

iOS ↔ Caddy HTTPS ↔ loopback HTTP API（pairing / devices / sync / events）
```

履歴、最新状態、送信キューは同じDBトランザクションで保存し、APNs送信はcommit後に行います。受信処理と通知送信を分け、APNs障害で保存済みの履歴を失わない構成です。

正式名称と配置は次のとおりです。CLI名とsystemdサービス名は異なります。通信やDBの保存形式は変更していません。

| 項目 | 正規値 |
|---|---|
| 表示名 / DMDATA appName | Quick Relay |
| Bundle ID / APNs Topic | `jp.kb-dev.quickrelay` |
| systemd / 専用ユーザー | `quick-relay` |
| CLI / Go module / Xcode target | `quakerelay` / `quakerelay/server` / `QuakeRelay` |
| バイナリ | `/opt/quick-relay/`（`quakerelay`、`apns-send`） |
| 設定 | `/etc/quick-relay/quick-relay.env` |
| APNs鍵 | `/etc/quick-relay/keys/`（共通鍵の配置例は `AuthKey.p8`） |
| DB | `/var/lib/quick-relay/quickrelay.db` |
| 公開hostname | `relay.example.com` |

受信対象は契約済み3区分の20種です。[受信対象と表示](earthquake-tsunami-products.md)を参照してください。切断区間の自動backfill、OAuth自動更新、履歴自動削除は未実装です。

Windows、EarthQuickly、Talker、PHP、MySQL、PWAはmainの実装・ビルド対象から除外しました。この公開リポジトリには旧版の実装や個別の運用記録を含めません。

- [イベント・通知仕様](event-semantics.md)
- [API契約](../contracts/README.md)
- [VPS運用](../server/README.md)
- [iOS受入](../ios/README.md)
