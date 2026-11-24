# サーバー側の解析・APIの改善候補

iOS/WatchのUI刷新（`claude/ui-redesign`）では、サーバーが現在返している項目だけを使っています。表示に使っていなかった `affected_areas` を含め、届いている項目はすべて表示します。このページには、XML原電文にはあるもののVPSで抽出していない項目と、APIや運用の改善候補をまとめます。実装はまだ行っていません。

対象のコードは `server/internal/dmdata/xml.go`（`parseXML` / `xmlEarthquake` / `xmlIntensityArea` / `xmlTsunamiItem`）、`bulletins.go`（`describeBulletin`）、`normalize.go`（`normalizeConverted`）、`areas.go`（`affectedAreas`）です。

## 1. 原電文にあるが抽出していない項目

### 緊急地震速報（VXSE43 / VXSE44 / VXSE45）

EEWには `Report.Bulletin` が作られません（`normalizeConverted` の `!r.IsEEW()` 分岐）。そのため、次の項目がAPIに出ていません。

| 項目 | XMLの位置 | 現状 | 用途 |
|---|---|---|---|
| 地域ごとの警報・予報の区分 | `Intensity/Forecast/Pref/Area/Category/Kind`（Name・Code） | 未抽出 | 地域ごとに「警報」「予報」を色分けする |
| 主要動の到達予想時刻 | `…/Area/ArrivalTime` | 未抽出 | 気象庁の発表値として時刻を表示する（秒読みではない） |
| 到達に関する状況 | `…/Area/Condition`（例：「既に主要動到達と推測」） | 未抽出 | 到達予想時刻の代わりに表示する |
| 地域ごとの予想震度の範囲 | `…/Area/ForecastInt/From`・`To` | `affected_areas` には `To`（over の場合は `From`+以上）だけが入る | 「4〜5弱」のような範囲で表示する |
| 長周期地震動の予想階級 | `Forecast/ForecastLgInt`、`…/Area/ForecastLgInt` | 未抽出 | 予想階級の表示 |
| 最大予想震度の変化と理由 | `Forecast/Appendix/MaxIntChange`・`MaxLgIntChange`・`MaxIntChangeReason` | 未抽出 | 報の推移表で「上方修正（M変化）」などを示す |
| 見出し | `Head/Headline/Text`（例：「豊後水道で地震　四国　九州　中国で強い揺れ」） | 未保存 | 一覧・詳細の見出し |
| 警報の対象（地方予報区） | `Headline/Information[type=緊急地震速報（地方予報区）]` | `zones` に読み込むが `affectedAreas` が使わない | 対象地域を「地方／府県／細分区域」の階層で表示する |
| 震源の精度 | `Hypocenter/Accuracy`（Epicenter rank/rank2、Depth rank、MagnitudeCalculation rank、NumberOfMagnitudeCalculation） | 仮定・低精度の判定だけに使い、値は捨てている | 精度ランクを数値で表示する |
| 陸域・海域 | `Hypocenter/Area/LandOrSea` | 未抽出 | 震源の補足 |
| マグニチュードの種別・表記 | `Magnitude@type`（Mj など）、`@description`、`@condition`（「Ｍ不明」「Ｍ８を超える巨大地震」） | 数値以外は捨てている | 「M不明」「M8超」をそのまま表示する。現在は値がないと「−」になる |
| 付加文 | `Comments/WarningComment` など | 未保存 | 「強い揺れに警戒してください。」などの固定文 |

DMDATAの表示条件により、警報（VXSE43）のM・深さは追加の項目でも返さないでください。

### 震度速報・震源・震度情報・震源要素更新・長周期（VXSE51 / 52 / 53 / 61 / 62）

| 項目 | XMLの位置 | 現状 |
|---|---|---|
| 震度5弱以上未入電 | `Pref/Area/City/Condition`（「震度５弱以上未入電」） | `xmlIntensityArea` が `Condition` を読まないため、未入電の市町村が値なしになる |
| 震度の修正 | `…/Revise`（「上方修正」「追加」） | 未抽出 |
| 地域・観測点のコード | `…/Code` | 読み込むが、bulletinの行（`describeBulletin` の `areas`）に出ていない |
| 地震の発現時刻 | `Earthquake/ArrivalTime` | 未抽出 |
| 震源の詳細名・コード・決定機関 | `Hypocenter/Area/Code`、`DetailedName`、`Source` | 未抽出 |
| 座標の文字表現 | `jmx_eb:Coordinate@description` | 未抽出 |
| 度分表記の座標（VXSE61） | `Coordinate@type="震源位置（度分）"` | 度の値だけを使っている（意図どおり） |
| 周期帯別の長周期・絶対速度応答（VXSE62） | `LgIntPerPeriod`、`SvaPerPeriod` | `readXML` の繰り返し許可にはあるが、出力していない |
| 見出しの震度別地域（VXSE51） | `Head/Headline/Information[type=震度速報]` | 未抽出（本文の観測値と同じ内容のため優先度は低い） |

