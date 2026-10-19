package nido

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"go.yaml.in/yaml/v3"
)

// Explicit opt-in only. This tests pure QuickStart generation injected via
// Service.Configure and native core forwarding, not an interactive CLI flow or
// external-tool bindings. Every upstream, provider, health check and origin is
// a temporary loopback fixture. Neither core is installed as a system service.
func TestProxyRealMihomoQuickStart(t *testing.T) {
	artifact := os.Getenv("NIDO_TEST_MIHOMO")
	if artifact == "" {
		t.Skip("设置 NIDO_TEST_MIHOMO 才执行 Linux amd64 真实 quick start 隔离验证")
	}
	if !filepath.IsAbs(artifact) || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("真实 quick start 专项需要 Linux amd64 和绝对核心路径")
	}
	reports, err := prepareIsolatedRaceReports(os.Getenv("NIDO_TEST_RACE_REPORT_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reports.check(); err != nil {
			t.Error(err)
		}
	})
	if _, err := (proxy.OSRunner{}).Inspect(os.Getpid()); err != nil {
		t.Fatalf("显式真实测试需要内核归属权限: %v", err)
	}
	cli := newRealProxyCLI(t, reports)
	home := filepath.Dir(cli.root)
	for name, data := range map[string]string{
		".codex/.env": "KEEP=literal\n", ".codex/state.sqlite": "DB-SENTINEL\n",
		".gitconfig": "[user]\n\tname = Quick Start Fixture\n", ".bashrc": "# SHELL-SENTINEL\n",
		".config/mihomo/config.yaml": "# EXTERNAL-PROXY-SENTINEL\n",
	} {
		path := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		cli.sentinels[path] = []byte(data)
	}
	cli.must("init", "--yes", "--json")

	var origins, healthRequests, ispHealth, providerDownloads, deadChecks atomic.Int64
	origin := httptest.NewServer(realOrigin(&origins))
	t.Cleanup(origin.Close)
	tlsOrigin := httptest.NewTLSServer(realOrigin(&origins))
	t.Cleanup(tlsOrigin.Close)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthRequests.Add(1)
		if r.URL.Path == "/isp" {
			ispHealth.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(health.Close)
	businessTargets := []string{origin.Listener.Addr().String(), tlsOrigin.Listener.Addr().String()}
	var dailyTraffic, downloadTraffic, relayTraffic, dailyHealth, downloadHealth, relayHealth atomic.Int64
	ssPassword := "p-ass:word@+/%2D"
	ssPort := quickStartSSFixture(t, artifact, ssPassword)
	ssAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(ssPort))
	daily := quickStartConnectProxy(t, &dailyTraffic, &dailyHealth, businessTargets, health.Listener.Addr().String())
	download := quickStartConnectProxy(t, &downloadTraffic, &downloadHealth, businessTargets, health.Listener.Addr().String())
	relay := quickStartConnectProxy(t, &relayTraffic, &relayHealth, []string{ssAddress}, health.Listener.Addr().String())
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadChecks.Add(1)
		http.Error(w, "fixture intentionally unavailable", http.StatusBadGateway)
	}))
	t.Cleanup(dead.Close)
	portOf := func(server *httptest.Server) int { return server.Listener.Addr().(*net.TCPAddr).Port }
	providerData := []byte(fmt.Sprintf("proxies:\n"+
		"  - {name: Daily-0-dead, type: http, server: 127.0.0.1, port: %d}\n"+
		"  - {name: Daily-1-live, type: http, server: 127.0.0.1, port: %d}\n"+
		"  - {name: Download-0-dead, type: http, server: 127.0.0.1, port: %d}\n"+
		"  - {name: Download-1-live, type: http, server: 127.0.0.1, port: %d}\n"+
		"  - {name: Relay-0-dead, type: http, server: 127.0.0.1, port: %d}\n"+
		"  - {name: Relay-1-live, type: http, server: 127.0.0.1, port: %d}\n",
		portOf(dead), portOf(daily), portOf(dead), portOf(download), portOf(dead), portOf(relay)))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerDownloads.Add(1)
		if r.URL.Path != "/provider.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write(providerData)
	}))
	t.Cleanup(provider.Close)
	t.Cleanup(cli.cleanup)

	proxyPort, releaseProxy := realProxyPort(t)
	downloadPort, releaseDownload := realProxyPort(t)
	ispPort, releaseISP := realProxyPort(t)
	nodeURL := fmt.Sprintf("ss://aes-128-gcm:p%%2Dass%%3Aword%%40+%%2F%%252D@127.0.0.1:%d#ISP%%2Dnode", ssPort)
	coreData, err := readFile(artifact, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(coreData)
	cli.must("proxy", "install", "--artifact", artifact, "--sha256", hex.EncodeToString(sum[:]), "--yes", "--json")
	generated, err := proxy.QuickStart(proxy.QuickStartOptions{
		ProviderURL: provider.URL + "/provider.yaml", ProxyFilter: "^Daily-", ProxyPort: proxyPort,
		Download: &proxy.QuickStartRoute{Filter: "^Download-", Port: downloadPort},
		ISP:      &proxy.QuickStartISP{NodeURL: nodeURL, Port: ispPort, TestURL: health.URL + "/isp", RelayFilter: "^Relay-"},
	})
	if err != nil {
		t.Fatal(err)
	}
	quickStartAssertTopology(t, generated, ssPassword)
	if _, err := os.Stat(filepath.Join(cli.root, "proxy", "process.json")); !os.IsNotExist(err) {
		t.Fatal("quick start 自行启动了核心或创建了运行记录")
	}
	// The defaults are verified above. Only test targets are localized; active
	// health checks and nonzero URLTest intervals are deliberately preserved.
	localized := realQuickStartHealthConfig(t, generated, health.URL+"/health")
	cli.configure(localized)
	releaseProxy()
	releaseDownload()
	releaseISP()
	cli.must("proxy", "start", "--yes", "--json")
	first := cli.assertRunning()
	var endpoints []proxy.Endpoint
	if err := json.Unmarshal(cli.must("proxy", "endpoints", "--json"), &endpoints); err != nil || len(endpoints) != 3 {
		t.Fatalf("listeners 未正确导出: %+v %v", endpoints, err)
	}
	dailyEndpoint, downloadEndpoint, ispEndpoint := cli.endpoint("endpoint-proxy"), cli.endpoint("endpoint-download"), cli.endpoint("endpoint-isp")
	cachePath := filepath.Join(cli.root, "proxy", "providers", "subscription.yaml")
	quickStartAwait(t, "provider 缓存和活跃健康检查", func() bool {
		cache, _ := os.ReadFile(cachePath)
		return providerDownloads.Load() > 0 && bytes.Equal(cache, providerData) && healthRequests.Load() > 0 && dailyHealth.Load() > 0 && downloadHealth.Load() > 0 && relayHealth.Load() > 0 && deadChecks.Load() >= 3
	})
	for _, endpoint := range []proxy.Endpoint{dailyEndpoint, downloadEndpoint, ispEndpoint} {
		quickStartAwaitEndpoint(t, endpoint, health.URL+"/ready")
	}
	quickStartAwait(t, "ISP 自定义地址的自动健康检查", func() bool { return ispHealth.Load() > 0 })
	if relayTraffic.Load() == 0 {
		t.Fatal("ISP 健康检查未经过 HTTP 前置到 SS 服务端")
	}
	// Each provider route uses a distinct constrained proxy. Health targets
	// have separate ports/counters, so they cannot fake a business tunnel.
	realProxyTraffic(t, dailyEndpoint.URL(), dailyEndpoint.URL(), origin, tlsOrigin, &origins, &dailyTraffic)
	realProxyTraffic(t, downloadEndpoint.URL(), downloadEndpoint.URL(), origin, tlsOrigin, &origins, &downloadTraffic)
	realProxyTraffic(t, ispEndpoint.URL(), ispEndpoint.URL(), origin, tlsOrigin, &origins, &relayTraffic)
	t.Logf("真实 url-test 选择: dead 检查=%d，daily/download/relay 健康=%d/%d/%d，ISP 自定义测速=%d，目标=%d，daily/download/ISP 前置连接=%d/%d/%d", deadChecks.Load(), dailyHealth.Load(), downloadHealth.Load(), relayHealth.Load(), ispHealth.Load(), origins.Load(), dailyTraffic.Load(), downloadTraffic.Load(), relayTraffic.Load())

	// Configuration and forwarding must not change any external tool state.
	realAssertRetainedBytes(t, cli.sentinels)

	// Killing the only permitted relay must break the ISP path while leaving
	// daily traffic usable. This proves the ISP does not silently dial DIRECT.
	relay.CloseClientConnections()
	relay.Close()
	beforeOrigins := origins.Load()
	quickStartRejectTraffic(t, ispEndpoint, origin.URL+"/relay-unavailable")
	if origins.Load() != beforeOrigins {
		t.Fatal("ISP 前置关闭后仍绕过前置到达目标")
	}
	realProxyTraffic(t, dailyEndpoint.URL(), dailyEndpoint.URL(), origin, tlsOrigin, &origins, &dailyTraffic)
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertGone(t, first)
	for _, endpoint := range endpoints {
		realAssertClosed(t, endpoint)
	}

	// The native file remains freely editable, including all listener names.
	renamed := bytes.ReplaceAll(localized, []byte("endpoint-proxy"), []byte("off"))
	cli.configure(renamed)
	cli.must("proxy", "start", "--yes", "--json")
	renamedEndpoint := cli.endpoint("off")
	realProxyTraffic(t, renamedEndpoint.URL(), renamedEndpoint.URL(), origin, tlsOrigin, &origins, &dailyTraffic)
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertClosed(t, renamedEndpoint)
	realQuickStartNodeMatrix(t, artifact, provider.URL+"/provider.yaml", health.URL+"/matrix")
	retained := realProxyFiles(t, filepath.Join(cli.root, "proxy"))
	cli.must("proxy", "remove", "--yes", "--json")
	for path, want := range retained {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("卸载改动既有组件数据 %s: %v", path, err)
		}
	}
	for path, want := range cli.sentinels {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("卸载改动外部哨兵 %s: %v", path, err)
		}
	}
	t.Logf("真实 quick start 完成: provider=%d，health=%d，业务目标=%d；配置生成与注入、HTTP/SOCKS/HTTPS、重命名 listener、停机和卸载保留均通过；未验证交互 CLI 或外部工具绑定", providerDownloads.Load(), healthRequests.Load(), origins.Load())
}

