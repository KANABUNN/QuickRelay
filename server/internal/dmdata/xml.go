package dmdata

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	reportNS  = "http://xml.kishou.go.jp/jmaxml1/"
	headNS    = "http://xml.kishou.go.jp/jmaxml1/informationBasis1/"
	bodyNS    = "http://xml.kishou.go.jp/jmaxml1/body/seismology1/"
	elementNS = "http://xml.kishou.go.jp/jmaxml1/elementBasis1/"
)

// Only versions verified against JMA's published samples/specifications are accepted.
// Unknown versions remain in the receipt journal for review instead of being guessed.
var xmlInfoKinds = map[string]struct{ kind, versions string }{
	"VXSE43": {"緊急地震速報", "1.2_0"},
	"VXSE44": {"緊急地震速報", "1.0_0"},
	"VXSE45": {"緊急地震速報", "1.2_0"},
	"VXSE51": {"震度速報", "1.0_0|1.0_1"},
	"VXSE52": {"震源速報", "1.0_0|1.0_1"},
	"VXSE53": {"地震情報", "1.0_0|1.0_1"},
	"VXSE56": {"地震の活動状況等に関する情報", "1.0_0"},
	"VXSE60": {"地震回数情報", "1.0_0"},
	"VXSE61": {"震源要素更新のお知らせ", "1.0_0"},
	"VXSE62": {"長周期地震動に関する観測情報", "1.3_0"},
	"VYSE50": {"南海トラフ地震に関連する情報", "1.1_0"},
	"VYSE51": {"南海トラフ地震に関連する情報", "1.1_0"},
	"VYSE52": {"南海トラフ地震に関連する情報", "1.1_0"},
	"VYSE60": {"北海道・三陸沖後発地震注意情報", "1.3_1"},
	"VTSE41": {"津波警報・注意報・予報", "1.0_1"},
	"VTSE51": {"津波情報", "1.0_1"},
	"VTSE52": {"津波情報", "1.0_1"},
	"VZSE40": {"地震・津波に関するお知らせ", "1.0_0"},
}

type xmlNode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     strings.Builder
	seen     map[xml.Name]bool
	children []*xmlNode
}

func (n *xmlNode) attr(key string) string {
	if n != nil {
		for _, a := range n.attrs {
			if a.Name.Space == "" && a.Name.Local == key {
				return a.Value
			}
		}
	}
	return ""
}
func (n *xmlNode) value() string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.text.String())
}
func (n *xmlNode) list(key string) []*xmlNode {
	if n == nil {
		return nil
	}
	ns := n.name.Space
	if ns == reportNS {
		if key == "Head" {
			ns = headNS
		}
		if key == "Body" {
			ns = bodyNS
		}
	}
	if ns == bodyNS && (key == "Coordinate" || key == "Magnitude" || key == "TsunamiHeight") {
		ns = elementNS
	}
	var out []*xmlNode
	for _, c := range n.children {
		if c.name.Space == ns && c.name.Local == key {
			out = append(out, c)
		}
	}
	return out
}
func (n *xmlNode) child(key string) *xmlNode {
	for _, c := range n.list(key) {
		return c
	}
	return nil
}
func (n *xmlNode) get(path ...string) string {
	for _, key := range path {
		n = n.child(key)
	}
	return n.value()
}

// encoding/xml never resolves external entities. Also reject DTDs/directives,
// multiple roots, duplicate singleton fields, excessive depth and node counts.
func readXML(data []byte) (*xmlNode, error) {
	if len(data) == 0 || len(data) > MaxDecodedBytes {
		return nil, errors.New("invalid XML size")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var root *xmlNode
	var stack []*xmlNode
	count := 0
	repeat := map[string]bool{"Information": true, "Item": true, "Pref": true, "Area": true, "City": true, "Station": true,
		"IntensityStation": true, "Earthquake": true, "Coordinate": true, "Magnitude": true, "Type": true, "LgIntPerPeriod": true, "SvaPerPeriod": true}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("invalid XML")
		}
		switch v := token.(type) {
		case xml.StartElement:
			count++
			if len(stack) >= 64 || count > 100000 {
				return nil, errors.New("XML complexity limit exceeded")
			}
			n := &xmlNode{name: v.Name, attrs: v.Attr, seen: make(map[xml.Name]bool)}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("multiple XML roots")
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				if !repeat[n.name.Local] && parent.seen[n.name] {
					return nil, errors.New("duplicate XML field")
				}
				parent.seen[n.name] = true
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("invalid XML nesting")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(v)) != "" {
					return nil, errors.New("text outside XML document")
				}
			} else {
				_, _ = stack[len(stack)-1].text.Write(v)
			}
		case xml.Directive:
			return nil, errors.New("XML directives are unsupported")
		}
	}
	if root == nil || root.name != (xml.Name{Space: reportNS, Local: "Report"}) || len(stack) != 0 {
		return nil, errors.New("unsupported XML report namespace")
	}
	return root, nil
}

