package desktop

import "testing"

func TestCapabilitiesRoundTrip(t *testing.T) {
	c := Capabilities{ContractVersion: CapsContractVersion, Verbs: []string{VerbWindowFocus, VerbAppQuit}}
	env := c.Encode()
	got, err := ParseCapabilities(env)
	if err != nil {
		t.Fatalf("ParseCapabilities: %v", err)
	}
	if got.ContractVersion != CapsContractVersion {
		t.Fatalf("version=%d", got.ContractVersion)
	}
	if !got.Supports(VerbWindowFocus) || got.Supports("window.fly") {
		t.Fatalf("Supports wrong: %+v", got.Verbs)
	}
}

func TestParseCapabilitiesEmpty(t *testing.T) {
	got, err := ParseCapabilities("")
	if err != nil {
		t.Fatalf("empty should not error: %v", err)
	}
	if got.Supports(VerbWindowFocus) {
		t.Fatal("empty caps should support nothing")
	}
}
