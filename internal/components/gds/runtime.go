package gds

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) readRecord(name string) (gdsapi.Record, gdsutil.Snapshot, error) {
	var r gdsapi.Record
	dir, program, e := s.paths()
	if e != nil {
		return r, gdsutil.Snapshot{}, e
	}
	path := filepath.Join(dir, name)
	if e = gdsutil.SafePath(s.Root, path); e != nil {
		return r, gdsutil.Snapshot{}, e
	}
	b, e := gdsutil.Capture(path, 32<<10, true)
	if e != nil {
		return r, b, e
	}
	if b.Info == nil {
		return r, b, os.ErrNotExist
	}
	if json.Unmarshal(b.Data, &r) != nil {
		return r, b, fmt.Errorf("GDS %s 身份记录不完整；保留现场", name)
	}
	validProgram := r.Process.Executable == program
	validUUID := gdsapi.ValidUUID(r.UUID)
	// Candidate helper records are confined to this private component directory.
	if name == "invocation.json" {
		validProgram = validProgram || filepath.Dir(r.Process.Executable) == dir && strings.HasPrefix(filepath.Base(r.Process.Executable), ".gds-candidate-")
		validUUID = r.UUID == "" || validUUID
	}
	if r.Schema != 1 || len(r.Token) < 32 || r.Process.PID <= 1 || r.Process.UID != uint32(os.Geteuid()) || r.Process.Boot == "" || r.Process.Started == "" || !validProgram || !validHash(r.ConfigHash) || !validHash(r.ProgramHash) || !validUUID || (r.Phase != "starting" && r.Phase != "running" && r.Phase != "stopped") {
		return r, b, fmt.Errorf("GDS %s 身份记录不完整；保留现场", name)
	}
	return r, b, nil
}
func (s *Service) actual(r gdsapi.Record) (gdsapi.Process, error) {
	boot, err := s.bootID()
	if err != nil {
		return gdsapi.Process{}, err
	}
	// A PID from another kernel boot cannot still identify this child. Its
	// current owner may be unreadable or belong to another user after reuse.
	if r.Process.Boot != boot {
		return gdsapi.Process{}, os.ErrNotExist
	}
	p, e := s.runner().Inspect(r.Process.PID)
	if e != nil {
		return p, e
	}
	if !gdsutil.SameBirth(p, r.Process) {
		return p, os.ErrNotExist
	}
	if p.Executable != r.Process.Executable {
		return p, fmt.Errorf("GDS 记录进程的程序身份已变化")
	}
	return p, nil
}

