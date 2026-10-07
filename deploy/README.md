# Quick RelayのVPS配備

初回導入から実機Pushまでの手順は [Ubuntu導入ガイド](../docs/deployment.md) を使用してください。

| 場所 | 用途 |
|---|---|
| `install.sh` | 専用ユーザー、権限、秘密値を表示しない初回設定生成、unit配置 |
| `update.sh` | 新binaryの設定検査、整合DB backup、binary更新、再起動、health確認 |
| `caddy/Caddyfile` | 採用hostname `relay.example.com` からloopbackへHTTPS reverse proxy |
| `systemd/quick-relay.service` | 非rootのGoリレー |
| `systemd/quick-relay-health.service` / `.timer` | live readiness監視 |
| `monitoring/alerts.yml` | Prometheusアラート定義 |

CLIバイナリの名前は既存どおり `quakerelay` です。unit・ユーザー・ディレクトリは `quick-relay` に揃えています。設定は `/etc/quick-relay/quick-relay.env`、鍵は `keys/`、DBは `/var/lib/quick-relay/quickrelay.db` へ配置します。installは既存秘密値とCaddyサイト設定を上書きしません。

配備元はmainから新規cloneし、CIと手元で検証したcommitへ固定して記録します。旧Windows作業フォルダ全体や古いvendor、秘密設定をコピーしません。Caddyの既存設定を退避し、専用設定がimportの連鎖・globを含めて1回だけ読み込まれることを確認してから、設定検査 → reload → 外部HTTPS確認を行います。SSHを維持するUFW/Xserver設定とDNSのみでの初回TLS確認は、導入ガイドの第3節に従ってください。
