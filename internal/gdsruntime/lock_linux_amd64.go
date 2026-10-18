//go:build linux && amd64

package gdsruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

func deviceLockPath(uuid string) string {
	return filepath.Join("/tmp", fmt.Sprintf("nido-gds-%d", os.Geteuid()), "gpu-"+uuid+".lock")
}
func validateDeviceLock(f *os.File, uuid string) error {
	got, err := f.Stat()
	if err != nil {
		return err
	}
	want, err := os.Lstat(deviceLockPath(uuid))
	if err != nil {
		return err
	}
	st, ok := got.Sys().(*syscall.Stat_t)
	if !ok || !got.Mode().IsRegular() || got.Mode().Perm() != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || !os.SameFile(got, want) || want.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid inherited device lock")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("inherited device lock is not held: %w", err)
	}
	return nil
}

// Keep one raw duplicate for this disposable worker's entire process lifetime.
// An os.File wrapper can be finalized after its last Go use, long before a CUDA
// allocation is released. A raw FD has no finalizer, and only process exit may
// close this final fence, including after a failed or wedged native cleanup.
func retainDeviceLock(f *os.File) error {
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_DUPFD_CLOEXEC, 0)
	runtime.KeepAlive(f)
	if errno != 0 {
		return fmt.Errorf("retain inherited device lock: %w", errno)
	}
	return nil
}
