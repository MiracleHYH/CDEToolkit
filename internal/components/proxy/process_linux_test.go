//go:build linux

package proxy

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Linux kernel regressions fail on denied procfs or signal interfaces. In the
// isolated native Linux job, a sandbox denial is a missing verification, not a
// passing test. No real core or application home is used by these fixtures.
func linuxKernelSelf(t *testing.T) Process {
	t.Helper()
	p, err := (OSRunner{}).Inspect(os.Getpid())
	if err != nil {
		t.Fatalf("Linux kernel identity unavailable: %v", err)
	}
	return p
}

func TestLinuxKernelPIDFDSignalZero(t *testing.T) {
	p := linuxKernelSelf(t)
	// These syscall numbers are shared by the supported amd64/arm64 targets.
	// Signal zero checks the actual handle and permission without delivering a
	// signal to this test process.
	fd, _, openErr := syscall.Syscall(434, uintptr(p.PID), 0, 0)
	switch openErr {
	case 0:
		defer syscall.Close(int(fd))
	case syscall.ENOSYS, syscall.EINVAL:
		t.Logf("pidfd_open unavailable (%v): production uses the documented birth-check/kill fallback, with a check-to-signal window", openErr)
		return
	default:
		t.Fatalf("pidfd_open denied or failed; native signal verification did not run: %v", openErr)
	}
	_, _, signalErr := syscall.Syscall6(424, fd, 0, 0, 0, 0, 0)
	switch signalErr {
	case 0:
		t.Log("pidfd_open and pidfd_send_signal(signal 0) are available under the current kernel and seccomp policy")
	case syscall.ENOSYS, syscall.EINVAL:
		// The implementation falls back only when pidfd_open is unsupported.
		// A separately unsupported send operation preserves the record and
		// returns an error; it does not silently downgrade to PID signaling.
		t.Logf("pidfd_open succeeded but pidfd_send_signal is unavailable (%v): production reports the signal error and retains the record; there is no send-only fallback", signalErr)
	default:
		t.Fatalf("pidfd_send_signal(signal 0) denied or failed; native signal verification did not run: %v", signalErr)
	}
}

func linuxIPv6Unavailable(err error) bool {
	return errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EPROTONOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ENETUNREACH)
}

