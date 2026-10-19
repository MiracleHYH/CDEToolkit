//go:build linux

package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxRunningImageMatchesExactTestProcessInode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p, err := (OSRunner{}).Inspect(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	matched, err := (OSRunner{}).RunningImageMatches(p, p.Executable)
	if err != nil || !matched {
		t.Fatalf("own test image not recognized: %v %v", matched, err)
	}
	p.Started += "-other-birth"
	if matched, err := (OSRunner{}).RunningImageMatches(p, p.Executable); err == nil || matched {
		t.Fatal("unrelated birth accepted")
	}
	p, err = (OSRunner{}).Inspect(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(other, coreNativeFixture("linux", "amd64"), 0700); err != nil {
		t.Fatal(err)
	}
	if matched, err := (OSRunner{}).RunningImageMatches(p, other); err == nil || matched {
		t.Fatal("different fixed program accepted as running image")
	}
	p.ExecutableDeleted = true
	if matched, err := (OSRunner{}).RunningImageMatches(p, p.Executable); err == nil || matched {
		t.Fatal("deleted image accepted")
	}
}
