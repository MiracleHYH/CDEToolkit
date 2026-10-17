//go:build linux

package cdet

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
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
		{name: "zombie", status: "Name:\tfixture\nState:\tZ (zombie)\n", exited: true},
		{name: "dead", status: "State:\tX (dead)\n", exited: true},
		{name: "disappeared", missing: true, exited: true},
		{name: "running", status: "State:\tR (running)\n"},
		{name: "sleeping", status: "State:\tS (sleeping)\n"},
		{name: "unknown", status: "Name:\tfixture\n"},
		{name: "empty-state", status: "State:\t\n"},
		{name: "malformed-state", status: "State:\tZ-invalid\n"},
		{name: "not-state", status: "NotState:\tZ (zombie)\n"},
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
			// Only a fake /proc-like directory is inspected. Permission errors
			// never justify skipping a live or unidentifiable process.
			for _, readErr := range []error{syscall.EACCES, syscall.EPERM, syscall.ESRCH, os.ErrNotExist, errors.New("synthetic read error")} {
				if got := procDisappeared(base, readErr); got != test.exited {
					t.Fatalf("read error %v: confirmed exit = %v, want %v", readErr, got, test.exited)
				}
			}
		})
	}
}
