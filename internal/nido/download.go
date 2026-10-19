package nido

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/selfupdate"
)

func (m *Manager) githubPolicy(explicit string) (string, error) {
	return m.networkPolicy(explicit, "")
}

func (m *Manager) setDownload(policy string) error {
	config, err := normalizeDownload(policy)
	if err != nil {
		return err
	}
	current, err := m.savedNetwork()
	if err != nil {
		return err
	}
	config.ProxyURL = current.ProxyURL
	return m.setNetwork(config)
}

// This is an in-memory identity snapshot for one explicit update, not a
// transaction record. It spans the metadata lookup and user confirmation.
type selfIdentity struct {
	bin, program os.FileInfo
	data         []byte
}

// selfTarget only accepts the active, already managed command. Updating an
// unrelated root or an unowned entry is not a self-update operation.
func (m *Manager) selfTarget() ([]byte, error) {
	if m.configBytes == nil {
		return nil, fmt.Errorf("尚未初始化，请先安装 Nido")
	}
	if err := m.checkRoot(); err != nil {
		return nil, err
	}
	path := filepath.Join(m.Root, "bin", "nido")
	if err := safeParents(filepath.Dir(path)); err != nil {
		return nil, err
	}
	bin, err := os.Lstat(filepath.Dir(path))
	if err != nil || !bin.IsDir() {
		return nil, fmt.Errorf("Nido 程序目录无效")
	}
	target, err := os.Lstat(path)
	active, activeErr := os.Stat(m.Executable)
	if err != nil || activeErr != nil || m.executableInfo == nil || !target.Mode().IsRegular() || !os.SameFile(target, m.executableInfo) || !os.SameFile(active, m.executableInfo) || target.Mode() != m.executableInfo.Mode() {
		return nil, fmt.Errorf("自身更新只替换当前运行的受管 bin/nido；请使用该目录中的命令")
	}
	data, err := readFile(path, 512<<20)
	if err != nil {
		return nil, err
	}
	info, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil || info.Path != "github.com/MiracleHYH/CDEToolkit/cmd/nido" || info.Main.Path != "github.com/MiracleHYH/CDEToolkit" {
		return nil, fmt.Errorf("bin/nido 不是受管 Nido 程序，未替换")
	}
	identity := &selfIdentity{bin: bin, program: target, data: data}
	if err := m.checkSelfIdentity(identity); err != nil {
		return nil, err
	}
	m.self = identity
	return data, nil
}

func (m *Manager) checkSelfIdentity(identity *selfIdentity) error {
	bin := filepath.Join(m.Root, "bin")
	if err := m.checkRoot(); err != nil {
		return err
	}
	if err := safeParents(bin); err != nil {
		return err
	}
	currentBin, err := os.Lstat(bin)
	current, entryErr := os.Lstat(filepath.Join(bin, "nido"))
	if err != nil || entryErr != nil || !os.SameFile(currentBin, identity.bin) || currentBin.Mode() != identity.bin.Mode() || !os.SameFile(current, identity.program) || current.Mode() != identity.program.Mode() {
		return fmt.Errorf("Nido 程序目录或入口在版本查询或确认期间发生变化")
	}
	return unchangedFile(filepath.Join(bin, "nido"), identity.data, 512<<20)
}

// The candidate is already checked as a release executable. Its sole probe is
// the version command, with no inherited configuration or proxy environment.
func candidateVersion(path string) (string, error) {
	return candidateVersionWithTimeout(path, 10*time.Second)
}

type versionOutput struct{ buffer bytes.Buffer }

func (b *versionOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 128 {
		return 0, fmt.Errorf("候选 Nido 版本输出过长")
	}
	return b.buffer.Write(p)
}

func candidateVersionWithTimeout(path string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return os.ErrProcessDone
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	var out versionOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if cmd.Process != nil {
			_ = cmd.Cancel()
		}
		return "", fmt.Errorf("候选 Nido 版本检查失败")
	}
	return strings.TrimSuffix(out.buffer.String(), "\n"), nil
}

