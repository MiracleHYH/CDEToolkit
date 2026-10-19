package nido

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"go.yaml.in/yaml/v3"
)

// This opt-in test injects native configs through Service.Configure, then uses
// the CLI for validation and lifecycle/traffic checks. It does not test the
// interactive configuration flow, download a program or contact external hosts.
// NIDO_TEST_MIHOMO must name an explicitly supplied, native official binary.
// NIDO_TEST_NIDO may name a prebuilt native CLI for environments without Go.
// Example: NIDO_TEST_MIHOMO=/absolute/path/mihomo go test ./internal/nido -run TestProxyRealMihomo -v
func TestProxyRealMihomo(t *testing.T) {
	artifact := os.Getenv("NIDO_TEST_MIHOMO")
	if artifact == "" {
		t.Skip("设置 NIDO_TEST_MIHOMO 为官方 Mihomo 绝对路径，才执行临时目录与回环网络的真实核心测试")
	}
	if !filepath.IsAbs(artifact) {
		t.Fatal("NIDO_TEST_MIHOMO 必须是绝对路径")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Fatal("真实 Proxy 测试仅支持 Darwin/Linux")
	}
	reports, err := prepareIsolatedRaceReports(os.Getenv("NIDO_TEST_RACE_REPORT_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	// Registered first, this runs after the CLI, core and server cleanups.
	// The directory lives outside the CLI scratch, so those cleanups cannot
	// erase a launcher's report before it is checked.
	t.Cleanup(func() {
		if err := reports.check(); err != nil {
			t.Error(err)
		}
	})
	// Explicit opt-in must fail rather than silently skip missing kernel access.
	if _, err := (proxy.OSRunner{}).Inspect(os.Getpid()); err != nil {
		t.Fatalf("显式真实测试需要内核进程身份读取权限: %v", err)
	}
	data, err := readFile(artifact, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	cli := newRealProxyCLI(t, reports)
	cli.must("init", "--json")
	cli.must("proxy", "install", "--artifact", artifact, "--sha256", hex.EncodeToString(sum[:]), "--yes", "--json")

	var originRequests, upstreamTunnels, providerDownloads atomic.Int64
	origin := httptest.NewServer(realOrigin(&originRequests))
	t.Cleanup(origin.Close)
	tlsOrigin := httptest.NewTLSServer(realOrigin(&originRequests))
	t.Cleanup(tlsOrigin.Close)
	upstream := realConnectProxy(t, &upstreamTunnels, origin.Listener.Addr().String(), tlsOrigin.Listener.Addr().String())
	_, upstreamPortText, _ := net.SplitHostPort(upstream.Listener.Addr().String())
	upstreamPort, _ := strconv.Atoi(upstreamPortText)
	providerData := []byte(fmt.Sprintf("proxies:\n  - name: e2e-upstream\n    type: http\n    server: 127.0.0.1\n    port: %d\n", upstreamPort))
	var providerDisabled atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerDownloads.Add(1)
		if providerDisabled.Load() || r.URL.Path != "/provider.yaml" {
			http.Error(w, "provider disabled", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write(providerData)
	}))
	t.Cleanup(provider.Close)
	// Register after all servers: stop the core before closing its fixtures.
	t.Cleanup(cli.cleanup)

	port, release := realProxyPort(t)
	template, err := proxy.Template(provider.URL + "/provider.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(template, &document); err != nil {
		t.Fatal(err)
	}
	root := document.Content[0]
	realQuickStartLocalHealth(root, origin.URL)
	listener := realYAMLField(root, "listeners").Content[0]
	realYAMLField(listener, "port").Value = strconv.Itoa(port)
	config := realYAMLBytes(t, &document)
	cli.configure(config)
	release()
	cli.must("proxy", "start", "--yes", "--json")
	endpoint := cli.endpoint("endpoint-proxy")
	cachePath := filepath.Join(cli.root, "proxy", "providers", "subscription.yaml")
	realAwaitProviderCache(t, cachePath, &providerDownloads)
	realProxyTraffic(t, endpoint.URL(), endpoint.URL(), origin, tlsOrigin, &originRequests, &upstreamTunnels)
	cli.assertRunning()
	if providerDownloads.Load() == 0 {
		t.Fatal("真实核心未读取本地 HTTP provider")
	}
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertClosed(t, endpoint)
	cache, err := os.ReadFile(cachePath)
	if err != nil || !bytes.Contains(cache, []byte("e2e-upstream")) {
		t.Fatalf("HTTP provider 未保留可重用缓存: %v", err)
	}

	// Reuse that exact downloaded cache while the provider refuses all requests.
	// This also exercises traditional mixed-port after the Template listener.
	providerDisabled.Store(true)
	downloads := providerDownloads.Load()
	providerNode := realYAMLField(realYAMLField(root, "proxy-providers"), "subscription")
	realYAMLField(providerNode, "type").Value = "file"
	realYAMLDrop(providerNode, "url", "interval")
	realYAMLDrop(root, "listeners")
	// This legacy-port branch intentionally returns to global routing after
	// removing the listener that normally pins its own outbound group.
	rules := realYAMLField(root, "rules")
	rules.Content[len(rules.Content)-1].Value = "MATCH,proxy"
	port, release = realProxyPort(t)
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "mixed-port"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(port)})
	fileConfig := realYAMLBytes(t, &document)
	cli.configure(fileConfig)
	release()
	cli.must("proxy", "start", "--yes", "--json")
	endpoint = cli.endpoint("mixed")
	realProxyTraffic(t, endpoint.URL(), endpoint.URL(), origin, tlsOrigin, &originRequests, &upstreamTunnels)
	beforeRestart := cli.assertRunning()
	cli.must("proxy", "start", "--yes", "--json")
	if same := cli.assertRunning(); same != beforeRestart {
		t.Fatalf("重复 start 替换了存活出生身份: before=%+v after=%+v", beforeRestart, same)
	}
	cli.rejectInvalidRestart(fileConfig, false)
	if same := cli.assertRunning(); same != beforeRestart {
		t.Fatalf("静态无效配置 restart 停止或替换了旧身份: before=%+v after=%+v", beforeRestart, same)
	}
	realProxyTraffic(t, endpoint.URL(), endpoint.URL(), origin, tlsOrigin, &originRequests, &upstreamTunnels)
	// A statically valid candidate is validated by the core only after the
	// original birth is stopped. Native validation failure does not restart it.
	cli.rejectInvalidRestart(fileConfig, true)
	realAssertGone(t, beforeRestart)
	realAssertClosed(t, endpoint)
	var failedRestart proxy.Status
	if err := json.Unmarshal(cli.must("proxy", "status", "--json"), &failedRestart); err != nil || failedRestart.Runtime.State != "stopped" || failedRestart.Runtime.Listeners {
		t.Fatalf("原生校验失败的 restart 未保持停止: %+v %v", failedRestart, err)
	}
	// The helper restored the original private bytes; recovery is explicit.
	cli.must("proxy", "start", "--yes", "--json")
	recovered := cli.assertRunning()
	if realSameBirth(beforeRestart, recovered) {
		t.Fatal("失败 restart 后的显式 start 复用了旧出生身份")
	}
	realProxyTraffic(t, endpoint.URL(), endpoint.URL(), origin, tlsOrigin, &originRequests, &upstreamTunnels)
	cli.must("proxy", "restart", "--yes", "--json")
	afterRestart := cli.assertRunning()
	if realSameBirth(recovered, afterRestart) {
		t.Fatalf("restart 未创建新的出生身份: before=%+v after=%+v", recovered, afterRestart)
	}
	realAssertGone(t, recovered)
	t.Logf("真实 restart 出生身份: before=%+v after=%+v", recovered, afterRestart)
	realProxyTraffic(t, endpoint.URL(), endpoint.URL(), origin, tlsOrigin, &originRequests, &upstreamTunnels)
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertGone(t, afterRestart)
	cli.must("proxy", "stop", "--yes", "--json")
	var stopped proxy.Status
	if err := json.Unmarshal(cli.must("proxy", "status", "--json"), &stopped); err != nil || stopped.Runtime.State != "stopped" || stopped.Runtime.Listeners || len(stopped.Problems) != 0 {
		t.Fatalf("重复 stop 后状态错误: %+v %v", stopped, err)
	}
	realAssertClosed(t, endpoint)
	if providerDownloads.Load() != downloads {
		t.Fatal("file provider 重启时仍访问了 HTTP provider")
	}
	if current, err := os.ReadFile(cachePath); err != nil || !bytes.Equal(current, cache) {
		t.Fatalf("file provider 复用改变了既有缓存: %v", err)
	}

	// Independent native HTTP/SOCKS listeners and an inline provider use the
	// same constrained upstream, with no DIRECT node or fallback in the group.
	httpPort, releaseHTTP := realProxyPort(t)
	socksPort, releaseSOCKS := realProxyPort(t)
	inline := []byte(fmt.Sprintf("allow-lan: false\nmode: rule\nlog-level: warning\ngeo-auto-update: false\nfind-process-mode: off\nlisteners:\n  - name: http-entry\n    type: http\n    listen: 127.0.0.1\n    port: %d\n  - name: socks-entry\n    type: socks\n    listen: 127.0.0.1\n    port: %d\nproxy-providers:\n  local:\n    type: inline\n    payload:\n      - name: e2e-upstream\n        type: http\n        server: 127.0.0.1\n        port: %d\nproxy-groups:\n  - name: PROXY\n    type: select\n    use: [local]\n    empty-fallback: REJECT\nrules:\n  - MATCH,PROXY\n", httpPort, socksPort, upstreamPort))
	cli.configure(inline)
	releaseHTTP()
	releaseSOCKS()
	cli.must("proxy", "start", "--yes", "--json")
	httpEndpoint, socksEndpoint := cli.endpoint("http-entry"), cli.endpoint("socks-entry")
	if httpEndpoint.Scheme != "http" || socksEndpoint.Scheme != "socks5h" {
		t.Fatalf("原生 listener 端点类型错误: %+v %+v", httpEndpoint, socksEndpoint)
	}
	realProxyTraffic(t, httpEndpoint.URL(), socksEndpoint.URL(), origin, tlsOrigin, &originRequests, &upstreamTunnels)
	cli.assertRunning()
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertClosed(t, httpEndpoint)
	realAssertClosed(t, socksEndpoint)
	// Static policy accepts a native mode scalar, but the real validator must
	// reject an unknown mode without replacing the previous configuration.
	badPath := filepath.Join(filepath.Dir(cli.root), "invalid-native.yaml")
	if err := os.WriteFile(badPath, bytes.Replace(inline, []byte("mode: rule"), []byte("mode: invalid-e2e-mode"), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cli.service().Configure(badPath); err == nil {
		t.Fatal("真实核心未拒绝无效原生 mode")
	} else if !strings.Contains(err.Error(), "核心配置验证失败") {
		t.Fatalf("无效 mode 未到达真实核心校验: %v", err)
	}
	if current, err := os.ReadFile(filepath.Join(cli.root, "proxy", "config.yaml")); err != nil || !bytes.Equal(current, inline) {
		t.Fatalf("真实核心验证失败后原配置被替换: %v", err)
	}

	// A fresh, unavailable subscription must not turn Template's group into a
	// DIRECT route, even though the core can still open its inbound listener.
	missing, err := proxy.Template(provider.URL + "/unavailable.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(missing, &document); err != nil {
		t.Fatal(err)
	}
	root = document.Content[0]
	realQuickStartLocalHealth(root, origin.URL)
	port, release = realProxyPort(t)
	realYAMLField(realYAMLField(root, "listeners").Content[0], "port").Value = strconv.Itoa(port)
	realYAMLField(realYAMLField(realYAMLField(root, "proxy-providers"), "subscription"), "path").Value = "providers/unavailable-e2e.yaml"
	cli.configure(realYAMLBytes(t, &document))
	release()
	cli.must("proxy", "start", "--yes", "--json")
	endpoint = cli.endpoint("endpoint-proxy")
	realProxyRejectsUnavailable(t, endpoint, origin.URL, &originRequests, &upstreamTunnels)
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertClosed(t, endpoint)

	sentinel := filepath.Join(cli.root, "proxy", "unknown-data", "keep.bin")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("unknown component data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	retained := cli.rejectArtifactUpdate(artifact, hex.EncodeToString(sum[:]))
	cli.must("proxy", "remove", "--yes", "--json")
	if _, err := os.Stat(filepath.Join(cli.root, "proxy", "mihomo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove 未移除临时核心: %v", err)
	}
	for path, want := range retained {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("remove 修改了应保留文件 %s: %v", path, err)
		}
	}
	for path, want := range cli.sentinels {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("临时 HOME 中的认证/会话模拟数据被修改 %s: %v", path, err)
		}
	}
	t.Logf("真实回环证据: provider HTTP 请求=%d，origin 请求=%d，上游 CONNECT=%d；旧 artifact update 参数明确拒绝，remove 保留快照一致；未验证原生更新", providerDownloads.Load(), originRequests.Load(), upstreamTunnels.Load())
}

type realProxyCLI struct {
	t                *testing.T
	executable, root string
	env              []string
	sentinels        map[string][]byte
	preserveScratch  bool
}

func newRealProxyCLI(t *testing.T, reports isolatedRaceReports) *realProxyCLI {
	t.Helper()
	created, err := os.MkdirTemp("", "nido-real-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := filepath.EvalSymlinks(created)
	if err != nil {
		os.Remove(created)
		t.Fatal(err)
	}
	cli := &realProxyCLI{t: t, executable: filepath.Join(scratch, "nido"), sentinels: map[string][]byte{}}
	t.Cleanup(func() {
		if cli.preserveScratch {
			t.Logf("未确认临时核心退出，保留本测试目录及进程记录: %s", scratch)
			return
		}
		if err := os.RemoveAll(scratch); err != nil {
			t.Errorf("移除本测试临时目录失败: %v", err)
		}
	})
	home, tmp := filepath.Join(scratch, "home"), filepath.Join(scratch, "tmp")
	for _, path := range []string{home, tmp} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cli.root = filepath.Join(home, "nido")
	cli.env = []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "NIDO_HOME=" + cli.root, "CODEX_HOME=" + filepath.Join(home, ".codex"), "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"), "TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp, "LANG=C", "LC_ALL=C", "TZ=UTC", "NO_PROXY=127.0.0.1,::1", "no_proxy=127.0.0.1,::1"}
	if reports.path != "" {
		cli.env = append(cli.env, "GORACE=halt_on_error=1 log_path="+filepath.Join(reports.path, "race"))
	}
	for name, content := range map[string]string{".codex/auth.json": "{\"fixture\":\"auth\"}\n", ".codex/sessions/session.jsonl": "{\"fixture\":\"session\"}\n"} {
		path := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		cli.sentinels[path] = []byte(content)
	}
	if prebuilt := os.Getenv("NIDO_TEST_NIDO"); prebuilt != "" {
		if !filepath.IsAbs(prebuilt) {
			t.Fatal("NIDO_TEST_NIDO 必须是预构建 Nido 的绝对路径")
		}
		data, err := readFile(prebuilt, 512<<20)
		if err != nil {
			t.Fatalf("无法安全读取预构建 Nido: %v", err)
		}
		if err := os.WriteFile(cli.executable, data, 0700); err != nil {
			t.Fatalf("复制预构建 Nido 到临时目录: %v", err)
		}
		return cli
	}
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	moduleCache := os.Getenv("GOMODCACHE")
	if moduleCache == "" {
		out, err := exec.CommandContext(ctx, goTool, "env", "GOMODCACHE").Output()
		if err != nil {
			t.Fatalf("定位已有 Go module cache: %v", err)
		}
		moduleCache = strings.TrimSpace(string(out))
	}
	cmd := exec.CommandContext(ctx, goTool, "build", "-mod=readonly", "-trimpath", "-o", cli.executable, "./cmd/nido")
	cmd.Dir = filepath.Clean(filepath.Join("..", ".."))
	buildCache, goPath := os.Getenv("GOCACHE"), os.Getenv("GOPATH")
	if buildCache == "" {
		buildCache = filepath.Join(scratch, "go-build-cache")
	}
	if goPath == "" {
		goPath = filepath.Join(scratch, "go-path")
	}
	cmd.Env = append(append([]string(nil), cli.env...), "GOENV=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOCACHE="+buildCache, "GOPATH="+goPath, "GOMODCACHE="+moduleCache)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建临时 Nido: %v\n%s", err, out)
	}
	return cli
}

