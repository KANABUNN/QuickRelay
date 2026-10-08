# Quick Relay

DMDATAの地震情報をGoサーバーで受信し、APNs経由で本人のiOS端末へ通知する個人用リレーです。

    DMDATA → Go / Ubuntu VPS → SQLite → APNs → iOS
                 ↕ Caddy HTTPS / 認証付き履歴API

この公開リポジトリはソースコードの保管・検証用です。一般向けの通知サービスやTestFlight参加受付は提供しません。運用環境、鍵、受信履歴、端末データ、個人の作業記録は含めません。

## 内容

- server/: Goサーバー、DMDATA受信、通知文、APNs、SQLite
- ios/: SwiftUIアプリ、Xcodeプロジェクト、XCTest
- deploy/: HTTPS、systemd、監視・バックアップ
- contracts/: API仕様と合成テストデータ
- docs/: 構成、通知仕様、配備・運用手順
- scripts/: ローカルとCIの検証

[構成](docs/architecture.md) / [API](docs/api.md) / [通知仕様](docs/event-semantics.md) / [対象電文](docs/initial-release-scope.md)

受信状態の表示、地域・震度・種別の通知設定、自分の端末への通知テスト、EEW予報と津波警報・注意報のLive Activityに対応します。[通知とLive Activity](docs/notification-usability.md)を参照してください。

## 開発・検証

Go 1.26以上とPython 3で、POSIX環境は sh scripts/verify.sh、PowerShellは ./scripts/verify.ps1 を実行します。iOSのビルドとテストはMac/Xcode、またはこのリポジトリのGitHub Actionsで実行します。

CIは合成データとループバックの試験サーバーを使い、本番への通知は送りません。Windows・Ubuntu検証、iOS Simulator、Swift/Goの結合試験、署名前Release archiveを確認します。[シミュレーター受入](docs/ios-without-device-acceptance.md)

## 配備・内部配布

[Ubuntuへの配備](docs/deployment.md) / [運用と復旧](docs/operations.md) / [iOS](ios/README.md) / [本人用TestFlight](docs/testflight-distribution.md)

表示名はQuick Relay、Bundle ID/APNs Topicはjp.kb-dev.quickrelay、サービス名はquick-relayです。互換性のため内部ターゲット・CLI名はQuakeRelay/quakerelayを維持しています。文書の接続先は例示であり、実際のURLは本人の非公開設定を使用します。

## 制限とソースの扱い

地震・津波関連、EEW予報・警報の契約対象20種を受信します。[受信対象と表示](docs/earthquake-tsunami-products.md)を参照してください。切断区間の自動補完、OAuth自動更新、履歴自動削除は未実装です。APNsの受理はiPhoneへの到達を保証しません。

公開コードにオープンソースライセンスは付与していません。依存ライブラリのライセンスは各提供元のものが適用されます。[セキュリティ](SECURITY.md)
