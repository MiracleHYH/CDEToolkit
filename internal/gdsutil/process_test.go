package gdsutil

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestProcessMetadataExitErrorConfirmsWholeDirectory(t *testing.T) {
	// Deterministically inject the exact procfs read error seen when the
	// process exits after stat was opened. ESRCH on a child alone proves
	// neither a dead thread group nor safe replacement of its identity.
	for _, errno := range []error{syscall.ENOENT, syscall.ESRCH} {
		t.Run(errno.Error(), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pid")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			cause := &os.PathError{Op: "read", Path: filepath.Join(dir, "stat"), Err: errno}
			if err := processMetadataError(dir, cause); !errors.Is(err, errProcessTransition) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
				t.Fatalf("existing PID directory must remain unconfirmed: %v", err)
			}
			if err := os.Remove(dir); err != nil {
				t.Fatal(err)
			}
			if err := processMetadataError(dir, cause); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("confirmed whole-directory disappearance must be gone: %v", err)
			}
			// PID reuse during the recheck must never classify the newly
			// present directory as gone based on the earlier child error.
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := processMetadataError(dir, cause); !errors.Is(err, errProcessTransition) || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("reappeared PID directory must remain unconfirmed: %v", err)
			}
		})
	}
}

func TestProcessMetadataErrorsDoNotBecomeGone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent-pid")
	for _, errno := range []error{syscall.EACCES, syscall.EPERM, syscall.EIO, syscall.EINVAL} {
		cause := &os.PathError{Op: "read", Path: filepath.Join(dir, "stat"), Err: errno}
		if err := processMetadataError(dir, cause); err != cause || errors.Is(err, os.ErrNotExist) || errors.Is(err, errProcessTransition) {
			t.Errorf("non-exit error must retain its meaning even without the directory: %v", err)
		}
	}
	file := filepath.Join(t.TempDir(), "not-a-pid-directory")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := processMetadataError(file, syscall.ESRCH); err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, errProcessTransition) {
		t.Fatalf("invalid PID directory must remain an error: %v", err)
	}
}