func (c *realProxyCLI) run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.executable, append([]string{"--root", c.root}, args...)...)
	cmd.Env = c.env
	cmd.Dir = filepath.Dir(c.root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%v: %w: %s", args, err, out)
	}
	return out, nil
}

func (c *realProxyCLI) must(args ...string) []byte {
	c.t.Helper()
	out, err := c.run(args...)
	if err != nil {
		log, _ := os.ReadFile(filepath.Join(c.root, "proxy", "output.log"))
		c.t.Fatalf("真实 CLI 失败: %v\n临时核心日志: %s", err, log)
	}
	if !json.Valid(out) {
		c.t.Fatalf("真实 CLI 未返回 JSON %v: %s", args, out)
	}
	return out
}

func (c *realProxyCLI) configure(data []byte) {
	c.t.Helper()
	path := filepath.Join(filepath.Dir(c.root), "candidate.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		c.t.Fatal(err)
	}
	// This is explicit test injection through the component API. The current
	// CLI configures interactively and deliberately has no --file import flag.
	if err := c.service().Configure(path); err != nil {
		c.t.Fatalf("真实核心未接受注入的原生配置: %v", err)
	}
	c.must("proxy", "validate", "--json")
}

func (c *realProxyCLI) service() *proxy.Service {
	return &proxy.Service{Root: c.root, Env: append([]string(nil), c.env...), Launcher: c.executable}
}

