package gdsruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsutil"
)

// Backend is constructed only in disposable __probe and __worker processes.
// Allocate and Allocation.Free execute on the same locked OS thread.
type Backend interface {
	Probe(uuid string) (gdsapi.Sample, error)
	Allocate(uuid string, bytes, minFree uint64) (Allocation, error)
}
type Allocation interface{ Free() error }

type WorkerOptions struct {
	GPUUUID, Token, BootID string
	Input                  io.Reader
	Output                 io.Writer
	Clock                  Clock
	// DeviceLock is the descriptor inherited from the monitor (normally FD 3).
	DeviceLock    *os.File
	lockValidated bool // in-package CPU protocol tests have no device lock
}

func RunProbe(w io.Writer, backend Backend, uuid string) error {
	if !gdsapi.ValidUUID(uuid) {
		return fmt.Errorf("invalid full GPU UUID")
	}
	s, err := backend.Probe(uuid)
	if err != nil {
		return err
	}
	if !s.Complete || !s.Supported || s.UUID != uuid {
		return fmt.Errorf("GPU observation unavailable: %s", s.Problem)
	}
	return json.NewEncoder(w).Encode(s)
}

func encodeMessage(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > MaxMessageBytes {
		return fmt.Errorf("protocol message exceeds limit")
	}
	b = append(b, '\n')
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}
func readMessage[T any](r *bufio.Scanner) (T, error) {
	var v T
	if !r.Scan() {
		if err := r.Err(); err != nil {
			return v, err
		}
		return v, io.EOF
	}
	d := json.NewDecoder(bytes.NewReader(r.Bytes()))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		return v, err
	}
	var tail any
	if err := d.Decode(&tail); err != io.EOF {
		return v, fmt.Errorf("trailing protocol data")
	}
	return v, nil
}
func protocolScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 1024), MaxMessageBytes+1)
	return s
}

type leaseValidator struct {
	uuid, token, boot string
	seq               uint64
	bytes, reserve    uint64
	allocated         bool
	expires           int64
}

func (v *leaseValidator) accept(l gdsapi.Lease, now int64) error {
	if l.Protocol != gdsapi.ProtocolVersion || l.Boot != v.boot || l.Token != v.token || l.UUID != v.uuid || l.Seq == 0 || l.Seq <= v.seq {
		return fmt.Errorf("worker lease identity or sequence mismatch")
	}
	if l.Type == "STOP" {
		v.seq = l.Seq
		return nil
	}
	if l.Type != "ALLOCATE" && l.Type != "RENEW" {
		return fmt.Errorf("unknown worker command")
	}
	if v.allocated && now >= v.expires {
		return fmt.Errorf("previous worker lease already expired")
	}
	if l.ExpiresAtBootNS <= now || l.ExpiresAtBootNS-now > int64(LeaseDuration) {
		return fmt.Errorf("expired or excessive absolute worker lease")
	}
	if !v.allocated {
		if l.Type != "ALLOCATE" || l.Bytes == 0 || l.MinFreeBytes == 0 {
			return fmt.Errorf("worker has no allocation authorization")
		}
		v.bytes = l.Bytes
		v.reserve = l.MinFreeBytes
		v.allocated = true
	} else if l.Type != "RENEW" || l.Bytes != v.bytes || l.MinFreeBytes != v.reserve {
		return fmt.Errorf("worker allocation changed or repeated")
	}
	v.seq = l.Seq
	v.expires = l.ExpiresAtBootNS
	return nil
}

type watchdog struct {
	mu              sync.Mutex
	deadline        int64
	cleanupDeadline int64
	clock           Clock
	finished        chan struct{}
	expired         chan struct{}
}

func (w *watchdog) set(deadline int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cleanupDeadline != 0 {
		return false
	}
	w.deadline = deadline
	return true
}
func (w *watchdog) shorten(deadline int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	leaseLimit, err := checkedDeadline(w.deadline, int64(CleanupTimeout))
	if err != nil {
		os.Exit(124)
	}
	if leaseLimit < deadline {
		deadline = leaseLimit
	}
	if w.cleanupDeadline == 0 || deadline < w.cleanupDeadline {
		w.cleanupDeadline = deadline
	}
}
func (w *watchdog) run() {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-w.finished:
			return
		case <-t.C:
			now, err := w.clock.Now()
			if err != nil {
				os.Exit(124)
			}
			w.mu.Lock()
			if w.cleanupDeadline == 0 && now >= w.deadline {
				var e error
				w.cleanupDeadline, e = checkedDeadline(w.deadline, int64(CleanupTimeout))
				if e != nil {
					os.Exit(124)
				}
				close(w.expired)
			}
			deadline := w.cleanupDeadline
			w.mu.Unlock()
			if deadline != 0 && now >= deadline {
				os.Exit(124)
			}
		}
	}
}

type allocationResult struct{ err error }

