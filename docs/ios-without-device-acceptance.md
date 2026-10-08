# 実機を使わないiOS受入試験

公開CIは候補コードをMac上のiPhone SimulatorとローカルGoサーバーで検証します。本番URL・秘密鍵・実APNsは使いません。

## 実行

GitHub Actionsの「iOS acceptance without a physical device」はPR、main更新、手動実行で動きます。Macでは次を実行できます。

    python3 scripts/verify-ios-acceptance.py --source-mode candidate

6件の合成電文をofflineサーバーへ投入し、Swiftからペアリング、端末登録、同期、順序、取消、仮定値の保持、警報・予報の区別、失効後の認証拒否を検証します。合成電文から実通知は送信しません。

UIテストは前面・背景・終了の3状態でシミュレーター通知を注入し、表示とタップ後の復帰を検査します。成果物は検証ログ、summary.json、合成通知、画面だけで、秘密設定・DB・ペアリングコード入り設定は保存対象外です。

## 配備済みソースとの比較

公開リポジトリは個人用の旧履歴を含みません。運用者は配備時にserverとios全体のGit tree IDを非公開JSONへ記録します。

    {"server": "serverの40文字のGit tree ID", "ios": "ios全体の40文字のGit tree ID"}

Live ActivityのWidgetKit拡張・プロジェクト設定も照合するため、iosはディレクトリ全体を対象とします。旧ios/QuakeRelayのみの記録は、そのまま配備一致の根拠には使えません。

次のモードは、明示した記録と両方のソースが一致しなければ失敗します。candidateで記録がない場合は、配備一致を判定したことにしません。

    python3 scripts/verify-ios-acceptance.py --source-mode deployed --deployment-baseline /private/deployment-baseline.json

## 任意の実環境確認

運用者が明示的に --live-url https://your-host.example/api/v1 を渡した場合だけ、そのHTTPS先の準備状態と未認証リクエストの拒否をGETで検査します。URL内の認証情報・クエリは拒否します。外部PRや通常の公開CIへ本番URLを渡しません。

通常のXCTestでは一時設定の必要な受入試験をskipします。専用workflowではSwift/Go結合試験と通知UI試験を実行し、本番VPSの試験は明示URLがなければskipします。summary.jsonでも本番はnot_testedと記録します。

## 検証の限界

シミュレーターの成功は実APNsの受理、iPhone到達、通知音、Focus、署名・TestFlight配布、実DMDATAの受信を証明しません。これらは[実機手順](../ios/README.md)と[配備手順](deployment.md)で別途確認します。