### 津波（VTSE41 / 51 / 52）

ほぼすべて抽出済みです。残っているのは次の項目です。
- 沖合観測点のコード・位置。地図に点で描く場合に必要です（現在は推測を避けて沿岸の線だけを描いています）。
- 関連する地震の座標・震央コード。`describeBulletin` の「関連する地震」は発生時刻・震源名・深さ・M・補足だけを出しています。

### その他

- IXAC41（推計震度分布図作図用BUFR）：復号していません。復号できれば、推計震度の格子を地図に描けます。作業量は大きいです。
- WEPA60：原文の表示だけです。

## 2. API・モデルの改善候補

1. **EEW用の構造化オブジェクト**：`Report.eew` を任意の項目として追加し、地域ごとの名前・コード・予想震度（From/To）・長周期階級・警報/予報区分・到達予想時刻・状況、変化理由、精度ランク、見出し、付加文を入れます。iOSの `EEWForecastAreas` と報の推移表は、この項目があれば表示を増やせるようにしてあります。
2. **観測震度の構造化**：現在は bulletin のセクション名「観測 / 県 / 地域 / 市町村 / 地点」を、iOSで分解して階層に戻しています（`IntensityObservationTree`）。`Report.intensity_observation`（コード・未入電・修正を含む木構造）を任意の項目として追加すれば、文字列の分解が不要になります。表示用のセクションは互換性のため残します。
3. **`affected_areas` の拡張**：`code`、`forecast_from`・`forecast_to` を任意の項目として追加します。細分区域のポリゴンがあれば、EEWの予想震度を地図に描けます。
4. **`Hypocenter` の拡張**：`magnitude_condition`、`magnitude_type`、`land_or_sea`、`accuracy`（epicenter・depth・magnitudeのランク）、`source` を追加します。
5. **重複判定の維持**：新しい項目を `Report` に追加するときは、`store.ingest` の指紋計算で `TsunamiAreas` と同様に除外し、旧版で保存した電文との重複判定が変わらないようにします。
6. **保存済み電文の再解析**：`reports.raw` に原電文が残っています。新しい項目を過去の履歴にも付けるには、通知を作らない一回限りの再解析コマンド（`IngestHistorical` と同様の扱い）を用意し、`payload` だけを更新します。現在の方針（保存済みの電文は再正規化しない）を変える判断になるため、実施は別途決めます。

## 3. 運用・構成の改善候補

構成（DMDATA → Go/VPS → APNs → iOS、Live Activity）は今のままで問題ありません。そのうえで、優先度の高い順に挙げます。

1. **切断区間の補完（優先度：高）**：WebSocketの切断中に発表された電文（特にVTSE41・VXSE53）は、現在は取り戻せません。再接続のときに、DMDATAのREST APIで過去の電文を取得できるかを確認します（電文リストAPIとカーソル指定の可否、保持期間、契約上の扱い）。取得できる場合は、`IngestHistorical` で通知せずに保存します。最新のVTSE41だけは発表中の判定に必要なので、通知するかどうかを個別に決めます。
2. **受信遅延の記録**：アプリの電文情報に、配信時刻から受信までの時間を表示するようにしました。APNsへの送信時刻（`deliveries` の受理時刻）もAPIで返せば、「受信→通知送信」の遅延を端末で確認できます。
3. **ディスク使用量**：履歴の自動削除がありません。IXAC41の分割BUFRとWEPA60の原文は増えやすいので、原文だけを一定期間で削除する設定を検討します。
4. **Live Activityの構造化**：今回はサーバーを変更せず、既存の項目（タイトル・要約・震度文字列・報数・状態）でレイアウトだけを刷新しました。`LiveContent` に震央・M・深さ・発生時刻・震度コードを任意の項目として追加すれば、ロック画面とDynamic Islandにも個別の値を表示できます。4KBの上限に注意します。
5. **Watch単体への通知**：現在のWatchは、iPhoneから届いた要約を表示するだけです。iPhoneが手元にない場面を考える場合は、watchOSアプリ向けのAPNs配信を検討します。

## 変更時の手順

サーバーの項目を追加するときは、[API契約](../contracts/README.md)のSchemaとOpenAPI、Goの契約テスト、iOSの `APIModels.swift`（任意の項目として追加）を同時に更新します。そのうえで、VPSを先に更新してからTestFlightを更新します。
