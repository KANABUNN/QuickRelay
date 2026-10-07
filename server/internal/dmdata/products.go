package dmdata

type product struct{ classification, schema, category, eventType, title string }

// Source: https://dmdata.jp/docs/telegrams/ (earthquake-related contracted categories).
// VXSE42 is a distribution test, excluded from operational ingestion alongside training messages.
var products = map[string]product{
	"VXSE44": {"eew.forecast", "eew-information", "earthquake", "eew_forecast", "緊急地震速報（予報）"},
	"VXSE45": {"eew.forecast", "eew-information", "earthquake", "eew_forecast", "緊急地震速報（予報）"},
	"VXSE43": {"eew.warning", "eew-information", "earthquake", "eew_warning", "緊急地震速報（警報）"},
	"VXSE51": {"telegram.earthquake", "earthquake-information", "earthquake", "earthquake_info", "震度速報"},
	"VXSE52": {"telegram.earthquake", "earthquake-information", "earthquake", "earthquake_info", "震源情報"},
	"VXSE53": {"telegram.earthquake", "earthquake-information", "earthquake", "earthquake_info", "震源・震度情報"},
	"VXSE56": {"telegram.earthquake", "earthquake-explanation", "advisory", "seismic_advisory", "地震の活動状況等に関する情報"},
	"VXSE60": {"telegram.earthquake", "earthquake-counts", "advisory", "seismic_advisory", "地震回数に関する情報"},
	"VXSE61": {"telegram.earthquake", "earthquake-hypocenter-update", "earthquake", "earthquake_update", "顕著な地震の震源要素更新のお知らせ"},
	"VXSE62": {"telegram.earthquake", "earthquake-information", "earthquake", "earthquake_info", "長周期地震動に関する観測情報"},
	"VYSE50": {"telegram.earthquake", "earthquake-nankai", "advisory", "nankai_info", "南海トラフ地震臨時情報"},
	"VYSE51": {"telegram.earthquake", "earthquake-nankai", "advisory", "nankai_info", "南海トラフ地震関連解説情報"},
	"VYSE52": {"telegram.earthquake", "earthquake-nankai", "advisory", "nankai_info", "南海トラフ地震関連解説情報（定例）"},
	"VYSE60": {"telegram.earthquake", "earthquake-nankai", "advisory", "seismic_advisory", "北海道・三陸沖後発地震注意情報"},
	"VTSE41": {"telegram.earthquake", "tsunami-information", "tsunami", "tsunami_info", "津波警報・注意報・予報"},
	"VTSE51": {"telegram.earthquake", "tsunami-information", "tsunami", "tsunami_info", "津波情報"},
	"VTSE52": {"telegram.earthquake", "tsunami-information", "tsunami", "tsunami_info", "沖合の津波観測に関する情報"},
	"VZSE40": {"telegram.earthquake", "earthquake-information", "advisory", "seismic_advisory", "地震・津波に関するお知らせ"},
	"WEPA60": {"telegram.earthquake", "", "tsunami", "tsunami_info", "国際津波関連情報（国内向け）"},
	"IXAC41": {"telegram.earthquake", "", "advisory", "earthquake_data", "推計震度分布図作図用データ"},
}
