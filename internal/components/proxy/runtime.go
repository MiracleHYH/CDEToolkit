package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type RuntimeStatus struct {
	State         string `json:"state"`
	PID           int    `json:"pid,omitempty"`
	ConfigHash    string `json:"config_hash,omitempty"`
	ConfigChanged bool   `json:"config_changed"`
	Listeners     bool   `json:"listeners"`
	Log           string `json:"log"`
	Diagnostic    string `json:"diagnostic,omitempty"`
}

type processRecord struct {
	Process     Process  `json:"process"`
	Program     string   `json:"program"`
	Launcher    string   `json:"launcher"`
	ProgramHash string   `json:"program_hash"`
	ConfigHash  string   `json:"config_hash"`
	Endpoints   []string `json:"endpoints"`
	Phase       string   `json:"phase"`
}

func (s *Service) recordPath() (string, error) {
	dir, _, err := s.paths()
	return filepath.Join(dir, "process.json"), err
}

func (s *Service) readRecord() (processRecord, error) {
	r, _, err := s.readRecordSnapshot()
	return r, err
}

// The decoded identity and the expected record must come from one capture.
// Capturing again after Inspect could adopt a different writer's record.
func (s *Service) readRecordSnapshot() (processRecord, fileSnapshot, error) {
	path, err := s.recordPath()
	if err != nil {
		return processRecord{}, fileSnapshot{}, err
	}
	if err = safePath(s.Root, path); err != nil {
		return processRecord{}, fileSnapshot{}, err
	}
	before, err := capture(path, 32<<10, true)
	if err != nil {
		return processRecord{}, before, err
	}
	if before.info == nil {
		return processRecord{}, before, os.ErrNotExist
	}
	var r processRecord
	if json.Unmarshal(before.data, &r) != nil {
		return r, before, fmt.Errorf("Proxy 进程记录格式无效；保留记录")
	}
	_, program, err := s.paths()
	if err != nil {
		return r, before, err
	}
	if r.Process.PID <= 1 || r.Process.UID != uint32(os.Geteuid()) || r.Process.Started == "" || r.Process.Boot == "" || r.Program != program || !filepath.IsAbs(r.Launcher) || r.Process.Executable != r.Launcher || r.Process.ExecutableDeleted || !validChecksum(r.ConfigHash) || !validChecksum(r.ProgramHash) || len(r.Endpoints) == 0 || r.Phase != "starting" && r.Phase != "running" && r.Phase != "stopped" {
		return r, before, fmt.Errorf("Proxy 进程记录身份不完整；保留记录")
	}
	for _, endpoint := range r.Endpoints {
		u, err := url.Parse(endpoint)
		if err != nil {
			return r, before, fmt.Errorf("Proxy 进程记录端点无效")
		}
		ip := net.ParseIP(u.Hostname())
		port, portErr := strconv.Atoi(u.Port())
		if ip == nil || !ip.IsLoopback() || u.User != nil || portErr != nil || port < 1 || port > 65535 || u.Scheme != "http" && u.Scheme != "socks5h" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return r, before, fmt.Errorf("Proxy 进程记录端点无效")
		}
	}
	return r, before, nil
}

// Each production phase write must refer to the exact record captured before
// the corresponding wait or core invocation. Never adopt a newly observed
// record merely because atomicWrite can safely replace its current inode.
func (s *Service) saveRecordExpected(r processRecord, before fileSnapshot, directory directorySnapshot) (fileSnapshot, error) {
	path, err := s.recordPath()
	if err != nil {
		return before, err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return before, err
	}
	installed, err := atomicWriteSnapshotExpected(path, append(b, '\n'), 0600, before, directory)
	if err != nil {
		return before, fmt.Errorf("Proxy 进程记录在操作期间发生变化；保留新记录: %w", err)
	}
	return installed, nil
}

// recordActual distinguishes a gone/reused PID from an unverifiable live
// identity. Only the former authorizes replacing a record or claiming stopped.
func (s *Service) recordActual(r processRecord) (Process, error) {
	p, err := s.runner().Inspect(r.Process.PID)
	if err != nil {
		if errors.Is(err, errProcessTransition) && sameBirth(p, r.Process) {
			return p, err
		}
		return Process{}, err
	}
	if !sameBirth(p, r.Process) {
		return Process{}, os.ErrNotExist
	}
	if p.Executable != r.Program && p.Executable != r.Launcher {
		return Process{}, fmt.Errorf("Proxy 进程程序身份无法核验；保留记录")
	}
	return p, nil
}