func (c *realProxyCLI) endpoint(name string) proxy.Endpoint {
	c.t.Helper()
	var endpoint proxy.Endpoint
	if err := json.Unmarshal(c.must("proxy", "endpoint", name, "--json"), &endpoint); err != nil || endpoint.Port == 0 || endpoint.Authentication {
		c.t.Fatalf("真实端点无效 %+v: %v", endpoint, err)
	}
	return endpoint
}

func (c *realProxyCLI) assertRunning() proxy.Process {
	c.t.Helper()
	var status proxy.Status
	if err := json.Unmarshal(c.must("proxy", "status", "--json"), &status); err != nil || status.Runtime.State != "running" || !status.Runtime.Listeners || status.Runtime.ConfigChanged || len(status.Problems) != 0 {
		c.t.Fatalf("真实核心状态错误 %+v: %v", status, err)
	}
	var record struct {
		Process  proxy.Process `json:"process"`
		Program  string        `json:"program"`
		Launcher string        `json:"launcher"`
		Phase    string        `json:"phase"`
	}
	data, err := readFile(filepath.Join(c.root, "proxy", "process.json"), 32<<10)
	if err != nil || json.Unmarshal(data, &record) != nil || record.Program != filepath.Join(c.root, "proxy", "mihomo") || record.Launcher != c.executable || record.Process.Executable != c.executable || record.Phase != "running" {
		c.t.Fatalf("真实核心记录不属于本测试 root/launcher: %+v %v", record, err)
	}
	p, err := (proxy.OSRunner{}).Inspect(record.Process.PID)
	if err != nil || p.PID <= 1 || p.PID != status.Runtime.PID || p.UID != uint32(os.Geteuid()) || p.Boot == "" || p.Started == "" || !realSameBirth(p, record.Process) || p.Executable != record.Program || p.ExecutableDeleted {
		c.t.Fatalf("真实核心内核身份不匹配: process=%+v record=%+v err=%v", p, record, err)
	}
	return p
}

