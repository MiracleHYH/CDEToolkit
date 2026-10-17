//go:build linux

package cdet

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLinuxProcessOwnershipRequiresAnEffectiveUID(t *testing.T) {
	uid, ok := processUID("Name:\tcodex\nUid:\t1000\t1001\t1000\t1000\n")
	if !ok || uid != 1001 {
		t.Fatal("effective process owner was not decoded")
	}
	for _, malformed := range []string{"", "Uid:\t1000", "Uid:\t1000 invalid"} {
		if _, ok := processUID(malformed); ok {
			t.Fatal("unknown process ownership accepted")
		}
	}
}

func TestLinuxProcessReadErrorsRequireConfirmedExit(t *testing.T) {
	for _, test := range []struct {
		name, status string
		missing      bool
		exited       bool
	}{
		{name: "zombie", status: "Name:\tfixture\nState:\tZ (zombie)\nThreads:\t1\n", exited: true},
		{name: "dead", status: "Threads:\t1\nState:\tX (dead)\n", exited: true},
		{name: "disappeared", missing: true, exited: true},
		{name: "running", status: "State:\tR (running)\nThreads:\t1\n"},
		{name: "sleeping", status: "State:\tS (sleeping)\nThreads:\t1\n"},
		{name: "unknown", status: "Name:\tfixture\n"},
		{name: "empty-state", status: "State:\t\nThreads:\t1\n"},
		{name: "malformed-state", status: "State:\tZ-invalid\nThreads:\t1\n"},
		{name: "not-state", status: "NotState:\tZ (zombie)\nThreads:\t1\n"},
		{name: "missing-threads", status: "State:\tZ (zombie)\n"},
		{name: "empty-threads", status: "State:\tZ (zombie)\nThreads:\t\n"},
		{name: "zero-threads", status: "State:\tZ (zombie)\nThreads:\t0\n"},
		{name: "live-workers", status: "State:\tZ (zombie)\nThreads:\t2\n"},
		{name: "malformed-threads", status: "State:\tZ (zombie)\nThreads:\t1-invalid\n"},
		{name: "extra-thread-field", status: "State:\tZ (zombie)\nThreads:\t1 extra\n"},
		{name: "duplicate-state", status: "State:\tZ (zombie)\nState:\tS (sleeping)\nThreads:\t1\n"},
		{name: "duplicate-threads", status: "State:\tZ (zombie)\nThreads:\t1\nThreads:\t2\n"},
		{name: "unreadable-status"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "123")
			if !test.missing {
				if err := os.Mkdir(base, 0700); err != nil {
					t.Fatal(err)
				}
				status := filepath.Join(base, "status")
				if test.name == "unreadable-status" {
					if err := os.Mkdir(status, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(status, []byte(test.status), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Only a fake /proc-like directory is inspected. The production
			// wrapper ignores the original error and uses this same predicate.
			if got := procDisappearedWithin(base, time.Millisecond); got != test.exited {
				t.Fatalf("confirmed exit = %v, want %v", got, test.exited)
			}
			if !test.missing && test.name != "unreadable-status" && processExited(test.status) != test.exited {
				t.Fatal("initial status and read-error checks disagree")
			}
		})
	}
}

func TestLinuxProcessReadErrorsWaitForConfirmedExit(t *testing.T) {
	for _, transition := range []string{"disappeared", "terminal-status"} {
		t.Run(transition, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "123")
			if err := os.Mkdir(base, 0700); err != nil {
				t.Fatal(err)
			}
			status := filepath.Join(base, "status")
			if err := os.WriteFile(status, []byte("State:\tS (sleeping)\nThreads:\t1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			changed := make(chan error, 1)
			go func() {
				// The former three 1ms probes finish before this transition.
				time.Sleep(35 * time.Millisecond)
				if transition == "disappeared" {
					changed <- os.RemoveAll(base)
					return
				}
				candidate := filepath.Join(base, "next-status")
				if err := os.WriteFile(candidate, []byte("State:\tZ (zombie)\nThreads:\t1\n"), 0600); err != nil {
					changed <- err
					return
				}
				changed <- os.Rename(candidate, status)
			}()
			confirmed := procDisappeared(base, syscall.EACCES)
			if err := <-changed; err != nil {
				t.Fatal(err)
			}
			if !confirmed {
				t.Fatal("rejected a process confirmed to exit within the bounded wait")
			}
		})
	}
}

func TestLinuxProcessExitCheckStillProbesAtExpiredDeadline(t *testing.T) {
	base := filepath.Join(t.TempDir(), "123")
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(base, "status")
	for _, terminal := range []bool{false, true} {
		state := "S (sleeping)"
		if terminal {
			state = "Z (zombie)"
		}
		if err := os.WriteFile(status, []byte("State:\t"+state+"\nThreads:\t1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := procDisappearedWithin(base, 0); got != terminal {
			t.Fatalf("expired deadline: confirmed exit = %v, want %v", got, terminal)
		}
	}
}