func quickStartAssertTopology(t *testing.T, data []byte, password string) {
	t.Helper()
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"port", "mixed-port", "socks-port", "external-controller"} {
		if _, exists := config[name]; exists {
			t.Fatalf("quick start 启用了非 listener 入口: %s", name)
		}
	}
	if rules := config["rules"].([]any); len(rules) != 3 ||
		rules[0] != "AND,((IN-TYPE,INNER),(DST-PORT,443),(DOMAIN,github.com)),download" ||
		rules[1] != "AND,((IN-TYPE,INNER),(DST-PORT,443),(DOMAIN,release-assets.githubusercontent.com)),download" ||
		rules[2] != "MATCH,DIRECT" {
		t.Fatal("默认规则发生变化")
	}
	node := config["proxies"].([]any)[0].(map[string]any)
	if node["password"] != password || node["dialer-proxy"] != "isp-relay" || node["type"] != "ss" {
		t.Fatalf("SS 解析或 ISP 链方向错误: %+v", node)
	}
	groups := config["proxy-groups"].([]any)
	if len(groups) != 4 || len(config["listeners"].([]any)) != 3 {
		t.Fatal("quick start 缺少组或入口")
	}
	wantFilters := map[string]string{"proxy": "^Daily-", "download": "^Download-", "isp-relay": "^Relay-"}
	for _, value := range groups {
		group := value.(map[string]any)
		if group["type"] != "url-test" || group["interval"] != 300 || group["empty-fallback"] != "REJECT" {
			t.Fatalf("未保留活跃 url-test: %+v", group)
		}
		if filter, exists := wantFilters[group["name"].(string)]; exists && (group["filter"] != filter || group["url"] != proxy.DefaultTestURL) {
			t.Fatalf("独立 filter 或默认测速地址错误: %+v", group)
		}
	}
}

