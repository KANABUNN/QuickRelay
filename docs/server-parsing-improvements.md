# サーバー側の解析・APIの改善候補

XML原電文にあってVPSで抽出していなかった項目のうち、下の「対応済み」の項目を抽出し、Reportの任意項目としてAPIで返すようにしました。iOS・Watchもこれらの項目を表示します。項目の一覧は[API](api.md)、画面は[iOS・Apple Watch・Live Activityの画面](ios-ui.md)を参照してください。残りの項目と、APIや運用の改善候補もこのページにまとめます。

対象のコードは `server/internal/dmdata/xml.go`（`parseXML` / `xmlEarthquake` / `xmlIntensityArea` / `xmlTsunamiItem`）、`details.go`（構造化した項目の組み立て）、`bulletins.go`（`describeBulletin`）、`normalize.go`（`normalizeConverted`）、`areas.go`（`affectedAreas`）です。

## 1. 原電文の項目

### 緊急地震速報（VXSE43 / VXSE44 / VXSE45）

| 項目 | XMLの位置 | 状態 |
|---|---|---|
| 地域ごとの警報・予報の区分 | `Intensity/Forecast/Pref/Area/Category/Kind` | 対応済み：`eew.areas[].kind`・`kind_code`・`is_warning` |
| 主要動の到達予想時刻 | `…/Area/ArrivalTime` | 対応済み：`eew.areas[].arrival_time`。発表値として表示し、秒読みはしません |
| 到達に関する状況 | `…/Area/Condition` | 対応済み：`eew.areas[].condition` |
| 地域ごとの予想震度の範囲 | `…/Area/ForecastInt/From`・`To` | 対応済み：`eew.areas[].forecast_intensity`（`from`・`to`・表示用の `text`） |
| 長周期地震動の予想階級 | `Forecast/ForecastLgInt`、`…/Area/ForecastLgInt` | 対応済み：`eew.forecast_max_long_period_class`、`eew.areas[].forecast_long_period_class` |
| 最大予想震度の変化と理由 | `Forecast/Appendix` | 対応済み：`eew.change`（コードと説明文） |
| 見出し | `Head/Headline/Text` | 対応済み：`eew.headline` |
| 警報の対象（地方予報区など） | `Headline/Information` | 対応済み：`eew.warning_areas`（地方・府県予報区・細分区域） |
| 震源の精度 | `Hypocenter/Accuracy` | 対応済み：`hypocenter.accuracy` |
| 陸域・海域 | `Hypocenter/Area/LandOrSea` | 対応済み：`hypocenter.land_or_sea` |
| マグニチュードの種別・表記 | `Magnitude@type`・`@condition`・`@description` | 対応済み：`hypocenter.magnitude_type`・`magnitude_condition`・`magnitude_text`。値がないときは「Ｍ不明」などを表示します |
| 付加文 | `Comments/WarningComment` など | 対応済み：`eew.comments` |

DMDATAの表示条件により、警報（VXSE43）ではM・深さに関わる項目（`coordinate_text`、`magnitude_*`、深さ・Mの精度ランク）を返しません。テスト（`TestEEWWarningDetailsKeepDisplayRestriction`）で確認しています。

### 震度速報・震源・震度情報・震源要素更新・長周期（VXSE51 / 52 / 53 / 61 / 62）

| 項目 | XMLの位置 | 状態 |
|---|---|---|
| 震度5弱以上未入電 | `…/City/Condition` | 対応済み：`intensity_observation` の `condition`、震度の値「5弱以上未入電」。bulletinの「最大震度」と `affected_areas` にも出ます |
| 震度の修正 | `…/Revise` | 対応済み：`intensity_observation` の `revise`、bulletinの「修正」行 |
| 地域・観測点のコード | `…/Code` | 対応済み：`intensity_observation` の `code`。iOSの地図は観測点コードで照合します |
| 地震の発現時刻 | `Earthquake/ArrivalTime` | 対応済み：`hypocenter.arrival_time` |
| 震源の詳細名・コード・決定機関 | `Hypocenter/Area/Code`、`DetailedName`、`NameFromMark`、`Source` | 対応済み：`hypocenter.code`・`detailed_name`・`name_from_mark`・`source` |
| 座標の文字表現 | `jmx_eb:Coordinate@description` | 対応済み：`hypocenter.coordinate_text` |
| 度分表記の座標（VXSE61） | `Coordinate@type="震源位置（度分）"` | 度の値だけを使います（意図どおり） |
| 周期帯別の長周期・絶対速度応答（VXSE62） | `LgIntPerPeriod`、`SvaPerPeriod` | 対応済み：`intensity_observation` の `periods` |
| 見出しの震度別地域（VXSE51） | `Head/Headline/Information[type=震度速報]` | 未対応。本文の観測値と同じ内容のため不要と判断しました |