func (c *realProxyCLI) rejectArtifactUpdate(artifact, checksum string) map[string][]byte {
	c.t.Helper()
	program := filepath.Join(c.root, "proxy", "mihomo")
	before, err := readFile(program, 256<<20)
	if err != nil {
		c.t.Fatal(err)
	}
	retained := realProxyFiles(c.t, filepath.Join(c.root, "proxy"))
	output, err := c.run("proxy", "update", "--artifact", artifact, "--sha256", checksum, "--yes", "--json")
	if err == nil || !bytes.Contains(output, []byte("程序包选项仅用于首次安装")) {
		c.t.Fatalf("旧 artifact update 未明确拒绝: %v %s", err, output)
	}
	if after, err := readFile(program, 256<<20); err != nil || !bytes.Equal(after, before) {
		c.t.Fatalf("拒绝旧 artifact update 时改变了已安装程序: %v", err)
	}
	realAssertRetainedBytes(c.t, retained)
	realAssertRetainedBytes(c.t, c.sentinels)
	return retained
}

func realInvalidRestartConfig(config []byte, native bool) ([]byte, error) {
	replacement := []byte("mode: rule\ntun: {enable: true}")
	if native {
		replacement = []byte("mode: invalid-e2e-restart")
	}
	invalid := bytes.Replace(config, []byte("mode: rule"), replacement, 1)
	if bytes.Equal(invalid, config) {
		return nil, fmt.Errorf("无法构造无效 restart 配置")
	}
	return invalid, nil
}