func (s *Service) recordReady(r processRecord, p Process) (bool, error) {
	return s.recordReadyUntil(r, p, time.Time{})
}

// Each ownership query can block briefly in the OS. Startup must stop querying
// further endpoints, and must not accept a late successful observation, once
// its deadline expires. Read-only status has no startup deadline.
func (s *Service) recordReadyUntil(r processRecord, p Process, deadline time.Time) (bool, error) {
	if p.Executable != r.Program || p.ExecutableDeleted {
		return false, nil
	}
	for _, endpoint := range r.Endpoints {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return false, nil
		}
		owned, err := s.runner().OwnsEndpoint(p, endpoint)
		if err != nil || !owned {
			return false, err
		}
	}
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return false, nil
	}
	actual, err := s.recordActual(r)
	if err != nil {
		return false, err
	}
	if actual != p {
		return false, fmt.Errorf("Proxy 就绪检查期间进程发生变化")
	}
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return false, nil
	}
	return true, nil
}

// Runtime is a read-only local observation: it never executes Mihomo -t,
// creates a lock, contacts a remote service, or mutates a process record.
func (s *Service) Runtime() (RuntimeStatus, error) {
	dir, _, err := s.paths()
	result := RuntimeStatus{State: "stopped", Log: filepath.Join(dir, "output.log")}
	if err != nil {
		return result, err
	}
	r, err := s.readRecord()
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		result.State = "unknown"
		return result, err
	}
	result.PID, result.ConfigHash = r.Process.PID, r.ConfigHash
	data, configErr := readRegular(filepath.Join(dir, "config.yaml"), 4<<20)
	result.ConfigChanged = configErr != nil || hash(data) != r.ConfigHash
	p, err := s.recordActual(r)
	if errors.Is(err, os.ErrNotExist) {
		result.State = "exited"
		if r.Phase == "stopped" {
			result.State = "stopped"
		}
		return result, nil
	}
	if err != nil {
		result.State = "unknown"
		return result, err
	}
	if p.ExecutableDeleted {
		result.State, result.Diagnostic = "unknown", "受管进程的运行映像已被删除或替换"
		return result, fmt.Errorf("Proxy 仍存活，但运行映像已被删除或替换；请先停止并检查程序")
	}
	program, programErr := readRegular(r.Program, 256<<20)
	if programErr != nil || hash(program) != r.ProgramHash {
		result.State, result.Diagnostic = "unknown", "受管程序与启动记录不一致"
		return result, fmt.Errorf("Proxy 仍存活，但磁盘程序与启动记录不一致；请先停止并检查程序")
	}
	result.State = "starting"
	ready, err := s.recordReady(r, p)
	if err != nil {
		result.Diagnostic = "无法核验全部监听归属"
		return result, err
	}
	result.Listeners = ready
	if ready {
		result.State = "running"
	}
	return result, nil
}

