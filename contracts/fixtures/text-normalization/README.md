# 再利用する読み上げデータ

`legacy/windows-relay` / `v1-windows-relay` の次の3ファイルを、バイト内容を変更せず移動しました。

| 現在 | 元のパス |
|---|---|
| `default_readings.yaml` | `windows/talker-bridge/dictionaries/default_readings.yaml` |
| `default_replacements.yaml` | `windows/talker-bridge/dictionaries/default_replacements.yaml` |
| `cases.yaml` | `windows/talker-bridge/tests/fixtures/normalization/cases.yaml` |

地名の読み、単位・報数・全角文字・句読点・未知文字列に関する8つのfixtureです。`default_replacements.yaml` はPython正規表現の記法です。他言語へ持ち込む際は互換性の検証が必要です。現行Go通知文生成はこれらを読み込まず、fixtureは現行Go出力の期待値を示すものではありません。

Windows/TCP/Named Pipe/AivisSpeechの実行コードは含めません。旧パイプライン全体と試験は保全ブランチを参照してください。