// parseXML maps JMA fields to the existing internal telegram representation.
// The small JSON details document is local adapter data for the existing bulletin
// and region builders, never a DMDATA JSON conversion or a replacement for raw.
func parseXML(data []byte, code string, p product) (converted, []byte, error) {
	var out converted
	root, err := readXML(data)
	if err != nil {
		return out, nil, err
	}
	control, head, body := root.child("Control"), root.child("Head"), root.child("Body")
	if control == nil || head == nil || body == nil || control.get("Title") == "" || head.get("Title") == "" || control.get("Status") == "" {
		return out, nil, errors.New("missing XML control, head or body")
	}
	if control.get("Status") != "通常" {
		return out, nil, ErrIgnored
	}
	kind, ok := xmlInfoKinds[code]
	version := head.get("InfoKindVersion")
	validVersion := false
	for _, allowed := range strings.Split(kind.versions, "|") {
		if version == allowed {
			validVersion = true
		}
	}
	if !ok || head.get("InfoKind") != kind.kind || !validVersion {
		return out, nil, errors.New("unsupported XML information kind or version")
	}
	press, err := time.Parse(time.RFC3339, control.get("DateTime"))
	if err != nil {
		return out, nil, errors.New("invalid XML control time")
	}
	report, err := time.Parse(time.RFC3339, head.get("ReportDateTime"))
	if err != nil {
		return out, nil, errors.New("invalid XML report time")
	}
	info := head.get("InfoType")
	b := object{"text": body.get("Text"), "nextAdvisory": body.get("NextAdvisory")}
	isEEW := p.schema == "eew-information"
	if isEEW {
		if advisory := body.child("NextAdvisory"); advisory != nil && info != "取消" {
			if code == "VXSE43" || advisory.value() != "この情報をもって、緊急地震速報：最終報とします。" {
				return out, nil, errors.New("unknown XML EEW termination advisory")
			}
		}
		b["isCanceled"] = info == "取消"
		// JMA II.21 section 5 defines this fixed field as publication termination.
		// Warning publications have no final report except cancellation.
		b["isLastInfo"] = info == "取消" || code != "VXSE43" && body.child("NextAdvisory") != nil
		b["isWarning"] = code == "VXSE43"
		for _, information := range head.child("Headline").list("Information") {
			key := map[string]string{"緊急地震速報（地方予報区）": "zones", "緊急地震速報（府県予報区）": "prefectures", "緊急地震速報（細分区域）": "regions"}[information.attr("type")]
			if key == "" {
				continue
			}
			for _, item := range information.list("Item") {
				if item.get("Kind", "Code") != "31" {
					continue
				}
				b["isWarning"] = true
				for _, a := range item.child("Areas").list("Area") {
					values, _ := b[key].([]any)
					b[key] = append(values, object{"name": a.get("Name"), "code": a.get("Code")})
				}
			}
		}
	}
	equations := body.list("Earthquake")
	if info != "取消" {
		for _, eq := range equations {
			if _, err := time.Parse(time.RFC3339, eq.get("ArrivalTime")); err != nil || len(eq.list("Magnitude")) == 0 {
				return out, nil, errors.New("missing required XML earthquake content")
			}
		}
	}
	if len(equations) > 0 {
		if p.category == "earthquake" {
			b["earthquake"] = xmlEarthquake(equations[0])
		} else {
			var values []any
			for _, eq := range equations {
				values = append(values, xmlEarthquake(eq))
			}
			b["earthquakes"] = values
		}
	}
	if in := body.child("Intensity"); in != nil {
		details := object{}
		for _, key := range []string{"Forecast", "Observation"} {
			n := in.child(key)
			if n == nil {
				continue
			}
			if key == "Forecast" {
				details["forecastMaxInt"] = xmlRange(n.child("ForecastInt"))
			}
			for _, field := range []string{"MaxInt", "MaxLgInt", "LgCategory"} {
				details[lowerFirst(field)] = n.get(field)
			}
			var values []any
			for _, pref := range n.list("Pref") {
				values = append(values, xmlIntensityArea(pref))
			}
			details["prefectures"] = values
		}
		b["intensity"] = details
	}
	if ts := body.child("Tsunami"); ts != nil {
		t := object{}
		for name, key := range map[string]string{"Forecast": "forecasts", "Observation": "observations", "Estimation": "estimations"} {
			var values []any
			for _, item := range ts.child(name).list("Item") {
				if info != "取消" && name == "Forecast" {
					code := item.get("Category", "Kind", "Code")
					switch code {
					case "00", "50", "51", "52", "53", "60", "62", "71", "72", "73":
					default:
						return out, nil, errors.New("unknown XML tsunami forecast code")
					}
				}
				values = append(values, xmlTsunamiItem(item))
			}
			t[key] = values
		}
		b["tsunami"] = t
	}
	if n := body.child("EarthquakeInfo"); n != nil {
		name := n.get("InfoKind")
		if serial := n.get("InfoSerial", "Name"); serial != "" {
			name += "（" + serial + "）"
		}
		b["earthquakeInfo"] = object{"kind": object{"name": name}, "text": n.get("Text"), "appendix": n.get("Appendix")}
	}
	if counts := body.child("EarthquakeCount"); counts != nil {
		var values []any
		for _, n := range counts.list("Item") {
			values = append(values, object{"type": n.attr("type"), "targetTime": object{"start": n.get("StartTime"), "end": n.get("EndTime")},
				"values": object{"all": n.get("Number"), "felt": n.get("FeltNumber")}})
		}
		b["earthquakeCounts"] = values
	}
	if n := body.child("Comments"); n != nil {
		c := object{"free": n.get("FreeFormComment")}
		for _, key := range []string{"Warning", "Forecast", "Var"} {
			c[lowerFirst(key)] = object{"text": n.get(key+"Comment", "Text")}
		}
		b["comments"] = c
	}
	if info != "取消" {
		missing := false
		switch code {
		case "VXSE43", "VXSE44", "VXSE45", "VXSE52", "VXSE53", "VXSE61":
			missing = len(equations) != 1
		case "VXSE51", "VXSE62":
			observation := body.child("Intensity").child("Observation")
			missing = observation == nil || observation.get("MaxInt") == "" && observation.get("MaxLgInt") == "" && len(observation.list("Pref")) == 0
		case "VTSE41", "VTSE51", "VTSE52":
			ts := body.child("Tsunami")
			missing = len(ts.child("Forecast").list("Item"))+len(ts.child("Observation").list("Item"))+len(ts.child("Estimation").list("Item")) == 0
		case "VXSE60":
			missing = body.child("EarthquakeCount") == nil
		case "VYSE50", "VYSE51", "VYSE52", "VYSE60":
			missing = body.child("EarthquakeInfo") == nil
		case "VXSE56", "VZSE40":
			missing = body.get("Text") == ""
		}
		if missing {
			return out, nil, errors.New("missing required XML bulletin content")
		}
	}
	top := object{"_schema": object{"type": p.schema, "version": "1.0.0"}, "eventId": head.get("EventID"), "serialNo": head.get("Serial"),
		"pressDateTime": press, "status": control.get("Status"), "infoType": info, "reportDateTime": report,
		"title": head.get("Title"), "headline": head.get("Headline", "Text"), "body": b}
	// JMA publishes an explicit expiry for tsunami forecasts when applicable.
	// Missing validity is unknown; notification TTL is not a bulletin expiry.
	if valid := head.child("ValidDateTime"); valid != nil {
		parsed, err := time.Parse(time.RFC3339, valid.value())
		if err != nil || parsed.IsZero() {
			return out, nil, errors.New("invalid XML expiry time")
		}
		top["validDateTime"] = parsed
	}
	details, err := json.Marshal(top)
	if err != nil {
		return out, nil, errors.New("XML adapter failed")
	}
	if err := json.Unmarshal(details, &out); err != nil {
		return out, nil, errors.New("XML normalization failed")
	}
	return out, details, nil
}

