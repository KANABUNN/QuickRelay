# EEW ブリッジへの受け渡し

QuickRelay がライブで受けた緊急地震速報（VXSE43・VXSE45）を、別サービスの EEW ブリッジへ渡すための仕組みです。QuickRelay は取込と同じトランザクションで受け渡し記録を書き、同じ VPS 内の Unix ドメインソケットで読み取り専用 API を提供します。ブリッジには DB ファイルや DMDATA・APNs の資格情報を渡しません。

既定では無効です。無効のときは受け渡し記録を書かず、ソケットも作りません。既存の受信・APNs・Live Activity の挙動は変わりません。

## 設定

`/etc/quick-relay/quick-relay.env` に設定します。

| キー | 既定値 | 内容 |
| --- | --- | --- |
| `EEW_HANDOFF` | `off` | `on` で受け渡しを有効にする。 |
| `EEW_HANDOFF_SOCKET` | `/run/quick-relay-handoff/handoff.sock` | ソケットの絶対パス（100 バイト以下）。 |
| `EEW_HANDOFF_SOCKET_GROUP` | なし | ソケットに付けるグループ。指定するとモード 0660 にする。指定しなければ所有者だけが接続できる。 |

ブリッジのユーザーには、このグループだけを付与します。`quick-relay` グループには `/etc/quick-relay` の秘密情報を読む権限があるので使いません。配備では `quick-relay-handoff` グループを使います。`quick-relay.service` がランタイムディレクトリ `/run/quick-relay-handoff`（0711）を作り、このグループを補助グループに持ちます（[配備と運用](eew-bridge-deployment.md#3-受け渡しを有効にする)）。

ソケットの作成に失敗しても、QuickRelay の受信は止めません。エラーをログに出し、受け渡し記録の書き込みだけを続けます。

## 記録するもの

次の条件をすべて満たす報だけを `eew_handoff` に記録します。

- ライブ受信の報である。`replay`（履歴投入）では書かない。
- 電文種別が VXSE43 または VXSE45 である。
- `reports` に新規に挿入された報である。同じ電文の再受信や、内容が同じ別経路の電文は記録しない。

| 列 | 内容 |
| --- | --- |
| `ingress_seq` | 受け渡し位置。単調に増え、削除後も再利用しない。 |
| `receipt_id` | `received_telegrams` のキー（フレームの SHA-256）。 |
| `report_sequence` / `report_id` | `reports` の対応する行。 |
| `received_ms` | VPS がフレームを受けた時刻。 |
| `committed_ms` | 取込をコミットした時刻。 |
| `upstream_generation` | DMDATA 接続の世代（`起動ID:接続番号`）。世代が変わる間には受信の欠落があり得る。 |
| `applied` | QuickRelay がこの報で系列 `(event_id, telegram_type)` を更新したときの状態。更新しなかった報は NULL。 |

受け渡しの書き込みに失敗した場合は取込全体をロールバックします。取込結果と受け渡し記録が食い違うことはありません。

明示取消の採否は QuickRelay の判定に従います。報数が小さい取消でも、QuickRelay が系列に確定していれば `applied.is_cancelled=true` になります。ブリッジは `applied` が NULL の報から取消を発行しません。

## API

HTTP/1.1 over Unix ドメインソケット。応答は JSON で、`Cache-Control: no-store` を付けます。

### `GET /v1/handoff`

| パラメーター | 既定値 | 範囲 | 内容 |
| --- | --- | --- | --- |
| `after` | 0 | 0 以上 | この位置より後の行を返す。 |
| `after_receipt` | なし | 64 文字以下 | `after` の行の `receipt_id`。行が残っていれば一致を確かめる。 |
| `limit` | 64 | 1〜256 | 最大件数。 |
| `max_bytes` | 524288 | 1〜4194304 | フレームの合計バイト数の上限。1 件で上限を超えるフレームは単独で返す。 |
| `wait_ms` | 0 | 0〜25000 | 返す行がないとき、次のコミットまで待つ時間（long-poll）。 |

```json
{
  "items": [{
    "ingress_seq": 101,
    "receipt_id": "…",
    "report_sequence": 5321,
    "report_id": "…",
    "telegram_type": "VXSE43",
    "received_at": "2026-11-25T03:00:12.06Z",
    "committed_at": "2026-11-25T03:00:12.071Z",
    "upstream_generation": "…:3",
    "applied": {"event_id": "…", "source_event_id": "…", "telegram_type": "VXSE43",
                "serial": 2, "is_cancelled": false, "is_final": false},
    "frame": "<base64>"
  }],
  "latest": 101,
  "purged_through": 40,
  "gap": false,
  "more": false,
  "upstream": {"configured": true, "connected": true, "fresh": true, "generation": "…:3",
               "last_frame_at": "…", "last_data_at": "…", "reconnects": 2, "server_time": "…"}
}
```

- `frame` は受信したフレームのバイト列そのもの（base64）。SHA-256 は `receipt_id` と一致する。
- `latest` はこれまでに割り当てた最大の位置。`purged_through` は削除済みの最大の位置。
- `gap=true` は、`after` より後の行が読まれる前に削除されたことを示す。ブリッジは欠落として扱い、無音で状態を作り直す。
- `more=true` は、件数またはバイト数の上限で打ち切ったことを示す。続けてすぐに読む。

| 状態 | 意味 |
| --- | --- |
| 400 `invalid_parameters` | パラメーターが範囲外。 |
| 409 `cursor_mismatch` | `after` が `latest` より大きい、または `after_receipt` が一致しない。DB の復元などで履歴が変わっている。ブリッジは保存した位置を捨てて作り直す。 |
| 429 `busy` | 同時に処理できる要求（2 件）を超えた。 |

DB の読み取りは応答を書く前に終えます。待っている間は DB 接続を使いません。

### `GET /v1/upstream`

`/v1/handoff` の `upstream` と同じ内容を返します。`fresh` は `/readyz` と同じ基準（接続中で、最後のフレームから 100 秒未満）です。

## 保持

保持の上限は 15 分または 10,000 件で、どちらかを超えた古い行から 30 秒ごとに削除します。削除した範囲は同じトランザクションで `eew_handoff_meta.purged_through` に記録します。受け渡しを無効にした後も、残った行は同じ規則で削除します。`received_telegrams` と `reports` は削除しません。

## 負荷の比較

受け渡しの有無による取込時間の差は、次のベンチマークで比べられます。

```sh
cd server
go test ./internal/ingest -run XXX -bench Process -benchtime 500x -count 5
```
