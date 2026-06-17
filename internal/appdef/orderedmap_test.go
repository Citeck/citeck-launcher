package appdef

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOrderedMapJSONPreservesOrder(t *testing.T) {
	var m OrderedMap
	m.Set("ZEBRA", "1")
	m.Set("ALPHA", "2")
	m.Set("MIDDLE", "3")
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"ZEBRA":"1","ALPHA":"2","MIDDLE":"3"}` {
		t.Fatalf("order not preserved in JSON: %s", b)
	}
	var back OrderedMap
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Len() != 3 || back[0].Key != "ZEBRA" || back[2].Key != "MIDDLE" {
		t.Fatalf("round-trip order lost: %+v", back)
	}
}

func TestOrderedMapYAMLPreservesOrder(t *testing.T) {
	var m OrderedMap
	m.Set("ZEBRA", "1")
	m.Set("ALPHA", "2")
	b, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "ZEBRA: \"1\"\nALPHA: \"2\"\n" {
		t.Fatalf("order not preserved in YAML: %q", b)
	}
	var back OrderedMap
	if err := yaml.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Len() != 2 || back[0].Key != "ZEBRA" {
		t.Fatalf("round-trip order lost: %+v", back)
	}
}

func TestOrderedMapYAMLCoercesScalarValues(t *testing.T) {
	var m OrderedMap
	if err := yaml.Unmarshal([]byte("PORT: 5672\nENABLED: true\n"), &m); err != nil {
		t.Fatal(err)
	}
	if v, _ := m.Get("PORT"); v != "5672" {
		t.Errorf("PORT = %q, want \"5672\"", v)
	}
	if v, _ := m.Get("ENABLED"); v != "true" {
		t.Errorf("ENABLED = %q, want \"true\"", v)
	}
}

func TestOrderedMapSetUpdatesInPlace(t *testing.T) {
	var m OrderedMap
	m.Set("A", "1")
	m.Set("B", "2")
	m.Set("A", "9") // update keeps position
	if m[0].Key != "A" || m[0].Value != "9" || m.Len() != 2 {
		t.Fatalf("Set should update in place: %+v", m)
	}
}

func TestOrderedMapFromMapSorted(t *testing.T) {
	m := OrderedMapFromMap(map[string]string{"c": "3", "a": "1", "b": "2"})
	if m[0].Key != "a" || m[1].Key != "b" || m[2].Key != "c" {
		t.Fatalf("expected sorted keys, got %+v", m)
	}
}
