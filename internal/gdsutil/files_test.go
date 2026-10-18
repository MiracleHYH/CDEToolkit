package gdsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotPreservesConcurrentEdit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	os.WriteFile(p, []byte("before"), 0600)
	before, e := Capture(p, 1024, false)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("edited"), 0600)
	if e = WriteExpected(p, []byte("after"), 0600, before); e == nil {
		t.Fatal("overwrote edit")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "edited" {
		t.Fatal("edit lost")
	}
}
func TestStableLockAndReplacedInodeGuard(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".lock")
	f, e := Lock(p)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if second, e := Lock(p); e == nil {
		second.Close()
		t.Fatal("duplicate lock accepted")
	}
	guard, e := CaptureDirectory(dir)
	if e != nil {
		t.Fatal(e)
	}
	os.Rename(p, p+"old")
	os.WriteFile(p, []byte{}, 0600)
	if e = guard.Check(); e == nil {
		t.Fatal("lock replacement ignored")
	}
}
func TestReadRejectsSymlinkAndHardlink(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data")
	os.WriteFile(p, []byte("secret"), 0600)
	link := p + "link"
	os.Symlink(p, link)
	if _, e := Read(link, 1024); e == nil {
		t.Fatal("read symlink")
	}
	os.Remove(link)
	os.Link(p, link)
	if _, e := Read(p, 1024); e == nil {
		t.Fatal("read shared inode")
	}
}
func TestInspectAndStopRejectWrongBirth(t *testing.T) {
	p, e := Inspect(os.Getpid())
	if e != nil {
		t.Skipf("当前隔离环境拒绝内核进程出生信息: %v", e)
	}
	if p.PID != os.Getpid() || p.Started == "" || p.Boot == "" {
		t.Fatal(p)
	}
	p.Started += "wrong"
	if e = Stop(p); e == nil {
		t.Fatal("wrong birth authorized signal")
	}
}
func TestPrivateDirectoryDoesNotRepairSharedMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "private")
	if e := EnsureDir(p); e != nil {
		t.Fatal(e)
	}
	os.Chmod(p, 0755)
	if e := EnsureDir(p); e == nil || !strings.Contains(e.Error(), "0700") {
		t.Fatal("shared dir accepted")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0755 {
		t.Fatal("changed existing dir")
	}
}