func TestRealRestartCandidatesSeparateStaticAndNativeValidation(t *testing.T) {
	config := []byte("mode: rule\nmixed-port: 17890\n")
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			candidate, err := realInvalidRestartConfig(config, native)
			if err != nil {
				t.Fatal(err)
			}
			_, err = proxy.ParseConfig(candidate, t.TempDir())
			if (err == nil) != native {
				t.Fatalf("native=%v 的候选静态校验结果错误: %v", native, err)
			}
			if string(config) != "mode: rule\nmixed-port: 17890\n" {
				t.Fatal("构造候选修改了原配置")
			}
		})
	}
	if _, err := realInvalidRestartConfig([]byte("mixed-port: 17890\n"), true); err == nil {
		t.Fatal("没有目标字段时仍声称构造了无效配置")
	}
}

func (c *realProxyCLI) rejectInvalidRestart(config []byte, native bool) {
	c.t.Helper()
	path := filepath.Join(c.root, "proxy", "config.yaml")
	current, err := readFile(path, 4<<20)
	if err != nil || !bytes.Equal(current, config) {
		c.t.Fatalf("无效 restart 夹具未匹配既有配置: %v", err)
	}
	invalid, err := realInvalidRestartConfig(config, native)
	if err != nil {
		c.t.Fatal(err)
	}
	// Only the test's private managed config is edited. Restore even when an
	// assertion fails. Native validation failure deliberately leaves it stopped;
	// the caller must explicitly start after these original bytes are restored.
	defer func() {
		if err := os.WriteFile(path, config, 0600); err != nil {
			c.t.Errorf("恢复本测试原配置失败: %v", err)
		}
	}()
	if err := os.WriteFile(path, invalid, 0600); err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.run("proxy", "restart", "--yes", "--json"); err == nil {
		c.t.Fatalf("restart 接受了无效配置（native=%v）", native)
	}
	if after, err := readFile(path, 4<<20); err != nil || !bytes.Equal(after, invalid) {
		c.t.Fatalf("失败的 restart 修改了候选原配置: %v", err)
	}
}

