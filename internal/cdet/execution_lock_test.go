//go:build linux || darwin

package cdet

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func leaseFixture(t *testing.T) *Manager {
	t.Helper()
	f := newFixture(t)
	f.init(t)
	f.install(t, "proxy")
	return f.manager(t, Options{})
}

func TestExecutionLocksAreSharedForRuntimeAndExclusiveForWriters(t *testing.T) {
	m := leaseFixture(t)
	before, e := snapshotTree(filepath.Join(m.Store, "proxy"))
	if e != nil {
		t.Fatal(e)
	}
	first, e := m.ExecutionLease("proxy")
	if e != nil {
		t.Fatal(e)
	}
	second, e := m.ExecutionLease("proxy")
	if e != nil {
		t.Fatal(e)
	}
	if release, e := m.lockComponents([]string{"proxy"}); e == nil {
		release()
		t.Fatal("writer entered while readers were active")
	}
	_ = first.Close()
	_ = second.Close()
	release, e := m.lockComponents([]string{"proxy", "proxy"})
	if e != nil {
		t.Fatal(e)
	}
	if reader, e := m.ExecutionLease("proxy"); e == nil {
		_ = reader.Close()
		t.Fatal("runtime entered while mutation lock was held")
	}
	if other, e := m.lockComponents([]string{"proxy"}); e == nil {
		other()
		t.Fatal("second writer entered")
	}
	release()
	release()
	reader, e := m.ExecutionLease("proxy")
	if e != nil {
		t.Fatal("writer did not release its lock", e)
	}
	_ = reader.Close()
	after, e := snapshotTree(filepath.Join(m.Store, "proxy"))
	if e != nil || before.RawDigest != after.RawDigest {
		t.Fatal("execution locks changed component data", e)
	}
}

func TestExecutionLocksUnwindPartiallyAcquiredSortedLocks(t *testing.T) {
	m := leaseFixture(t)
	release, e := m.lockComponents([]string{"proxy"})
	if e != nil {
		t.Fatal(e)
	}
	if partial, e := m.lockComponents([]string{"proxy", "codex", "codex"}); e == nil {
		partial()
		t.Fatal("writer ignored busy component")
	}
	free, e := m.lockComponents([]string{"codex"})
	if e != nil {
		t.Fatal("partial acquisition leaked earlier sorted lock", e)
	}
	free()
	release()
	if _, e = m.lockComponents([]string{"../outside"}); e == nil {
		t.Fatal("invalid lock name accepted")
	}
}

func TestExecutionLocksRejectSymlinksAndUninitializedReaders(t *testing.T) {
	for _, mode := range []string{"parent", "leaf"} {
		t.Run(mode, func(t *testing.T) {
			m := leaseFixture(t)
			external := filepath.Join(t.TempDir(), "external")
			if e := os.Mkdir(external, 0700); e != nil {
				t.Fatal(e)
			}
			locks := filepath.Join(m.metadataDir(), "locks")
			if e := os.RemoveAll(locks); e != nil {
				t.Fatal(e)
			}
			if mode == "parent" {
				if e := os.Symlink(external, locks); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := os.Mkdir(locks, 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(external, "keep"), []byte("PRIVATE-SENTINEL"), 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(filepath.Join(external, "keep"), filepath.Join(locks, "proxy")); e != nil {
					t.Fatal(e)
				}
			}
			if f, e := m.ExecutionLease("proxy"); e == nil {
				_ = f.Close()
				t.Fatal("runtime followed external lock link")
			}
			if release, e := m.lockComponents([]string{"proxy"}); e == nil {
				release()
				t.Fatal("writer followed external lock link")
			}
			entries, e := os.ReadDir(external)
			if e != nil || mode == "parent" && len(entries) != 0 {
				t.Fatal("external directory changed", e)
			}
		})
	}
	root := t.TempDir()
	m := &Manager{Store: filepath.Join(root, "not-created")}
	if f, e := m.ExecutionLease("proxy"); e == nil {
		_ = f.Close()
		t.Fatal("uninitialized runtime opened a lock")
	}
	if !absent(m.Store) {
		t.Fatal("uninitialized runtime created storage")
	}
}

func TestExecutionLeaseSyntheticProcess(t *testing.T) {
	if os.Getenv("CDET_TEST_EXEC_LEASE") != "1" {
		return
	}
	m, e := New(Options{Prefix: os.Getenv("CDET_TEST_LEASE_PREFIX"), Store: os.Getenv("CDET_TEST_LEASE_STORE")})
	if e != nil {
		t.Fatal(e)
	}
	file, e := m.ExecutionLease("proxy")
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	// Exec a synthetic shell that only reports readiness and waits for EOF.
	if e = syscall.Exec("/bin/sh", []string{"sh", "-c", "printf 'READY\\n'; read -r cdet_test_release"}, os.Environ()); e != nil {
		t.Fatal(e)
	}
}

func TestExecutionLeaseSurvivesExecUntilSyntheticProcessExits(t *testing.T) {
	m := leaseFixture(t)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(exe, "-test.run=^TestExecutionLeaseSyntheticProcess$")
	cmd.Env = append(os.Environ(), "CDET_TEST_EXEC_LEASE=1", "CDET_TEST_LEASE_PREFIX="+m.Prefix, "CDET_TEST_LEASE_STORE="+m.Store)
	stdin, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan error, 1)
	go func() {
		line, e := bufio.NewReader(stdout).ReadString('\n')
		if e == nil && strings.TrimSpace(line) != "READY" {
			e = fmt.Errorf("unexpected readiness")
		}
		ready <- e
	}()
	select {
	case e = <-ready:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("synthetic exec timed out")
	}
	if release, e := m.lockComponents([]string{"proxy"}); e == nil {
		release()
		t.Fatal("lease disappeared during syscall.Exec")
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	release, e := m.lockComponents([]string{"proxy"})
	if e != nil {
		t.Fatal("lease remained after exec process exit", e)
	}
	release()
}
