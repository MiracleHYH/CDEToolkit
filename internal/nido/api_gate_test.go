package nido

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/components/gds"
	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
)

type apiGateReader struct{ reads int }

func (r *apiGateReader) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

type apiGateProxyRunner struct {
	cliProxyRunner
	inspects, ownership int
}

func (r *apiGateProxyRunner) Inspect(int) (proxy.Process, error) {
	r.inspects++
	return proxy.Process{}, os.ErrNotExist
}

func (r *apiGateProxyRunner) OwnsEndpoint(proxy.Process, string) (bool, error) {
	r.ownership++
	return false, nil
}

type apiGateGDSRunner struct{ runs, starts, inspects, stops int }

func (r *apiGateGDSRunner) Run(gds.Command) ([]byte, error) {
	r.runs++
	return nil, fmt.Errorf("disabled API must not execute GDS")
}

func (r *apiGateGDSRunner) Start(gds.Command, func(gdsapi.Process) error) (gdsapi.Process, <-chan error, error) {
	r.starts++
	return gdsapi.Process{}, nil, fmt.Errorf("disabled API must not start GDS")
}

func (r *apiGateGDSRunner) Inspect(int) (gdsapi.Process, error) {
	r.inspects++
	return gdsapi.Process{}, os.ErrNotExist
}

func (r *apiGateGDSRunner) Stop(gdsapi.Process) error {
	r.stops++
	return fmt.Errorf("disabled API must not stop GDS")
}

