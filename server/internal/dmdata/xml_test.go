package dmdata

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const firstEEW = "77_01_01_240613_VXSE45.xml"

var xmlNow = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "jma", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func transport(t testing.TB, code, format string, body []byte) []byte {
	t.Helper()
	var z bytes.Buffer
	gz := gzip.NewWriter(&z)
	if _, err := gz.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	e := Envelope{Type: "data", ID: "xml-test-" + code, Classification: products[code].classification, Format: format,
		Encoding: "base64", Compression: "gzip", Body: base64.StdEncoding.EncodeToString(z.Bytes())}
	e.Head.Type = code
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func operational(data []byte) []byte {
	data = bytes.ReplaceAll(data, []byte("<Status>訓練</Status>"), []byte("<Status>通常</Status>"))
	return bytes.ReplaceAll(data, []byte("<Status>試験</Status>"), []byte("<Status>通常</Status>"))
}
func mutateEnvelope(t *testing.T, data []byte, change func(*Envelope)) []byte {
	t.Helper()
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	change(&e)
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestPublishedXMLFixtures(t *testing.T) {
	manifest, err := os.ReadFile("testdata/jma/SHA256SUMS.json")
	if err != nil {
		t.Fatal(err)
	}
	var sums map[string]string
	if err = json.Unmarshal(manifest, &sums); err != nil {
		t.Fatal(err)
	}
	for name, want := range sums {
		t.Run(name, func(t *testing.T) {
			data := fixture(t, name)
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != want {
				t.Fatal("official sample modified")
			}
			code := strings.TrimSuffix(name[strings.LastIndex(name, "_")+1:], ".xml")
			r, err := Normalize(transport(t, code, "xml", data), xmlNow)
			training := bytes.Contains(data, []byte("<Status>訓練</Status>")) || bytes.Contains(data, []byte("<Status>試験</Status>"))
			if training && !errors.Is(err, ErrIgnored) {
				t.Fatal("non-operational XML accepted", err)
			}
			if training {
				r, err = Normalize(transport(t, code, "xml", operational(data)), xmlNow)
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.EventType == "" || r.Category != products[code].category || r.InfoType == "" {
				t.Fatal("identity or category lost", r)
			}
			if !r.IsEEW() && (r.Bulletin == nil || r.Bulletin.Document.Format != "xml" || r.Bulletin.Document.ByteCount == 0) {
				t.Fatal("source document missing")
			}
			if r.IsEEW() && r.Serial == nil {
				t.Fatal("EEW sequence lost")
			}
		})
	}
}
func TestXMLAndJSONHaveIdenticalEEWSemantics(t *testing.T) {
	xml := transport(t, "VXSE45", "xml", fixture(t, firstEEW))
	data, err := os.ReadFile("testdata/eew-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	converted := transport(t, "VXSE45", "json", data)
	a, err := Normalize(xml, xmlNow)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Normalize(converted, xmlNow)
	if err != nil {
		t.Fatal(err)
	}
	a.Raw, b.Raw = nil, nil
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("transport changed normalized report:\nXML %+v\nJSON %+v", a, b)
	}
	if a.Hypocenter.Status != "estimated" || *a.DepthKM != 40 || *a.Magnitude != 4.2 || *a.Latitude != 33.1 || *a.Longitude != 132.4 || *a.MaxIntensity != "3" {
		t.Fatal("numeric conversion", a)
	}
}
func TestXMLWarningCancellationCorrectionAndFinal(t *testing.T) {
	data := fixture(t, "37_01_01_240613_VXSE43.xml")
	r, err := Normalize(transport(t, "VXSE43", "xml", data), xmlNow)
	if err != nil || !r.Warning || r.Final || r.Hypocenter == nil || r.Magnitude != nil || r.DepthKM != nil || r.Hypocenter.Magnitude != nil || r.Hypocenter.DepthKM != nil {
		t.Fatal("warning semantics", r, err)
	}
	if !strings.Contains(r.Body, "対象地域：愛媛、高知、大分、山口、広島") || !strings.Contains(r.Body, "予想最大震度5弱") || len(r.AffectedAreas) < 10 {
		t.Fatal("warning areas lost", r.Body, r.AffectedAreas)
	}
	data = fixture(t, "77_01_33_240613_VXSE45.xml")
	r, err = Normalize(transport(t, "VXSE45", "xml", data), xmlNow)
	if err != nil || !r.Cancelled || !r.Final || r.Warning || r.Hypocenter != nil || r.EventType != "eew_cancel" {
		t.Fatal("cancel semantics", r, err)
	}
	data = fixture(t, firstEEW)
	data = bytes.Replace(data, []byte("</Body>"), []byte("<NextAdvisory>この情報をもって、緊急地震速報：最終報とします。</NextAdvisory></Body>"), 1)
	r, err = Normalize(transport(t, "VXSE45", "xml", data), xmlNow)
	if err != nil || !r.Final || r.Cancelled || !strings.Contains(r.Title, "最終") {
		t.Fatal("final report", r, err)
	}
	data = operational(fixture(t, "32-35_01_03_240613_VXSE53.xml"))
	original, err := Normalize(transport(t, "VXSE53", "xml", data), xmlNow)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("<InfoType>発表</InfoType>"), []byte("<InfoType>訂正</InfoType>"), 1)
	corrected, err := Normalize(transport(t, "VXSE53", "xml", data), xmlNow)
	if err != nil || corrected.InfoType != "訂正" || !strings.Contains(corrected.Title, "訂正") || !corrected.NewerThan(original) {
		t.Fatal("correction lost", corrected, err)
	}
}
func TestXMLAssumedLowAccuracyAndUnknownNumbers(t *testing.T) {
	source := fixture(t, firstEEW)
	for _, tc := range []struct{ name, old, new, status string }{
		{"plum", "<Earthquake>", "<Earthquake><Condition>仮定震源要素</Condition>", "assumed"},
		{"level", `<MagnitudeCalculation rank="2">`, `<MagnitudeCalculation rank="8">`, "assumed"},
		{"single", `<Epicenter rank="4" rank2="4">`, `<Epicenter rank="1" rank2="1">`, "low_accuracy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Replace(source, []byte(tc.old), []byte(tc.new), 1)
			r, err := Normalize(transport(t, "VXSE45", "xml", data), xmlNow)
			if err != nil || r.Hypocenter == nil || r.Hypocenter.Status != tc.status || !strings.Contains(r.Body, r.Hypocenter.Note) {
				t.Fatal(r, err)
			}
			if tc.status == "assumed" && (r.Latitude != nil || r.DepthKM != nil || r.Magnitude != nil) {
				t.Fatal("assumed values became unqualified")
			}
		})
	}
	data := bytes.Replace(source, []byte("+33.1+132.4-40000/"), []byte("+33.1+132.4/"), 1)
	data = bytes.Replace(data, []byte(">4.2</jmx_eb:Magnitude>"), []byte(">NaN</jmx_eb:Magnitude>"), 1)
	r, err := Normalize(transport(t, "VXSE45", "xml", data), xmlNow)
	if err != nil || r.DepthKM != nil || r.Magnitude != nil || r.Hypocenter.DepthKM != nil || r.Latitude == nil {
		t.Fatal("unknown invented as zero", r, err)
	}
}
func TestXMLTsunamiReleaseCancelAndObservation(t *testing.T) {
	data := fixture(t, "38-39_02_01_250206_VTSE41.xml")
	r, err := Normalize(transport(t, "VTSE41", "xml", data), xmlNow)
	if err != nil || !r.Warning || r.EventType != "tsunami_warning" || r.Cancelled || len(r.AffectedAreas) == 0 {
		t.Fatal(r, err)
	}
	data = bytes.Replace(data, []byte("<InfoType>発表</InfoType>"), []byte("<InfoType>取消</InfoType>"), 1)
	cancel, err := Normalize(transport(t, "VTSE41", "xml", data), xmlNow)
	if err != nil || !cancel.Cancelled || cancel.Warning || cancel.Final || cancel.EventID != r.EventID {
		t.Fatal("cancel interpreted as release", cancel, err)
	}
	release, err := Normalize(transport(t, "VTSE41", "xml", fixture(t, "38-39_02_04_250206_VTSE41.xml")), xmlNow)
	if err != nil || release.Warning || release.Cancelled || release.EventType != "tsunami_info" {
		t.Fatal("release", release, err)
	}
	obs, err := Normalize(transport(t, "VTSE52", "xml", operational(fixture(t, "32-39_12_05_250206_VTSE52.xml"))), xmlNow)
	text, _ := json.Marshal(obs.Bulletin)
	if err != nil || !strings.Contains(string(text), "沿岸の推定") || !strings.Contains(string(text), "津波観測") || !strings.Contains(string(text), "1.8m") {
		t.Fatal("observation/estimation lost", string(text), err)
	}
}
func TestXMLMalformedMissingAndUnknown(t *testing.T) {
	source := fixture(t, firstEEW)
	for name, data := range map[string][]byte{
		"unknown_final_advisory": bytes.Replace(source, []byte("</Body>"), []byte("<NextAdvisory>未知の次回発表予定</NextAdvisory></Body>"), 1),
		"truncated":              source[:len(source)/2],
		"wrong_namespace":        bytes.ReplaceAll(source, []byte(reportNS), []byte("urn:unknown")),
		"wrong_head_namespace":   bytes.ReplaceAll(source, []byte(headNS), []byte("urn:unknown")),
		"unknown_version":        bytes.Replace(source, []byte("1.2_0"), []byte("9.0_0"), 1),
		"missing_serial":         bytes.Replace(source, []byte("<Serial>1</Serial>"), nil, 1),
		"duplicate_serial":       bytes.Replace(source, []byte("<Serial>1</Serial>"), []byte("<Serial>1</Serial><Serial>2</Serial>"), 1),
		"missing_identity":       bytes.Replace(source, []byte("<EventID>20240417231454</EventID>"), nil, 1),
		"missing_time":           bytes.Replace(source, []byte("<ReportDateTime>2024-04-17T23:14:57+09:00</ReportDateTime>"), nil, 1),
		"missing_body":           []byte(`<Report xmlns="` + reportNS + `"/>`),
		"missing_eew_content":    bytes.Replace(source, []byte("<Earthquake>"), []byte("<Unknown>"), 1),
		"multiple_roots":         append(append([]byte(nil), source...), source...),
		"dtd":                    []byte(`<!DOCTYPE Report [<!ENTITY ext SYSTEM "file:///private">]><Report xmlns="` + reportNS + `">&ext;</Report>`),
		"deep":                   []byte(strings.Repeat("<x>", 70) + strings.Repeat("</x>", 70)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Normalize(transport(t, "VXSE45", "xml", data), xmlNow); err == nil {
				t.Fatal("invalid XML accepted")
			}
		})
	}
	valid := transport(t, "VXSE45", "xml", source)
	for _, change := range []func(*Envelope){
		func(e *Envelope) { e.Encoding = "unknown" }, func(e *Envelope) { e.Compression = "unknown" }, func(e *Envelope) { e.Body = "not-base64" },
		func(e *Envelope) { e.Compression = "gzip"; e.Encoding = "utf-8" }, func(e *Envelope) { e.ID = "" },
	} {
		if _, err := Normalize(mutateEnvelope(t, valid, change), xmlNow); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	for _, format := range []string{"future", "binary", "a/n"} {
		if _, err := Normalize(mutateEnvelope(t, valid, func(e *Envelope) { e.Format = format }), xmlNow); !errors.Is(err, ErrIgnored) {
			t.Fatal("unknown format normalized", format, err)
		}
	}
}
func TestXMLZipGzipAndPlainBodies(t *testing.T) {
	data := fixture(t, firstEEW)
	var z bytes.Buffer
	zw := zip.NewWriter(&z)
	f, err := zw.Create("telegram.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(data)
	_ = zw.Close()
	valid := transport(t, "VXSE45", "xml", data)
	for _, change := range []func(*Envelope){
		func(e *Envelope) { e.Compression = "zip"; e.Body = base64.StdEncoding.EncodeToString(z.Bytes()) },
		func(e *Envelope) { e.Compression = ""; e.Encoding = "utf-8"; e.Body = string(data) },
		func(e *Envelope) {
			e.Compression = ""
			e.Encoding = "base64"
			e.Body = base64.StdEncoding.EncodeToString(data)
		},
	} {
		if _, err := Normalize(mutateEnvelope(t, valid, change), xmlNow); err != nil {
			t.Fatal(err)
		}
	}
	corrupted := z.Bytes()[:len(z.Bytes())/2]
	if _, err := Normalize(mutateEnvelope(t, valid, func(e *Envelope) { e.Compression = "zip"; e.Body = base64.StdEncoding.EncodeToString(corrupted) }), xmlNow); err == nil {
		t.Fatal("truncated zip accepted")
	}
}

// These content-matched inputs isolate local decode/parse/normalize work.
// They do not measure upstream JSON conversion, transport latency or APNs.
func BenchmarkNormalizeFormats(b *testing.B) {
	data, err := os.ReadFile("testdata/eew-reference.json")
	if err != nil {
		b.Fatal(err)
	}
	inputs := map[string][]byte{"raw_xml": transport(b, "VXSE45", "xml", fixture(b, firstEEW)), "converted_json": transport(b, "VXSE45", "json", data)}
	for name, input := range inputs {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			for b.Loop() {
				if _, err := Normalize(input, xmlNow); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Set this to an extracted official JMA sample set for the full published corpus.
func TestPublishedXMLCorpus(t *testing.T) {
	path := os.Getenv("DMDATA_JMA_SAMPLES_DIR")
	if path == "" {
		t.Skip("optional full official corpus; see docs/dmdata-raw-migration.md")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	tested := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".xml") {
			continue
		}
		code := strings.TrimSuffix(name[strings.LastIndex(name, "_")+1:], ".xml")
		if _, ok := xmlInfoKinds[code]; !ok {
			continue
		}
		tested++
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(path, name))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Normalize(transport(t, code, "xml", operational(data)), xmlNow); err != nil {
				t.Fatal(err)
			}
		})
	}
	if tested < 21 {
		t.Fatal("sample directory does not contain the expected earthquake/tsunami corpus")
	}
	t.Logf("Validated %d official XML samples (training/test Status replaced only for parsing coverage).", tested)
}

func TestXMLMissingRequiredContentAndUnknownForecastCodes(t *testing.T) {
	data := fixture(t, firstEEW)
	data = bytes.Replace(data, []byte("<ArrivalTime>2024-04-17T23:14:54+09:00</ArrivalTime>"), nil, 1)
	if _, err := Normalize(transport(t, "VXSE45", "xml", data), xmlNow); err == nil {
		t.Fatal("missing earthquake detection time accepted")
	}
	tsunami := fixture(t, "38-39_02_01_250206_VTSE41.xml")
	tsunami = bytes.ReplaceAll(tsunami, []byte("<Code>62</Code>"), []byte("<Code>99</Code>"))
	if _, err := Normalize(transport(t, "VTSE41", "xml", tsunami), xmlNow); err == nil {
		t.Fatal("unknown tsunami code guessed")
	}
	root, err := readXML(fixture(t, "38-39_02_01_250206_VTSE41.xml"))
	if err != nil {
		t.Fatal(err)
	}
	empty := fixture(t, "38-39_02_01_250206_VTSE41.xml")
	start, end := bytes.Index(empty, []byte("<Tsunami>")), bytes.Index(empty, []byte("</Tsunami>"))
	empty = append(append(append([]byte(nil), empty[:start]...), []byte("<Tsunami/>")...), empty[end+len("</Tsunami>"):]...)
	if _, err := Normalize(transport(t, "VTSE41", "xml", empty), xmlNow); err == nil {
		t.Fatal("empty tsunami content accepted")
	}
	body := root.child("Body")
	if len(body.child("Tsunami").child("Forecast").list("Item")) == 0 {
		t.Fatal("test fixture has no forecast")
	}
}
func TestXMLDepthAndHeightQualifiers(t *testing.T) {
	for _, tc := range []struct {
		coordinate, condition string
		want                  *int
	}{
		{"+33.1+132.4+0/", "ごく浅い", func() *int { n := 0; return &n }()},
		{"+33.1+132.4-700000/", "７００ｋｍ以上", func() *int { n := 700; return &n }()},
		{"+33.1+132.4/", "不明", nil},
		{"+99.1+132.4-40000/", "不明", nil},
	} {
		data := bytes.Replace(fixture(t, firstEEW), []byte("+33.1+132.4-40000/"), []byte(tc.coordinate), 1)
		r, err := Normalize(transport(t, "VXSE45", "xml", data), xmlNow)
		if err != nil || !reflect.DeepEqual(r.DepthKM, tc.want) || r.Hypocenter.DepthCondition != tc.condition {
			t.Fatal("depth qualification lost", r.Hypocenter, err)
		}
	}
	for _, tc := range []struct{ value, description, condition, want string }{
		{"NaN", "巨大", "不明", "巨大"},
		{"10", "１０ｍ超", "", "１０ｍ超"},
		{"0.2", "０．２ｍ未満", "", "０．２ｍ未満"},
		{"2.8", "２．８ｍ以上", "", "2.8m以上"},
		{"1.2", "１．２ｍ 上昇中", "", "上昇中"},
	} {
		n := &xmlNode{name: xml.Name{Space: bodyNS, Local: "MaxHeight"}}
		h := &xmlNode{name: xml.Name{Space: elementNS, Local: "TsunamiHeight"}, attrs: []xml.Attr{
			{Name: xml.Name{Local: "unit"}, Value: "m"}, {Name: xml.Name{Local: "description"}, Value: tc.description}, {Name: xml.Name{Local: "condition"}, Value: tc.condition}}}
		h.text.WriteString(tc.value)
		n.children = []*xmlNode{h}
		encoded, err := json.Marshal(xmlWave(n))
		if err != nil {
			t.Fatal(err)
		}
		var generic map[string]any
		if err = json.Unmarshal(encoded, &generic); err != nil {
			t.Fatal(err)
		}
		if value := wave(generic); !strings.Contains(value, tc.want) {
			t.Fatal("height qualifier lost", value, tc.want)
		}
	}
}
func FuzzNormalizeXML(f *testing.F) {
	f.Add(fixture(f, firstEEW))
	f.Add([]byte("<Report>"))
	f.Add([]byte("<!DOCTYPE Report>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxMessageBytes/2 {
			t.Skip()
		}
		e := Envelope{Type: "data", ID: "fuzz", Classification: "eew.forecast", Format: "xml", Encoding: "utf-8", Body: string(data)}
		e.Head.Type = "VXSE45"
		frame, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Normalize(frame, xmlNow)
		if err == nil {
			if _, err = json.Marshal(r); err != nil {
				t.Fatal("nonfinite value escaped normalization", err)
			}
		}
	})
}
