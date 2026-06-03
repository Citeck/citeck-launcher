package desktop

import (
	"os"
	"testing"
)

func TestSelectDaemonBinaryReturnsExecutable(t *testing.T) {
	got, err := SelectDaemonBinary()
	if err != nil {
		t.Fatalf("SelectDaemonBinary: %v", err)
	}
	exe, _ := os.Executable()
	if got != exe {
		t.Fatalf("SelectDaemonBinary()=%q, want os.Executable()=%q", got, exe)
	}
}