// Fetch under the existing short-lived management lock. Only the executable
// is replaced; configuration, env.sh and component data are never rewritten.
func (m *Manager) updateSelf(before []byte, fetch func() (selfupdate.Prepared, error), probe func(string) (string, error)) error {
	return m.mutate(false, func() error {
		bin := filepath.Join(m.Root, "bin")
		path := filepath.Join(bin, "nido")
		identity := m.self
		if identity == nil {
			binInfo, err := os.Lstat(bin)
			if err != nil || !binInfo.IsDir() {
				return fmt.Errorf("Nido 程序目录无效")
			}
			oldInfo, err := os.Lstat(path)
			if err != nil || !oldInfo.Mode().IsRegular() {
				return fmt.Errorf("Nido 程序入口无效")
			}
			identity = &selfIdentity{bin: binInfo, program: oldInfo, data: before}
		} else if !bytes.Equal(before, identity.data) {
			return fmt.Errorf("Nido 更新快照不匹配")
		}
		checkTarget := func() error {
			if err := m.checkSelfIdentity(identity); err != nil {
				return err
			}
			return unchanged(filepath.Join(m.Root, "config.toml"), m.configBytes)
		}
		if err := checkTarget(); err != nil {
			return err
		}
		prepared, err := fetch()
		if err != nil {
			return err
		}
		if len(prepared.Data) == 0 || len(prepared.Data) > 512<<20 {
			return fmt.Errorf("候选程序为空或超过大小限制")
		}
		if err = checkTarget(); err != nil {
			return err
		}
		f, err := os.CreateTemp(bin, ".nido-update-*")
		if err != nil {
			return err
		}
		createdInfo, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			return statErr
		}
		defer func() {
			// A replaced directory or filename is no longer our cleanup target.
			currentBin, e := os.Lstat(bin)
			current, e2 := os.Lstat(f.Name())
			if e == nil && e2 == nil && safeParents(bin) == nil && os.SameFile(currentBin, identity.bin) && os.SameFile(current, createdInfo) {
				_ = os.Remove(f.Name())
			}
		}()
		if err = f.Chmod(0755); err == nil {
			_, err = f.Write(prepared.Data)
		}
		if err == nil {
			err = f.Sync()
		}
		candidateInfo, statErr := f.Stat()
		if err == nil {
			err = statErr
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		checkCandidate := func() error {
			if err := checkTarget(); err != nil {
				return err
			}
			current, err := os.Lstat(f.Name())
			if err != nil || !os.SameFile(current, candidateInfo) || current.Mode() != candidateInfo.Mode() {
				return fmt.Errorf("候选 Nido 在检查期间发生变化")
			}
			return unchangedFile(f.Name(), prepared.Data, 512<<20)
		}
		if err := checkCandidate(); err != nil {
			return err
		}
		if probe == nil {
			probe = candidateVersion
		}
		reported, err := probe(f.Name())
		if err != nil || reported != "nido "+prepared.Version {
			return fmt.Errorf("候选 Nido 与发布版本不符，未替换现有程序")
		}
		if err = checkCandidate(); err != nil {
			return err
		}
		return os.Rename(f.Name(), path)
	})
}

func (c *CLI) update(m *Manager, o cliOptions, args []string) int {
	if len(args) != 0 || o.check && o.version != "" {
		return c.fail(fmt.Errorf("使用 update [--check] 或 update --version X.Y.Z"))
	}
	// Invalid explicit flags are still rejected offline. Saved routing and
	// listener readiness matter only when an actual online operation is needed.
	if o.githubProxy != "" {
		if _, err := normalizeDownload(o.githubProxy); err != nil {
			return c.fail(err)
		}
	}
	if o.proxy != "" {
		if _, err := m.generalProxyURL(o.proxy); err != nil {
			return c.fail(err)
		}
	}
	if o.version != "" && !selfupdate.ValidVersion(o.version) {
		return c.fail(fmt.Errorf("版本必须是稳定版本 X.Y.Z"))
	}
	before, err := m.selfTarget()
	if err != nil {
		return c.fail(err)
	}
	if !selfupdate.ValidVersion(Version) {
		return c.fail(fmt.Errorf("开发构建请使用原安装方式更新"))
	}
	if o.version != "" && o.version != Version && !selfupdate.IsNewer(Version, o.version) {
		return c.fail(fmt.Errorf("自身更新不支持降级；旧版本可能不兼容当前配置"))
	}
	if o.version == Version {
		return c.output(o, selfupdate.Info{Current: Version, Latest: Version})
	}
	if o.dry {
		config, err := m.resolveNetwork(o.githubProxy, o.proxy)
		mode := config.GitHubAccelerate
		if err != nil {
			mode = "需要选择"
		}
		return c.output(o, map[string]string{"operation": "update-nido", "root": m.Root, "mode": mode, "version": o.version})
	}
	service := selfupdate.Service{}
	if c.uiSession != nil {
		service.Ctx = c.uiSession.Context()
	}
	version := o.version
	service.APIHTTP, err = m.networkAPIClient(o.githubProxy, o.proxy)
	if err != nil {
		return c.fail(err)
	}
	defer service.APIHTTP.CloseIdleConnections()
	if version == "" {
		info, err := service.Check(Version)
		if err != nil {
			return c.fail(err)
		}
		if o.check {
			return c.output(o, info)
		}
		if !info.Available {
			if o.json {
				return c.output(o, info)
			}
			fmt.Fprintf(c.Out, "已是最新版本：Nido %s\n", info.Current)
			return 0
		}
		version = info.Latest
		fmt.Fprintf(c.Err, "Nido %s → %s\n", info.Current, version)
	}
	if err = c.confirm(o, "更新 Nido 程序，保留配置和组件"); err != nil {
		return c.fail(err)
	}
	// Version metadata uses the general proxy independently of the file route.
	// Resolve GitHub file acceleration only when the user confirms a download.
	policy, err := m.networkPolicy(o.githubProxy, o.proxy)
	if err != nil {
		return c.fail(err)
	}
	client, err := m.networkHTTP(c.UpdateHTTP, o.githubProxy, o.proxy)
	if err != nil {
		return c.fail(err)
	}
	defer client.CloseIdleConnections()
	service.HTTP, service.GitHubProxy = client, policy
	if err = m.updateSelf(before, func() (selfupdate.Prepared, error) { return service.Prepare(version) }, nil); err != nil {
		return c.fail(err)
	}
	return c.output(o, map[string]string{"version": version, "detail": "已更新 Nido；配置与组件保持原样，未启动服务。"})
}