func lowerFirst(s string) string { return strings.ToLower(s[:1]) + s[1:] }
func xmlRange(n *xmlNode) object { return object{"from": n.get("From"), "to": n.get("To")} }

var coordinatePattern = regexp.MustCompile(`^([+-][0-9]{2}(?:\.[0-9]+)?)([+-][0-9]{3}(?:\.[0-9]+)?)([+-][0-9]+(?:\.[0-9]+)?)?/$`)

func xmlEarthquake(eq *xmlNode) object {
	h := eq.child("Hypocenter")
	a := h.child("Area")
	coords := object{"latitude": object{"value": nil}, "longitude": object{"value": nil}}
	depth := object{"value": nil, "condition": "不明"}
	for _, n := range a.list("Coordinate") {
		// Update bulletins may also contain degree/minute coordinates. The default
		// degree representation is the one the existing public model can represent.
		if n.attr("type") != "" {
			continue
		}
		m := coordinatePattern.FindStringSubmatch(n.value())
		if len(m) == 0 {
			continue
		}
		lat, e1 := strconv.ParseFloat(m[1], 64)
		lon, e2 := strconv.ParseFloat(m[2], 64)
		if e1 != nil || e2 != nil || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
			continue
		}
		coords["latitude"], coords["longitude"] = object{"value": strconv.FormatFloat(lat, 'f', -1, 64)}, object{"value": strconv.FormatFloat(lon, 'f', -1, 64)}
		if m[3] != "" {
			altitude, err := strconv.ParseFloat(m[3], 64)
			if err == nil && altitude <= 0 && altitude >= -1000000 {
				km := -altitude / 1000
				depth = object{"value": strconv.FormatFloat(km, 'f', -1, 64), "condition": ""}
				if km == 0 {
					depth["condition"] = "ごく浅い"
				}
				if km == 700 {
					depth["condition"] = "７００ｋｍ以上"
				}
			}
		}
		break
	}
	ac := h.child("Accuracy")
	var selected *xmlNode
	for _, candidate := range eq.list("Magnitude") {
		if candidate.attr("type") == "Mj" {
			selected = candidate
			break
		}
		if len(eq.list("Magnitude")) == 1 {
			selected = candidate
		}
	}
	magnitude := xmlScalar(selected)
	return object{"originTime": text(eq.get("OriginTime")), "condition": eq.get("Condition"), "magnitude": magnitude,
		"hypocenter": object{"name": a.get("Name"), "coordinate": coords, "depth": depth,
			"accuracy": object{"epicenters": []string{ac.child("Epicenter").attr("rank"), ac.child("Epicenter").attr("rank2")},
				"magnitudeCalculation": ac.child("MagnitudeCalculation").attr("rank")}}}
}

