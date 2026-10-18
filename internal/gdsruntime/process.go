package gdsruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsutil"
)

var ErrUnconfirmedExit = errors.New("GDS child exit unconfirmed")

type boundedBuffer struct {
	mu       sync.Mutex
	b        []byte
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.b)+len(p) > b.limit {
		b.overflow = true
		n := b.limit - len(b.b)
		b.b = append(b.b, p[:n]...)
	} else {
		b.b = append(b.b, p...)
	}
	return len(p), nil
}
func (b *boundedBuffer) result() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.b...), b.overflow
}

// Probe always reaps a timed-out native query before another query is allowed.
// A process that cannot be confirmed exited is a terminal monitor failure.
func Probe(ctx context.Context, executable, uuid string) (gdsapi.Sample, error) {
	var sample gdsapi.Sample
	cmd := exec.Command(executable, "__probe", "--gpu", uuid)
	out := &boundedBuffer{limit: 64 << 10}
	stderr := &boundedBuffer{limit: 8 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return sample, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(ProbeTimeout)
	defer timer.Stop()
	var reason error
	select {
	case err := <-done:
		if err != nil {
			b, _ := stderr.result()
			return sample, fmt.Errorf("GPU probe: %w: %s", err, b)
		}
	case <-ctx.Done():
		reason = ctx.Err()
	case <-timer.C:
		reason = fmt.Errorf("GPU probe timeout")
	}
	if reason != nil {
		_ = cmd.Process.Kill()
		t := time.NewTimer(CleanupTimeout)
		defer t.Stop()
		select {
		case <-done:
			return sample, reason
		case <-t.C:
			return sample, fmt.Errorf("%w: %v", ErrUnconfirmedExit, reason)
		}
	}
	b, overflow := out.result()
	if overflow {
		return sample, fmt.Errorf("GPU probe output exceeds limit")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&sample); err != nil {
		return sample, fmt.Errorf("GPU probe protocol: %w", err)
	}
	var tail any
	if err := d.Decode(&tail); err != io.EOF {
		return sample, fmt.Errorf("GPU probe trailing output")
	}
	return sample, nil
}

type workerChild struct {
	cmd               *exec.Cmd
	process           gdsapi.Process
	stdin             io.WriteCloser
	responses         chan workerResponse
	done              chan error
	reaped            bool
	token, uuid, boot string
	seq               uint64
	bytes, reserve    uint64
}
type workerResponse struct {
	ready gdsapi.Ready
	err   error
}

func (w *workerChild) write(ctx context.Context, l gdsapi.Lease) error {
	done := make(chan error, 1)
	go func() { done <- encodeMessage(w.stdin, l) }()
	timer := time.NewTimer(ReplyTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = w.stdin.Close()
		return ctx.Err()
	case <-timer.C:
		_ = w.stdin.Close()
		return fmt.Errorf("worker pipe write timeout")
	}
}
func (w *workerChild) lease(kind string, started int64) (gdsapi.Lease, error) {
	deadline, err := checkedDeadline(started, int64(LeaseDuration))
	if err != nil {
		return gdsapi.Lease{}, err
	}
	w.seq++
	return gdsapi.Lease{Type: kind, Protocol: gdsapi.ProtocolVersion, Boot: w.boot, Token: w.token, UUID: w.uuid, Seq: w.seq, ExpiresAtBootNS: deadline, Bytes: w.bytes, MinFreeBytes: w.reserve}, nil
}
func (w *workerChild) exchange(ctx context.Context, kind string, started int64) error {
	l, err := w.lease(kind, started)
	if err != nil {
		return err
	}
	if err = w.write(ctx, l); err != nil {
		return err
	}
	timer := time.NewTimer(ReplyTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-w.done:
		w.reaped = true
		return fmt.Errorf("worker exited before reply: %v", err)
	case <-timer.C:
		return fmt.Errorf("worker reply timeout")
	case response := <-w.responses:
		if response.err != nil {
			return response.err
		}
		r := response.ready
		want := "RENEWED"
		if kind == "ALLOCATE" {
			want = "READY"
		}
		if r.Type != want || r.Protocol != gdsapi.ProtocolVersion || r.Token != w.token || r.UUID != w.uuid || r.PID != w.process.PID || r.Seq != w.seq || r.AllocatedBytes != w.bytes || r.Problem != "" {
			return fmt.Errorf("worker reply identity or allocation mismatch")
		}
		return nil
	}
}
func (w *workerChild) stop() error {
	// Closing the only parent write end releases even a worker that has not been
	// authorized. Do not wait for an ACK in a potentially wedged CUDA call.
	_ = w.stdin.Close()
	if w.reaped {
		return nil
	}
	timer := time.NewTimer(200 * time.Millisecond)
	select {
	case <-w.done:
		w.reaped = true
		timer.Stop()
		return nil
	case <-timer.C:
	}
	if err := gdsutil.Stop(w.process); err != nil {
		select {
		case <-w.done:
			w.reaped = true
			return nil
		default:
			return fmt.Errorf("%w: %v", ErrUnconfirmedExit, err)
		}
	}
	timer = time.NewTimer(CleanupTimeout)
	defer timer.Stop()
	select {
	case <-w.done:
		w.reaped = true
		return nil
	case <-timer.C:
		return fmt.Errorf("%w: worker was not reaped", ErrUnconfirmedExit)
	}
}
func launchWorker(ctx context.Context, exe, uuid, token, boot string, deviceLock *os.File, bytes, reserve uint64) (*workerChild, error) {
	expectedExecutable, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "__worker", "--gpu", uuid, "--token", token, "--boot-id", boot)
	cmd.ExtraFiles = []*os.File{deviceLock}
	cmd.Stderr = os.Stderr
	// Probe has already verified the user's visibility restriction. Narrow this
	// worker to that UUID; an index can never select a remapped physical device.
	cmd.Env = replaceEnv(os.Environ(), "CUDA_VISIBLE_DEVICES", uuid)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}
	w := &workerChild{cmd: cmd, stdin: stdin, responses: make(chan workerResponse, 1), done: make(chan error, 1), token: token, uuid: uuid, boot: boot, bytes: bytes, reserve: reserve}
	go func() { w.done <- cmd.Wait() }()
	// The worker cannot allocate until its captured identity is committed by the
	// caller and exchange(ALLOCATE) succeeds.
	process, err := gdsutil.Inspect(cmd.Process.Pid)
	if err == nil && (process.Executable != expectedExecutable || process.ExecutableDeleted) {
		err = fmt.Errorf("worker executable identity mismatch")
	}
	if err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		select {
		case <-w.done:
			return nil, err
		case <-time.After(CleanupTimeout):
			return nil, fmt.Errorf("%w: capture worker: %v", ErrUnconfirmedExit, err)
		}
	}
	w.process = process
	go func() {
		s := protocolScanner(stdout)
		for {
			r, e := readMessage[gdsapi.Ready](s)
			select {
			case w.responses <- workerResponse{r, e}:
			case <-ctx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	return w, nil
}
func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if len(e) < len(prefix) || e[:len(prefix)] != prefix {
			out = append(out, e)
		}
	}
	return append(out, prefix+value)
}

// readGate is deliberately byte-sized and bounded. No JSON stream can smuggle
// an authorization before the parent records this monitor's kernel identity.
func readGate(ctx context.Context, gate *os.File) error {
	defer gate.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		var b [1]byte
		_, err := io.ReadFull(gate, b[:])
		if err == nil && b[0] != 1 {
			err = fmt.Errorf("monitor start denied")
		}
		done <- err
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("monitor authorization timeout")
	}
}