func apiGateFixture(t *testing.T, emptyBinding bool, binary []byte) (*Manager, string) {
	t.Helper()
	m, home := fixture(t)
	if err := m.InitWithDownload("https://saved-file-only.invalid"); err != nil {
		t.Fatal(err)
	}
	if emptyBinding {
		if err := os.WriteFile(filepath.Join(m.Root, apiProxyFile), []byte("schema=1\nproduct='nido-api-proxy'\nlistener=''\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for relative, data := range map[string]string{
		".gitconfig":           "[user]\n name = Original Fixture User\n",
		".condarc":             "ssl_verify: /original/cert.pem\nchannels: [original]\n",
		".config/pip/pip.conf": "[global]\nindex-url = https://original.invalid/simple\n",
		"codex/.env":           "HTTPS_PROXY=http://original.invalid:7890\n",
		"codex/auth.json":      "ORIGINAL AUTH DATA\n", "codex/state.sqlite": "ORIGINAL STATE\n",
		"codex/sessions/session.jsonl": "ORIGINAL SESSION\n",
	} {
		path := filepath.Join(home, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(m.Root, "proxy"), 0700); err != nil {
		t.Fatal(err)
	}
	writeBindingListeners(t, m, bindingListeners)
	m.Config.Codex.Home = filepath.Join(home, "codex")
	m.GDS.Platform = "linux/amd64"
	program := filepath.Join(m.Root, "bin", "nido")
	if err := os.WriteFile(program, binary, 0755); err != nil {
		t.Fatal(err)
	}
	m.Executable = program
	var err error
	m.executableInfo, err = os.Stat(program)
	if err != nil {
		t.Fatal(err)
	}
	return m, home
}

func TestAPIGateDisabledBlocksEveryOnlineEntryPoint(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("managed self-update identity fixture targets isolated Linux amd64")
	}
	// This builds format-valid managed identities without executing a candidate.
	// All component runners and network paths below are counted private fakes.
	binary := updateCLIBinaries(t)["0.5.0"]
	previousVersion := Version
	Version = "0.5.0"
	t.Cleanup(func() { Version = previousVersion })
	for _, mode := range []string{"missing-binding", "empty-listener"} {
		t.Run(mode, func(t *testing.T) {
			m, home := apiGateFixture(t, mode == "empty-listener", binary)
			var httpCalls, dialCalls atomic.Int32
			client := &http.Client{Transport: updateCLITransport(func(*http.Request) (*http.Response, error) {
				httpCalls.Add(1)
				return nil, fmt.Errorf("disabled API must not issue HTTP requests")
			})}
			transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: func(context.Context, string, string) (net.Conn, error) {
				dialCalls.Add(1)
				return nil, fmt.Errorf("disabled API must not connect to a network")
			}}
			previousTransport := http.DefaultTransport
			http.DefaultTransport = transport
			t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
			pristineEnv := append([]string(nil), m.Env...)
			for _, attempt := range []struct {
				name, explicitProxy, githubProxy string
				environmentProxy                 bool
				environmentGitHub                bool
			}{
				{name: "saved-file-prefix"},
				{name: "environment-network-proxy", environmentProxy: true},
				{name: "explicit-network-proxy", explicitProxy: "http://127.0.0.1:1"},
				{name: "explicit-github-direct", githubProxy: "direct"},
				{name: "explicit-file-prefix", githubProxy: "https://explicit-file-only.invalid"},
				{name: "environment-github-prefix", environmentGitHub: true},
				{name: "combined-overrides", explicitProxy: "socks5h://127.0.0.1:1", githubProxy: "https://explicit-file-only.invalid", environmentProxy: true, environmentGitHub: true},
			} {
				t.Run(attempt.name, func(t *testing.T) {
					m.Env = append([]string(nil), pristineEnv...)
					if attempt.environmentGitHub {
						m.Env = append(m.Env, "NIDO_GITHUB_PROXY=https://environment-file-only.invalid")
					}
					if attempt.environmentProxy {
						for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
							t.Setenv(key, "http://127.0.0.1:1")
							m.Env = append(m.Env, key+"=http://127.0.0.1:1")
						}
					}
					if got, err := m.downloadAPIClient(attempt.explicitProxy); got != nil || !errors.Is(err, ErrAPIDisabled) {
						t.Fatalf("override enabled disabled API client: client=%v err=%v", got, err)
					}
					for _, operation := range []struct {
						name, component string
						args            []string
						options         cliOptions
					}{
						{name: "nido-check", component: "nido", options: cliOptions{check: true}},
						{name: "nido-update", component: "nido"},
						{name: "nido-pinned-update", component: "nido", options: cliOptions{version: "0.6.0"}},
						{name: "proxy-install", component: "proxy", args: []string{"install"}},
						{name: "proxy-install-artifact-latest", component: "proxy", args: []string{"install"}, options: cliOptions{artifact: "latest"}},
						{name: "proxy-update", component: "proxy", args: []string{"update"}},
						{name: "proxy-update-artifact-latest", component: "proxy", args: []string{"update"}, options: cliOptions{artifact: "latest"}},
						{name: "proxy-check", component: "proxy", args: []string{"update"}, options: cliOptions{check: true}},
						{name: "gds-install", component: "gds", args: []string{"install"}},
						{name: "gds-install-latest", component: "gds", args: []string{"install", "latest"}},
						{name: "gds-install-pinned", component: "gds", args: []string{"install", "0.1.0"}},
						{name: "gds-update", component: "gds", args: []string{"update"}},
						{name: "gds-update-latest", component: "gds", args: []string{"update", "latest"}},
						{name: "gds-update-pinned", component: "gds", args: []string{"update", "gds-v0.1.0"}},
						{name: "gds-check", component: "gds", args: []string{"update"}, options: cliOptions{check: true}},
						{name: "codex-check", component: "codex", args: []string{"update"}, options: cliOptions{check: true}},
					} {
						t.Run(operation.name, func(t *testing.T) {
							proxyRunner := &apiGateProxyRunner{}
							gdsRunner := &apiGateGDSRunner{}
							var codexRuns int
							m.Proxy.Runner, m.Proxy.HTTP = proxyRunner, client
							m.GDS.Runner, m.GDS.HTTP = gdsRunner, client
							m.Codex.Run = func(string, []string, []string, []byte) ([]byte, error) {
								codexRuns++
								return nil, fmt.Errorf("disabled API must not execute Codex or curl")
							}
							reader := &apiGateReader{}
							var out, stderr bytes.Buffer
							cli := CLI{In: reader, Out: &out, Err: &stderr, UpdateHTTP: client}
							options := operation.options
							options.proxy, options.githubProxy, options.json = attempt.explicitProxy, attempt.githubProxy, true
							if operation.component == "codex" {
								// --github-proxy does not apply to Codex commands;
								// environment overrides still remain in this case.
								options.githubProxy = ""
							}
							before := proxyBootstrapSnapshot(t, "", m.Root, home)
							code := 0
							switch operation.component {
							case "nido":
								code = cli.update(m, options, nil)
							case "proxy":
								code = cli.proxy(m, options, operation.args)
							case "gds":
								code = cli.gds(m, options, operation.args)
							case "codex":
								code = cli.codex(m, options, operation.args)
							}
							if code == 0 || !strings.Contains(stderr.String(), ErrAPIDisabled.Error()) {
								t.Fatalf("online entry point bypassed API gate: code=%d stdout=%s stderr=%s", code, out.String(), stderr.String())
							}
							if httpCalls.Load() != 0 || dialCalls.Load() != 0 || reader.reads != 0 || len(proxyRunner.commands) != 0 || proxyRunner.starts != 0 || proxyRunner.stops != 0 || proxyRunner.inspects != 0 || proxyRunner.ownership != 0 || *gdsRunner != (apiGateGDSRunner{}) || codexRuns != 0 {
								t.Fatalf("disabled API attempted HTTP, confirmation or execution: HTTP=%d dials=%d reads=%d proxy=%+v gds=%+v codex=%d", httpCalls.Load(), dialCalls.Load(), reader.reads, proxyRunner, gdsRunner, codexRuns)
							}
							if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
								t.Fatal("disabled API changed a program, binding, configuration or user data")
							}
						})
					}
				})
			}
		})
	}
}