func TestLinuxKernelIPv6AndWildcardOwnership(t *testing.T) {
	p := linuxKernelSelf(t)
	runner := OSRunner{}
	t.Run("IPv6 loopback", func(t *testing.T) {
		listener, err := net.Listen("tcp6", "[::1]:0")
		if linuxIPv6Unavailable(err) {
			t.Skipf("IPv6 loopback unavailable in this network namespace: %v", err)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		endpoint := "http://" + listener.Addr().String()
		owned, err := runner.OwnsEndpoint(p, endpoint)
		if err != nil || !owned {
			t.Fatalf("native IPv6 loopback socket was not attributed to its owner: owned=%v err=%v", owned, err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		owned, err = runner.OwnsEndpoint(p, "http://127.0.0.1:"+strconv.Itoa(port))
		if err != nil || owned {
			t.Fatalf("IPv6 socket was accepted as an IPv4 listener: owned=%v err=%v", owned, err)
		}
	})
	for _, test := range []struct {
		name, network, address, loopback string
	}{
		{"IPv4 wildcard", "tcp4", "0.0.0.0:0", "127.0.0.1"},
		{"IPv6 wildcard", "tcp6", "[::]:0", "::1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.network == "tcp6" {
				// A network-none namespace can permit an IPv6 wildcard bind
				// while having no usable ::1 address or loopback route. Check
				// the literal loopback fixture before testing wildcard semantics;
				// later wildcard dial errors remain real failures.
				probe, err := net.Listen("tcp6", "[::1]:0")
				if linuxIPv6Unavailable(err) {
					t.Skipf("IPv6 loopback unavailable in this network namespace: %v", err)
				}
				if err != nil {
					t.Fatal(err)
				}
				conn, err := net.DialTimeout("tcp6", probe.Addr().String(), time.Second)
				probe.Close()
				if conn != nil {
					conn.Close()
				}
				if linuxIPv6Unavailable(err) {
					t.Skipf("IPv6 loopback route unavailable in this network namespace: %v", err)
				}
				if err != nil {
					t.Fatalf("IPv6 loopback fixture probe failed: %v", err)
				}
			}
			// The native container job uses --network none. A wildcard still
			// accepts loopback connections, but that connectivity must never
			// satisfy the component's exact-listen-address readiness contract.
			listener, err := net.Listen(test.network, test.address)
			if test.network == "tcp6" && linuxIPv6Unavailable(err) {
				t.Skipf("IPv6 unavailable in this network namespace: %v", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			port := listener.Addr().(*net.TCPAddr).Port
			address := net.JoinHostPort(test.loopback, strconv.Itoa(port))
			conn, err := net.DialTimeout(test.network, address, time.Second)
			if err != nil {
				t.Fatalf("wildcard fixture did not accept its loopback address: %v", err)
			}
			conn.Close()
			owned, err := runner.OwnsEndpoint(p, "http://"+address)
			if err != nil || owned {
				t.Fatalf("wildcard socket was accepted as an exact loopback listener: owned=%v err=%v", owned, err)
			}
		})
	}
}

func linuxKernelHelper(t *testing.T) (*Service, Process, string) {
	t.Helper()
	linuxKernelSelf(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	s := helperServiceAtPort(t, port, "")
	s.StartTimeout = 4 * time.Second
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	record, err := s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.recordActual(record)
	if err != nil {
		t.Fatal(err)
	}
	return s, p, "http://127.0.0.1:" + strconv.Itoa(port)
}

func TestLinuxKernelStopRejectsTamperedBirth(t *testing.T) {
	s, original, endpoint := linuxKernelHelper(t)
	runner := OSRunner{}
	for _, test := range []struct {
		name   string
		mutate func(*Process)
	}{
		{"start time", func(p *Process) { p.Started += "-changed" }},
		{"boot", func(p *Process) { p.Boot += "-changed" }},
		{"uid", func(p *Process) { p.UID ^= 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrong := original
			test.mutate(&wrong)
			if err := runner.Stop(wrong); err == nil {
				t.Fatal("tampered kernel birth authorized a stop")
			}
			// Check more than a still-unreaped PID: the original identity and
			// its listening inode must remain alive after the rejected signal.
			for attempt := 0; attempt < 3; attempt++ {
				actual, err := runner.Inspect(original.PID)
				if err != nil || actual != original {
					t.Fatalf("rejected stop disturbed the fixture: actual=%+v err=%v", actual, err)
				}
				owned, err := runner.OwnsEndpoint(original, endpoint)
				if err != nil || !owned {
					t.Fatalf("rejected stop closed the fixture listener: owned=%v err=%v", owned, err)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("valid fixture identity could not be stopped: %v", err)
	}
}

func TestLinuxKernelSameBytesReplacementKeepsStopAuthority(t *testing.T) {
	s, original, _ := linuxKernelHelper(t)
	record, err := s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(record.Program)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(record.Program)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(record.Program), "same-bytes-replacement")
	if err := os.WriteFile(replacement, data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, record.Program); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(record.Program)
	if err != nil || os.SameFile(before, after) {
		t.Fatalf("fixture did not replace the executable inode: %v", err)
	}
	currentData, err := os.ReadFile(record.Program)
	if err != nil || !bytes.Equal(data, currentData) {
		t.Fatalf("fixture changed executable contents: %v", err)
	}
	actual, err := (OSRunner{}).Inspect(original.PID)
	if err != nil || !sameBirth(original, actual) || actual.Executable != original.Executable || !actual.ExecutableDeleted {
		t.Fatalf("same-byte replacement lost the original deleted-image identity: actual=%+v err=%v", actual, err)
	}
	status, err := s.Runtime()
	if err == nil || status.State != "unknown" || status.Listeners {
		t.Fatalf("same hash hid an unlinked running image: status=%+v err=%v", status, err)
	}
	if err := s.Start(); err == nil {
		t.Fatal("deleted-image instance was accepted as an idempotent ready start")
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("same-byte replacement removed authority to stop the exact birth: %v", err)
	}
	if _, err := (OSRunner{}).Inspect(original.PID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture survived its valid stop: %v", err)
	}
}
