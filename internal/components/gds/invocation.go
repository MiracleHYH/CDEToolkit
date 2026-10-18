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
)

func (s *Service) invocationStopped() error {
	r, b, e := s.readRecord("invocation.json")
	if errors.Is(e, os.ErrNotExist) {
		return b.Check()
	}
	if e != nil {
		return e
	}
	_, e = s.actual(r)
	if e == nil {
		return fmt.Errorf("GDS 独立辅助进程仍未退出；请先停止")
	}
	if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return b.Check()
}

// runHelper serializes explicit metadata/probe calls across CLI processes. An
// unconfirmed exit persists its exact kernel birth before returning to the user.
func (s *Service) runHelper(c Command) ([]byte, error) {
	if c.ExpectedProgram == nil || c.ExpectedProgram.Path != c.Program {
		return nil, fmt.Errorf("辅助进程缺少预期程序快照")
	}
	if err := c.ExpectedProgram.Check(); err != nil {
		return nil, err
	}
	if e := s.invocationStopped(); e != nil {
		return nil, e
	}
	dir, _, e := s.paths()
	if e != nil {
		return nil, e
	}
	lock, e := gdsutil.Lock(filepath.Join(dir, ".invocation.lock"))
	if e != nil {
		return nil, e
	}
	defer lock.Close()
	if e = s.invocationStopped(); e != nil {
		return nil, e
	}
	before, e := gdsutil.Capture(filepath.Join(dir, "invocation.json"), 32<<10, true)
	if e != nil {
		return nil, e
	}
	guard, e := gdsutil.CaptureDirectory(dir)
	if e != nil {
		return nil, e
	}
	token := make([]byte, 24)
	if _, e = rand.Read(token); e != nil {
		return nil, e
	}
	config, e := gdsutil.Capture(filepath.Join(dir, "config.toml"), maxConfig, true)
	if e != nil {
		return nil, e
	}
	var cfg gdsapi.Config
	if config.Info != nil {
		if cfg, e = gdsapi.ParseConfig(config.Data); e != nil {
			return nil, e
		}
	}
	program, e := gdsutil.Capture(c.Program, maxProgram, false)
	if e != nil {
		return nil, e
	}
	committed := false
	c.Commit = func(p gdsapi.Process) error {
		if p.PID <= 1 || p.UID != uint32(os.Geteuid()) || p.Boot == "" || p.Started == "" || p.Executable != c.Program || p.ExecutableDeleted {
			return fmt.Errorf("辅助进程内核身份不符合预期")
		}
		if e := program.Check(); e != nil {
			return e
		}
		if e := c.ExpectedProgram.Check(); e != nil {
			return e
		}
		if e := config.Check(); e != nil {
			return e
		}
		record := gdsapi.Record{Schema: 1, Token: hex.EncodeToString(token), Process: p, ConfigHash: gdsutil.Hash(config.Data), ProgramHash: gdsutil.Hash(program.Data), UUID: cfg.GPUUUID, Phase: "running"}
		b, _ := json.Marshal(record)
		if e := gdsutil.WriteExpected(before.Path, append(b, '\n'), 0600, before, guard); e != nil {
			return fmt.Errorf("无法在授权前持久化辅助进程身份: %w", e)
		}
		committed = true
		return nil
	}
	c.Dir = dir
	data, runErr := s.runner().Run(c)
	var uncertain *UnconfirmedRunError
	if errors.As(runErr, &uncertain) && !committed {
		// Even an unreadable kernel identity must leave durable, invalid evidence.
		// Future operations fail closed rather than silently treating it as absent.
		record := gdsapi.Record{Schema: 1, Token: hex.EncodeToString(token), Process: uncertain.Process, ConfigHash: gdsutil.Hash(config.Data), ProgramHash: gdsutil.Hash(program.Data), UUID: cfg.GPUUUID, Phase: "running"}
		b, _ := json.Marshal(record)
		if e = gdsutil.WriteExpected(before.Path, append(b, '\n'), 0600, before, guard); e != nil {
			return nil, errors.Join(runErr, fmt.Errorf("无法持久化未确认辅助进程身份，保留现场: %w", e))
		}
	}
	if runErr == nil && !committed {
		return nil, fmt.Errorf("GDS runner 未提交辅助进程身份；拒绝接受探测结果")
	}
	return data, runErr
}
