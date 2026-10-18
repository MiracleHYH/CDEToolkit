// Package gds manages the optional standalone GDS program without importing
// native NVIDIA bindings or depending on another Nido component.
package gds

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsutil"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type Service struct {
	Root         string
	Env          []string
	Runner       Runner
	Platform     string
	StartTimeout time.Duration
	// HTTP permits a per-operation explicit proxy and isolated test transport.
	HTTP *http.Client
	// API is only a constructor seam for isolated tests. Production leaves it
	// empty; no environment variable or saved configuration overrides the API.
	API string
	// Injectable kernel clock providers are used only by isolated CPU tests.
	BootID     func() (string, error)
	BootTimeNS func() (int64, error)
}

func (s *Service) bootID() (string, error) {
	if s.BootID != nil {
		return s.BootID()
	}
	return gdsutil.BootID()
}
func (s *Service) bootTimeNS() (int64, error) {
	if s.BootTimeNS != nil {
		return s.BootTimeNS()
	}
	return gdsutil.BootTimeNS()
}

type Status struct {
	Invocation    string        `json:"invocation,omitempty"`
	InvocationPID int           `json:"invocation_pid,omitempty"`
	Installed     bool          `json:"installed"`
	Configured    bool          `json:"configured"`
	Problem       string        `json:"problem,omitempty"`
	Scheduler     string        `json:"scheduler,omitempty"`
	Directory     string        `json:"directory"`
	ProgramPath   string        `json:"program_path"`
	Program       string        `json:"program"`
	Config        string        `json:"config"`
	Runtime       string        `json:"runtime"`
	PID           int           `json:"pid,omitempty"`
	WorkerPID     int           `json:"worker_pid,omitempty"`
	Worker        string        `json:"worker"`
	ConfigChanged bool          `json:"config_changed"`
	Stale         bool          `json:"stale"`
	State         *gdsapi.State `json:"state,omitempty"`
	Problems      []string      `json:"problems,omitempty"`
}