### 津波（VTSE41 / 51 / 52）

- 関連する地震の座標・震央地名コード：対応済み（`related_earthquakes`）。津波・関連情報の「関連する地震」に座標・コード・Mの記述を表示します。
- 沖合観測点の位置：未対応。電文には観測点のコードと名称だけがあり、位置は含まれません。地図に点で描くには、DMDATAの津波観測点パラメータなど別の位置情報が必要です。

### その他

- IXAC41（推計震度分布図作図用BUFR）：復号していません。復号できれば、推計震度の格子を地図に描けます。作業量は大きいです。
- WEPA60：原文の表示だけです（自由文のため構造化する項目はありません）。

## 2. API・モデルの改善候補

1. **EEW用の構造化オブジェクト**：対応済み（`Report.eew`）。
2. **観測震度の構造化**：対応済み（`Report.intensity_observation`）。iOSはこの項目があれば文字列を分解せずに階層を作ります。表示用のセクションは互換性のため残しています。
3. **`affected_areas` の拡張**：見送りました。EEWの地域ごとのコード・範囲は `eew.areas`、観測値のコードは `intensity_observation` にあります。細分区域のポリゴンがあれば、これらを使ってEEWの予想震度を地図に描けます。
4. **`Hypocenter` の拡張**：対応済み。
5. **重複判定の維持**：対応済み。`model.Report.WithoutStructuredDetails` で新しい項目を除いて指紋を計算します（`TestFingerprintIgnoresStructuredDetails`）。bulletinに行が増えるのは、未入電・修正を含む電文だけです。
6. **保存済み電文の再解析**：未実施。`reports.raw` に原電文が残っています。新しい項目を過去の履歴にも付けるには、通知を作らない一回限りの再解析コマンド（`IngestHistorical` と同様の扱い）を用意し、`payload` だけを更新します。現在の方針（保存済みの電文は再正規化しない）を変える判断になるため、実施は別途決めます。

## 3. 運用・構成の改善候補

構成（DMDATA → Go/VPS → APNs → iOS、Live Activity）は今のままで問題ありません。そのうえで、優先度の高い順に挙げます。

1. **切断区間の補完（優先度：高）**：WebSocketの切断中に発表された電文（特にVTSE41・VXSE53）は、現在は取り戻せません。再接続のときに、DMDATAのREST APIで過去の電文を取得できるかを確認します（電文リストAPIとカーソル指定の可否、保持期間、契約上の扱い）。取得できる場合は、`IngestHistorical` で通知せずに保存します。最新のVTSE41だけは発表中の判定に必要なので、通知するかどうかを個別に決めます。
2. **受信遅延の記録**：アプリの電文情報に、配信時刻から受信までの時間を表示するようにしました。APNsへの送信時刻（`deliveries` の受理時刻）もAPIで返せば、「受信→通知送信」の遅延を端末で確認できます。
3. **ディスク使用量**：履歴の自動削除がありません。IXAC41の分割BUFRとWEPA60の原文は増えやすいので、原文だけを一定期間で削除する設定を検討します。構造化した観測震度を加えたため、大きな地震情報（VXSE53）の `payload` は公式サンプルで約1.6倍になります。
4. **Live Activityの構造化**：今回はサーバーを変更せず、既存の項目（タイトル・要約・震度文字列・報数・状態）でレイアウトだけを刷新しました。`LiveContent` に震央・M・深さ・発生時刻・震度コードを任意の項目として追加すれば、ロック画面とDynamic Islandにも個別の値を表示できます。4KBの上限に注意します。
5. **Watch単体への通知**：現在のWatchは、iPhoneから届いた要約を表示するだけです。iPhoneが手元にない場面を考える場合は、watchOSアプリ向けのAPNs配信を検討します。

## 変更時の手順

サーバーの項目を追加するときは、[API契約](../contracts/README.md)のSchemaとOpenAPI、Goの契約テスト、iOSの `APIModels.swift`（任意の項目として追加）を同時に更新します。そのうえで、VPSを先に更新してからTestFlightを更新します。
