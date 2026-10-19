package nido

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type proxyCanceledDriver struct {
	proxyPageCapture
	ctx context.Context
}

func (u *proxyCanceledDriver) Context() context.Context { return u.ctx }

func TestProxyCLIInstallAndMetadataUseTheCurrentTaskContext(t *testing.T) {
	for _, operation := range []string{"install", "check-update"} {
		t.Run(operation, func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			runner := &cliProxyRunner{}
			m.Proxy.Runner = runner
			artifact := proxyCLIArtifact(t)
			if operation == "check-update" {
				if err := m.Proxy.Install(artifact, ""); err != nil {
					t.Fatal(err)
				}
			}
			commands := len(runner.commands)
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			ui := &proxyCanceledDriver{ctx: ctx}
			var output bytes.Buffer
			cli := &CLI{UI: ui, Out: &output, Err: &output}
			args, options := []string{"install"}, cliOptions{artifact: artifact, yes: true}
			if operation == "check-update" {
				args, options = []string{"update"}, cliOptions{check: true, githubProxy: "direct"}
			}
			if code := cli.proxy(m, options, args); code == 0 || !strings.Contains(output.String(), "canceled") {
				t.Fatal("CLI discarded the canceled foreground context", code, output.String())
			}
			if len(runner.commands) != commands || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
				t.Fatal("canceled operation executed a core or changed managed data")
			}
		})
	}
}

type proxyManagementTraffic struct {
	sync.Mutex
	requests []string
}

func proxyManagementFixture(t *testing.T, handler http.HandlerFunc) (*Manager, *cliProxyRunner, *proxyManagementTraffic) {
	t.Helper()
	traffic := &proxyManagementTraffic{}
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer isolated-controller-secret" {
			t.Error("controller request lacked the selected private secret")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		traffic.Lock()
		traffic.requests = append(traffic.requests, r.Method+" "+r.URL.RequestURI())
		traffic.Unlock()
		handler(w, r)
	}))
	t.Cleanup(controller.Close)
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{ready: true}
	if code, _, stderr := proxyCLI(t, m, runner, "", "proxy", "install", "--artifact", proxyCLIArtifact(t), "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	m.Proxy.Runner = runner
	config := fmt.Sprintf("listeners: [{name: daily, type: mixed, listen: 127.0.0.1, port: 17890}]\nexternal-controller: %s\nsecret: isolated-controller-secret\nrules: [MATCH,DIRECT]\n", strings.TrimPrefix(controller.URL, "http://"))
	if err := m.Proxy.ConfigureBytes([]byte(config)); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := proxyCLI(t, m, runner, "", "proxy", "start", "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	return m, runner, traffic
}

func proxyManagementGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/version":
		_, _ = fmt.Fprint(w, `{"meta":true,"version":"v1.19.32"}`)
	case "/proxies":
		_, _ = fmt.Fprint(w, `{"proxies":{"proxy":{"type":"URLTest","now":"isp","all":["isp"]},"isp":{"type":"Selector","now":"leaf","all":["leaf"]},"leaf":{"type":"Shadowsocks","history":[{"time":"2026-10-19T00:00:00Z","delay":81}]}}}`)
	case "/providers/proxies":
		_, _ = fmt.Fprint(w, `{"providers":{"subscription":{"name":"subscription","type":"Proxy","vehicleType":"HTTP","proxies":[]},"COMPATIBLE":{"type":"Proxy","vehicleType":"Compatible","proxies":[]}}}`)
	case "/providers/rules":
		_, _ = fmt.Fprint(w, `{"providers":{"rules":{"name":"rules","type":"Rule","vehicleType":"HTTP","ruleCount":0}}}`)
	case "/configs":
		_, _ = fmt.Fprint(w, `{"mixed-port":0,"port":0,"socks-port":0,"mode":"rule","allow-lan":false}`)
	default:
		return false
	}
	return true
}

