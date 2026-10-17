package builtin

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

type fakeRunner struct {
	process Process
	starts  int
	stops   int
	err     error
	owned   bool
}

func (r *fakeRunner) Inspect(pid int) (Process, error) {
	if r.err != nil {
		return Process{}, r.err
	}
	if r.process.PID == 0 {
		return Process{}, os.ErrNotExist
	}
	return r.process, nil
}

func (r *fakeRunner) Stop(p Process) error {
	r.stops++
	if r.process != p {
		return errors.New("fake refused mismatched identity")
	}
	r.process = Process{}
	return r.err
}

// These headers are never executed. They let installation/architecture tests
// validate ordinary files without compiling or starting a component program.
func nativeFixture(goos, arch string) []byte {
	if goos == "linux" {
		b := make([]byte, 64)
		copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
		binary.LittleEndian.PutUint16(b[16:], 2)
		machine := uint16(62)
		if arch == "arm64" {
			machine = 183
		}
		binary.LittleEndian.PutUint16(b[18:], machine)
		binary.LittleEndian.PutUint32(b[20:], 1)
		binary.LittleEndian.PutUint16(b[52:], 64)
		binary.LittleEndian.PutUint16(b[54:], 56)
		binary.LittleEndian.PutUint16(b[58:], 64)
		return b
	}
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b, 0xfeedfacf)
	cpu := uint32(0x01000007)
	if arch == "arm64" {
		cpu = 0x0100000c
	}
	binary.LittleEndian.PutUint32(b[4:], cpu)
	binary.LittleEndian.PutUint32(b[12:], 2)
	return b
}

func fixtureFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, nativeFixture(runtime.GOOS, runtime.GOARCH), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalGDSAndUnsafeArtifacts(t *testing.T) {
	m := Manager{Root: t.TempDir(), Runner: &fakeRunner{}}
	if err := m.Install("gds", "", ""); err == nil {
		t.Fatal("GDS got an implicit online installation")
	}
	if err := m.Install("codex", fixtureFile(t), ""); err == nil {
		t.Fatal("Codex accepted as built-in")
	}
	script := filepath.Join(t.TempDir(), "script")
	writeFixture(t, script, "#!/bin/sh\nexit 0\n")
	if err := m.Install("gds", script, ""); err == nil {
		t.Fatal("runtime-dependent script accepted")
	}
	if err := m.Install("gds", fixtureFile(t), strings.Repeat("0", 64)); err == nil {
		t.Fatal("invalid checksum accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(fixtureFile(t), link); err != nil {
		t.Fatal(err)
	}
	if err := m.Install("gds", link, ""); err == nil {
		t.Fatal("external source symlink accepted")
	}
}

func TestGDSStartIsExplicitlyUnavailableWithoutWritingOrRunning(t *testing.T) {
	runner := &fakeRunner{}
	m := Manager{Root: filepath.Join(t.TempDir(), "not-created"), Runner: runner}
	if err := m.Start("gds"); err == nil || !strings.Contains(err.Error(), "尚未适配") {
		t.Fatal("GDS start did not report the missing scheduler adapter")
	}
	if _, err := os.Lstat(m.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsupported GDS start wrote to the software directory")
	}
	if s := m.Status("gds"); s.Installed || s.Configured || s.Problem != "" {
		t.Fatalf("optional missing GDS counted as abnormal: %+v", s)
	}
	artifact := fixtureFile(t)
	if err := m.Install("gds", artifact, ""); err != nil {
		t.Fatal(err)
	}
	if s := m.Status("gds"); !s.Installed || s.Configured || !strings.Contains(s.Problem, "尚未适配") {
		t.Fatalf("GDS worker counted as a ready scheduler: %+v", s)
	}
	if err := m.Start("gds"); err == nil || runner.starts != 0 {
		t.Fatal("GDS artifact executed without a scheduler adapter")
	}
	if err := m.Update("gds", artifact, ""); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(m.Root, "gds", "config.toml"), "preserved user configuration\n")
	if err := m.Remove("gds"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(m.Root, "gds", "config.toml")); string(b) != "preserved user configuration\n" {
		t.Fatal("GDS removal discarded user configuration")
	}
}

func TestGDSStopOnlyUsesRecordedOwnedProcess(t *testing.T) {
	runner := &fakeRunner{}
	m := Manager{Root: t.TempDir(), Runner: runner}
	if err := m.Install("gds", fixtureFile(t), ""); err != nil {
		t.Fatal(err)
	}
	dir, program, _ := m.paths("gds")
	runner.process = Process{PID: 4242, Executable: program, Started: "prior-recorded-process"}
	b, _ := json.Marshal(runner.process)
	if err := os.WriteFile(filepath.Join(dir, "process.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop("gds"); err != nil || runner.stops != 1 {
		t.Fatalf("could not stop the exact previously recorded process: %v", err)
	}
	if runner.starts != 0 {
		t.Fatal("GDS cleanup started a component")
	}
}

func TestSymlinkComponentCannotTouchExternalFiles(t *testing.T) {
	outside := t.TempDir()
	writeFixture(t, filepath.Join(outside, "gds"), "external executable")
	m := Manager{Root: t.TempDir(), Runner: &fakeRunner{}}
	if err := os.Symlink(outside, filepath.Join(m.Root, "gds")); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("gds"); err == nil {
		t.Fatal("followed external component symlink")
	}
	if got, _ := os.ReadFile(filepath.Join(outside, "gds")); string(got) != "external executable" {
		t.Fatal("external data changed")
	}
}

func TestConcurrentOperationsFailInsteadOfReplacingProgram(t *testing.T) {
	m := Manager{Root: t.TempDir(), Runner: &fakeRunner{}}
	artifact := fixtureFile(t)
	if err := m.Install("gds", artifact, ""); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(m.Root, "gds", ".lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err = m.Remove("gds"); err == nil {
		t.Fatal("removed component while another operation holds the lock")
	}
	if s := m.Status("gds"); !s.Installed {
		t.Fatal("competing operation changed the component")
	}
}