func TestAPIGateReadOnlyDisabledAndOffState(t *testing.T) {
	t.Setenv("NIDO_GITHUB_PROXY", "direct")
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	var httpCalls, dialCalls atomic.Int32
	client := &http.Client{Transport: updateCLITransport(func(*http.Request) (*http.Response, error) {
		httpCalls.Add(1)
		return nil, fmt.Errorf("local API state must not issue HTTP requests")
	})}
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dialCalls.Add(1)
		return nil, fmt.Errorf("local API state must not dial a network")
	}}
	previousTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
	proxyRunner, gdsRunner := &apiGateProxyRunner{}, &apiGateGDSRunner{}
	var codexRuns int
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, stderr bytes.Buffer
		cli := CLI{Out: &out, Err: &stderr, ProxyRunner: proxyRunner, GDSRunner: gdsRunner, UpdateHTTP: client, GDSHTTP: client,
			Runner: func(string, []string, []string, []byte) ([]byte, error) {
				codexRuns++
				return nil, fmt.Errorf("local API state must not execute Codex or curl")
			}}
		code := cli.Run(append([]string{"--root", m.Root}, args...))
		return code, out.String(), stderr.String()
	}
	show := func(wantState string) {
		t.Helper()
		before := proxyBootstrapSnapshot(t, "", m.Root, home)
		code, out, stderr := run("download", "show", "--json")
		var state map[string]string
		if code != 0 || json.Unmarshal([]byte(out), &state) != nil || state["api_state"] != wantState {
			t.Fatalf("local API state: want=%s code=%d stdout=%s stderr=%s", wantState, code, out, stderr)
		}
		if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
			t.Fatal("download show changed local files")
		}
	}
	checkDisabledReadOnly := func() {
		t.Helper()
		// This fixture has no linked external tool or installed component. Its
		// owned environment, download preference and static component status
		// must be healthy before asserting that disabled API is not a fault.
		if err := m.checkEnv(); err != nil {
			t.Fatalf("doctor fixture has an unrelated environment issue: %v", err)
		}
		if _, err := m.githubPolicy(""); err != nil {
			t.Fatalf("doctor fixture has an unrelated download issue: %v", err)
		}
		for _, row := range m.Status() {
			if row.Detail != "" {
				t.Fatalf("doctor fixture has an unrelated %s issue: %s", row.Name, row.Detail)
			}
		}
		show("disabled")
		for _, command := range []string{"status", "doctor"} {
			before := proxyBootstrapSnapshot(t, "", m.Root, home)
			code, out, stderr := run(command, "--json")
			if !json.Valid([]byte(out)) || code != 0 || strings.Contains(out+stderr, ErrAPIDisabled.Error()) {
				t.Fatalf("disabled API misreported by %s: code=%d stdout=%s stderr=%s", command, code, out, stderr)
			}
			if command == "doctor" {
				var report struct {
					Issues []string `json:"issues"`
				}
				if err := json.Unmarshal([]byte(out), &report); err != nil || len(report.Issues) != 0 {
					t.Fatalf("disabled API doctor has unexpected issues: %s %v", out, err)
				}
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
				t.Fatal("local status or doctor changed configuration/data")
			}
		}
	}
	checkDisabledReadOnly()
	if err := os.Mkdir(filepath.Join(m.Root, "proxy"), 0700); err != nil {
		t.Fatal(err)
	}
	writeBindingListeners(t, m, bindingListeners)
	if code, _, stderr := run("proxy", "bind", "download", "--endpoint", "arbitrary-daily", "--yes"); code != 0 {
		t.Fatal("offline listener selection failed", stderr)
	}
	show("configured")
	protected := proxyBootstrapSnapshot(t, filepath.Join(m.Root, apiProxyFile), m.Root, home)
	if code, _, stderr := run("proxy", "bind", "download", "--proxy", "off", "--yes"); code != 0 {
		t.Fatal("API off failed", stderr)
	}
	if !reflect.DeepEqual(protected, proxyBootstrapSnapshot(t, filepath.Join(m.Root, apiProxyFile), m.Root, home)) {
		t.Fatal("API off changed other configuration or data")
	}
	checkDisabledReadOnly()
	if httpCalls.Load() != 0 || dialCalls.Load() != 0 || codexRuns != 0 || len(proxyRunner.commands) != 0 || proxyRunner.starts != 0 || proxyRunner.stops != 0 || proxyRunner.inspects != 0 || proxyRunner.ownership != 0 || *gdsRunner != (apiGateGDSRunner{}) {
		t.Fatalf("local state/selection contacted API or ran a component: HTTP=%d dials=%d Codex=%d Proxy=%+v GDS=%+v", httpCalls.Load(), dialCalls.Load(), codexRuns, proxyRunner, gdsRunner)
	}
}

