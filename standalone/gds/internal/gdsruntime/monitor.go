package gdsruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsutil"
)

type RunOptions struct {
	Dir, Executable, Token, BootID string
	ExpectedStateHash              string
	Config                         gdsapi.Config
	Managed                        bool
	Clock                          Clock
	testLockPath                   string
}

var (
	errProgramChanged       = errors.New("GDS executable changed during monitor run")
	errRuntimeRecordChanged = errors.New("GDS runtime ownership record changed")
)

func randomToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return gdsutil.Write(path, append(b, '\n'), 0600)
}

// Each runtime record remains tied to the inode and bytes this run observed or
// last committed. A new capture at every publish would silently adopt and then
// overwrite a concurrent editor's replacement, including during final cleanup.
type runtimeRecord struct {
	snapshot gdsutil.Snapshot
	dir      gdsutil.DirectorySnapshot
}

func captureRuntimeRecord(path string, dir gdsutil.DirectorySnapshot) (*runtimeRecord, error) {
	snapshot, err := gdsutil.Capture(path, 64<<10, true)
	if err != nil {
		return nil, err
	}
	return &runtimeRecord{snapshot: snapshot, dir: dir}, nil
}

func (r *runtimeRecord) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	next, err := gdsutil.WriteSnapshotExpected(r.snapshot.Path, append(b, '\n'), 0600, r.snapshot, r.dir)
	if err == nil {
		r.snapshot = next
	}
	return err
}

func (r *runtimeRecord) check() error {
	if err := r.dir.Check(); err != nil {
		return err
	}
	return r.snapshot.Check()
}
func readRecord(path string) (gdsapi.Record, error) {
	var r gdsapi.Record
	b, err := gdsutil.Read(path, 64<<10)
	if err != nil {
		return r, err
	}
	if err = decodeRuntimeJSON(b, &r); err != nil {
		return r, err
	}
	if r.Schema != 1 || !validRuntimeToken(r.Token) || r.ParentToken != "" && !validRuntimeToken(r.ParentToken) || r.Process.PID <= 1 || r.Process.Started == "" || r.Process.Boot == "" || !filepath.IsAbs(r.Process.Executable) || !gdsapi.ValidUUID(r.UUID) || !validRuntimeHash(r.ConfigHash) || !validRuntimeHash(r.ProgramHash) || r.Phase != "starting" && r.Phase != "running" && r.Phase != "stopped" {
		return r, fmt.Errorf("invalid process ownership record")
	}
	return r, nil
}

func decodeRuntimeJSON(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var tail any
	if err := d.Decode(&tail); err != io.EOF {
		return fmt.Errorf("trailing runtime JSON data")
	}
	return nil
}

func validRuntimeToken(token string) bool { return len(token) >= 32 && len(token) <= 128 }

func validRuntimeHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func validRuntimeState(state gdsapi.State) bool {
	if state.Schema != 1 || state.Boot == "" || !validRuntimeToken(state.Token) || !gdsapi.ValidUUID(state.UUID) || !validRuntimeHash(state.ConfigHash) || state.Timestamp < 0 || state.ObservedBootNS < 0 || state.IdleSeconds < 0 || state.WorkerPID < 0 || state.WorkerPID == 1 || state.WorkerPID == 0 && state.AllocatedBytes != 0 {
		return false
	}
	switch state.Runtime {
	case "starting", "running", "exited", "unknown":
	default:
		return false
	}
	switch state.Scheduler {
	case "observing", "idle", "starting-worker", "holding", "releasing", "backoff", "unavailable":
	default:
		return false
	}
	return state.Scheduler != "holding" || state.WorkerPID > 1 && state.AllocatedBytes != 0
}