// RunWorker has no receipt-relative TTL. Its independent watchdog is active
// before any backend call, and process exit is the final defense for a wedged
// driver. The watchdog never calls CUDA from another OS thread.
func RunWorker(ctx context.Context, o WorkerOptions, backend Backend) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !gdsapi.ValidUUID(o.GPUUUID) || len(o.Token) < 32 || len(o.Token) > 128 {
		return fmt.Errorf("invalid worker identity")
	}
	if o.Clock == nil {
		o.Clock = BootClock{}
	}
	if o.Input == nil {
		o.Input = os.Stdin
	}
	if o.Output == nil {
		o.Output = os.Stdout
	}
	if !o.lockValidated {
		boot, err := gdsutil.BootID()
		if err != nil {
			return err
		}
		if boot != o.BootID {
			return fmt.Errorf("worker boot identity mismatch")
		}
		if o.DeviceLock == nil {
			o.DeviceLock = os.NewFile(3, "gds-device-lock")
		}
		if o.DeviceLock == nil {
			return fmt.Errorf("missing inherited device lock")
		}
		if err := validateDeviceLock(o.DeviceLock, o.GPUUUID); err != nil {
			return err
		}
		if err := retainDeviceLock(o.DeviceLock); err != nil {
			return err
		}
	}
	now, err := o.Clock.Now()
	if err != nil {
		return err
	}
	initial, err := checkedDeadline(now, int64(5*time.Second))
	if err != nil {
		return err
	}
	wd := &watchdog{deadline: initial, clock: o.Clock, finished: make(chan struct{}), expired: make(chan struct{})}
	go wd.run()
	defer func() {
		if retErr == nil {
			close(wd.finished)
		}
	}()
	type incoming struct {
		lease gdsapi.Lease
		err   error
	}
	messages := make(chan incoming, 1)
	readerDone := make(chan struct{})
	defer close(readerDone)
	go func() {
		s := protocolScanner(o.Input)
		for {
			l, e := readMessage[gdsapi.Lease](s)
			select {
			case messages <- incoming{l, e}:
			case <-readerDone:
				return
			}
			if e != nil {
				return
			}
		}
	}()
	validator := leaseValidator{uuid: o.GPUUUID, token: o.Token, boot: o.BootID}
	var started bool
	var stop chan struct{}
	var result chan allocationResult
	var done chan error
	var ready bool
	cleanup := func(cause error) error {
		if !started {
			return cause
		}
		now, e := o.Clock.Now()
		if e != nil {
			os.Exit(124)
		}
		deadline, e := checkedDeadline(now, int64(CleanupTimeout))
		if e != nil {
			os.Exit(124)
		}
		wd.shorten(deadline)
		close(stop)
		freeErr := <-done
		// On any error, keep the final process-exit fence active while the
		// CLI reports it: stderr may block, and Free itself may have failed.
		return errors.Join(cause, freeErr)
	}
	for {
		select {
		case <-wd.expired:
			return cleanup(fmt.Errorf("worker lease expired"))
		case <-ctx.Done():
			return cleanup(ctx.Err())
		case msg := <-messages:
			// A queued authorization must not win select's random choice after
			// cancellation. Once stop is observed, native allocation cannot start.
			if err := ctx.Err(); err != nil {
				return cleanup(err)
			}
			if msg.err != nil {
				if errors.Is(msg.err, io.EOF) {
					return cleanup(nil)
				}
				return cleanup(msg.err)
			}
			now, err = o.Clock.Now()
			if err != nil {
				return cleanup(err)
			}
			if err = validator.accept(msg.lease, now); err != nil {
				return cleanup(err)
			}
			if msg.lease.Type == "STOP" {
				return cleanup(nil)
			}
			if !wd.set(msg.lease.ExpiresAtBootNS) {
				return cleanup(fmt.Errorf("worker already releasing"))
			}
			if !started {
				started = true
				stop = make(chan struct{})
				result = make(chan allocationResult, 1)
				done = make(chan error, 1)
				go func(l gdsapi.Lease) {
					runtime.LockOSThread()
					defer runtime.UnlockOSThread()
					a, e := backend.Allocate(o.GPUUUID, l.Bytes, l.MinFreeBytes)
					result <- allocationResult{e}
					if e != nil {
						done <- e
						return
					}
					<-stop
					done <- a.Free()
				}(msg.lease)
			} else {
				if !ready {
					return cleanup(fmt.Errorf("renewal arrived before READY"))
				}
				reply := gdsapi.Ready{Type: "RENEWED", Protocol: gdsapi.ProtocolVersion, Token: o.Token, UUID: o.GPUUUID, PID: os.Getpid(), Seq: validator.seq, AllocatedBytes: validator.bytes}
				if err := encodeMessage(o.Output, reply); err != nil {
					return cleanup(err)
				}
			}
		case r := <-result:
			result = nil
			if r.err != nil {
				return cleanup(r.err)
			}
			ready = true
			reply := gdsapi.Ready{Type: "READY", Protocol: gdsapi.ProtocolVersion, Token: o.Token, UUID: o.GPUUUID, PID: os.Getpid(), Seq: validator.seq, AllocatedBytes: validator.bytes}
			if err := encodeMessage(o.Output, reply); err != nil {
				return cleanup(err)
			}
		}
	}
}