func TestAPIGateOneOffProxyRequiresNonemptyBinding(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(m.Root, "proxy"), 0700); err != nil {
		t.Fatal(err)
	}
	writeBindingListeners(t, m, bindingListeners)
	var dials atomic.Int32
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("constructing an API client must not dial a network")
	}}
	previousTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport; transport.CloseIdleConnections() })
	runner := &apiGateProxyRunner{}
	m.Proxy.Runner = runner
	const oneOff = "http://127.0.0.1:1"
	assertDisabled := func() {
		t.Helper()
		before := proxyBootstrapSnapshot(t, "", m.Root, home)
		if client, err := m.downloadAPIClient(oneOff); client != nil || !errors.Is(err, ErrAPIDisabled) {
			t.Fatalf("one-off proxy enabled an unbound API: client=%v err=%v", client, err)
		}
		if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
			t.Fatal("refused one-off proxy changed files")
		}
	}
	assertDisabled()
	endpoint, err := m.Proxy.Endpoint("arbitrary-daily")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.configureAPIProxy(endpoint.URL(), &endpoint); err != nil {
		t.Fatal(err)
	}
	before := proxyBootstrapSnapshot(t, "", m.Root, home)
	client, err := m.downloadAPIClient(oneOff)
	if err != nil || client == nil {
		t.Fatalf("configured API rejected a temporary override: client=%v err=%v", client, err)
	}
	client.CloseIdleConnections()
	if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
		t.Fatal("one-off proxy construction changed the saved binding or user data")
	}
	if err := m.configureAPIProxy("", nil); err != nil {
		t.Fatal(err)
	}
	assertDisabled()
	if dials.Load() != 0 || len(runner.commands) != 0 || runner.starts != 0 || runner.stops != 0 || runner.inspects != 0 || runner.ownership != 0 {
		t.Fatalf("client construction used network or a core: dials=%d runner=%+v", dials.Load(), runner)
	}
}