func checkExpectedState(snapshot gdsutil.Snapshot, managed bool, expected string) error {
	if !managed {
		if expected != "" {
			return fmt.Errorf("state snapshot authorization requires managed startup")
		}
		return nil
	}
	if expected == "absent" {
		if snapshot.Info != nil {
			return fmt.Errorf("GDS state appeared after startup authorization")
		}
		return nil
	}
	if !validRuntimeHash(expected) || snapshot.Info == nil || gdsutil.Hash(snapshot.Data) != expected {
		return fmt.Errorf("GDS state changed after startup authorization")
	}
	return nil
}
func noRemainingWorker(path string) error {
	r, err := readRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot verify previous worker: %w", err)
	}
	actual, err := gdsutil.Inspect(r.Process.PID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("previous worker ownership unknown: %w", err)
	}
	if gdsutil.SameBirth(actual, r.Process) {
		return fmt.Errorf("previous worker is still running")
	}
	return nil
}

func rejectUnknownPreviousRun(dir, boot string, managed bool) error {
	b, err := gdsutil.Read(filepath.Join(dir, "state.json"), 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state gdsapi.State
	if err = decodeRuntimeJSON(b, &state); err != nil {
		return fmt.Errorf("previous GDS state cannot be verified: %w", err)
	}
	if !validRuntimeState(state) {
		return fmt.Errorf("previous GDS state has invalid schema, identity or phase")
	}
	record, err := readRecord(filepath.Join(dir, "process.json"))
	if err != nil {
		return fmt.Errorf("previous GDS state has no verifiable process ownership record: %w", err)
	}
	if !managed && (record.Process.Boot != state.Boot || record.Token != state.Token || record.UUID != state.UUID || record.ConfigHash != state.ConfigHash) {
		return fmt.Errorf("previous standalone GDS state does not match its process ownership record")
	}
	// No child from a previous kernel boot can still hold a context. Retain
	// its evidence on disk until this start commits its own new records.
	if state.Boot != boot {
		return nil
	}
	if state.Runtime == "unknown" {
		return fmt.Errorf("previous GDS run has an unconfirmed child exit; ownership records retained")
	}
	if record.Process.Boot != state.Boot || record.Token == state.Token && (record.UUID != state.UUID || record.ConfigHash != state.ConfigHash) {
		return fmt.Errorf("previous GDS state does not match its process ownership record")
	}
	return nil
}

// Run never loads a native driver. It keeps one monitor instance lock and the
// host-wide per-user device lock until every authorized worker has exited.
func Run(ctx context.Context, o RunOptions) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.Managed {
		gate := os.NewFile(3, "gds-monitor-authorization")
		if gate == nil {
			return fmt.Errorf("missing monitor authorization gate")
		}
		if err := readGate(ctx, gate); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.Config.Validate(); err != nil {
		return err
	}
	if o.Clock == nil {
		o.Clock = BootClock{}
	}
	if _, err := o.Clock.Now(); err != nil {
		return err
	}
	boot, err := gdsutil.BootID()
	if err != nil {
		return err
	}
	if o.BootID == "" {
		o.BootID = boot
	}
	if o.BootID != boot {
		return fmt.Errorf("monitor boot identity mismatch")
	}
	if o.Executable == "" {
		o.Executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	o.Executable, err = filepath.Abs(o.Executable)
	if err != nil {
		return err
	}
	if o.Token == "" {
		o.Token, err = randomToken()
		if err != nil {
			return err
		}
	}
	if len(o.Token) < 32 || len(o.Token) > 128 {
		return fmt.Errorf("invalid monitor token")
	}
	if !filepath.IsAbs(o.Dir) {
		return fmt.Errorf("GDS directory must be absolute")
	}
	if err = gdsutil.EnsureDir(o.Dir); err != nil {
		return err
	}
	// Standalone starts participate in the manager's mutation lock until their
	// kernel identity is published. Otherwise an update/remove could pass its
	// final stopped check while this process is still acquiring runtime locks.
	// Managed starts already have a parent holding this exact lock and gate.
	var startupLock *os.File
	if !o.Managed {
		startupLock, err = gdsutil.Lock(filepath.Join(o.Dir, ".lock"))
		if err != nil {
			return err
		}
	}
	unlockStartup := func() {
		if startupLock != nil {
			_ = startupLock.Close()
			startupLock = nil
		}
	}
	defer unlockStartup()
	runLock, err := gdsutil.Lock(filepath.Join(o.Dir, ".run.lock"))
	if err != nil {
		return err
	}
	defer runLock.Close()
	if err = rejectUnknownPreviousRun(o.Dir, o.BootID, o.Managed); err != nil {
		return err
	}
	if !o.Managed {
		if err = noRemainingWorker(filepath.Join(o.Dir, "process.json")); err != nil {
			return fmt.Errorf("previous monitor: %w", err)
		}
	}
	if err = noRemainingWorker(filepath.Join(o.Dir, "worker.json")); err != nil {
		return err
	}
	directory, err := gdsutil.CaptureDirectory(o.Dir)
	if err != nil {
		return err
	}
	processFile, err := captureRuntimeRecord(filepath.Join(o.Dir, "process.json"), directory)
	if err != nil {
		return err
	}
	workerFile, err := captureRuntimeRecord(filepath.Join(o.Dir, "worker.json"), directory)
	if err != nil {
		return err
	}
	stateFile, err := captureRuntimeRecord(filepath.Join(o.Dir, "state.json"), directory)
	if err != nil {
		return err
	}
	if err = checkExpectedState(stateFile.snapshot, o.Managed, o.ExpectedStateHash); err != nil {
		return err
	}
	lockPath := deviceLockPath(o.Config.GPUUUID)
	if o.testLockPath != "" {
		lockPath = o.testLockPath
	}
	if lockPath == "" {
		return fmt.Errorf("GDS runtime requires Linux amd64")
	}
	if err = gdsutil.EnsureDir(filepath.Dir(lockPath)); err != nil {
		return err
	}
	deviceLock, err := gdsutil.Lock(lockPath)
	if err != nil {
		return fmt.Errorf("GPU already reserved or lock unsafe: %w", err)
	}
	defer deviceLock.Close()
	// Tie this run to the actual disk snapshot, preserving comments and formatting
	// in the same digest the manager uses to report config_changed.
	configData, err := gdsutil.Read(filepath.Join(o.Dir, "config.toml"), 64<<10)
	if err != nil {
		return err
	}
	diskConfig, err := gdsapi.ParseConfig(configData)
	if err != nil {
		return err
	}
	if diskConfig != o.Config {
		return fmt.Errorf("GDS configuration changed before monitor startup")
	}
	configHash := gdsutil.Hash(configData)
	programSnapshot, err := gdsutil.Capture(o.Executable, 256<<20, false)
	if err != nil {
		return err
	}
	programHash := gdsutil.Hash(programSnapshot.Data)
	self, err := gdsutil.Inspect(os.Getpid())
	if err != nil {
		return err
	}
	monitorRecord := gdsapi.Record{Schema: 1, Token: o.Token, Process: self, ConfigHash: configHash, ProgramHash: programHash, UUID: o.Config.GPUUUID, Phase: "starting"}
	if o.Managed {
		existing, e := readRecord(filepath.Join(o.Dir, "process.json"))
		if e != nil {
			return e
		}
		if existing.Token != o.Token || existing.Process != self || existing.UUID != o.Config.GPUUUID || existing.ConfigHash != configHash || existing.ProgramHash != programHash {
			return fmt.Errorf("monitor authorization record mismatch")
		}
	} else if err = processFile.write(monitorRecord); err != nil {
		return err
	}
	unlockStartup()
	machine, _ := NewMachine(o.Config)
	state := gdsapi.State{Schema: 1, Boot: o.BootID, Token: o.Token, UUID: o.Config.GPUUUID, Runtime: "starting", Scheduler: "observing", ConfigHash: configHash}
	publish := func() error {
		state.Timestamp = time.Now().Unix()
		return stateFile.write(state)
	}
	var worker *workerChild
	var workerRecord gdsapi.Record
	release := func() error {
		if worker == nil {
			return nil
		}
		state.Scheduler = "releasing"
		_ = publish()
		if err := worker.stop(); err != nil {
			state.Runtime = "unknown"
			state.Problem = err.Error()
			_ = publish()
			return err
		}
		workerRecord.Phase = "stopped"
		if err := workerFile.write(workerRecord); err != nil {
			return err
		}
		worker = nil
		state.WorkerPID = 0
		state.AllocatedBytes = 0
		state.IdleSeconds = 0
		machine.WorkerReleased()
		return nil
	}
	defer func() {
		terminalPhase := machine.Phase()
		if worker != nil {
			if err := release(); err != nil {
				retErr = errors.Join(retErr, err)
				state.Runtime = "unknown"
			}
		}
		if state.Runtime != "unknown" {
			state.Runtime = "exited"
			monitorRecord.Phase = "stopped"
			_ = processFile.write(monitorRecord)
		}
		if retErr != nil {
			state.Problem = retErr.Error()
		}
		// Releasing a worker resets normal observation credit, but cannot
		// erase the terminal reason that stopped this monitor.
		if state.Runtime == "unknown" {
			state.Scheduler = "unavailable"
			if worker != nil {
				state.Scheduler = "releasing"
			}
		} else if terminalPhase == "unavailable" {
			state.Scheduler = terminalPhase
		} else {
			state.Scheduler = machine.Phase()
		}
		_ = publish()
	}()
	if err = publish(); err != nil {
		return err
	}
	first := true
	probe := func() (gdsapi.Sample, error, int64, int64) {
		// Runtime evidence is also the manager's authority for Stop/update. If
		// another writer replaces it, this run must release rather than continue
		// holding a device under records that no longer identify its processes.
		for _, record := range []*runtimeRecord{processFile, workerFile, stateFile} {
			if e := record.check(); e != nil {
				return gdsapi.Sample{}, fmt.Errorf("%w: %v", errRuntimeRecordChanged, e), 0, 0
			}
		}
		if e := programSnapshot.Check(); e != nil {
			return gdsapi.Sample{}, fmt.Errorf("%w: %v", errProgramChanged, e), 0, 0
		}
		started, e := o.Clock.Now()
		if e != nil {
			return gdsapi.Sample{}, e, 0, 0
		}
		s, e := Probe(ctx, o.Executable, o.Config.GPUUUID)
		completed, clockErr := o.Clock.Now()
		if clockErr != nil {
			e = errors.Join(e, clockErr)
		}
		return s, e, started, completed
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		s, probeErr, started, completed := probe()
		if errors.Is(probeErr, errProgramChanged) || errors.Is(probeErr, errRuntimeRecordChanged) {
			machine.phase = "unavailable"
			return probeErr
		}
		if errors.Is(probeErr, ErrUnconfirmedExit) {
			state.Runtime = "unknown"
			return probeErr
		}
		pid := 0
		if worker != nil {
			pid = worker.process.PID
			// Exclusion requires the same verified kernel birth and executable on every
			// observation. A sole context PID or a stale record is never ownership.
			actual, e := gdsutil.Inspect(pid)
			if e != nil || actual != worker.process {
				probeErr = fmt.Errorf("worker kernel identity no longer confirmed")
			}
			select {
			case e := <-worker.done:
				worker.reaped = true
				probeErr = fmt.Errorf("worker exited: %v", e)
			default:
			}
		}
		d := machine.Observe(s, probeErr, started, completed, pid)
		state.Scheduler = d.Phase
		state.Problem = d.Problem
		state.IdleSeconds = d.IdleSeconds
		if probeErr == nil && s.Complete && s.Supported {
			state.ObservedBootNS = started
		}
		if first {
			first = false
			if d.Phase == "unavailable" || probeErr != nil {
				return fmt.Errorf("initial GPU observation failed: %s", d.Problem)
			}
			monitorRecord.Phase = "running"
			if err = processFile.write(monitorRecord); err != nil {
				return err
			}
			state.Runtime = "running"
		}
		if err = publish(); err != nil {
			return err
		}
		switch d.Action {
		case Release:
			if err = release(); err != nil {
				return err
			}
		case Renew:
			if d.Bytes != worker.bytes {
				state.Problem = "GPU memory capacity changed during reservation"
				if err = release(); err != nil {
					return err
				}
				break
			}
			if err = worker.exchange(ctx, "RENEW", started); err != nil {
				state.Problem = err.Error()
				if err = release(); err != nil {
					return err
				}
				machine.WorkerFailed(completed)
			}
		case Allocate:
			// Allocation has its own fresh complete query; an idle threshold is never
			// itself a reusable authorization.
			fresh, e, start, end := probe()
			if errors.Is(e, errProgramChanged) || errors.Is(e, errRuntimeRecordChanged) {
				machine.phase = "unavailable"
				return e
			}
			if errors.Is(e, ErrUnconfirmedExit) {
				state.Runtime = "unknown"
				return e
			}
			if err = machine.FreshPreflight(fresh, e, start, end); err != nil {
				state.Problem = err.Error()
				machine.WorkerReleased()
				break
			}
			state.ObservedBootNS = start
			bytes := targetBytes(fresh.TotalBytes, o.Config.MemoryPercent)
			token, e := randomToken()
			if e != nil {
				return e
			}
			if e = programSnapshot.Check(); e != nil {
				machine.phase = "unavailable"
				return fmt.Errorf("%w: %v", errProgramChanged, e)
			}
			worker, e = launchWorker(ctx, o.Executable, o.Config.GPUUUID, token, o.BootID, deviceLock, bytes, uint64(o.Config.MinFreeMiB)<<20)
			if e != nil {
				worker = nil
				state.Problem = e.Error()
				if errors.Is(e, ErrUnconfirmedExit) {
					state.Runtime = "unknown"
					return e
				}
				machine.WorkerFailed(end)
				break
			}
			workerRecord = gdsapi.Record{Schema: 1, Token: token, ParentToken: o.Token, Process: worker.process, ConfigHash: configHash, ProgramHash: programHash, UUID: o.Config.GPUUUID, Phase: "starting"}
			if err = workerFile.write(workerRecord); err != nil {
				return err
			}
			state.WorkerPID = worker.process.PID
			state.Scheduler = "starting-worker"
			state.IdleSeconds = 0
			if err = publish(); err != nil {
				return err
			}
			if err = worker.exchange(ctx, "ALLOCATE", start); err != nil {
				state.Problem = err.Error()
				if err = release(); err != nil {
					return err
				}
				machine.WorkerFailed(end)
				break
			}
			confirm, e, confirmStart, confirmEnd := probe()
			if errors.Is(e, errProgramChanged) || errors.Is(e, errRuntimeRecordChanged) {
				machine.phase = "unavailable"
				return e
			}
			if errors.Is(e, ErrUnconfirmedExit) {
				state.Runtime = "unknown"
				return e
			}
			actual, identityErr := gdsutil.Inspect(worker.process.PID)
			if identityErr != nil || actual != worker.process {
				e = fmt.Errorf("worker identity not confirmed after allocation")
			}
			confirmed := machine.Observe(confirm, e, confirmStart, confirmEnd, worker.process.PID)
			if confirmed.Action != Renew {
				state.Problem = confirmed.Problem
				if err = release(); err != nil {
					return err
				}
				machine.WorkerFailed(confirmEnd)
				break
			}
			// Renew from the fresh post-allocation sample; READY alone cannot extend it.
			if err = worker.exchange(ctx, "RENEW", confirmStart); err != nil {
				state.Problem = err.Error()
				if err = release(); err != nil {
					return err
				}
				machine.WorkerFailed(confirmEnd)
				break
			}
			machine.WorkerConfirmed()
			workerRecord.Phase = "running"
			if err = workerFile.write(workerRecord); err != nil {
				return err
			}
			state.Scheduler = "holding"
			state.AllocatedBytes = bytes
			state.ObservedBootNS = confirmStart
		}
		state.Scheduler = machine.Phase()
		if err = publish(); err != nil {
			return err
		}
		timer := time.NewTimer(time.Duration(o.Config.ProbeIntervalSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