// requireStopped is called while the component operation lock is held. A
// missing or confirmed exited identity permits configuration/program changes;
// an unknown identity never does.
func (s *Service) requireStopped() error {
	r, before, err := s.readRecordSnapshot()
	if errors.Is(err, os.ErrNotExist) {
		return before.check()
	}
	if err != nil {
		return err
	}
	_, err = s.recordActual(r)
	if errors.Is(err, os.ErrNotExist) {
		if err := before.check(); err != nil {
			return fmt.Errorf("Proxy 进程记录在停机检查期间发生变化；未授权修改: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("Proxy 仍在运行或启动中；请先显式停止")
}

func (s *Service) stoppedCheck() error { return s.requireStopped() }

func (s *Service) Start() error { return s.withLock(s.startLocked) }

func (s *Service) startLocked(directory directorySnapshot) error {
	if err := directory.check(); err != nil {
		return err
	}
	dir, program, err := s.paths()
	if err != nil {
		return err
	}
	config, err := s.ReadConfig()
	if err != nil {
		return err
	}
	previous, recordBefore, err := s.readRecordSnapshot()
	if err == nil {
		p, inspectErr := s.recordActual(previous)
		if inspectErr == nil {
			programData, readErr := readRegular(program, 256<<20)
			if readErr != nil || hash(programData) != previous.ProgramHash {
				return fmt.Errorf("Proxy 存活实例的磁盘程序与启动记录不一致；请先停止")
			}
			if previous.ConfigHash != config.Digest {
				return fmt.Errorf("Proxy 配置已更改，请使用 restart 应用")
			}
			ready, readyErr := s.recordReady(previous, p)
			if readyErr != nil {
				return readyErr
			}
			if ready {
				if err := directory.check(); err != nil {
					return err
				}
				if err := recordBefore.check(); err != nil {
					return fmt.Errorf("Proxy 进程记录在就绪检查期间发生变化；保留新记录: %w", err)
				}
				return nil
			}
			return fmt.Errorf("Proxy 已有存活实例但尚未就绪，保留记录；请检查状态或先停止")
		}
		if !errors.Is(inspectErr, os.ErrNotExist) {
			return inspectErr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	programSnapshot, err := s.programSnapshot()
	if err != nil {
		return err
	}
	configSnapshot, err := capture(filepath.Join(dir, "config.yaml"), 4<<20, false)
	if err != nil {
		return err
	}
	if err := directory.check(); err != nil {
		return err
	}
	if err := s.validateConfig(program, config.Data); err != nil {
		return err
	}
	if err := programSnapshot.check(); err != nil {
		return err
	}
	if err := configSnapshot.check(); err != nil {
		return err
	}
	// Compare the actual bytes as well: snapshots must refer to the bytes
	// already parsed and passed to the core validator.
	current, err := readRegular(filepath.Join(dir, "config.yaml"), 4<<20)
	if err != nil || hash(current) != config.Digest {
		return fmt.Errorf("Proxy 配置在启动验证期间发生变化")
	}
	if err := recordBefore.check(); err != nil {
		return fmt.Errorf("Proxy 进程记录在启动验证期间发生变化；未派生启动器")
	}
	if err := s.requireStopped(); err != nil {
		return err
	}
	launcher := s.Launcher
	if launcher == "" {
		launcher, err = os.Executable()
		if err != nil {
			return err
		}
	}
	launcher, err = filepath.EvalSymlinks(launcher)
	if err != nil {
		return fmt.Errorf("无法定位 Proxy 启动器")
	}
	r := processRecord{Program: program, Launcher: launcher, ProgramHash: hash(programSnapshot.data), ConfigHash: config.Digest, Phase: "starting"}
	for _, endpoint := range config.Endpoints {
		r.Endpoints = append(r.Endpoints, endpoint.URL())
	}
	if len(r.Endpoints) == 0 {
		return fmt.Errorf("Proxy 没有可核验的回环代理监听")
	}
	c := Command{Program: program, Dir: dir, Log: filepath.Join(dir, "output.log"), Launcher: launcher, Args: []string{"-d", dir, "-f", filepath.Join(dir, "config.yaml")}, Env: s.controlledEnv()}
	if err := safePath(s.Root, c.Log); err != nil {
		return err
	}
	if err := directory.check(); err != nil {
		return err
	}
	var activeRecord fileSnapshot
	_, done, startErr := s.runner().Start(c, func(p Process) error {
		if p.PID <= 1 || p.UID != uint32(os.Geteuid()) || p.Boot == "" || p.Started == "" || p.Executable != launcher || p.ExecutableDeleted {
			return fmt.Errorf("Proxy 启动器返回无效身份")
		}
		if err := programSnapshot.check(); err != nil {
			return err
		}
		if err := configSnapshot.check(); err != nil {
			return err
		}
		if err := directory.check(); err != nil {
			return err
		}
		if err := recordBefore.check(); err != nil {
			return fmt.Errorf("Proxy 进程记录在启动验证期间发生变化；未授权执行")
		}
		if err := s.requireStopped(); err != nil {
			return err
		}
		r.Process = p
		var err error
		activeRecord, err = s.saveRecordExpected(r, recordBefore, directory)
		return err
	})
	if startErr != nil {
		if r.Process.PID != 0 {
			return s.failedStart(r, activeRecord, directory, "Proxy 启动失败: "+startErr.Error())
		}
		return startErr
	}
	if r.Process.PID == 0 {
		return fmt.Errorf("Proxy 启动器未提交进程身份")
	}
	timeout := s.StartTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if !time.Now().Before(deadline) {
			return s.failedStart(r, activeRecord, directory, "Proxy 启动超时，未确认全部监听归属")
		}
		select {
		case exitErr, ok := <-done:
			if ok {
				if exitErr == nil {
					return fmt.Errorf("Proxy 启动期间提前退出 (exit status 0)；日志: %s", c.Log)
				}
				return fmt.Errorf("Proxy 启动期间提前退出 (%v)；日志: %s", exitErr, c.Log)
			}
		default:
		}
		p, inspectErr := s.recordActual(r)
		if errors.Is(inspectErr, os.ErrNotExist) {
			return startupExit(done, c.Log)
		}
		if errors.Is(inspectErr, errProcessTransition) && sameBirth(p, r.Process) {
			if !time.Now().Before(deadline) {
				return s.failedStart(r, activeRecord, directory, "Proxy 启动超时，进程身份过渡未完成")
			}
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if inspectErr != nil {
			return fmt.Errorf("Proxy 已派生但身份无法核验，保留记录: %w", inspectErr)
		}
		ready, readyErr := s.recordReadyUntil(r, p, deadline)
		if ready && readyErr == nil {
			// A second observation after a short settling window catches a
			// listener that appears immediately before an early process exit.
			time.Sleep(25 * time.Millisecond)
			if !time.Now().Before(deadline) {
				return s.failedStart(r, activeRecord, directory, "Proxy 启动超时，未确认全部监听归属")
			}
			p2, e := s.recordActual(r)
			if e == nil && p2 == p {
				ready2, e2 := s.recordReadyUntil(r, p2, deadline)
				if ready2 && e2 == nil {
					if err := directory.check(); err != nil {
						return s.failedStart(r, activeRecord, directory, "Proxy 目录在启动期间发生变化")
					}
					if err := programSnapshot.check(); err != nil {
						return s.failedStart(r, activeRecord, directory, "Proxy 程序在启动期间发生变化")
					}
					if err := configSnapshot.check(); err != nil {
						return s.failedStart(r, activeRecord, directory, "Proxy 配置在启动期间发生变化")
					}
					if !time.Now().Before(deadline) {
						return s.failedStart(r, activeRecord, directory, "Proxy 启动超时，未确认全部监听归属")
					}
					running := r
					running.Phase = "running"
					if _, err := s.saveRecordExpected(running, activeRecord, directory); err != nil {
						return s.failedStart(r, activeRecord, directory, "Proxy 已就绪，但无法更新启动阶段: "+err.Error())
					}
					return nil
				}
			}
		}
		if !time.Now().Before(deadline) {
			return s.failedStart(r, activeRecord, directory, "Proxy 启动超时，未确认全部监听归属")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func startupExit(done <-chan error, log string) error {
	// Kernel exit may become observable just before exec.Cmd.Wait publishes
	// its status. Give this launch's wait channel a bounded chance to deliver
	// the exact exit code instead of losing it to a scheduler race.
	select {
	case err, ok := <-done:
		if ok {
			if err == nil {
				return fmt.Errorf("Proxy 启动期间提前退出 (exit status 0)；日志: %s", log)
			}
			return fmt.Errorf("Proxy 启动期间提前退出 (%v)；日志: %s", err, log)
		}
	case <-time.After(100 * time.Millisecond):
	}
	return fmt.Errorf("Proxy 启动期间提前退出；日志: %s", log)
}

func (s *Service) failedStart(r processRecord, before fileSnapshot, directory directorySnapshot, reason string) error {
	// This birth was authorized by this Start, even if another writer has
	// since replaced its record. Stop only this child; never adopt or rewrite
	// the replacement record, and never leave our child running untracked.
	if err := s.stopIdentity(r, nil); err != nil {
		return fmt.Errorf("%s；无法确认新进程已停止，保留记录: %w", reason, err)
	}
	if before.path == "" || directory.check() != nil || before.check() != nil {
		return fmt.Errorf("%s；本次新进程已安全停止；保留变化后的进程记录", reason)
	}
	r.Phase = "stopped"
	if _, err := s.saveRecordExpected(r, before, directory); err != nil {
		return fmt.Errorf("%s；本次新进程已安全停止；保留变化后的进程记录: %w", reason, err)
	}
	return fmt.Errorf("%s；新进程已安全停止；请检查日志", reason)
}

func (s *Service) Stop() error {
	return s.withLock(func(directory directorySnapshot) error {
		r, before, err := s.readRecordSnapshot()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.stopRecord(r, before, directory)
	})
}

func (s *Service) stopRecord(r processRecord, before fileSnapshot, directory directorySnapshot) error {
	check := func() error {
		if err := directory.check(); err != nil {
			return err
		}
		if err := before.check(); err != nil {
			return fmt.Errorf("Proxy 进程记录在停止期间发生变化；保留新记录")
		}
		return nil
	}
	if err := s.stopIdentity(r, check); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	r.Phase = "stopped"
	_, err := s.saveRecordExpected(r, before, directory)
	return err
}

func (s *Service) stopIdentity(r processRecord, check func() error) error {
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; attempt < 2; attempt++ {
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		p, err := s.waitRecordActual(r, check, deadline)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("Proxy 停止期限内未确认稳定身份，保留进程记录")
		}
		stopErr := s.runner().Stop(p)
		actual, checkErr := s.waitRecordActual(r, check, deadline)
		if errors.Is(checkErr, os.ErrNotExist) {
			return nil
		}
		if checkErr != nil {
			return fmt.Errorf("无法确认 Proxy 已退出，保留进程记录: %w", checkErr)
		}
		// The authorized launcher may exec between inspection and Stop. Retry
		// precisely that transition, never a different birth or executable.
		if actual != p && attempt == 0 {
			continue
		}
		if stopErr != nil {
			return stopErr
		}
		return fmt.Errorf("停止操作返回后 Proxy 仍存活，保留进程记录")
	}
	return fmt.Errorf("Proxy 停止未完成，保留进程记录")
}

func (s *Service) waitRecordActual(r processRecord, check func() error, deadline time.Time) (Process, error) {
	for {
		if check != nil {
			if err := check(); err != nil {
				return Process{}, err
			}
		}
		p, err := s.recordActual(r)
		if !errors.Is(err, errProcessTransition) {
			return p, err
		}
		// Waiting performs no signal or record write. Only a later complete
		// identity or confirmed disappearance may authorize the next step.
		if !time.Now().Before(deadline) {
			return p, fmt.Errorf("Proxy 内核进程过渡未在期限内完成: %w", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (s *Service) Restart() error {
	return s.withLock(func(directory directorySnapshot) error {
		if err := directory.check(); err != nil {
			return err
		}
		dir, program, err := s.paths()
		if err != nil {
			return err
		}
		r, recordBefore, recordErr := s.readRecordSnapshot()
		if recordErr != nil && !errors.Is(recordErr, os.ErrNotExist) {
			return recordErr
		}
		config, err := s.ReadConfig()
		if err != nil {
			return err
		}
		core, err := s.programSnapshot()
		if err != nil {
			return err
		}
		before, err := capture(filepath.Join(dir, "config.yaml"), 4<<20, false)
		if err != nil {
			return err
		}
		if hash(before.data) != config.Digest {
			return fmt.Errorf("Proxy 配置在重启验证前发生变化")
		}
		if err := directory.check(); err != nil {
			return err
		}
		if err := s.validateConfig(program, config.Data); err != nil {
			return err
		}
		if err := core.check(); err != nil {
			return err
		}
		if err := before.check(); err != nil {
			return err
		}
		if err := directory.check(); err != nil {
			return err
		}
		if err := recordBefore.check(); err != nil {
			return fmt.Errorf("Proxy 进程记录在重启验证期间发生变化；未停止任何实例: %w", err)
		}
		if recordErr == nil {
			if err = s.stopRecord(r, recordBefore, directory); err != nil {
				return err
			}
		}
		return s.startLocked(directory)
	})
}
