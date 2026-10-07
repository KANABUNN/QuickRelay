# Ubuntu 24.04から実機Pushまで

正式名は **Quick Relay**。Apple App ID DescriptionとDMDATA appNameも同じです。Bundle ID / APNs Topicは **jp.kb-dev.quickrelay**、以下のhostname **relay.example.com** は例示です。実際の配備先へ置き換えてください。CLIは `quakerelay`、Go moduleは `quakerelay/server`、Xcode targetは `QuakeRelay` を維持します。

この手順の本番配置は `/opt/quick-relay/`、設定は `/etc/quick-relay/quick-relay.env` と `keys/`、DBは `/var/lib/quick-relay/quickrelay.db`。CLI名は既存互換の `quakerelay`、systemd名・専用ユーザーは `quick-relay` です。既存配備がある場合は末尾の移行手順から始めてください。

以下の実環境操作は運用者の配備作業です。リポジトリ修正・ローカル検証だけでは、本番配備やDNS・Cloudflare・VPS・Apple・DMDATAの設定完了を意味しません。個別の実環境の作業記録は運用者が非公開で保管してください。

## 1. Ubuntuの準備とビルド

VPSへSSHで入り、sudo権限のある一般ユーザーで上から実行します。SSH接続先・ユーザー・鍵は自身の管理情報を使い、チャットやGitに貼りません。

配備元は **mainの新規cloneから、検証済みの40桁commitへ固定したクリーンなcheckout** にします。既存のWindows作業フォルダ全体をコピーしません。旧 `windows/`・`pwa/`・`vps/`・PHP版 `server/vendor/`、秘密設定・DB・鍵は持ち込まず、既存作業フォルダにも一括削除や `git clean -fdx` を行いません。必要な監査資料だけを内容確認のうえ引き継ぎます。

公開ソースはHTTPSで読み取れるため、clone用のDeploy keyやPATは不要です。配備先の管理認証と、DMDATA/APNsの秘密設定は別途非公開で管理します。

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl git python3 openssl sqlite3 build-essential shellcheck dnsutils ufw
# QuickRelayディレクトリが既にある場合は、別の空の作業場所でcloneします。
git clone --branch main https://github.com/KANABUNN/QuickRelay.git || exit 1
cd QuickRelay || exit 1

# PRをmainへ取り込み、対象SHAのCI成功を確認してから実値を指定します。
VERIFIED_COMMIT=REPLACE_WITH_VERIFIED_40_CHARACTER_COMMIT
case "$VERIFIED_COMMIT" in
  ''|*[!0-9a-f]*) printf '%s\n' '検証済みの完全なcommit SHAを指定してください。' >&2; exit 1 ;;
esac
[ "${#VERIFIED_COMMIT}" -eq 40 ] || exit 1
git fetch origin main || exit 1
git cat-file -e "$VERIFIED_COMMIT^{commit}" || exit 1
git merge-base --is-ancestor "$VERIFIED_COMMIT" origin/main || exit 1
git checkout --detach "$VERIFIED_COMMIT" || exit 1
[ "$(git rev-parse HEAD)" = "$VERIFIED_COMMIT" ] || exit 1
[ -z "$(git status --porcelain)" ] || exit 1
git show -s --format='%H %s'

python3 deploy/bootstrap-go.py || exit 1
export PATH="$PWD/.local/toolchain/go/bin:$PATH"
go version
sh scripts/verify.sh || exit 1
shellcheck deploy/install.sh deploy/update.sh scripts/verify-deploy.sh ios/Scripts/test.sh || exit 1
sh scripts/verify-deploy.sh || exit 1
# Caddy未導入ならこの検査のCaddy部分はSKIP。第3節で必ず再検査します。
mkdir -p server/bin
(cd server && CGO_ENABLED=0 go build -trimpath -o bin/quakerelay ./cmd/quakerelay) || exit 1
(cd server && CGO_ENABLED=0 go build -trimpath -o bin/apns-send ./cmd/apns-send) || exit 1

