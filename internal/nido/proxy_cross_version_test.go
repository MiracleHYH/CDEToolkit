package nido

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"go.yaml.in/yaml/v3"
)

// Both inputs must be independently verified official native cores. Neither
// core is downloaded by this test. Linux entry points expose the second input
// only on explicit opt-in, in the same private namespace as the existing E2E.
// The function and NIDO_TEST_MIHOMO_UPDATE name remain entry-point compatible;
// this tests cross-version remove/first-install compatibility, not /upgrade.
// Configs are injected through Service.Configure; the interactive config flow
// and external-tool proxy configuration are outside this fixture's coverage.
func TestProxyRealMihomoCrossVersion(t *testing.T) {
	next := os.Getenv("NIDO_TEST_MIHOMO_UPDATE")
	if next == "" {
		t.Skip("设置 NIDO_TEST_MIHOMO_UPDATE 为已核验的较新官方核心，才执行跨版本卸载重装兼容性测试；不验证原生更新")
	}
	previous := os.Getenv("NIDO_TEST_MIHOMO")
	if !filepath.IsAbs(previous) || !filepath.IsAbs(next) {
		t.Fatal("跨版本测试需要两个已核验核心的绝对路径")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Fatal("真实跨版本测试仅支持 Linux/Darwin")
	}
	oldBytes, err := readFile(previous, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	newBytes, err := readFile(next, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	oldHash, newHash := sha256.Sum256(oldBytes), sha256.Sum256(newBytes)
	if oldHash == newHash {
		t.Fatal("跨版本测试不能使用相同程序字节")
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
		t.Fatalf("显式跨版本测试需要内核进程身份读取权限: %v", err)
	}
	cli := newRealProxyCLI(t, reports)
	cli.must("init", "--json")
	cli.must("proxy", "install", "--artifact", previous, "--sha256", hex.EncodeToString(oldHash[:]), "--yes", "--json")
	oldVersion := realInstalledMihomoVersion(t, cli)

	var origins, tunnels, downloads atomic.Int64
	origin := httptest.NewServer(realOrigin(&origins))
	t.Cleanup(origin.Close)
	tlsOrigin := httptest.NewTLSServer(realOrigin(&origins))
	t.Cleanup(tlsOrigin.Close)
	upstream := realConnectProxy(t, &tunnels, origin.Listener.Addr().String(), tlsOrigin.Listener.Addr().String())
	_, upstreamPort, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	providerBytes := []byte(fmt.Sprintf("proxies:\n  - name: e2e-upstream\n    type: http\n    server: 127.0.0.1\n    port: %s\n", upstreamPort))
	var disabled atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		if disabled.Load() || r.URL.Path != "/provider.yaml" {
			http.Error(w, "provider disabled", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(providerBytes)
	}))
	t.Cleanup(provider.Close)
	// Stop the private core before taking down its provider and targets.
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
	realYAMLField(realYAMLField(root, "listeners").Content[0], "port").Value = strconv.Itoa(port)
	cli.configure(realYAMLBytes(t, &document))
	release()
	cli.must("proxy", "start", "--yes", "--json")
	endpoint := cli.endpoint("endpoint-proxy")
	cachePath := filepath.Join(cli.root, "proxy", "providers", "subscription.yaml")
	realAwaitProviderCache(t, cachePath, &downloads)
	realProxyTraffic(t, endpoint.URL(), endpoint.URL(), origin, tlsOrigin, &origins, &tunnels)
	oldHTTPBirth := cli.assertRunning()
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertGone(t, oldHTTPBirth)
	realAssertClosed(t, endpoint)
	cache, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}

	// The same configuration is validated and run on both versions. Only its
	// provider source changes before removal/reinstallation, using the old cache.
	disabled.Store(true)
	downloadsBefore := downloads.Load()
	providerNode := realYAMLField(realYAMLField(root, "proxy-providers"), "subscription")
	realYAMLField(providerNode, "type").Value = "file"
	realYAMLDrop(providerNode, "url", "interval")
	config := realYAMLBytes(t, &document)
	cli.configure(config)
	cli.must("proxy", "start", "--yes", "--json")
	realProxyTraffic(t, endpoint.URL(), endpoint.URL(), origin, tlsOrigin, &origins, &tunnels)
	oldBirth := cli.assertRunning()
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertGone(t, oldBirth)
	realAssertClosed(t, endpoint)
	unknown := filepath.Join(cli.root, "proxy", "unknown-data", "keep.bin")
	if err := os.MkdirAll(filepath.Dir(unknown), 0700); err != nil {
		t.Fatal(err)
	}
	unknownBytes := []byte("cross-version unknown data\n")
	if err := os.WriteFile(unknown, unknownBytes, 0600); err != nil {
		t.Fatal(err)
	}
	retained := cli.rejectArtifactUpdate(next, hex.EncodeToString(newHash[:]))
	cli.must("proxy", "remove", "--yes", "--json")
	if _, err := os.Lstat(filepath.Join(cli.root, "proxy", "mihomo")); !os.IsNotExist(err) {
		t.Fatalf("重装前 remove 未移除旧程序: %v", err)
	}
	realAssertRetainedBytes(t, retained)
	realAssertRetainedBytes(t, cli.sentinels)
	cli.must("proxy", "install", "--artifact", next, "--sha256", hex.EncodeToString(newHash[:]), "--yes", "--json")
	installed, err := readFile(filepath.Join(cli.root, "proxy", "mihomo"), 256<<20)
	if err != nil || sha256.Sum256(installed) != newHash {
		t.Fatalf("跨版本卸载后首次 install 未安装目标程序字节: %v", err)
	}
	newVersion := realInstalledMihomoVersion(t, cli)
	if !realMihomoVersionBefore(oldVersion, newVersion) {
		t.Fatalf("跨版本测试要求不同且升序的稳定版本: %s -> %s", oldVersion, newVersion)
	}
	realAssertRetainedBytes(t, retained)
	realAssertRetainedBytes(t, cli.sentinels)
	var stopped proxy.Status
	if err := json.Unmarshal(cli.must("proxy", "status", "--json"), &stopped); err != nil || stopped.Runtime.State != "stopped" || stopped.Runtime.Listeners || stopped.Runtime.ConfigChanged || len(stopped.Problems) != 0 {
		t.Fatalf("跨版本卸载重装未保持停止及原配置状态: %+v %v", stopped, err)
	}
	realAssertGone(t, oldBirth)
	realAssertClosed(t, endpoint)
	cli.must("proxy", "validate", "--json")
	cli.must("proxy", "start", "--yes", "--json")
	newBirth := cli.assertRunning()
	if realSameBirth(oldBirth, newBirth) {
		t.Fatal("跨版本启动复用了旧核心出生身份")
	}
	realProxyTraffic(t, endpoint.URL(), endpoint.URL(), origin, tlsOrigin, &origins, &tunnels)
	cli.must("proxy", "start", "--yes", "--json")
	if same := cli.assertRunning(); same != newBirth {
		t.Fatal("新版重复 start 替换了存活出生身份")
	}
	cli.must("proxy", "restart", "--yes", "--json")
	restarted := cli.assertRunning()
	if realSameBirth(newBirth, restarted) {
		t.Fatal("新版 restart 未更换出生身份")
	}
	realAssertGone(t, newBirth)
	realProxyTraffic(t, endpoint.URL(), endpoint.URL(), origin, tlsOrigin, &origins, &tunnels)
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertGone(t, restarted)
	cli.must("proxy", "stop", "--yes", "--json")
	realAssertClosed(t, endpoint)
	if downloads.Load() != downloadsBefore {
		t.Fatalf("file provider 在跨版本复用中仍联网: %d -> %d", downloadsBefore, downloads.Load())
	}
	realAssertRetainedBytes(t, map[string][]byte{filepath.Join(cli.root, "proxy", "config.yaml"): config, cachePath: cache, unknown: unknownBytes})
	retained = realProxyFiles(t, filepath.Join(cli.root, "proxy"))
	cli.must("proxy", "remove", "--yes", "--json")
	if _, err := os.Lstat(filepath.Join(cli.root, "proxy", "mihomo")); !os.IsNotExist(err) {
		t.Fatalf("跨版本测试 remove 未仅移除程序: %v", err)
	}
	realAssertRetainedBytes(t, retained)
	realAssertRetainedBytes(t, cli.sentinels)
	t.Logf("真实跨版本卸载后首次重装兼容性: %s -> %s；旧出生=%+v，新出生=%+v；provider HTTP=%d，origin=%d，CONNECT=%d；配置、旧 provider 缓存与既有数据字节保留；未验证原生更新", oldVersion, newVersion, oldBirth, newBirth, downloads.Load(), origins.Load(), tunnels.Load())
}

