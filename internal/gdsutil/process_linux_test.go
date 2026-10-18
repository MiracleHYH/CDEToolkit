//go:build linux

package gdsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
)

func TestLinuxProcessDirectoryExitErrors(t *testing.T) {
	for _, errno := range []error{syscall.ENOENT, syscall.ESRCH} {
		cause := &os.PathError{Op: "stat", Path: "/proc/unused-fixture", Err: errno}
		if err := linuxProcessDirectoryError(cause); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("whole PID directory lookup must normalize exit: %v", err)
		}
	}
	for _, errno := range []error{syscall.EACCES, syscall.EPERM, syscall.EIO, syscall.EINVAL} {
		cause := &os.PathError{Op: "stat", Path: "/proc/unused-fixture", Err: errno}
		if err := linuxProcessDirectoryError(cause); err != cause || errors.Is(err, os.ErrNotExist) {
			t.Errorf("whole PID directory lookup hid a non-exit error: %v", err)
		}
	}
}

func TestLinuxProcessStateRejectsDamagedAndChangedBirth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stat")
	for _, text := range []string{"invalid", "123 (fixture) R 1"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := linuxProcessState(dir, gdsapi.Process{}); err == nil || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("damaged process metadata accepted as gone: %v", err)
		}
	}
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0], fields[19] = "S", "12345"
	if err := os.WriteFile(path, []byte("123 (fixture) "+strings.Join(fields, " ")), 0600); err != nil {
		t.Fatal(err)
	}
	for _, process := range []gdsapi.Process{
		{UID: uint32(os.Geteuid()), Started: "54321"},
		{UID: uint32(os.Geteuid()) + 1, Started: fields[19]},
	} {
		if _, err := linuxProcessState(dir, process); err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, errProcessTransition) {
			t.Fatalf("changed birth/user must remain an ownership error (%s): %v", strconv.Itoa(int(process.UID)), err)
		}
	}
}