func TestProxyPageReadsOnlyCoreStateAndExistingGroupHistory(t *testing.T) {
	m, runner, traffic := proxyManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if !proxyManagementGet(w, r) {
			t.Errorf("page issued a mutation, probe, or unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	commands := len(runner.commands)
	cli := &CLI{}
	text := cli.proxyPageStatus(m)
	for _, want := range []string{"v1.19.32", "proxy -> isp -> leaf", "81 ms", "2026-10-19", "非网络追踪", `订阅 proxy/"subscription"`, `订阅 rule/"rules"`, "未报告"} {
		if !strings.Contains(text, want) {
			t.Fatalf("state omitted %q: %s", want, text)
		}
	}
	if strings.Contains(text, "isolated-controller-secret") || len(runner.commands) != commands || runner.starts != 1 || runner.stops != 0 {
		t.Fatal("state exposed a secret, executed a core, or changed lifecycle", text)
	}
	traffic.Lock()
	defer traffic.Unlock()
	for _, request := range traffic.requests {
		if !strings.HasPrefix(request, "GET ") || strings.Contains(request, "delay") || strings.Contains(request, "healthcheck") {
			t.Fatal("state started active measurement or mutation", request)
		}
	}
}

func TestProxyNativeUpdateDoesNotUseNidoDownloadsOrStopFallback(t *testing.T) {
	posts := 0
	m, runner, _ := proxyManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if proxyManagementGet(w, r) {
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/upgrade" || r.URL.Query().Get("channel") != "release" || r.URL.Query().Has("force") {
			t.Errorf("unexpected native upgrade request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		posts++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, `{"message":"https://private.invalid/?token=NEVER-DISPLAY"}`)
	})
	commands := len(runner.commands)
	program := filepath.Join(m.Root, "proxy", "mihomo")
	before, _ := os.ReadFile(program)
	code, _, stderr := proxyCLI(t, m, runner, "", "proxy", "update", "--yes", "--json")
	wantPosts := 1
	if runtime.GOOS != "linux" {
		wantPosts = 0
		if !strings.Contains(stderr, "Linux") {
			t.Fatal("unsupported native update platform was not reported", stderr)
		}
	}
	if code == 0 || posts != wantPosts || strings.Contains(stderr, "NEVER-DISPLAY") || strings.Contains(stderr, "请选择网络模式") {
		t.Fatalf("native upgrade routed/retried incorrectly: %d posts=%d %s", code, posts, stderr)
	}
	if len(runner.commands) != commands || runner.starts != 1 || runner.stops != 0 {
		t.Fatal("native upgrade failure executed candidate checks, stopped, or restarted")
	}
	if after, _ := os.ReadFile(program); !bytes.Equal(before, after) {
		t.Fatal("native upgrade failure invoked a Nido replacement fallback")
	}
	for _, extra := range [][]string{{"--artifact", proxyCLIArtifact(t)}, {"--proxy", "http://127.0.0.1:19999"}, {"--github-proxy", "direct"}} {
		args := append([]string{"proxy", "update", "--yes"}, extra...)
		if code, _, _ := proxyCLI(t, m, runner, "", args...); code == 0 || posts != wantPosts {
			t.Fatal("native update accepted downloader flags or contacted controller", extra, code, posts)
		}
	}
}

func TestProxyNativeProviderAllReportsEachResultAndContinues(t *testing.T) {
	puts := map[string]int{}
	m, runner, _ := proxyManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if proxyManagementGet(w, r) {
			return
		}
		if r.Method != http.MethodPut {
			t.Errorf("unexpected provider method: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		puts[r.URL.Path]++
		if r.URL.Path == "/providers/proxies/subscription" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"message":"NEVER-DISPLAY provider private data"}`)
		} else if r.URL.Path == "/providers/rules/rules" {
			w.WriteHeader(http.StatusNoContent)
		} else {
			t.Errorf("unexpected provider path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	commands := len(runner.commands)
	code, out, stderr := proxyCLI(t, m, runner, "", "proxy", "provider-update", "all", "--yes", "--json")
	var results []proxyProviderResult
	if code == 0 || json.Unmarshal([]byte(out), &results) != nil || len(results) != 2 || strings.Contains(out+stderr, "NEVER-DISPLAY") {
		t.Fatalf("provider batch omitted partial results: %d %s %s", code, out, stderr)
	}
	states := map[string]string{}
	for _, result := range results {
		states[result.Kind] = result.State
	}
	if states["proxy"] != "failed" || states["rule"] != "updated" || puts["/providers/proxies/subscription"] != 1 || puts["/providers/rules/rules"] != 1 {
		t.Fatal("provider failure blocked other kind or retried", states, puts)
	}
	if len(runner.commands) != commands || runner.starts != 1 || runner.stops != 0 {
		t.Fatal("provider refresh changed lifecycle or executed core")
	}
}

func TestProxyNativeProviderExplicitMissingCannotReportEmptySuccess(t *testing.T) {
	m, runner, traffic := proxyManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && (r.URL.Path == "/providers/proxies" || r.URL.Path == "/providers/rules") {
			_, _ = fmt.Fprint(w, `{"providers":{"internal":{"vehicleType":"Compatible"}}}`)
			return
		}
		if !proxyManagementGet(w, r) {
			t.Error("missing provider caused a mutation", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	code, _, stderr := proxyCLI(t, m, runner, "", "proxy", "provider-update", "proxy", "missing", "--yes", "--json")
	if code == 0 || !strings.Contains(stderr, "未找到") {
		t.Fatal("explicit missing provider was reported as success", code, stderr)
	}
	if code, out, stderr := proxyCLI(t, m, runner, "", "proxy", "provider-update", "all", "--yes", "--json"); code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatal("empty all-provider selection did not report an empty list", code, out, stderr)
	}
	traffic.Lock()
	defer traffic.Unlock()
	for _, request := range traffic.requests {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatal("empty provider list caused a mutation", request)
		}
	}
}

func TestProxyNativeReloadUsesFixedFileAndRefreshesRecordWithoutCoreCheck(t *testing.T) {
	puts := 0
	var m *Manager
	m, runner, _ := proxyManagementFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if proxyManagementGet(w, r) {
			return
		}
		var body struct{ Path, Payload string }
		if r.Method != http.MethodPut || r.URL.Path != "/configs" || r.URL.Query().Get("force") != "true" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Path != filepath.Join(m.Root, "proxy", "config.yaml") || body.Payload != "" {
			t.Errorf("reload did not use the one managed path: %s %s %+v", r.Method, r.URL, body)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		puts++
		w.WriteHeader(http.StatusNoContent)
	})
	configPath := filepath.Join(m.Root, "proxy", "config.yaml")
	config, _ := os.ReadFile(configPath)
	config = append(config, []byte("# explicitly edited before reload\n")...)
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	commands := len(runner.commands)
	code, _, stderr := proxyCLI(t, m, runner, "", "proxy", "reload", "--yes", "--json")
	if code != 0 || puts != 1 || m.Proxy.Status().ConfigChanged {
		t.Fatalf("reload did not converge its applied record: %d puts=%d %s", code, puts, stderr)
	}
	if len(runner.commands) != commands || runner.starts != 1 || runner.stops != 0 {
		t.Fatal("reload executed standalone -t or controlled lifecycle")
	}
	if after, _ := os.ReadFile(configPath); !bytes.Equal(config, after) {
		t.Fatal("reload wrote or restored user YAML")
	}
}