func (s *Service) paths() (string, string, error) {
	if !filepath.IsAbs(s.Root) || filepath.Clean(s.Root) == "/" {
		return "", "", fmt.Errorf("Nido 软件目录必须是非根目录的绝对路径")
	}
	root := filepath.Clean(s.Root)
	if e := gdsutil.SafePath(root, filepath.Join(root, "gds")); e != nil {
		return "", "", e
	}
	if real, e := filepath.EvalSymlinks(root); e == nil {
		root = real
	}
	dir := filepath.Join(root, "gds")
	return dir, filepath.Join(dir, "gds"), nil
}
func (s *Service) platform() string {
	if s.Platform != "" {
		return s.Platform
	}
	return runtime.GOOS + "/" + runtime.GOARCH
}
func (s *Service) runner() Runner {
	if s.Runner != nil {
		return s.Runner
	}
	return OSRunner{}
}
func (s *Service) environment() []string {
	env := s.Env
	if env == nil {
		env = os.Environ()
	}
	out := []string{}
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		switch k {
		case "CUDA_VISIBLE_DEVICES", "NVIDIA_VISIBLE_DEVICES", "CUDA_MPS_PIPE_DIRECTORY", "CUDA_MPS_LOG_DIRECTORY":
			out = append(out, v)
		}
	}
	return out
}
func (s *Service) withLock(fn func(gdsutil.DirectorySnapshot) error) error {
	dir, _, e := s.paths()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(dir), 0700); e != nil {
		return e
	}
	if e = gdsutil.SafePath(s.Root, dir); e != nil {
		return e
	}
	if e = gdsutil.EnsureDir(dir); e != nil {
		return e
	}
	lock, e := gdsutil.Lock(filepath.Join(dir, ".lock"))
	if e != nil {
		return e
	}
	defer lock.Close()
	guard, e := gdsutil.CaptureDirectory(dir)
	if e != nil {
		return e
	}
	return fn(guard)
}
func (s *Service) ConfigPath() (string, error) {
	dir, _, e := s.paths()
	return filepath.Join(dir, "config.toml"), e
}
func (s *Service) ReadConfig() (gdsapi.Config, error) {
	p, e := s.ConfigPath()
	if e != nil {
		return gdsapi.Config{}, e
	}
	if e = gdsutil.SafePath(s.Root, p); e != nil {
		return gdsapi.Config{}, e
	}
	b, e := gdsutil.Read(p, maxConfig)
	if e != nil {
		return gdsapi.Config{}, e
	}
	return gdsapi.ParseConfig(b)
}
func (s *Service) Configure(uuid, file string) error {
	return s.withLock(func(guard gdsutil.DirectorySnapshot) error {
		if (uuid == "") == (file == "") {
			return fmt.Errorf("请指定一个 GPU UUID 或完整配置文件")
		}
		if e := s.requireStopped(); e != nil {
			return e
		}
		p, e := s.ConfigPath()
		if e != nil {
			return e
		}
		before, e := gdsutil.Capture(p, maxConfig, true)
		if e != nil {
			return e
		}
		var data []byte
		if uuid != "" {
			if before.Info != nil {
				return fmt.Errorf("已有 GDS 配置；请通过 --file 显式替换")
			}
			data, e = gdsapi.DefaultConfig(uuid).Encode()
		} else {
			if !filepath.IsAbs(file) {
				return fmt.Errorf("配置文件必须使用绝对路径")
			}
			data, e = gdsutil.Read(file, maxConfig)
		}
		if e != nil {
			return e
		}
		if _, e = gdsapi.ParseConfig(data); e != nil {
			return e
		}
		if e = s.requireStopped(); e != nil {
			return e
		}
		return gdsutil.WriteExpected(p, data, 0600, before, guard)
	})
}
func (s *Service) programSnapshot() (gdsutil.Snapshot, error) {
	_, p, e := s.paths()
	if e != nil {
		return gdsutil.Snapshot{}, e
	}
	if e = gdsutil.SafePath(s.Root, p); e != nil {
		return gdsutil.Snapshot{}, e
	}
	b, e := gdsutil.Capture(p, maxProgram, false)
	if e != nil {
		return b, e
	}
	if e = s.checkExecutable(b); e != nil {
		return b, e
	}
	_, e = s.ownedProgram(b)
	return b, e
}
func (s *Service) checkExecutable(b gdsutil.Snapshot) error {
	if b.Info == nil || b.Info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("GDS 路径不是可执行组件；保留现有文件")
	}
	if e := nativeProgram(b.Data, s.platform()); e != nil {
		return fmt.Errorf("GDS 路径不是受支持组件；保留现有文件: %w", e)
	}
	return nil
}
func (s *Service) Install(a, c string) error {
	return s.withLock(func(g gdsutil.DirectorySnapshot) error { return s.install(a, c, false, g) })
}
func (s *Service) Update(a, c string) error {
	return s.withLock(func(g gdsutil.DirectorySnapshot) error { return s.install(a, c, true, g) })
}
func (s *Service) install(a, c string, update bool, guard gdsutil.DirectorySnapshot) error {
	dir, p, e := s.paths()
	if e != nil {
		return e
	}
	before, e := gdsutil.Capture(p, maxProgram, true)
	if e != nil {
		return e
	}
	if update && before.Info == nil {
		return fmt.Errorf("GDS 尚未安装")
	}
	if !update && before.Info != nil {
		return fmt.Errorf("GDS 已安装，请使用 update")
	}
	if update {
		if e = s.checkExecutable(before); e != nil {
			return e
		}
	}
	receiptBefore, receipt, e := s.receiptSnapshot(true)
	if e != nil {
		return e
	}
	if update && (receiptBefore.Info == nil || receipt.SHA256 != gdsutil.Hash(before.Data)) {
		return fmt.Errorf("GDS 程序缺少匹配的安装凭证；保留现有文件")
	}
	conf, e := gdsutil.Capture(filepath.Join(dir, "config.toml"), maxConfig, true)
	if e != nil {
		return e
	}
	if conf.Info != nil {
		if _, e = gdsapi.ParseConfig(conf.Data); e != nil {
			return e
		}
	}
	if e = s.requireStopped(); e != nil {
		return e
	}
	prepared, e := s.artifact(a, c)
	if e != nil {
		return e
	}
	if update && (a == "" || a == "latest") && prepared.releaseTag != "" {
		currentTag := "gds-v" + receipt.Version
		if stableReleaseTag.MatchString(currentTag) && stableVersionLess(prepared.releaseTag, currentTag) {
			return fmt.Errorf("官方最新 GDS 版本低于已安装版本；如需回退，请显式指定发布标签")
		}
	}
	f, e := os.CreateTemp(dir, ".gds-candidate-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return e
	}
	retainCandidate := false
	defer func() {
		if !retainCandidate {
			gdsutil.RemoveCreated(tmp, info)
		}
	}()
	if e = f.Chmod(0700); e == nil {
		_, e = f.Write(prepared.data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	candidate, e := gdsutil.Capture(tmp, maxProgram, false)
	if e != nil {
		return e
	}
	metadata, e := s.metadata(candidate)
	if e != nil {
		var uncertain *UnconfirmedRunError
		retainCandidate = errors.As(e, &uncertain)
		return e
	}
	if prepared.expectedVersion != "" && metadata.Version != prepared.expectedVersion {
		return fmt.Errorf("GDS 程序版本与官方发布标签不匹配")
	}
	if e = s.requireStopped(); e != nil {
		return e
	}
	for _, b := range []gdsutil.Snapshot{before, conf, candidate, receiptBefore} {
		if e = b.Check(); e != nil {
			return e
		}
	}
	if e = guard.Check(); e != nil {
		return e
	}
	installed, e := gdsutil.CommitCandidate(candidate, before, guard)
	if e != nil {
		return e
	}
	receipt = installReceipt{Schema: 1, Product: metadata.Product, Protocol: metadata.Protocol, Platform: metadata.Platform, Version: metadata.Version, SHA256: gdsutil.Hash(installed.Data), ReleaseTag: prepared.releaseTag}
	b, _ := json.Marshal(receipt)
	if e = installed.Check(); e != nil {
		return e
	}
	if e = gdsutil.WriteExpected(receiptBefore.Path, append(b, '\n'), 0600, receiptBefore, guard); e != nil {
		return fmt.Errorf("GDS 程序已提交但安装凭证未提交；后续操作将拒绝不匹配现场: %w", e)
	}
	return installed.Check()
}
func (s *Service) Remove() error {
	return s.withLock(func(g gdsutil.DirectorySnapshot) error {
		if e := s.requireStopped(); e != nil {
			return e
		}
		_, p, e := s.paths()
		if e != nil {
			return e
		}
		b, e := gdsutil.Capture(p, maxProgram, true)
		if e != nil || b.Info == nil {
			return e
		}
		if e = s.checkExecutable(b); e != nil {
			return e
		}
		receipt, e := s.ownedProgram(b)
		if e != nil {
			return e
		}
		if e = s.requireStopped(); e != nil {
			return e
		}
		if e = g.Check(); e != nil {
			return e
		}
		if e = b.Check(); e != nil {
			return e
		}
		if e = receipt.Check(); e != nil {
			return e
		}
		return gdsutil.RemoveExpected(b, g)
	})
}

// Check performs no driver calls, writes, child execution or network access.
func (s *Service) Check() error {
	if _, e := s.programSnapshot(); e != nil {
		return e
	}
	_, e := s.ReadConfig()
	return e
}
func (s *Service) Probe() (gdsapi.Sample, error) {
	var sample gdsapi.Sample
	err := s.withLock(func(guard gdsutil.DirectorySnapshot) error {
		var err error
		sample, err = s.probeLocked()
		if err != nil {
			return err
		}
		return guard.Check()
	})
	return sample, err
}

// All explicit helpers share the component operation lock with replacement and
// removal. A completed snapshot check alone cannot protect the execution window.
func (s *Service) probeLocked() (gdsapi.Sample, error) {
	var sample gdsapi.Sample
	if err := s.refuseUnresolved(); err != nil {
		return sample, err
	}
	program, e := s.programSnapshot()
	if e != nil {
		return sample, e
	}
	confPath, e := s.ConfigPath()
	if e != nil {
		return sample, e
	}
	confSnapshot, e := gdsutil.Capture(confPath, maxConfig, false)
	if e != nil {
		return sample, e
	}
	conf, e := gdsapi.ParseConfig(confSnapshot.Data)
	if e != nil {
		return sample, e
	}
	b, e := s.runHelper(Command{Program: program.Path, ExpectedProgram: &program, Args: []string{"probe", "--uuid", conf.GPUUUID}, Dir: filepath.Dir(program.Path), Env: s.environment(), Timeout: 3 * time.Second})
	if e != nil {
		return sample, e
	}
	if json.Unmarshal(b, &sample) != nil || sample.UUID != conf.GPUUUID || !sample.Complete || !sample.Supported || sample.TotalBytes == 0 || sample.FreeBytes > sample.TotalBytes || sample.Utilization < 0 || sample.Utilization > 100 {
		return sample, fmt.Errorf("GDS 探测不完整或设备能力不受支持")
	}
	target := sample.TotalBytes/100*uint64(conf.MemoryPercent) + sample.TotalBytes%100*uint64(conf.MemoryPercent)/100
	reserve := uint64(conf.MinFreeMiB) << 20
	if reserve > sample.TotalBytes || target > sample.TotalBytes-reserve {
		return sample, fmt.Errorf("GDS 显存比例及最低余量超过设备容量")
	}
	if e = program.Check(); e != nil {
		return sample, e
	}
	if e = confSnapshot.Check(); e != nil {
		return sample, e
	}
	return sample, nil
}
func (s *Service) Validate() error {
	return s.withLock(func(g gdsutil.DirectorySnapshot) error {
		core, e := s.programSnapshot()
		if e != nil {
			return e
		}
		confPath, e := s.ConfigPath()
		if e != nil {
			return e
		}
		conf, e := gdsutil.Capture(confPath, maxConfig, false)
		if e != nil {
			return e
		}
		if _, e = s.ReadConfig(); e != nil {
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
		return g.Check()
	})
}
func (s *Service) Logs(tail int) (string, error) {
	if tail < 1 || tail > 10000 {
		return "", fmt.Errorf("日志行数必须为 1–10000")
	}
	dir, _, e := s.paths()
	if e != nil {
		return "", e
	}
	p := filepath.Join(dir, "output.log")
	if e = gdsutil.SafePath(s.Root, p); e != nil {
		return "", e
	}
	f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return "", e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || !gdsutil.SingleLink(info) || !gdsutil.Owned(info) {
		return "", fmt.Errorf("GDS 日志必须是当前用户的单链接普通文件")
	}
	current, e := os.Lstat(p)
	if e != nil || !os.SameFile(info, current) {
		return "", fmt.Errorf("GDS 日志路径发生变化")
	}
	start := info.Size() - (4 << 20)
	if start < 0 {
		start = 0
	}
	b, e := io.ReadAll(io.NewSectionReader(f, start, info.Size()-start))
	if e != nil {
		return "", e
	}
	if start > 0 {
		if i := strings.IndexByte(string(b), '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return strings.Join(lines, "\n"), nil
}
func (s *Service) Preflight(action, artifact, checksum string) error {
	dir, p, e := s.paths()
	if e != nil {
		return e
	}
	for _, name := range []string{".lock", ".run.lock", "config.toml", "state.json", "output.log"} {
		if e = gdsutil.SafePath(s.Root, filepath.Join(dir, name)); e != nil {
			return e
		}
	}
	switch action {
	case "install", "update":
		if e = s.requireStopped(); e != nil {
			return e
		}
		b, e := gdsutil.Capture(p, maxProgram, true)
		if e != nil {
			return e
		}
		if action == "install" && b.Info != nil {
			return fmt.Errorf("GDS 已安装")
		}
		if _, _, e = s.receiptSnapshot(true); e != nil {
			return e
		}
		if action == "update" {
			if e = s.checkExecutable(b); e != nil {
				return e
			}
			if _, e = s.ownedProgram(b); e != nil {
				return e
			}
		}
		if e = ValidateSource(artifact, checksum); e != nil {
			return e
		}
		if officialSource(artifact) {
			if s.platform() != "linux/amd64" {
				return fmt.Errorf("GDS 首版仅支持 Linux amd64")
			}
			return nil
		}
		_, e = s.artifact(artifact, checksum)
		return e
	case "check-update":
		_, e := s.programSnapshot()
		return e
	case "probe":
		if e = s.refuseUnresolved(); e != nil {
			return e
		}
		if e = s.invocationStopped(); e != nil {
			return e
		}
		return s.Check()
	case "start", "restart", "validate":
		if e = s.Check(); e != nil {
			return e
		}
		if action == "start" {
			return s.preflightStart()
		}
		if e = s.refuseUnresolved(); e != nil {
			return e
		}
		return s.preflightStop()
	case "stop":
		return s.preflightStop()
	case "remove":
		if e = s.requireStopped(); e != nil {
			return e
		}
		b, e := gdsutil.Capture(p, maxProgram, true)
		if e != nil || b.Info == nil {
			return e
		}
		if e = s.checkExecutable(b); e != nil {
			return e
		}
		_, e = s.ownedProgram(b)
		return e
	case "configure":
		if e = s.requireStopped(); e != nil {
			return e
		}
		p, e := s.ConfigPath()
		if e != nil {
			return e
		}
		_, e = gdsutil.Capture(p, maxConfig, true)
		if e != nil || artifact == "" {
			return e
		}
		if !filepath.IsAbs(artifact) {
			return fmt.Errorf("配置文件必须使用绝对路径")
		}
		data, e := gdsutil.Read(artifact, maxConfig)
		if e != nil {
			return e
		}
		_, e = gdsapi.ParseConfig(data)
		return e
	default:
		return fmt.Errorf("未知 GDS 预检操作")
	}
}