func TestRealQuickStartFixtureUsesCurrentGeneratedRulesAndLoopbackHealth(t *testing.T) {
	options := proxy.QuickStartOptions{
		ProviderURL: "http://127.0.0.1:19100/provider.yaml", ProxyFilter: "^Daily-", ProxyPort: 19101,
		Download: &proxy.QuickStartRoute{Filter: "^Download-", Port: 19102},
		ISP: &proxy.QuickStartISP{
			NodeURL: "ss://aes-128-gcm:p%2Dass%3Aword%40+%2F%252D@127.0.0.1:19104#ISP%2Dnode",
			Port:    19103, TestURL: "http://127.0.0.1:19105/isp", RelayFilter: "^Relay-",
		},
	}
	generated, err := proxy.QuickStart(options)
	if err != nil {
		t.Fatal(err)
	}
	quickStartAssertTopology(t, generated, "p-ass:word@+/%2D")
	localized := realQuickStartHealthConfig(t, generated, "http://127.0.0.1:19105/health")
	parsed, err := proxy.ParseConfig(localized, t.TempDir())
	if err != nil || len(parsed.Endpoints) != 3 {
		t.Fatalf("迁移后的纯生成配置无效: %+v %v", parsed.Endpoints, err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(localized, &document); err != nil {
		t.Fatal(err)
	}
	provider := document["proxy-providers"].(map[string]any)["subscription"].(map[string]any)
	if provider["url"] != options.ProviderURL || provider["health-check"].(map[string]any)["url"] != "http://127.0.0.1:19105/health" {
		t.Fatal("provider 夹具目标未保持回环地址")
	}
	for _, value := range document["proxy-groups"].([]any) {
		group := value.(map[string]any)
		want := "http://127.0.0.1:19105/health"
		if group["name"] == "isp" {
			want = options.ISP.TestURL
		}
		if group["url"] != want || group["type"] != "url-test" {
			t.Fatalf("url-test 夹具目标未保持回环地址: %+v", group)
		}
	}
}

func realQuickStartHealthConfig(t *testing.T, data []byte, target string) []byte {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	root := document.Content[0]
	provider := realYAMLField(realYAMLField(root, "proxy-providers"), "subscription")
	realYAMLField(realYAMLField(provider, "health-check"), "url").Value = target
	for _, group := range realYAMLField(root, "proxy-groups").Content {
		// ISP already has its separately supplied loopback test target.
		if realYAMLField(group, "name").Value != "isp" {
			realYAMLField(group, "url").Value = target
		} else {
			// Its first check may precede provider initialization. After
			// asserting the generated 300s default, shorten only this test's
			// retry interval; never replace automatic checks with /ready traffic.
			realYAMLField(group, "interval").Value = "1"
		}
	}
	return realYAMLBytes(t, &document)
}

func quickStartAwait(t *testing.T, detail string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("等待真实 %s 超时", detail)
}

func quickStartAwaitEndpoint(t *testing.T, endpoint proxy.Endpoint, target string) {
	t.Helper()
	u, _ := http.NewRequest(http.MethodGet, target, nil)
	transport := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return url.Parse(endpoint.URL()) }, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	quickStartAwait(t, "url-test 健康选择 "+endpoint.Name, func() bool {
		response, err := client.Do(u.Clone(context.Background()))
		if err != nil {
			return false
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		response.Body.Close()
		return response.StatusCode == http.StatusNoContent
	})
}

func quickStartRejectTraffic(t *testing.T, endpoint proxy.Endpoint, target string) {
	t.Helper()
	transport := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return url.Parse(endpoint.URL()) }, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(target)
	if err == nil {
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatal("ISP 前置不可用仍转发成功")
		}
	}
}

