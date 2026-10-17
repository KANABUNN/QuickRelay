package dmdata

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"quakerelay/server/internal/model"
)

func TestConvertedBulletinValidity(t *testing.T) {
	want := time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		include bool
		value   any
		valid   bool
	}{
		{name: "missing"},
		{name: "explicit_null", include: true},
		{name: "explicit_expiry", include: true, value: "2026-10-05T15:00:00+09:00", valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := bulletinFrame(t, "VTSE41", func(c map[string]any) {
				if tc.include {
					c["validDateTime"] = tc.value
				}
			})
			r, err := Normalize(raw, bulletinTime)
			if err != nil {
				t.Fatal(err)
			}
			if tc.valid {
				if r.ValidUntil == nil || !r.ValidUntil.Equal(want) || r.ValidUntil.Location() != time.UTC {
					t.Fatal("source expiry lost or not normalized to UTC", r.ValidUntil)
				}
			} else if r.ValidUntil != nil {
				t.Fatal("expiry invented when source validity is missing", r.ValidUntil)
			}
			wire, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(wire, []byte(`"valid_until"`)) != tc.valid {
				t.Fatal("optional expiry changed the wire format", string(wire))
			}
			var restored model.Report
			if err = json.Unmarshal(wire, &restored); err != nil {
				t.Fatal(err)
			}
			if tc.valid && (restored.ValidUntil == nil || !restored.ValidUntil.Equal(want)) {
				t.Fatal("persisted expiry lost", restored.ValidUntil)
			}
		})
	}
}

func TestConvertedBulletinRejectsInvalidValidity(t *testing.T) {
	for _, value := range []any{"", "not-a-time", "2026-10-05", "2026-02-30T15:00:00+09:00", "0001-01-01T00:00:00Z", 123, false} {
		t.Run(string(mustJSON(t, value)), func(t *testing.T) {
			raw := bulletinFrame(t, "VTSE41", func(c map[string]any) {
				c["validDateTime"] = value
			})
			if _, err := Normalize(raw, bulletinTime); err == nil {
				t.Fatal("invalid source expiry accepted", value)
			}
		})
	}
}

func TestXMLBulletinValidity(t *testing.T) {
	// The official release sample has no validity. Extend its header in memory
	// to exercise the documented optional field without changing the sample.
	source := fixture(t, "38-39_02_04_250206_VTSE41.xml")
	want := time.Date(2015, 11, 14, 3, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		header string
		valid  bool
		fail   bool
	}{
		{name: "missing"},
		{name: "explicit_expiry", header: "<ValidDateTime>2015-11-14T12:00:00+09:00</ValidDateTime>", valid: true},
		{name: "empty", header: "<ValidDateTime/>", fail: true},
		{name: "malformed", header: "<ValidDateTime>not-a-time</ValidDateTime>", fail: true},
		{name: "date_only", header: "<ValidDateTime>2015-11-14</ValidDateTime>", fail: true},
		{name: "invalid_date", header: "<ValidDateTime>2015-02-30T12:00:00+09:00</ValidDateTime>", fail: true},
		{name: "zero", header: "<ValidDateTime>0001-01-01T00:00:00Z</ValidDateTime>", fail: true},
		{name: "duplicate", header: "<ValidDateTime>2015-11-14T12:00:00+09:00</ValidDateTime><ValidDateTime>2015-11-14T13:00:00+09:00</ValidDateTime>", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Replace(source, []byte("</Head>"), []byte(tc.header+"</Head>"), 1)
			r, err := Normalize(transport(t, "VTSE41", "xml", data), xmlNow)
			if tc.fail {
				if err == nil {
					t.Fatal("invalid XML source expiry accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.valid {
				if r.ValidUntil == nil || !r.ValidUntil.Equal(want) || r.ValidUntil.Location() != time.UTC {
					t.Fatal("XML source expiry lost or not normalized to UTC", r.ValidUntil)
				}
			} else if r.ValidUntil != nil {
				t.Fatal("expiry invented when XML validity is missing", r.ValidUntil)
			}
		})
	}
}

func mustJSON(t testing.TB, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
