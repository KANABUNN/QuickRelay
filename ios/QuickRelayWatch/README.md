# Quick Relay for Apple Watch

watchOS 10以降のiPhoneコンパニオンアプリです。Bundle IDは `jp.kb-dev.quickrelay.watchkitapp`、対応するiPhoneアプリは `jp.kb-dev.quickrelay` です。

地震・津波を選ぶと、継続中の情報がある場合の「発表中」と「履歴」を表示します。同じ情報を履歴でも「継続中」と表示します。詳細では要約・発生/最新発表時刻・状態を確認できます。EEWの対象時間が過ぎると、同期なしでも15秒間隔の表示更新で継続中から外れます。津波はiPhoneと同じ受信電文の判定を使用します。

WatchConnectivityでiPhoneから画面用スナップショットを受け取り、再起動後も表示できるよう保存します。iPhoneのサーバー接続解除を受信すると以前の情報を表示しません。認証情報はWatchへ転送せず、Watchからサーバーに直接接続しません。「iPhoneと同期」は接続中のiPhoneへ最新情報を要求します。オフライン時は保存済み情報と接続案内を表示します。

「iPhoneのサーバー同期」と「Watch用情報の作成」は別々に表示します。サーバー同期が未確認、または5分以上前の場合は最新状態が未確認であることを表示します。iPhoneが受信できていない解除や更新はWatchにも届きません。Watchの履歴は容量を制限した最近の要約で、継続中の情報を優先し、全履歴・地域別の詳細はiPhoneで確認します。

Macでは `ios/QuakeRelay.xcodeproj` の `QuickRelayWatch` schemeからWatch Simulatorへビルドできます。

```sh
cd ios
xcodebuild -project QuakeRelay.xcodeproj -scheme QuickRelayWatch \
  -destination 'generic/platform=watchOS Simulator' CODE_SIGNING_ALLOWED=NO build
```

iPhoneアプリには `Watch/QuickRelayWatch.app` として同梱します。配布時はWatchのApp IDと専用プロファイルをApple Developer側に用意し、既存のiPhone/Live Activityと同じTeam・バージョン・ビルド番号で署名します。署名プロファイル変数は `QUICK_RELAY_WATCH_PROFILE` です。Watch側にPush NotificationsやTime Sensitiveの権限は要求しません。iPhone通知のミラーリングはOSとユーザーの設定に従います。

実機受入では、iPhone/Watch同時インストール、初回同期、発表→解除、履歴内の継続中、EEW時間終了、圏外からの復帰、iPhoneの接続先変更・接続解除、アプリ再起動後の保存済み表示を確認してください。Windows上の静的確認はWatch Simulator実行・署名・実機同期の成功を意味しません。
