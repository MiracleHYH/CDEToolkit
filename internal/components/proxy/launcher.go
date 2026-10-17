package proxy

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const launcherArgument = "__nido_proxy_launcher"

// HandleLauncher must run before ordinary CLI parsing. FD 3 is a private,
// inherited authorization pipe. The timeout and EOF prevent orphan launchers.
// The launcher has no elevated privileges and is replaced by the core via exec.
func HandleLauncher(args []string) (handled bool, exit int) {
	if len(args) == 0 || args[0] != launcherArgument {
		return false, 0
	}
	if len(args) < 2 || !filepath.IsAbs(args[1]) {
		return true, 125
	}
	f := os.NewFile(3, "proxy-launch-authorization")
	if f == nil {
		return true, 125
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return true, 125
	}
	authorized := make(chan bool, 1)
	go func() {
		var token [1]byte
		_, err := io.ReadFull(f, token[:])
		authorized <- err == nil && token[0] == 1
	}()
	select {
	case ok := <-authorized:
		if !ok {
			return true, 125
		}
	case <-time.After(15 * time.Second):
		return true, 125
	}
	f.Close()
	if err := syscall.Exec(args[1], args[1:], os.Environ()); err != nil {
		return true, 126
	}
	return true, 126
}