func realSameBirth(a, b proxy.Process) bool {
	return a.PID == b.PID && a.UID == b.UID && a.Boot == b.Boot && a.Started == b.Started
}

func realAssertGone(t *testing.T, expected proxy.Process) {
	t.Helper()
	actual, err := (proxy.OSRunner{}).Inspect(expected.PID)
	if errors.Is(err, os.ErrNotExist) || err == nil && !realSameBirth(actual, expected) {
		return
	}
	t.Fatalf("旧出生身份仍存活或无法确认退出: expected=%+v actual=%+v err=%v", expected, actual, err)
}

func (c *realProxyCLI) cleanup() {
	if c.t.Failed() {
		path := filepath.Join(c.root, "proxy", "output.log")
		if f, err := os.Open(path); err == nil {
			defer f.Close()
			if info, err := f.Stat(); err == nil && info.Mode().IsRegular() {
				start := info.Size() - (32 << 10)
				if start < 0 {
					start = 0
				}
				log, _ := io.ReadAll(io.NewSectionReader(f, start, info.Size()-start))
				c.t.Logf("仅本测试临时核心日志 tail:\n%s", log)
			}
		}
	}
	if _, err := c.run("proxy", "stop", "--yes", "--json"); err == nil {
		return
	} else {
		c.t.Logf("清理 CLI stop 失败，按临时记录的精确身份重试: %v", err)
	}
	var record struct {
		Process  proxy.Process `json:"process"`
		Program  string        `json:"program"`
		Launcher string        `json:"launcher"`
	}
	data, err := os.ReadFile(filepath.Join(c.root, "proxy", "process.json"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil || json.Unmarshal(data, &record) != nil || record.Program != filepath.Join(c.root, "proxy", "mihomo") || record.Launcher != c.executable {
		c.preserveScratch = true
		c.t.Errorf("无法安全核验临时核心清理记录: %v", err)
		return
	}
	runner := proxy.OSRunner{}
	p, err := runner.Inspect(record.Process.PID)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil || p.PID != record.Process.PID || p.UID != record.Process.UID || p.Boot != record.Process.Boot || p.Started != record.Process.Started || p.Executable != record.Program && p.Executable != record.Launcher {
		c.preserveScratch = true
		c.t.Errorf("临时核心出生身份无法核验，未停止未知进程: %v", err)
		return
	}
	if err := runner.Stop(p); err != nil {
		c.preserveScratch = true
		c.t.Errorf("临时核心精确清理失败: %v", err)
		return
	}
	after, err := runner.Inspect(p.PID)
	if errors.Is(err, os.ErrNotExist) || err == nil && (after.UID != p.UID || after.Boot != p.Boot || after.Started != p.Started) {
		return
	}
	c.preserveScratch = true
	c.t.Errorf("精确停止后仍无法确认临时核心已退出: %+v %v", after, err)
}

func realAwaitProviderCache(t *testing.T, path string, downloads *atomic.Int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if downloads.Load() > 0 && err == nil && bytes.Contains(data, []byte("e2e-upstream")) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("真实核心启动后未在期限内加载本地 provider 并写入唯一节点缓存")
}

func realOrigin(requests *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, "real-proxy-e2e:"+r.URL.Path)
	})
}

