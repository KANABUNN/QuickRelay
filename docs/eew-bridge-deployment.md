# EEW ブリッジの配備と運用

[EEW ブリッジ](eew-bridge.md)を、稼働中の Quick Relay と同じ VPS に配備する手順です。リレーの配備は[Ubuntu導入ガイド](deployment.md)に従って済ませておきます。以下の実環境操作は運用者の作業です。リポジトリの修正やローカル検証だけでは、配備が完了したことにはなりません。

音声系は段階的に開きます。各段階で問題があれば、[切り戻し](#11-切り戻し)の手順で前の段階へ戻します。

| 段階 | この手順書の節 | 外部への影響 |
| --- | --- | --- |
| 配置と負荷測定 | 1〜2 | なし。受け渡しは無効のまま。 |
| 無音の並行運転 | 3〜4 | なし。ブリッジは状態とログを作るが、経路もトークンもない。 |
| 限定端末の WSS 接続 | 5〜7 | 発行したトークンを持つ端末だけ。 |
| 本番移行 | 8〜10 | 実契約と配信範囲を照合した端末。 |

## 前提

- 配備するリレーに、取消報を報数より優先する修正（#15）が含まれている。
- 端末へ WSS を開く前に、DMDATA の実契約区分、利用端末と聴取者、再配信の可否を確認している。確定するまでは、合成電文か許諾済みの入力だけで試験する。[DMDATA.JP 利用規約](https://dmdata.jp/terms/)・[再配信ポリシー](https://dmdata.jp/docs/eew/)
- Caddy が 2.7 以降である（`caddy version`）。`stream_close_delay` を使うためです。

## 構成

| 要素 | 実行ユーザー | 場所 |
| --- | --- | --- |
| `quick-relay.service` | `quick-relay`（補助グループ `quick-relay-handoff`） | 受け渡しソケット `/run/quick-relay-handoff/handoff.sock`。ディレクトリは 0711、ソケットは 0660 で `quick-relay-handoff` グループ。 |
| `quick-relay-bridge.service` | `quick-relay-bridge`（補助グループ `quick-relay-handoff`） | `/opt/quick-relay/eew-bridge`、設定 `/etc/quick-relay-bridge/bridge.env`、DB `/var/lib/quick-relay-bridge/bridge.db`、待受 `127.0.0.1:9082`。 |
| Caddy | `caddy` | `/v1/eew` を `127.0.0.1:9082` へ転送する。 |

`quick-relay-handoff` グループは受け渡しソケットへの接続にだけ使います。ブリッジは `quick-relay` グループに入らず、unit の `InaccessiblePaths` でも `/etc/quick-relay` と `/var/lib/quick-relay` を読めません。ブリッジの設定ファイルに秘密情報はありません。端末トークンは DB に SHA-256 だけを保存します。9082 は UFW・Xserver のどちらでも公開しません。

## 1. ビルドと配置

[Ubuntu導入ガイド](deployment.md)の第1節と同じく、検証済み commit の新規 clone でビルドします。ブリッジを配置した後は `update.sh` が `eew-bridge` も更新するので、毎回 3 つともビルドします。

```sh
sh scripts/verify.sh || exit 1
shellcheck deploy/install.sh deploy/install-bridge.sh deploy/update.sh scripts/verify-deploy.sh || exit 1
sh scripts/verify-deploy.sh || exit 1
mkdir -p server/bin
(cd server && CGO_ENABLED=0 go build -trimpath -o bin/quakerelay ./cmd/quakerelay) || exit 1
(cd server && CGO_ENABLED=0 go build -trimpath -o bin/apns-send ./cmd/apns-send) || exit 1
(cd server && CGO_ENABLED=0 go build -trimpath -o bin/eew-bridge ./cmd/eew-bridge) || exit 1
# 配備記録は導入ガイドの第1節と同じ形式で作り、eew-bridge のハッシュも含めます。

# 1) リレーをこの commit へ更新する。新しい unit と quick-relay-handoff グループが入る。受け渡しは無効のまま。
sudo sh deploy/update.sh || exit 1
# 2) ブリッジを配置する。起動はしない。既存の bridge.env は上書きしない。
sudo sh deploy/install-bridge.sh || exit 1
sudo sha256sum /opt/quick-relay/quakerelay /opt/quick-relay/apns-send /opt/quick-relay/eew-bridge >> "$DEPLOY_RECORD"
```

`install-bridge.sh` は、リレーの unit が受け渡しディレクトリを持たない場合に停止します。先に `update.sh` を実行してください。

## 2. 負荷測定

受け渡しは既存の取込トランザクションに書き込むため、有効にする前に負荷を測ります。`eew-loadtest` は、一時ディレクトリに作った別の DB に合成電文を流し込みます。リレーの取込、受け渡しソケット、ブリッジ、WSS 端末までを 1 プロセスで動かすツールで、本番の DB、DMDATA、APNs には接続しません。

```sh
(cd server && go build -trimpath -o .local/eew-loadtest ./cmd/eew-loadtest) || exit 1
# 本番 DB と同じディスク上の一時領域を使います。終了後に削除されます。
(cd server && .local/eew-loadtest -dir /var/tmp)
```

既定は、20 報/秒を 60 秒、端末 10 台です。最初に受け渡しなしの取込を別の DB で測り、続いて受け渡しありで測ります。

| 出力 | 意味 |
| --- | --- |
| `ingest without hand-off` / `with hand-off` | リレーの取込（受領保存〜コミット）にかかった時間。2 つを比べて既存経路への影響を見る。 |
| `receipt to client` | 受領から WSS 端末が `eew.state` を読むまで。全端末の値をまとめた統計。 |
| `Received N of M` | 端末が受け取った件数。取りこぼしがあれば FAIL。 |

目標は `receipt to client` の p95 が 150 ms 以下です。全件届いて目標内なら終了コード 0、外れたら 3 を返します。結果は配備記録に残します。測定中は VPS の CPU とディスクを使うので、静かな時間帯に実行します。稼働中のリレーは止めません。

## 3. 受け渡しを有効にする

```sh
sudoedit /etc/quick-relay/quick-relay.env
```

```dotenv
EEW_HANDOFF=on
EEW_HANDOFF_SOCKET=/run/quick-relay-handoff/handoff.sock
EEW_HANDOFF_SOCKET_GROUP=quick-relay-handoff
```

```sh
sudo -u quick-relay /opt/quick-relay/quakerelay -env-file /etc/quick-relay/quick-relay.env check
sudo systemctl restart quick-relay
sudo ls -l /run/quick-relay-handoff/
# srw-rw---- 1 quick-relay quick-relay-handoff ... handoff.sock
sudo journalctl -u quick-relay -n 50 --no-pager | grep -i 'hand-off'
curl --fail http://127.0.0.1:8080/readyz
```

`EEW hand-off enabled` が出ていれば有効です。ソケットを作れなかった場合、リレーは `EEW hand-off socket unavailable` を記録して取込を続けます。受け渡しの行は記録されるので、原因を直して再起動します。`telegram committed` のログの `commit_us` と `eew_handoff` を見ると、実電文で取込時間を比べられます。

## 4. ブリッジを起動する（無音の並行運転）

```sh
sudo systemctl enable --now quick-relay-bridge
curl --silent --show-error http://127.0.0.1:9082/healthz
sudo journalctl -u quick-relay-bridge -n 50 --no-pager
```

`/healthz` は `upstream` と `capture` が両方 `healthy` のときだけ 200 を返します。それ以外は 503 を返し、本文の JSON で状態がわかります。初回の起動では、リレーに残っている受け渡し（最大 15 分）を `origin=recovered` として取り込み、その後からライブとして扱います。

経路もトークンもないこの状態が、無音の並行運転です。ブリッジの状態とログを確かめ、QuickRelay の判定と比べます。複製 DB との比較は[無音検証](eew-bridge.md#複製-db-による無音検証)を使います。

## 5. 端末トークン

```sh
sudo -u quick-relay-bridge /opt/quick-relay/eew-bridge -env-file /etc/quick-relay-bridge/bridge.env token issue -name "居間の PC"
sudo -u quick-relay-bridge /opt/quick-relay/eew-bridge -env-file /etc/quick-relay-bridge/bridge.env token list
sudo -u quick-relay-bridge /opt/quick-relay/eew-bridge -env-file /etc/quick-relay-bridge/bridge.env token revoke t_xxxxxxxxxxxx
```

トークンは発行時に一度だけ表示されます。安全な経路で端末の資格情報ストアへ移し、チャット、Git、配備記録には残しません。端末ごとに別のトークンを発行します。1 トークンあたり 4 セッション、全体で 32 セッションまでです。失効させると、接続中のセッションは 5 秒以内に 4410 で閉じます。

## 6. WSS ルートを開く

導入ガイドの第3.3節と同じく、編集前に `/etc/caddy` を退避します。続いて `/etc/caddy/quick-relay.Caddyfile` のサイトブロックを、リポジトリの [Caddyfile](../deploy/caddy/Caddyfile) に合わせて 2 か所変更します。

```caddyfile
	@compress not path /v1/eew
	encode @compress zstd gzip
```

```caddyfile
	@eew path /v1/eew
	handle @eew {
		reverse_proxy 127.0.0.1:9082 {
			stream_close_delay 5m
		}
	}
```

```sh
sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile || exit 1
sudo systemctl reload caddy
```

VPS の外から、トークンを付けずにアップグレードを要求します。401 なら経路はブリッジまで届いています。502 はブリッジが停止中、404 は経路がありません。

```sh
curl --silent --output /dev/null --write-out '%{http_code}\n' --http1.1 \
  -H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' \
  -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' https://relay.example.com/v1/eew
```

このときブリッジの journal には、接続元のアドレスとともに `EEW authentication failed` が記録されます。ブリッジは認証失敗を接続元ごとに数え、1 分間に 20 回失敗したアドレスにだけ、それ以降 429 を返します。接続元のアドレスは、Caddy が付ける `X-Forwarded-For` から取ります。将来 Cloudflare のプロキシを有効にする場合は、Caddy の `trusted_proxies` に Cloudflare の範囲を設定し、`/v1/eew` に Challenge やキャッシュを適用しないでください。

## 7. 限定端末での確認

許可した端末だけにトークンを渡し、次を確かめます。受信側の確認項目（期限外の報知がないこと、既報知の系列の明示取消を一度だけ発話すること）は、受信プログラムの手順で確かめます。

| 試験 | 操作 | 期待する結果 |
| --- | --- | --- |
| 失効 | `token revoke` | 5 秒以内に 4410。再接続は 401。 |
| 版の不一致 | `schemaVersion` を 2 にして購読 | 4400 `unsupported_schema_version`。 |
| カーソルの保持切れ | 15 分以上前のカーソルで再開 | `sync_begin` が `snapshot`。 |
| 配信 DB の復元 | [第9節](#9-バックアップと復元)の手順で復元し、`rotate-epoch` | 新しい `epoch` でスナップショット。 |
| 同期中の更新 | 同期の直後に電文を受ける | 境界の次の連番から欠けずに届く。 |
| 上流断 | DMDATA の切断、またはリレーの再起動 | `health` が `degraded` になる。復帰後、警報は `partial`（`cause=reconcile`）。 |
| 遅い端末 | 端末の読み取りを止めたまま、送信バッファが埋まるまで配信を続ける | 書き込みが 5 秒詰まった端末だけ 4429 で閉じる。他の端末は続く。 |
| Caddy の再読込 | `systemctl reload caddy` | セッションは最大 5 分続き、その後に閉じて再開する。ブリッジの journal に再読込の時点での `EEW session closed` が出ない。 |
| ブリッジの再起動 | `systemctl restart quick-relay-bridge` | 1001 で閉じ、再接続すると同じ `epoch` で再開する。 |

## 8. 監視

ブリッジを運用に入れたら、運用ヘルス `/ops/readyz` にブリッジを含めます。`/etc/quick-relay-ops/config.json` に次の値を追加し、`quick-relay-ops` を再起動します。設定していない間は、ブリッジは判定に含まれません。

```json
"bridge_health_url": "http://127.0.0.1:9082/healthz"
```

ブリッジの journal には、セッションごとに `EEW session closed`（トークン ID、閉じたコード、理由、接続時間）が出ます。トークンや電文の本文は記録しません。上流断の期間、受け渡しの遅れ、端末ごとの切断は、それぞれ `/healthz`、リレーの `telegram committed`、この記録で追えます。

## 9. バックアップと復元

ブリッジの DB には、端末トークンの照合値と配信状態が入っています。配信状態は再構築できますが、トークンを失うと全端末に再発行が必要になります。日次の暗号化バックアップに含めるには、`/etc/quick-relay-ops/config.json` に次の値を加えます。

```json
"extra_databases": {"/var/lib/quick-relay-bridge/bridge.db": "bridge.sqlite3"},
"sources": {
  "/etc/quick-relay-bridge": "config/quick-relay-bridge",
  "/etc/systemd/system/quick-relay-bridge.service": "config/quick-relay-bridge.service",
  "/opt/quick-relay/eew-bridge": "bin/eew-bridge"
}
```

`sources` には既存の項目を残したまま追加します。`update.sh` も更新の前に `/var/lib/quick-relay-bridge/backup-*.db` を作ります。手動で作る場合は次のとおりです。

```sh
sudo -u quick-relay-bridge /opt/quick-relay/eew-bridge -env-file /etc/quick-relay-bridge/bridge.env backup /var/lib/quick-relay-bridge/backup-YYYYMMDD.db
```

復元では、まずサービスを止めます。現在の `bridge.db` を `-wal`・`-shm` とまとめて退避し、snapshot を `quick-relay-bridge:quick-relay-bridge / 0600` で配置します。そのうえで、**起動する前に** `rotate-epoch` を実行します。端末は新しい `epoch` を受けてスナップショットから同期し直します。

```sh
sudo systemctl stop quick-relay-bridge
sudo -u quick-relay-bridge /opt/quick-relay/eew-bridge -env-file /etc/quick-relay-bridge/bridge.env rotate-epoch
sudo systemctl start quick-relay-bridge
```

DB を失った場合は、空の DB で起動すれば新しい `epoch` で始まります。この場合はトークンを再発行します。リレーの DB を復元した場合は、受け渡しの位置が戻ったことをブリッジが検出し、自動で新しい `epoch` に替えます。

## 10. 更新

導入ガイドの第7節と同じ手順で、3 つのバイナリをビルドして `update.sh` を実行します。ブリッジを配置済みなら、`update.sh` は次を行います。

1. 新しい `eew-bridge` で設定を検査する。
2. ブリッジの DB の snapshot を作る。
3. `.previous` を残してバイナリを入れ替える。
4. リレーの health を確認する。
5. ブリッジが稼働中なら再起動し、`/healthz` を確認する。

`/healthz` が 503（劣化）のままなら警告を出します。応答がなければ失敗として終了します。戻すときは、`eew-bridge.previous` と同じ時刻の snapshot を組で戻し、`rotate-epoch` の後に起動します。

## 11. 切り戻し

1. **WSS を止める。** Caddy のサイトブロックから `@eew` の `handle` を削除し、validate してから reload します。接続中のセッションは `stream_close_delay` の間（最大 5 分）続きます。すぐ止めるときは、続けて `sudo systemctl stop quick-relay-bridge` を実行します。端末は 1001 で閉じ、再接続しても経路がないので 404 になります。
2. **受け渡しが原因なら、取込を元に戻す。** 受け渡しを有効にしてから `ingestion persistence failed` でリレーが再起動を繰り返す場合は、配信を止めた後に `EEW_HANDOFF=off` にします。`check` の後にリレーを再起動します。
3. **既存処理を確かめる。** `/readyz`、公開ヘルス、既存の Push 通知が正常なことを確かめます。
4. **ブリッジを無効にする。** 当面使わない場合は `sudo systemctl disable --now quick-relay-bridge` を実行します。

受け渡しを無効にしても、残った受け渡しの行は保持期限（15 分）で自動的に消えます。ブリッジの DB とトークンは残るので、再開するときは第3節から戻れます。
