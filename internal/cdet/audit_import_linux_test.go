//go:build linux

package cdet

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMappedWriterFixtureProcess(t *testing.T) {
	if os.Getenv("CDET_TEST_MAPPED_WRITER") != "1" {
		return
	}
	f, e := os.OpenFile(os.Getenv("CDET_TEST_FILE"), os.O_RDWR, 0)
	if e != nil {
		os.Exit(91)
	}
	mapped, e := syscall.Mmap(int(f.Fd()), 0, 1, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	_ = f.Close()
	if e != nil {
		os.Exit(92)
	}
	defer syscall.Munmap(mapped)
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestImportRefusesSharedWritableMappingAfterDescriptorClosed(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMappedWriterFixtureProcess$")
	cmd.Env = append(os.Environ(), "CDET_TEST_MAPPED_WRITER=1", "CDET_TEST_FILE="+filepath.Join(f.codex, "state.sqlite"))
	in, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = in.Close(); _ = cmd.Wait() }()
	ready := make([]byte, 6)
	if _, e = io.ReadFull(out, ready); e != nil || string(ready) != "ready\n" {
		t.Fatal("mapped writer fixture did not start", e)
	}
	if _, e = f.manager(t, Options{}).Install([]string{"codex"}); e == nil {
		t.Fatal("source with writable shared mapping was imported")
	}
	if !absent(filepath.Join(f.store, dataPath("codex"))) {
		t.Fatal("active data published")
	}
}
