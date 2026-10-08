# Quick Relay iOS — VPS / APNs版

iOS 17以上のSwiftUIアプリです。既存のAPNs登録・Keychain・SwiftData履歴・deep linkを再利用し、`server/` のGo APIに接続します。Windows PCを経由せず、VPSへ直接接続します。

## Macでのビルド

macOS、Xcode（iOS 17 SDK以降）、Apple DeveloperのPush対応Team/App IDを用意してください。正式名とApp ID Descriptionは **Quick Relay**、Bundle IDは **jp.kb-dev.quickrelay** です。

1. 既存 `QuakeRelay.xcodeproj` を開き、Signingで自分のTeamを選択します。Bundle IDは `jp.kb-dev.quickrelay` に設定済みです。VPSの `APNS_BUNDLE_ID` も同じ値にします。
2. 既存プロジェクトをそのまま使用できます。

   ```sh
   cd ios
   open QuakeRelay.xcodeproj
   ```

3. Signing & CapabilitiesでPush Notifications、Time Sensitive Notifications、Remote notificationsがprofileに含まれることを確認します。
4. DebugのAPNS_ENVIRONMENTはdevelopment、Releaseはproductionです。同じbuild settingをentitlementとInfo.plistに使います。配布configurationを追加した場合も両方を揃えてください。
5. 実機へインストールし、通知を許可します。

`project.yml` と既存 `QuakeRelay.xcodeproj` の識別子は一致させています。XcodeGenを使う場合だけ `project.yml` のDEVELOPMENT_TEAMを設定し、`xcodegen generate` で再生成してください。.p8鍵はiOSアプリに同梱しません。

## 接続

Macを所有しない場合の署名・アップロード手順は[内部TestFlight配布](../docs/testflight-distribution.md)を参照してください。

1. VPSで `quakerelay -env-file /etc/quick-relay/quick-relay.env pair` を実行します。
2. アプリへ `https://実ドメイン/api/v1` と8桁コードを入力します。
3. API認証tokenをKeychainへoriginと結び付けて保存します。HTTP redirectは追跡しません。
4. APNsの登録callbackを受けるたびにdevice tokenをAPIへ送ります。tokenはプロセス内だけに保持し、UserDefaults・SwiftData・ログへ保存しません。
5. サーバー登録が通信断で失敗した場合は、フォアグラウンド復帰時に再試行します。

端末設定から通知種別・Time Sensitive・音を保存できます。ペアリング解除は `DELETE /devices/me` が成功した後にローカル認証情報を消し、VPS側の送信対象も無効化します。API認証がすでに失効して401を返す場合はローカル接続を解除します。通信できないときはVPSの `quakerelay revoke INSTALLATION_ID` で無効化できます。

## 受信と履歴

Pushはalert本文を含むため、表示前のAPI取得を必要としません。起動・復帰・受信・タップ・手動更新で `/sync` を呼びます。バックグラウンド実行の許可や実行自体には依存しません。

- 予報・警報・取消: Time Sensitive（端末・ユーザー設定に従う）。
- 通常地震情報: active。
- 同じeventをthreadでまとめ、各報の通知を独立させます。
- 通知タップは `event_id/report_id` を使って履歴の該当報を開きます。
- `source_serial` はDMDATAの報数、`latest_revision` はVPS状態の更新番号です。画面には報数を表示します。
- timelineは発表時刻・server sequence順です。異なる電文の報数を相互比較しません。
- ページ保存とcursor更新は同じSwiftData transactionで行います。
- 接続先APIのURLが変わった後の同期ではローカル履歴とcursorを初期化し、新しいサーバーから取得し直します。
- 震源の数値は、電文に含まれる値と精度の注記を表示します。仮定震源には仮定値である旨を併記します。

独自CAF音は `Scripts/GenerateNotificationSounds.swift` からビルド時に生成します。Critical Alertは使用しません。Live ActivityはiOS 17.2以降で任意に有効化できます。

## テスト

```sh
xcodebuild -list -project QuakeRelay.xcodeproj
# 選択中のXcode SDKに対応するSimulatorを自動選択
sh Scripts/test.sh
```

Keychain origin拘束、API decoding、token更新競合、cursor transaction、旧更新の拒否、独立した地震情報への取消フラグ混入防止、サーバー変更時の再取得、deep linkのテストを含みます。

ビルドとXCTestは、このリポジトリのGitHub Actionsで確認します。シミュレーターの成功と実機・配布署名・実APNsの受入は区別します。

## Mac・iPhoneがない場合

[実機を使わない受入確認](../docs/ios-without-device-acceptance.md)では、GitHubのMac上でSwiftクライアントとGo APIを接続し、履歴保存・解除・本番HTTPSの読み取りを検証できます。APNsの実受理とiPhone到達は別の未検証項目として残します。

## 実機受入

- [ ] Debug / developmentでAPNs token登録とVPS側deviceレコードを確認。
- [ ] APNs単体テストが、前面・背景・終了状態で表示されることを確認。
- [ ] 予報・警報・取消のTime SensitiveとCAF音を確認。
- [ ] 通知拒否、Focus、通知設定変更時の表示を確認。
- [ ] Pushタップで対象event/reportへ移動。
- [ ] token更新後に旧tokenへのエラー応答で新tokenが無効化されない。
- [ ] Pushを欠落させても手動同期で履歴を取得。
- [ ] ペアリング解除後にサーバーから通知されない。
- [ ] Release / TestFlightのproduction APNsでも確認。
- [ ] 実DMDATA → VPS → APNs → iPhoneで時刻・通知遅延を測定。

APNsはbest effortです。DMDATA切断中の履歴の自動補完はVPS側に未実装であり、切断区間の欠落を端末同期だけで回収することはできません。

Bundle IDを変えたため旧雛形IDのアプリとは別アプリです。新IDでインストールしてペアリングし直してください。Keychain serviceも `jp.kb-dev.quickrelay.credentials` に統一しています。

XcodeのSWIFT_VERSIONはコンパイラのリリース番号ではなく言語モードなので、既存の5.9指定を5.0へ修正しています。Swift 5モードでiOS 17以降のSDKを使用します。CIは既存projectを使い、利用可能なiPhone SimulatorでビルドとXCTestを実行します。


地震・津波・関連情報の3タブと通知設定に対応します。通常情報は発表区分、EEWだけ報番号を表示します。[受信対象と表示](../docs/earthquake-tsunami-products.md)を参照してください。

## 受信状態・通知の絞り込み・通知テスト・Live Activity

一覧の受信状態はVPSのDMDATA接続と直近のフレームを使い、地震が来ない時間も接続を確認できます。アプリが確認できなくなった状態は「未確認」または接続エラーとして表示します。

通知設定はEEW予報・警報、津波警報や注意報・観測情報、南海トラフ・その他の関連情報を個別に切り替えられます。都道府県・細分地域・津波予報区名と最低震度の絞り込みはVPSで行います。履歴への受信・保存は継続します。詳細と保守的な判定の例は[通知とLive Activity](../docs/notification-usability.md)を参照してください。

設定の通知テストは操作した端末だけへ、明示したテスト文面を送ります。通常音・警報音を選択でき、結果は「APNs受理」と端末に表示された事実を区別します。

Live Activityは初期状態でオフです。設定を保存するとEEW予報（VXSE45）・津波警報や注意報（VTSE41）の続報をロック画面とDynamic Islandに表示します。開始時のアラートは通常通知と兼用し、続報の表示更新は無音です。津波観測情報や南海トラフ情報は通常通知・履歴で参照できます。WidgetKit拡張には専用の配布プロファイルが必要です。