func xmlScalar(n *xmlNode) object {
	v := n.value()
	var value any
	if number(&v) != nil {
		value = v
	}
	return object{"value": value, "unit": n.attr("unit"), "condition": n.attr("condition")}
}

func xmlIntensityArea(n *xmlNode) object {
	out := object{"name": n.get("Name"), "code": n.get("Code")}
	for _, key := range []string{"MaxInt", "Int", "MaxLgInt", "LgInt"} {
		out[lowerFirst(key)] = n.get(key)
	}
	if f := n.child("ForecastInt"); f != nil {
		out["forecastMaxInt"] = xmlRange(f)
	}
	if s := n.child("Sva"); s != nil {
		out["sva"] = xmlScalar(s)
	}
	for _, pair := range [][2]string{{"Area", "regions"}, {"City", "cities"}, {"IntensityStation", "stations"}} {
		var values []any
		for _, child := range n.list(pair[0]) {
			values = append(values, xmlIntensityArea(child))
		}
		out[pair[1]] = values
	}
	return out
}

func xmlTsunamiItem(n *xmlNode) object {
	out := object{"name": n.get("Area", "Name"), "code": n.get("Area", "Code"), "kind": object{
		"name": n.get("Category", "Kind", "Name"), "code": n.get("Category", "Kind", "Code"),
		"lastKind": object{"name": n.get("Category", "LastKind", "Name"), "code": n.get("Category", "LastKind", "Code")}}}
	for _, field := range []string{"FirstHeight", "MaxHeight"} {
		if w := n.child(field); w != nil {
			out[lowerFirst(field)] = xmlWave(w)
		}
	}
	var stations []any
	for _, s := range n.list("Station") {
		station := object{"name": s.get("Name"), "code": s.get("Code"), "sensor": s.get("Sensor"), "highTideDateTime": s.get("HighTideDateTime")}
		for _, field := range []string{"FirstHeight", "MaxHeight"} {
			if w := s.child(field); w != nil {
				station[lowerFirst(field)] = xmlWave(w)
			}
		}
		stations = append(stations, station)
	}
	out["stations"] = stations
	return out
}
func xmlWave(n *xmlNode) object {
	out := object{}
	for _, field := range []string{"ArrivalTime", "DateTime", "Condition", "Initial", "Revise"} {
		out[lowerFirst(field)] = n.get(field)
	}
	if h := n.child("TsunamiHeight"); h != nil {
		height := xmlScalar(h)
		description := h.attr("description")
		if height["value"] == nil && description != "" {
			height["condition"] = description
		}
		if strings.Contains(description, "以上") {
			height["over"] = true
		}
		// The existing over flag displays >=. Preserve strict bounds verbatim.
		if strings.Contains(description, "超") || strings.Contains(description, "未満") {
			height["value"], height["condition"] = nil, description
		}
		if strings.Contains(description, "上昇中") {
			height["condition"] = join(str(height["condition"]), "上昇中")
		}
		out["height"] = height
	}
	return out
}