// Unknown snapshots do not authorize signals. They retain evidence that this
// exact run could not reap a native child, even after its monitor has exited.
func (s *Service) unresolvedState(r gdsapi.Record) (*gdsapi.State, error) {
	dir, _, err := s.paths()
	if err != nil {
		return nil, err
	}
	boot, err := s.bootID()
	if err != nil {
		return nil, err
	}
	data, err := gdsutil.Read(filepath.Join(dir, "state.json"), 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state gdsapi.State
	if json.Unmarshal(data, &state) != nil || state.Schema != 1 || state.Boot == "" {
		return nil, fmt.Errorf("GDS 状态记录不完整；无法确认先前子进程退出")
	}
	if state.Boot != r.Process.Boot {
		return nil, fmt.Errorf("GDS 状态与进程记录的系统启动标识不匹配；保留未确认现场")
	}
	if r.Process.Boot != boot {
		return nil, nil
	}
	if state.Token != r.Token || state.UUID != r.UUID || state.ConfigHash != r.ConfigHash {
		return nil, fmt.Errorf("GDS 状态与当前启动的身份记录不匹配；保留未确认现场")
	}
	switch state.Runtime {
	case "starting", "running", "exited":
		return nil, nil
	case "unknown":
		return &state, nil
	default:
		return nil, fmt.Errorf("GDS 状态中的运行阶段无效；无法确认子进程退出")
	}
}

func (s *Service) stateWithoutRecord() error {
	dir, _, err := s.paths()
	if err != nil {
		return err
	}
	state, err := gdsutil.Capture(filepath.Join(dir, "state.json"), 64<<10, true)
	if err != nil {
		return err
	}
	if state.Info != nil {
		return fmt.Errorf("GDS 状态仍存在但进程身份记录缺失；无法确认先前子进程退出，保留现场")
	}
	return state.Check()
}

func (s *Service) refuseUnresolved() error {
	r, _, err := s.readRecord("process.json")
	if errors.Is(err, os.ErrNotExist) {
		return s.stateWithoutRecord()
	}
	if err != nil {
		return err
	}
	state, err := s.unresolvedState(r)
	if err != nil {
		return err
	}
	if state != nil {
		return fmt.Errorf("GDS 仍有未确认退出的子进程；禁止创建新探针或修改程序: %s", state.Problem)
	}
	return nil
}
func (s *Service) requireStopped() error {
	for _, name := range []string{"process.json", "worker.json", "invocation.json"} {
		r, b, e := s.readRecord(name)
		if errors.Is(e, os.ErrNotExist) {
			if e = b.Check(); e != nil {
				return e
			}
			if name == "process.json" {
				if e = s.stateWithoutRecord(); e != nil {
					return e
				}
			}
			continue
		}
		if e != nil {
			return e
		}
		_, e = s.actual(r)
		if e == nil {
			return fmt.Errorf("GDS %s 仍在运行；请先停止", name)
		}
		if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if name == "process.json" {
			state, stateErr := s.unresolvedState(r)
			if stateErr != nil {
				return stateErr
			}
			if state != nil {
				return fmt.Errorf("GDS 本次运行仍有未确认退出的子进程；保留 unknown 现场: %s", state.Problem)
			}
		}
		if e = b.Check(); e != nil {
			// The verified monitor/worker may commit its own final stopped phase
			// while exiting. Only that exact identity and token may be adopted.
			current, currentSnapshot, readErr := s.readRecord(name)
			expected := r
			expected.Phase = "stopped"
			if readErr != nil || current != expected {
				return e
			}
			b = currentSnapshot
		}
	}
	return nil
}
func (s *Service) preflightStop() error {
	for _, name := range []string{"process.json", "worker.json", "invocation.json"} {
		r, b, e := s.readRecord(name)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if _, e = s.actual(r); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e = b.Check(); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) preflightStart() error {
	if err := s.invocationStopped(); err != nil {
		return err
	}
	r, b, e := s.readRecord("process.json")
	if errors.Is(e, os.ErrNotExist) {
		return s.requireStopped()
	}
	if e != nil {
		return e
	}
	_, e = s.actual(r)
	if errors.Is(e, os.ErrNotExist) {
		return s.requireStopped()
	}
	if e != nil {
		return e
	}
	worker, workerSnapshot, workerErr := s.readRecord("worker.json")
	if workerErr != nil && !errors.Is(workerErr, os.ErrNotExist) {
		return workerErr
	}
	if workerErr == nil {
		actual, inspectErr := s.actual(worker)
		if inspectErr != nil && !errors.Is(inspectErr, os.ErrNotExist) {
			return inspectErr
		}
		if inspectErr == nil && (worker.ParentToken != r.Token || worker.UUID != r.UUID || worker.ConfigHash != r.ConfigHash || worker.ProgramHash != r.ProgramHash || actual.ExecutableDeleted) {
			return fmt.Errorf("GDS 存活 worker 与当前 monitor 身份不一致；请先停止")
		}
		if e = workerSnapshot.Check(); e != nil {
			return e
		}
	}
	core, e := s.programSnapshot()
	if e != nil {
		return e
	}
	path, e := s.ConfigPath()
	if e != nil {
		return e
	}
	conf, e := gdsutil.Read(path, maxConfig)
	if e != nil {
		return e
	}
	if gdsutil.Hash(core.Data) != r.ProgramHash || gdsutil.Hash(conf) != r.ConfigHash {
		return fmt.Errorf("GDS 运行配置或程序已更改；请 restart")
	}
	if _, e = s.readyState(r); e != nil {
		return e
	}
	return b.Check()
}
func (s *Service) readyState(r gdsapi.Record) (gdsapi.State, error) {
	var state gdsapi.State
	dir, _, e := s.paths()
	if e != nil {
		return state, e
	}
	b, e := gdsutil.Read(filepath.Join(dir, "state.json"), 64<<10)
	if e != nil {
		return state, e
	}
	if json.Unmarshal(b, &state) != nil || state.Schema != 1 || state.Boot != r.Process.Boot || state.Token != r.Token || state.UUID != r.UUID || state.ConfigHash != r.ConfigHash || state.Runtime != "running" {
		return state, fmt.Errorf("GDS 尚未提交匹配的就绪状态")
	}
	boot, e := s.bootID()
	if e != nil || boot != r.Process.Boot {
		return state, fmt.Errorf("GDS 状态的系统启动标识无法核验")
	}
	now, e := s.bootTimeNS()
	if e != nil {
		return state, e
	}
	if state.ObservedBootNS <= 0 || now < state.ObservedBootNS || now-state.ObservedBootNS > int64(20*time.Second) {
		return state, fmt.Errorf("GDS 状态快照已过期或时间无效")
	}
	return state, nil
}
func (s *Service) Start() error { return s.withLock(s.startLocked) }
func (s *Service) startLocked(guard gdsutil.DirectorySnapshot) error {
	if e := s.Check(); e != nil {
		return e
	}
	if e := s.requireStopped(); e != nil {
		return s.preflightStart()
	}
	dir, _, e := s.paths()
	if e != nil {
		return e
	}
	previousState, e := gdsutil.Capture(filepath.Join(dir, "state.json"), 64<<10, true)
	if e != nil {
		return e
	}
	stateHash := "absent"
	if previousState.Info != nil {
		stateHash = gdsutil.Hash(previousState.Data)
	}
	core, e := s.programSnapshot()
	if e != nil {
		return e
	}
	path, e := s.ConfigPath()
	if e != nil {
		return e
	}
	conf, e := gdsutil.Capture(path, maxConfig, false)
	if e != nil {
		return e
	}
	cfg, e := gdsapi.ParseConfig(conf.Data)
	if e != nil {
		return e
	}
	if _, e = s.metadata(core); e != nil {
		return e
	}
	if _, e = s.probeLocked(); e != nil {
		return e
	}
	previous, e := gdsutil.Capture(filepath.Join(dir, "process.json"), 32<<10, true)
	if e != nil {
		return e
	}
	tokenBytes := make([]byte, 24)
	if _, e = rand.Read(tokenBytes); e != nil {
		return e
	}
	token := hex.EncodeToString(tokenBytes)
	boot, e := s.bootID()
	if e != nil {
		return e
	}
	var record gdsapi.Record
	command := Command{Program: core.Path, Dir: dir, Log: filepath.Join(dir, "output.log"), Args: []string{"run", "--dir", dir, "--managed", "--token", token, "--boot-id", boot, "--state-sha256", stateHash}, Env: s.environment()}
	p, done, e := s.runner().Start(command, func(p gdsapi.Process) error {
		if p.PID <= 1 || p.Executable != core.Path || p.Boot != boot {
			return fmt.Errorf("GDS 启动身份不符合预期")
		}
		if e = guard.Check(); e != nil {
			return e
		}
		if e = core.Check(); e != nil {
			return e
		}
		if e = conf.Check(); e != nil {
			return e
		}
		if e = previousState.Check(); e != nil {
			return e
		}
		if e = s.requireStopped(); e != nil {
			return e
		}
		record = gdsapi.Record{Schema: 1, Token: token, Process: p, ConfigHash: gdsutil.Hash(conf.Data), ProgramHash: gdsutil.Hash(core.Data), UUID: cfg.GPUUUID, Phase: "starting"}
		b, _ := json.Marshal(record)
		return gdsutil.WriteExpected(previous.Path, append(b, '\n'), 0600, previous, guard)
	})
	if e != nil {
		if p.PID > 1 {
			if stopErr := s.runner().Stop(p); stopErr != nil {
				return fmt.Errorf("GDS 启动失败: %v；启动进程退出未确认: %w", e, stopErr)
			}
		}
		return e
	}
	timeout := s.StartTimeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, e = s.actual(record); e != nil {
			break
		}
		if _, e = s.readyState(record); e == nil {
			if e = guard.Check(); e != nil {
				break
			}
			if e = core.Check(); e != nil {
				break
			}
			if e = conf.Check(); e != nil {
				break
			}
			return nil
		}
		select {
		case <-done:
			e = fmt.Errorf("GDS monitor 在就绪前退出")
			deadline = time.Now()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if e == nil {
		e = fmt.Errorf("GDS 启动超时")
	}
	stopErr := s.runner().Stop(p)
	if stopErr == nil {
		stopErr = s.stopLocked(guard)
	}
	if stopErr != nil {
		return fmt.Errorf("GDS 启动失败: %v；清理未确认: %w", e, stopErr)
	}
	return e
}
func (s *Service) Stop() error { return s.withLock(s.stopLocked) }
func (s *Service) stopLocked(guard gdsutil.DirectorySnapshot) error {
	// Stop monitor first so it cannot authorize a new worker between inspections.
	var stateProblems error
	for _, name := range []string{"process.json", "worker.json", "invocation.json"} {
		r, b, e := s.readRecord(name)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		p, e := s.actual(r)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e == nil {
			if e = b.Check(); e != nil {
				return e
			}
			if e = guard.Check(); e != nil {
				return e
			}
			if e = s.runner().Stop(p); e != nil {
				return e
			}
			if _, e = s.actual(r); !errors.Is(e, os.ErrNotExist) {
				if e == nil {
					return fmt.Errorf("GDS %s 尚未退出", name)
				}
				return e
			}
		}
		if e = b.Check(); e != nil {
			current, latest, readErr := s.readRecord(name)
			expected := r
			expected.Phase = "stopped"
			if readErr != nil || current != expected {
				return e
			}
			b = latest
		}
		r.Phase = "stopped"
		if name == "process.json" {
			state, stateErr := s.unresolvedState(r)
			if stateErr != nil {
				// An unreadable state is not permission to signal an unknown
				// birth, but must not skip a separately verified live worker.
				stateProblems = errors.Join(stateProblems, stateErr)
				continue
			}
			if state != nil {
				continue
			}
		}
		data, _ := json.Marshal(r)
		if e = gdsutil.WriteExpected(b.Path, append(data, '\n'), 0600, b, guard); e != nil {
			return e
		}
	}
	return errors.Join(stateProblems, s.requireStopped())
}
func (s *Service) Restart() error {
	return s.withLock(func(g gdsutil.DirectorySnapshot) error {
		dir, _, e := s.paths()
		if e != nil {
			return e
		}
		originalRecord, e := gdsutil.Capture(filepath.Join(dir, "process.json"), 32<<10, true)
		if e != nil {
			return e
		}
		// Finish all candidate checks before terminating a usable old instance.
		if e := s.Check(); e != nil {
			return e
		}
		core, e := s.programSnapshot()
		if e != nil {
			return e
		}
		path, e := s.ConfigPath()
		if e != nil {
			return e
		}
		conf, e := gdsutil.Capture(path, maxConfig, false)
		if e != nil {
			return e
		}
		if _, e = s.metadata(core); e != nil {
			return e
		}
		if _, e = s.probeLocked(); e != nil {
			return e
		}
		if e = core.Check(); e != nil {
			return e
		}
		if e = conf.Check(); e != nil {
			return e
		}
		if e = g.Check(); e != nil {
			return e
		}
		if e = originalRecord.Check(); e != nil {
			return e
		}
		if e = s.stopLocked(g); e != nil {
			return e
		}
		return s.startLocked(g)
	})
}
func (s *Service) Status() Status {
	result := Status{Program: "missing", Config: "missing", Runtime: "stopped", Worker: "stopped", Stale: true}
	dir, p, e := s.paths()
	result.Directory, result.ProgramPath = dir, p
	problem := func(e error) {
		result.Problems = append(result.Problems, e.Error())
		if result.Problem == "" {
			result.Problem = e.Error()
		}
	}
	if e != nil {
		problem(e)
		result.Runtime = "unknown"
		return result
	}
	if _, e = s.programSnapshot(); e == nil {
		result.Program = "present"
		result.Installed = true
	} else if !errors.Is(e, os.ErrNotExist) {
		result.Program = "invalid"
		problem(e)
	}
	if _, e = s.ReadConfig(); e == nil {
		result.Config = "readable"
		result.Configured = true
	} else if !errors.Is(e, os.ErrNotExist) {
		result.Config = "invalid"
		problem(e)
	}
	record, _, e := s.readRecord("process.json")
	if e == nil {
		result.PID = record.Process.PID
		conf, _ := gdsutil.Read(filepath.Join(dir, "config.toml"), maxConfig)
		result.ConfigChanged = gdsutil.Hash(conf) != record.ConfigHash
		actual, pe := s.actual(record)
		switch {
		case errors.Is(pe, os.ErrNotExist):
			result.Runtime = "exited"
			if record.Phase == "stopped" {
				result.Runtime = "stopped"
			}
		case pe != nil:
			result.Runtime = "unknown"
			problem(pe)
		default:
			result.Runtime = "starting"
			core, ce := gdsutil.Read(p, maxProgram)
			if actual.ExecutableDeleted || ce != nil || gdsutil.Hash(core) != record.ProgramHash {
				result.Runtime = "unknown"
				problem(fmt.Errorf("GDS 运行映像或磁盘程序已变化"))
			} else if state, se := s.readyState(record); se == nil {
				result.State = &state
				result.Runtime = "running"
				result.Scheduler = state.Scheduler
				result.Stale = false
			} else {
				// Process liveness, scheduler progress, and GPU observation age
				// are distinct. A stale sample does not make a live monitor starting.
				if state.Schema == 1 && state.Boot == record.Process.Boot && state.Token == record.Token && state.UUID == record.UUID && state.ConfigHash == record.ConfigHash && state.Runtime == "running" {
					result.State = &state
					result.Runtime = "running"
					result.Scheduler = state.Scheduler
				}
				problem(se)
			}
		}
		if state, stateErr := s.unresolvedState(record); stateErr != nil {
			result.Runtime = "unknown"
			result.Stale = true
			problem(stateErr)
		} else if state != nil {
			result.State = state
			result.Runtime = "unknown"
			result.Scheduler = state.Scheduler
			result.Stale = true
			problem(fmt.Errorf("GDS 子进程退出尚未确认: %s", state.Problem))
		}
	} else if errors.Is(e, os.ErrNotExist) {
		if stateErr := s.stateWithoutRecord(); stateErr != nil {
			result.Runtime = "unknown"
			problem(stateErr)
		}
	} else {
		result.Runtime = "unknown"
		problem(e)
	}
	worker, _, e := s.readRecord("worker.json")
	if e == nil {
		result.WorkerPID = worker.Process.PID
		if _, e = s.actual(worker); e == nil {
			result.Worker = "running"
		} else if !errors.Is(e, os.ErrNotExist) {
			result.Worker = "unknown"
			problem(e)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		result.Worker = "unknown"
		problem(e)
	}
	invocation, _, invocationErr := s.readRecord("invocation.json")
	if invocationErr == nil {
		result.InvocationPID = invocation.Process.PID
		if _, err := s.actual(invocation); err == nil {
			result.Invocation = "unknown"
			result.Runtime = "unknown"
			problem(fmt.Errorf("GDS 独立辅助进程尚未确认退出"))
		} else if errors.Is(err, os.ErrNotExist) {
			result.Invocation = "exited"
		} else {
			result.Invocation = "unknown"
			result.Runtime = "unknown"
			problem(err)
		}
	} else if !errors.Is(invocationErr, os.ErrNotExist) {
		result.Invocation = "unknown"
		result.Runtime = "unknown"
		problem(invocationErr)
	}
	return result
}