func realConnectProxy(t *testing.T, tunnels *atomic.Int64, targets ...string) *httptest.Server {
	t.Helper()
	allowed := map[string]bool{}
	for _, target := range targets {
		allowed[target] = true
	}
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || !allowed[r.Host] {
			http.Error(w, "only local fixture CONNECT targets are allowed", http.StatusForbidden)
			return
		}
		target, err := net.DialTimeout("tcp4", r.Host, 3*time.Second)
		if err != nil {
			http.Error(w, "fixture target unavailable", http.StatusBadGateway)
			return
		}
		defer target.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		mu.Lock()
		connections[client], connections[target] = true, true
		mu.Unlock()
		defer func() { mu.Lock(); delete(connections, client); delete(connections, target); mu.Unlock() }()
		tunnels.Add(1)
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if buffered.Flush() != nil {
			return
		}
		go func() { _, _ = io.Copy(target, buffered); target.Close() }()
		_, _ = io.Copy(client, target)
	}))
	t.Cleanup(func() {
		server.Close()
		mu.Lock()
		for connection := range connections {
			connection.Close()
		}
		mu.Unlock()
	})
	return server
}

func realProxyTraffic(t *testing.T, httpEndpoint, socksEndpoint string, origin, tlsOrigin *httptest.Server, origins, tunnels *atomic.Int64) {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(tlsOrigin.Certificate())
	for _, request := range []struct{ protocol, target string }{{"http", origin.URL + "/http"}, {"socks", origin.URL + "/socks"}, {"http", tlsOrigin.URL + "/https-connect"}, {"socks", tlsOrigin.URL + "/https-socks"}} {
		transport := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
		if request.protocol == "http" {
			address, err := url.Parse(httpEndpoint)
			if err != nil {
				t.Fatal(err)
			}
			transport.Proxy = http.ProxyURL(address)
		} else {
			address, err := url.Parse(socksEndpoint)
			if err != nil {
				t.Fatal(err)
			}
			transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
				return realSOCKSDial(ctx, address.Host, target)
			}
		}
		beforeOrigins, beforeTunnels := origins.Load(), tunnels.Load()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, err := client.Get(request.target)
		if err != nil {
			transport.CloseIdleConnections()
			t.Fatalf("真实 %s 流量失败 %s: %v", request.protocol, request.target, err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
		response.Body.Close()
		transport.CloseIdleConnections()
		address, _ := url.Parse(request.target)
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "real-proxy-e2e:"+address.Path || origins.Load() != beforeOrigins+1 || tunnels.Load() <= beforeTunnels {
			t.Fatalf("流量未由真实 provider 节点承载: protocol=%s status=%d body=%q origins=%d->%d tunnels=%d->%d err=%v", request.protocol, response.StatusCode, body, beforeOrigins, origins.Load(), beforeTunnels, tunnels.Load(), err)
		}
	}
}

func realProxyRejectsUnavailable(t *testing.T, endpoint proxy.Endpoint, target string, origins, tunnels *atomic.Int64) {
	t.Helper()
	address, err := url.Parse(endpoint.URL())
	if err != nil {
		t.Fatal(err)
	}
	beforeOrigins, beforeTunnels := origins.Load(), tunnels.Load()
	transport := &http.Transport{Proxy: http.ProxyURL(address), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(target + "/unavailable-provider-must-not-direct")
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatal("订阅不可用时仍返回成功响应")
		}
	}
	if origins.Load() != beforeOrigins || tunnels.Load() != beforeTunnels {
		t.Fatalf("订阅不可用却转发了流量: origins=%d->%d tunnels=%d->%d", beforeOrigins, origins.Load(), beforeTunnels, tunnels.Load())
	}
}