var realStableMihomoVersion = regexp.MustCompile(`(?m)^Mihomo Meta (v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))(?:\s|$)`)

func realInstalledMihomoVersion(t *testing.T, cli *realProxyCLI) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(cli.root, "proxy", "mihomo"), "-v")
	cmd.Dir, cmd.Env = filepath.Dir(cli.root), cli.env
	output, err := cmd.CombinedOutput()
	match := realStableMihomoVersion.FindSubmatch(output)
	if err != nil || len(output) > 8192 || len(match) != 2 {
		t.Fatalf("已安装核心未报告有效稳定版本: %v", err)
	}
	return string(match[1])
}

func realMihomoVersionBefore(old, next string) bool {
	x, y := strings.Split(strings.TrimPrefix(old, "v"), "."), strings.Split(strings.TrimPrefix(next, "v"), ".")
	for i := range x {
		if len(x[i]) != len(y[i]) {
			return len(x[i]) < len(y[i])
		}
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

func realAssertRetainedBytes(t *testing.T, files map[string][]byte) {
	t.Helper()
	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("操作改变既有文件 %s: %v", path, err)
		}
	}
}

func TestRealCrossVersionIdentityRejectsFalseUpgrade(t *testing.T) {
	for _, version := range []string{"v1.19.32-", "v1.19.32+", "v1.19.32/other", "v01.19.32"} {
		if realStableMihomoVersion.MatchString("Mihomo Meta " + version + " linux arm64\n") {
			t.Fatalf("跨版本夹具截断或接受了非法稳定版本: %s", version)
		}
	}
	for _, pair := range [][2]string{{"v1.19.32", "v1.19.32"}, {"v1.19.32", "v1.19.31"}, {"v1.20.0", "v1.9.99"}} {
		if realMihomoVersionBefore(pair[0], pair[1]) {
			t.Fatalf("相同版本或降序版本被当成升序重装候选: %v", pair)
		}
	}
	if !realMihomoVersionBefore("v1.19.31", "v1.19.32") || !realMihomoVersionBefore("v1.9.99", "v1.20.0") {
		t.Fatal("有效升序版本重装候选被拒绝")
	}
}