# 配備commit・日時・Go版・binaryハッシュを秘密設定と分けて記録します。
install -d -m 0700 "$HOME/quick-relay-deployments"
DEPLOY_RECORD="$HOME/quick-relay-deployments/$(date -u +%Y%m%dT%H%M%SZ)-$VERIFIED_COMMIT.txt"
(
  umask 077
  { date -u; git show -s --format='%H %s'; go version; sha256sum server/bin/quakerelay server/bin/apns-send; } > "$DEPLOY_RECORD"
) || exit 1
sudo sh deploy/install.sh || exit 1
sudo sha256sum /opt/quick-relay/quakerelay /opt/quick-relay/apns-send >> "$DEPLOY_RECORD"
```

clone時のmain先端が検証済みとは限りません。引継ぎのSHAと、そのSHAを検証したCIのリンクを記録し、mainに含まれることを上記で確認します。squash/rebaseでSHAが変わった場合は、実際に配備するSHAでCIとローカル検証を確認し直します。配備後も対象SHAを記録から辿れるようにしてください。

Goは公式配布JSONから1.26以上のstableを選び、公式SHA-256照合後にこのcheckout内へ展開します。すでに対応Goがある場合はbootstrapとPATH設定を省略できます。記録したビルド元・配置先binaryのハッシュがそれぞれ一致することを確認します。installはoffline設定を作り、ランダムなPAIRING_SECRETをファイル内だけに保存します。既存設定は上書きしません。

## 2. サービス起動とローカルhealth

```sh
sudo -u quick-relay /opt/quick-relay/quakerelay -env-file /etc/quick-relay/quick-relay.env check
sudo systemctl enable --now quick-relay
curl --fail http://127.0.0.1:8080/health
sudo systemctl status quick-relay --no-pager
sudo ss -lntp
```

この時点でAPI・SQLite・migrationが稼働します。`/health` の `ok=true` を確認してください。offline時の `/readyz` は503が正常です。まだDMDATA/APNsへ接続しません。

`LISTEN_ADDR=127.0.0.1:8080` と、実際の8080待受がloopbackだけであることを確認します。Goの設定検査も非loopbackを拒否します。`0.0.0.0:8080` / `[::]:8080` を使わず、UFW・Xserverに8080の外部許可を追加しません。

## 3. SSHを維持するファイアウォール・DNS・HTTPS

### 3.1 現在のSSHと両方のフィルターを確認

**今のSSHセッションを開いたまま**、現在の接続先ポート・実待受・既存許可を確認します。22と決め打ちせず、socket activation等がある場合も `ss` と現在の接続を照合します。

```sh
# 順に接続元IP、接続元ポート、接続先IP、接続先ポート。
printf '%s\n' "$SSH_CONNECTION"
sudo sshd -T | grep -E '^(port|listenaddress) '
sudo ss -lntp
sudo ufw status verbose
sudo ufw status numbered
```

[Xserver VPSのパケットフィルター](https://vps.xserver.ne.jp/support/manual/man_server_port.php)も管理画面で確認し、既存SSHのポートと接続元制限を記録・維持します。UFWのactive状態だけでは各ポートの許可を確認したことになりません。

SSHの許可が不足する場合に限り、実際のポート・許可元を使って追加します。既存の許可元制限を無条件に全開放へ置き換えません。次はUFW側の追加例で、プレースホルダのまま実行しません。

```sh
SSH_PORT=REPLACE_WITH_CURRENT_SSH_PORT
SSH_SOURCE=REPLACE_WITH_ALLOWED_SOURCE_IP_OR_CIDR
sudo ufw allow proto tcp from "$SSH_SOURCE" to any port "$SSH_PORT"
```

UFWとXserver側の**両方**でTCP 80/443を許可します。次はUFW側です。初回のCaddy証明書取得に必要な外部到達性も確保します。

```sh
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw status verbose
sudo ufw status numbered
```

UFWがinactiveなら、SSH許可とXserver側の許可を先に確認してから `sudo ufw enable` を実行します。既存ルールのreset・一括削除は行いません。変更後は別の端末から同じSSH設定で新規ログインできることを確認し、それまでは既存接続を閉じません。8080は両方のフィルターで公開不要です。[UbuntuのUFW手順](https://ubuntu.com/server/docs/how-to/security/firewalls/)

### 3.2 初回はCloudflareを「DNSのみ」にする

初回導入は `relay.example.com` のAをVPSのIPv4へ向け、Cloudflareを **DNSのみ（灰色の雲）** にしてCaddy自身のTLSと疎通を確認する方式を推奨します。AAAAはIPv6でもVPSへ到達できる場合だけ設定します。古い・到達不能なAAAAを残さず、A/AAAAとプロキシ状態を管理画面で確認してください。

```sh
dig @1.1.1.1 relay.example.com A +short
dig @8.8.8.8 relay.example.com A +short
dig @1.1.1.1 relay.example.com AAAA +short
dig @8.8.8.8 relay.example.com AAAA +short
```

「VPS宛へ変更済み」という申告、公開DNSの回答、管理画面の設定値は別々に記録します。**プロキシ有効時に公開DNSがCloudflareのIPを返し、VPSのIPと一致しないことは正常**です。その不一致だけで設定不良と判定せず、オリジンの設定値は管理画面で確認します。DNSのみなら反映後の公開A/AAAAをVPSのIPと照合します。[Cloudflareのプロキシ状態の説明](https://developers.cloudflare.com/dns/proxy-status/)

### 3.3 Caddy設定を保護して1回だけimport

Caddy導入済みなら再インストールは不要です。未導入なら[Caddy公式Ubuntu手順](https://caddyserver.com/docs/install#debian-ubuntu-raspbian)に従います。

```sh
sudo apt-get install -y debian-keyring debian-archive-keyring apt-transport-https gnupg
curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/gpg.key -o /tmp/caddy-key.asc
sudo gpg --batch --yes --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg /tmp/caddy-key.asc
curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt -o /tmp/caddy-stable.list
sudo install -m 0644 /tmp/caddy-stable.list /etc/apt/sources.list.d/caddy-stable.list
sudo chmod 0644 /usr/share/keyrings/caddy-stable-archive-keyring.gpg
sudo apt-get update
sudo apt-get install -y caddy
```

`deploy/install.sh` は既存ファイルがなければ `/etc/caddy/quick-relay.Caddyfile` を配置済みです。既存Caddyfileを丸ごと置換せず、他サイト・グローバル設定を維持します。編集前に設定全体をimport対象外の場所へ退避します。

```sh
CADDY_BACKUP=$(sudo mktemp -d /var/backups/quick-relay-caddy.XXXXXXXX) || exit 1
sudo cp -a /etc/caddy/. "$CADDY_BACKUP/" || exit 1
printf 'Caddy backup: %s\n' "$CADDY_BACKUP"
sudoedit /etc/caddy/Caddyfile
sudoedit /etc/caddy/quick-relay.Caddyfile
```

専用設定はリポジトリの [Caddyfile](../deploy/caddy/Caddyfile) と照合し、hostname `relay.example.com`、転送先 `127.0.0.1:8080` を確認します。既存のimport先も辿り、相対パス・glob・入れ子のimportで既に専用設定が読み込まれていないか確認してください。[Caddy import仕様](https://caddyserver.com/docs/caddyfile/directives/import)

まだ読み込まれていない場合だけ、`/etc/caddy/Caddyfile` の**サイトブロックの外側**へ次の1行を追加します。既存のglobが読み込む場合は追加しません。同じhostnameのブロックが別ファイルや本体にある場合は、Quick Relayの定義だけを専用設定へ集約し、他サイトを保持します。単純な完全一致grepだけで重複なしとは判定しません。

```caddyfile
import /etc/caddy/quick-relay.Caddyfile
```

### 3.4 設定検査 → reload → 外部HTTPS確認

```sh
# 第1節でCaddy検査がSKIPだった場合も、ここで設定を検証します。
sh scripts/verify-deploy.sh || exit 1
sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile || exit 1
sudo systemctl reload caddy || exit 1
sudo systemctl status caddy --no-pager
sudo journalctl -u caddy -n 50 --no-pager
sudo ss -lntp
```

検査失敗時はreloadせず、退避設定と照合して修正します。検査成功だけでは証明書取得や外部到達の成功にはなりません。reload後、**VPS外の端末・回線**から次を実行します。Windows PowerShellでは `curl.exe` を使用します。

```sh
curl --fail --show-error --connect-timeout 5 --max-time 20 https://relay.example.com/health
```

証明書検証を無効化せず、TLS検証成功とJSONの `ok=true` を確認してから次へ進みます。VPS内のcurlだけでは外部到達性の代わりになりません。失敗時はDNSのA/AAAA・両フィルター・Caddyログ・loopbackのhealthを順に確認します。[Caddyの自動HTTPS要件](https://caddyserver.com/docs/automatic-https)

将来Cloudflareのプロキシを有効にする場合は、オリジンの有効な証明書を維持して **Full (strict)** を使います。API・health/readiness・metricsはキャッシュをbypassし、Cache Everything等で認証応答や履歴を共有キャッシュに載せません。iOS APIや監視URLにブラウザー向けChallengeを返すとJSON通信が成立しないため、それらのリクエストにChallengeが適用されないようルールを設計し、プロキシ経由で再検証します。設定変更はWork側の別作業です。[Full (strict)](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/full-strict/)、[Cache Rules](https://developers.cloudflare.com/cache/how-to/cache-rules/settings/)、[Challengeの制約](https://developers.cloudflare.com/cloudflare-challenges/challenge-types/challenge-pages/)

## 4. APNs設定

Apple DeveloperのCertificates, Identifiers & Profilesで、Description `Quick Relay`、Explicit Bundle ID `jp.kb-dev.quickrelay` のApp IDへPush Notificationsを有効化します。[Appleの環境限定・Topic Specific key](https://developer.apple.com/news/?id=wy4tb0uo)を使う場合は、環境とTopicを一致させます。

Team ID、Key ID、非公開 `.p8` を用意します。秘密鍵は安全なSCP等でVPSの非公開一時領域へ転送し、以下の `PRIVATE_KEY_PATH` をそのパスへ読み替えます。

Appleから取得した元ファイル名にかかわらず、共通鍵の配置名は `AuthKey.p8` に統一します。設定例の `APNS_KEY_PATH` もこのパスです。既存配備で別名を使う場合は、実際の配置先とPATHを必ず一致させ、既存鍵を意図せず上書きしないでください。

```sh
sudo install -o root -g quick-relay -m 0640 PRIVATE_KEY_PATH /etc/quick-relay/keys/AuthKey.p8
sudoedit /etc/quick-relay/quick-relay.env
```

設定する値:

```dotenv
APNS_TEAM_ID=YOUR_TEAM_ID
APNS_BUNDLE_ID=jp.kb-dev.quickrelay
APNS_ENVIRONMENT=sandbox
APNS_KEY_ID=YOUR_KEY_ID
APNS_KEY_PATH=/etc/quick-relay/keys/AuthKey.p8
```

Debug実機はsandbox/development、TestFlightはproductionです。`APNS_ENVIRONMENT` は**サーバーの送信・端末登録を許可する環境**も制限します。両方を同時に扱う場合だけ `both` とし、別鍵の `APNS_SANDBOX_KEY_ID` / `APNS_SANDBOX_KEY_PATH`、`APNS_PRODUCTION_KEY_ID` / `APNS_PRODUCTION_KEY_PATH` を設定します。IDとPATHは必ず対で指定します。旧来の両環境対応鍵なら共通KEY_ID/PATHをbothで利用できます。Topic Specific keyでもTopicは同じBundle IDです。

旧 `APNS_KEY_FILE` は新PATHが空の場合に使用されます。認証鍵、端末token全文をログやチャットへ貼る必要はありません。

## 5. DMDATA設定とlive起動

[DMDATA Socket Start v2](https://dmdata.jp/docs/reference/api/v2/socket.start/)に合わせ、契約済み区分を `DMDATA_CLASSIFICATIONS` へ指定します。既定は `eew.forecast,eew.warning,telegram.earthquake`、対象電文は区分に対応するVXSE45/VXSE43/VXSE51/VXSE52/VXSE53です。未知区分や重複は設定エラーにします。test=no、formatMode=json、appName=Quick Relayは固定です。

`telegram.earthquake` の全電文を受信する設定ではありません。津波・南海トラフ・長周期地震動等の専用電文は取得・保存・通知しません。今回の対象は[初回リリース範囲](initial-release-scope.md)の5電文で、それ以外の電文追加・切断区間backfill・OAuth自動更新・履歴自動削除は行いません。

APIキーへ `socket.start`、自分のsocketを終了する `socket.close`、購読する区分の `eew.get.forecast` / `eew.get.warning` / `telegram.get.earthquake` を付与します。未契約区分は設定から外します。

```sh
sudoedit /etc/quick-relay/quick-relay.env
# DMDATA_API_KEYへAPIキーを保存。
# DMDATA_CLASSIFICATIONSを契約に合わせ、RELAY_MODE=liveへ変更。
sudo -u quick-relay /opt/quick-relay/quakerelay -env-file /etc/quick-relay/quick-relay.env check
sudo systemctl restart quick-relay
curl --fail https://relay.example.com/readyz
sudo journalctl -u quick-relay -n 50 --no-pager
```

`check` は設定と鍵の構文確認、`readyz` はDB・鍵設定・DMDATA接続確認です。Appleの実受理やiPhone到達は次の手順で確認します。旧DMDATA_TOKENも使用できますが、API_KEYがあれば優先します。OAuth常駐更新は未実装なのでAPIキーを使用してください。

## 6. iOS登録 → テストPush → E2E

Macで [iOS手順](../ios/README.md) に従い既存Xcode projectを開き、Teamを選択してDebug実機へインストールします。Push NotificationsとTime Sensitiveの署名profileを用意し、通知を許可します。

```sh
sudo -u quick-relay /opt/quick-relay/quakerelay -env-file /etc/quick-relay/quick-relay.env pair
```

アプリへ `https://relay.example.com/api/v1` と表示された8桁コードを入力します。installation IDはKeychainへ保存され、APNs device tokenは自動登録されます。アプリの診断で「登録済み」を確認してください。

