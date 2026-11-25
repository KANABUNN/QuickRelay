package eewbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"quakerelay/server/internal/eew"
)

// schemaCheck validates the subset of JSON Schema used by contracts/eew-wss.schema.json:
// $ref, oneOf, allOf, type, const, enum, required, properties, items, pattern,
// minLength and minimum. format is not checked.
type schemaCheck struct{ root map[string]any }

func loadSchema(t *testing.T) schemaCheck {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "eew-wss.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err = json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return schemaCheck{root}
}

func (c schemaCheck) def(name string) map[string]any {
	return c.root["$defs"].(map[string]any)[name].(map[string]any)
}

func typeOK(want string, v any) bool {
	switch want {
	case "null":
		return v == nil
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	}
	return false
}

func (c schemaCheck) validate(s map[string]any, v any, path string) error {
	if ref, ok := s["$ref"].(string); ok {
		return c.validate(c.def(strings.TrimPrefix(ref, "#/$defs/")), v, path)
	}
	if all, ok := s["allOf"].([]any); ok {
		for _, sub := range all {
			if err := c.validate(sub.(map[string]any), v, path); err != nil {
				return err
			}
		}
	}
	if one, ok := s["oneOf"].([]any); ok {
		matched := 0
		var last error
		for _, sub := range one {
			if err := c.validate(sub.(map[string]any), v, path); err == nil {
				matched++
			} else {
				last = err
			}
		}
		if matched != 1 {
			return fmt.Errorf("%s: %d oneOf branches match (%v)", path, matched, last)
		}
	}
	if want, ok := s["const"]; ok && !reflect.DeepEqual(want, v) {
		return fmt.Errorf("%s: %v is not %v", path, v, want)
	}
	if enum, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			found = found || reflect.DeepEqual(e, v)
		}
		if !found {
			return fmt.Errorf("%s: %v not in %v", path, v, enum)
		}
	}
	switch want := s["type"].(type) {
	case string:
		if !typeOK(want, v) {
			return fmt.Errorf("%s: %v is not %s", path, v, want)
		}
	case []any:
		ok := false
		for _, w := range want {
			ok = ok || typeOK(w.(string), v)
		}
		if !ok {
			return fmt.Errorf("%s: %v is not %v", path, v, want)
		}
	}
	if text, ok := v.(string); ok {
		if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(text) {
			return fmt.Errorf("%s: %q does not match %s", path, text, p)
		}
		if n, ok := s["minLength"].(float64); ok && len(text) < int(n) {
			return fmt.Errorf("%s: too short", path)
		}
	}
	if n, ok := v.(float64); ok {
		if m, ok := s["minimum"].(float64); ok && n < m {
			return fmt.Errorf("%s: below minimum", path)
		}
	}
	if obj, ok := v.(map[string]any); ok {
		for _, r := range asList(s["required"]) {
			if _, present := obj[r.(string)]; !present {
				return fmt.Errorf("%s: missing %s", path, r)
			}
		}
		props, _ := s["properties"].(map[string]any)
		for key, sub := range props {
			if value, present := obj[key]; present {
				if err := c.validate(sub.(map[string]any), value, path+"."+key); err != nil {
					return err
				}
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		for i, item := range v.([]any) {
			if err := c.validate(items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func asList(v any) []any { l, _ := v.([]any); return l }

func (c schemaCheck) check(t *testing.T, name string, data []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(name, err)
	}
	if err := c.validate(c.root, v, name); err != nil {
		t.Fatal(err)
	}
}

func TestContractExamplesMatchGoTypes(t *testing.T) {
	c := loadSchema(t)
	for file, target := range map[string]any{
		"eew-subscribe.valid.json":     &SubscribeMessage{},
		"eew-sync-begin.valid.json":    &SyncBeginMessage{},
		"eew-snapshot.valid.json":      &SnapshotMessage{},
		"eew-state.valid.json":         &StateMessage{},
		"eew-sync-complete.valid.json": &SyncCompleteMessage{},
		"eew-health.valid.json":        &HealthMessage{},
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "examples", file))
		if err != nil {
			t.Fatal(err)
		}
		c.check(t, file, data)
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err = dec.Decode(target); err != nil {
			t.Fatal(file, err)
		}
		again, _ := json.Marshal(target)
		var a, b any
		_ = json.Unmarshal(data, &a)
		_ = json.Unmarshal(again, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s does not round-trip:\n%s", file, again)
		}
	}
}

// Messages built from real sample telegrams conform to the published schema.
func TestServerMessagesMatchSchema(t *testing.T) {
	c := loadSchema(t)
	s := openBridge(t)
	ctx := context.Background()
	steps := []struct {
		frame []byte
		relay *eew.RelayState
	}{
		{frame(t, "VXSE45", "f1", sample(t, "77_01_01_240613_VXSE45.xml")), applied(1)},
		{frame(t, "VXSE43", "w1", sample(t, "37_01_01_240613_VXSE43.xml")), applied(1)},
		{frame(t, "VXSE45", "r2", withSerial(sample(t, "37_01_01_240613_VXSE43.xml"), 2)), applied(2)},
		{frame(t, "VXSE43", "w2", withSerial(bytes.ReplaceAll(sample(t, "37_01_01_240613_VXSE43.xml"), []byte("<Code>00</Code></LastKind>"), []byte("<Code>31</Code></LastKind>")), 2)), applied(2)},
		{frame(t, "VXSE45", "c3", withSerial(sample(t, "77_01_33_240613_VXSE45.xml"), 3)), &eew.RelayState{Cancelled: true}},
	}
	for i, st := range steps {
		if out, err := s.Process(ctx, live(st.frame, st.relay), time.Now()); err != nil || out.Outcome != eew.Applied {
			t.Fatal(i, out, err)
		}
	}
	if _, err := s.Reconcile(ctx, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	for _, resume := range []*Resume{nil, {StreamID: StreamProduction, Seq: 0}} {
		view, err := s.SyncView(ctx, StreamProduction, resume, time.Now().Add(-time.Hour), 100)
		if err != nil {
			t.Fatal(err)
		}
		if resume != nil {
			stream, _ := s.Stream(ctx, StreamProduction)
			if view, err = s.SyncView(ctx, StreamProduction, &Resume{StreamID: StreamProduction, Epoch: stream.Epoch}, time.Now(), 100); err != nil || view.Mode != "resume" {
				t.Fatal(view.Mode, err)
			}
		}
		frames, err := syncFrames(view)
		if err != nil {
			t.Fatal(err)
		}
		for i, f := range frames {
			c.check(t, fmt.Sprintf("%s frame %d", view.Mode, i), f)
		}
	}
	health, _ := json.Marshal((&Server{Health: &Health{}}).health(time.Now()))
	c.check(t, "health", health)
}

func TestSchemaCheckRejectsDrift(t *testing.T) {
	c := loadSchema(t)
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "examples", "eew-state.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(m map[string]any){
		"numeric seq":    func(m map[string]any) { m["seq"] = 1052 },
		"missing cause":  func(m map[string]any) { delete(m, "cause") },
		"unknown status": func(m map[string]any) { m["event"].(map[string]any)["warning"].(map[string]any)["status"] = "final" },
		"region no level": func(m map[string]any) {
			delete(m["event"].(map[string]any)["warning"].(map[string]any)["data"].(map[string]any)["activeRegions"].([]any)[0].(map[string]any), "level")
		},
		"bad intensity": func(m map[string]any) {
			m["event"].(map[string]any)["forecast"].(map[string]any)["data"].(map[string]any)["maxIntensity"] = map[string]any{"kind": "big"}
		},
	} {
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		change(m)
		if err := c.validate(c.root, m, name); err == nil {
			t.Fatal("schema accepted", name)
		}
	}
}
