//go:build !linux || !amd64 || !cgo

package nvidia

import "testing"

func TestUnsupportedHasNoDriverFallback(t *testing.T) {
	if Available() {
		t.Fatal("unsupported platform advertised native driver support")
	}
	if _, err := New().Probe("GPU-00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("probe accepted unsupported platform")
	}
	if _, err := New().Allocate("GPU-00000000-0000-0000-0000-000000000000", 1, 1); err == nil {
		t.Fatal("allocation accepted unsupported platform")
	}
}