端末tokenを表示せず、登録済みinstallation IDだけでテストできます。

```sh
sudo -u quick-relay sqlite3 -readonly /var/lib/quick-relay/quickrelay.db \
  'SELECT installation_id,environment,push_active FROM devices WHERE revoked=0;'
# 下記INSTALLATION_IDを対象の値へ変更。
sudo -u quick-relay /opt/quick-relay/apns-send \
  -env-file /etc/quick-relay/quick-relay.env -installation-id INSTALLATION_ID
```

この通知は「Quick Relay 接続テスト」と表示されます。CLI成功はAppleの受理までです。前面・背景・終了状態の実機表示、通知タップからのevent詳細、Pushを受けなかった場合の手動syncを確認します。接続テストには架空の地震eventを付けません。event deep linkとE2Eは実際の通常電文の到着後に確認してください。実警報を模したテスト通知を本番端末へ自動配信しません。

実DMDATA → SQLite reports/deliveries → APNs accepted → iPhone表示の順を確認します。Release/TestFlightはproduction鍵・tokenで同様に確認してください。設定をproductionだけへ切り替える場合、残っているDebug端末へは送信されません。

## 7. 運用・更新・復旧

```sh
sudo systemctl enable --now quick-relay-health.timer
sudo systemctl list-timers quick-relay-health.timer
# 整合snapshot。既存ファイルは上書きしません。
sudo -u quick-relay /opt/quick-relay/quakerelay -env-file /etc/quick-relay/quick-relay.env \
  backup /var/lib/quick-relay/backup-YYYYMMDD.db
```

