# VPS / APNs移行の検証記録

過去の検証記録（今回の再実行結果ではありません）。確認日: 2026-10-05。対象は `server/` と更新した `ios/`、新構成の説明文書です。

## 段階別の結果

| 段階 | 実装 | 確認結果 |
|---|---|---|
| 1. APNs単体 | .p8 / ES256 JWT、50分cache、HTTP/2、単体送信CLI | ローカルTLS HTTP/2サーバーで署名・ヘッダー・payload・失効応答を検証 |
| 2. 端末登録API | 8桁pairing、端末別Bearer認証、SQLite、token更新・失効、通知設定 | 実SQLiteとHTTP、再起動後保持、他端末更新拒否、古い410の扱いを検証 |
| 3. DMDATA | API v2 / dmdata.v2、JSON ping/pong、再接続、正規化、重複・逆順・取消・最終・警報管理 | 模擬WebSocket、gzip/base64、5対象電文、訓練拒否、サイズ上限、切断時の受信済みキュー保持を検証 |
| 4. APNs連携 | 原子的outbox、端末別claim、TTL、retry、supersession、送信結果 | キュー失敗時の全体rollback、lease復旧、429、無効token、古い速報・取消済み報の抑止を検証 |
| 5. Swift iOS | APNs登録、Keychain、token API、通知受信・tap、履歴、サーバー切替、登録解除 | 既存コードを更新しXCTestを追加。plist解析成功。Xcode / XCTest実行は未検証 |
| 6. VPS運用 | Go runtime、systemd、Caddy、health/ready/status/metrics、監視timer、backup、運用手順 | Ubuntu 24.04で実プロセス検証、Caddy設定検証、systemdユニット検証成功。本番VPSへは未配置 |

## 実行した検証

- Windows: 公式Go 1.27.1、`go mod verify`、`go test ./...`、`go vet ./...`、ビルド。
- Ubuntu 24.04.4 LTS (既存WSL): 同じGoテスト・静的検査・Linuxビルド。
- Windows / Ubuntuそれぞれで `scripts/smoke.py` を実行。別プロセスで起動し、実HTTPでpairing・device登録・認証境界・履歴取込・ページ同期・status/metrics・端末失効を確認。
- SQLiteの稼働中スナップショットを別DBとして読み、`PRAGMA integrity_check` と報件数を確認。再起動後の認証・履歴保持も確認。
- LinuxではSIGTERM終了の正常終了コードを確認。Windowsのスモークテストはプロセス終了後の再起動・永続化を確認。
- Linux amd64 / arm64向けの `quakerelay` と `apns-send` を `CGO_ENABLED=0` でクロスビルド。arm64での実行は未検証。
- Caddy 2.11.7公式配布のSHA-512を照合し、`caddy validate --config ../deploy/caddy/Caddyfile --adapter caddyfile` が成功。
- Ubuntuの `systemd-analyze verify` が成功。実配置を行わず、検証用コピーのExecStartパスだけを作業フォルダー内Linuxバイナリへ差し替えた。ユニットは通常の0644権限で検証。
- 今回編集した追跡ファイルの `git diff --check` が成功。新ソースも末尾空白と秘密鍵の混入を確認。既存Windows / PHP / PWAに実質的な内容変更は加えていない。

再現コマンドは `server/scripts/verify.ps1` と `server/scripts/verify.sh`。テスト用データ・一時認証情報・コンパイラは `server/.local/` 以下に隔離しGit対象外にした。実秘密情報は使用していない。

## 未検証・制限

- Appleの実APNs endpoint、実iPhone表示・音・Time Sensitive、TestFlight署名、実DMDATA契約を使った受信・遅延は未検証。
- macOS / Xcodeがないため、iOSプロジェクト再生成、Swiftコンパイル、XCTest、codesign、実機動作は未検証。
- Linux環境にCコンパイラがないため、`go test -race` は明示的skip。通常のGoテストと本番用pure Goビルドは成功。
- 本番ドメインのDNS、TLS証明書発行、systemd常駐、再起動後の外部再接続、外部監視通知先、実VPS上のバックアップ復元は未検証。
- WebSocket切断中の電文をAPIから自動補完する機能は未実装。再接続後に受信は再開するが、切断区間を履歴同期だけで復元できるとは扱わない。
- APNsは端末到達・順序・重複排除を保証しない。受理後にプロセスが停止した場合は再送で重複し得る。
- 対応電文はVXSE45/43/51/52/53。津波・その他のtelegram.earthquake電文、Critical Alerts、Live Activitiesはこの版の対象外。
- 自動データ削除は未実装。DB成長とディスク空き容量を監視する。

## 次に入力する値

[設定例](../server/.env.example)のPAIRING_SECRET、DMDATA_TOKEN、Apple Team ID / Key ID / Bundle ID / .p8パスと、Caddyfileの実ドメインを設定する。APNs実機確認用tokenは単体送信時だけ使用する。`RELAY_MODE=live` に切り替える前に[配置手順](../server/README.md)と[iOS実機受入](../ios/README.md)を実施する。