func quickStartConnectProxy(t *testing.T, business, health *atomic.Int64, targets []string, healthTarget string) *httptest.Server {
	t.Helper()
	allowed := map[string]bool{}
	for _, target := range targets {
		allowed[target] = true
	}
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || !allowed[r.Host] && r.Host != healthTarget {
			http.Error(w, "only explicit loopback fixture CONNECT targets", http.StatusForbidden)
			return
		}
		target, err := net.DialTimeout("tcp4", r.Host, time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", http.StatusBadGateway)
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
		if r.Host == healthTarget {
			if health != nil {
				health.Add(1)
			}
		} else {
			business.Add(1)
		}
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

func quickStartSSFixture(t *testing.T, artifact, password string) int {
	t.Helper()
	dir := t.TempDir()
	port, release := realProxyPort(t)
	config := map[string]any{
		"mode": "rule", "log-level": "info", "geo-auto-update": false, "find-process-mode": "off",
		"listeners": []map[string]any{{"name": "isolated-ss", "type": "shadowsocks", "listen": "127.0.0.1", "port": port, "cipher": "aes-128-gcm", "password": password}},
		"rules":     []string{"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve", "MATCH,REJECT"},
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(dir, "output.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(artifact, "-d", dir, "-f", path)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, log, log
	cmd.Env = []string{"HOME=" + dir, "TMPDIR=" + dir, "XDG_CONFIG_HOME=" + dir, "XDG_CACHE_HOME=" + dir, "XDG_DATA_HOME=" + dir, "PATH=/nonexistent", "LANG=C", "LC_ALL=C", "TZ=UTC"}
	release()
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("临时 SS 子进程无法确认退出")
			}
		}
		log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(filepath.Join(dir, "output.log"))
			t.Logf("仅本次临时 SS 核心日志: %s", data)
		}
	})
	quickStartAwait(t, "SS 核心监听归属", func() bool {
		p, err := (proxy.OSRunner{}).Inspect(cmd.Process.Pid)
		if err != nil {
			return false
		}
		owned, err := (proxy.OSRunner{}).OwnsEndpoint(p, "http://127.0.0.1:"+strconv.Itoa(port))
		return err == nil && owned
	})
	return port
}