DBには端末tokenが含まれます。設定・p8・DBを制限した別媒体へbackupしてください。timerはsystemdに失敗を記録するだけなので、外部監視/Prometheus通知先は別途設定します。

更新時も第1節と同じく新規cloneからmainに含まれる検証済みcommitへ固定し、対象SHAのCIとローカル検証を確認します。build元commit・Go版・binaryハッシュを新しい `DEPLOY_RECORD` へ記録してから更新します。稼働中のDB・設定・鍵をcheckoutへコピーしません。

```sh
sh scripts/verify.sh || exit 1
shellcheck deploy/install.sh deploy/update.sh scripts/verify-deploy.sh ios/Scripts/test.sh || exit 1
sh scripts/verify-deploy.sh || exit 1
mkdir -p server/bin
(cd server && CGO_ENABLED=0 go build -trimpath -o bin/quakerelay ./cmd/quakerelay) || exit 1
(cd server && CGO_ENABLED=0 go build -trimpath -o bin/apns-send ./cmd/apns-send) || exit 1
# 第1節の記録作成部分を実行（install.shは再実行しません）。
sudo sh deploy/update.sh || exit 1
sudo sha256sum /opt/quick-relay/quakerelay /opt/quick-relay/apns-send >> "$DEPLOY_RECORD"
curl --fail https://relay.example.com/readyz
```