func realSOCKSDial(ctx context.Context, proxyAddress, target string) (_ net.Conn, err error) {
	host, portText, err := net.SplitHostPort(target)
	ip := net.ParseIP(host).To4()
	port, portErr := strconv.Atoi(portText)
	if err != nil || portErr != nil || ip == nil || !ip.IsLoopback() || port < 1 || port > 65535 {
		return nil, fmt.Errorf("SOCKS 测试只允许字面 IPv4 回环目标")
	}
	connection, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp4", proxyAddress)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			connection.Close()
		}
	}()
	connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = connection.Write([]byte{5, 1, 0}); err != nil {
		return nil, err
	}
	var greeting [2]byte
	if _, err = io.ReadFull(connection, greeting[:]); err != nil {
		return nil, err
	}
	if greeting != [2]byte{5, 0} {
		return nil, fmt.Errorf("SOCKS greeting 错误: %v", greeting)
	}
	command := append([]byte{5, 1, 0, 1}, ip...)
	command = binary.BigEndian.AppendUint16(command, uint16(port))
	if _, err = connection.Write(command); err != nil {
		return nil, err
	}
	var reply [4]byte
	if _, err = io.ReadFull(connection, reply[:]); err != nil {
		return nil, err
	}
	if reply[0] != 5 || reply[1] != 0 || reply[2] != 0 {
		return nil, fmt.Errorf("SOCKS CONNECT 错误: %v", reply)
	}
	addressSize := 0
	switch reply[3] {
	case 1:
		addressSize = 4
	case 4:
		addressSize = 16
	case 3:
		var length [1]byte
		if _, err = io.ReadFull(connection, length[:]); err != nil {
			return nil, err
		}
		addressSize = int(length[0])
	default:
		return nil, fmt.Errorf("SOCKS 回复地址类型无效")
	}
	if _, err = io.CopyN(io.Discard, connection, int64(addressSize+2)); err != nil {
		return nil, err
	}
	connection.SetDeadline(time.Time{})
	return connection, nil
}

func realProxyPort(t *testing.T) (int, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener.Addr().(*net.TCPAddr).Port, func() { listener.Close() }
}

func realAssertClosed(t *testing.T, endpoint proxy.Endpoint) {
	t.Helper()
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)), 200*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatalf("stop 后回环入口仍可连接: %s", endpoint.URL())
	}
}

func realYAMLField(mapping *yaml.Node, name string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return mapping.Content[i+1]
		}
	}
	panic("missing fixture YAML field: " + name)
}

// Keep these lifecycle/traffic fixtures deterministic and loopback-only.
// Their counters measure explicit traffic, not automatic health probes.
func realQuickStartLocalHealth(root *yaml.Node, target string) {
	providers := realYAMLField(root, "proxy-providers")
	for i := 1; i < len(providers.Content); i += 2 {
		health := realYAMLField(providers.Content[i], "health-check")
		realYAMLField(health, "url").Value = target
		realYAMLField(health, "enable").Value = "false"
	}
	for _, group := range realYAMLField(root, "proxy-groups").Content {
		realYAMLField(group, "url").Value = target
		realYAMLField(group, "interval").Value = "0"
	}
}

func realYAMLDrop(mapping *yaml.Node, names ...string) {
	var content []*yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		remove := false
		for _, name := range names {
			remove = remove || mapping.Content[i].Value == name
		}
		if !remove {
			content = append(content, mapping.Content[i], mapping.Content[i+1])
		}
	}
	mapping.Content = content
}

func realYAMLBytes(t *testing.T, document *yaml.Node) []byte {
	t.Helper()
	data, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func realProxyFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path == filepath.Join(dir, "mihomo") || path == filepath.Join(dir, ".lock") {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil {
			files[path] = data
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}