updateは新binaryのcheck、DB snapshot、previous binary保存、再起動、health確認を行います。設定・p8・Caddyは保持します。LISTEN_ADDRを変更した場合はCaddy/health timerと更新時の `QUICK_RELAY_HEALTH_URL` も一致させます。

復旧ではサービスを停止し、現DBとWAL/SHMをまとめて退避して、対応するsnapshotとprevious binaryを組で戻します。migration後のDBへ旧binaryだけを戻さないでください。snapshotは `sqlite3 SNAPSHOT 'PRAGMA integrity_check;'` で検査します。backupでserver_sequenceが端末cursorより小さくなった場合はiOSを再インストールして再ペアリングします。

DMDATA切断中の自動backfillは未実装です。再接続後のsyncではVPS未受信分は復元できません。APNs受理直後の異常停止では重複通知の可能性があります。履歴自動削除も未実装なので空き容量を監視します。

## 旧quakerelay配置からの移行

新規VPSではこの節は不要です。installは旧設定/DBを見つけ、新配置の設定がない場合に停止します。旧Windows版のブランチ・タグは操作しません。

1. 旧 `quakerelay.service` とhealth timerを停止・無効化し、旧CLI・旧設定で整合DB snapshotを作成します。旧ファイルは復旧用に保持します。
2. rootだけがアクセスできる `/etc/quick-relay` を作り、旧envを `/etc/quick-relay/quick-relay.env` へコピーします。これが明示的な移行準備になります。
3. installを実行し、snapshotを新DBパスへ `quick-relay:quick-relay / 0600` で配置します。鍵を新keysディレクトリへ `root:quick-relay / 0640` で配置します。新DBが既にある場合は上書きせず内容を先に確認します。
4. envのDATABASE_PATH・APNS_KEY_PATH・Bundle ID・APNS_ENVIRONMENTを新設定へ変更し、check → 新サービス起動 → health → readyzの順に確認します。旧APNS_KEY_FILEが残る場合、新PATHが優先されます。
5. Caddy importを新サイトに揃え、iOSを新Bundle IDで再インストールして再登録します。旧サービスと新サービスを同時起動しません。

## 日次バックアップと外部監視

暗号化バックアップ、保存先を限定したSSH転送、復元試験、外部監視の設定は [運用手順](operations.md) を参照してください。
