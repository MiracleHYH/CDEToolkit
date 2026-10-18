package nido

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
)

const cliGPUUUID = "GPU-12345678-1234-1234-1234-123456789abc"

func TestGDSCLIReleaseSources(t *testing.T) {
	for _, test := range []struct {
		name, artifact, want string
		args                 []string
		invalid              bool
	}{
		{name: "default"},
		{name: "latest", args: []string{"latest"}},
		{name: "number", args: []string{"0.1.0"}, want: "gds-v0.1.0"},
		{name: "tag", args: []string{"gds-v0.1.0"}, want: "gds-v0.1.0"},
		{name: "local", artifact: "/tmp/gds-artifact", want: "/tmp/gds-artifact"},
		{name: "mixed", artifact: "/tmp/gds-artifact", args: []string{"0.1.0"}, invalid: true},
		{name: "relative artifact", artifact: "gds", invalid: true},
		{name: "artifact latest", artifact: "latest", invalid: true},
		{name: "position path", args: []string{"/tmp/gds-artifact"}, invalid: true},
		{name: "nido tag", args: []string{"v0.1.0"}, invalid: true},
		{name: "prerelease", args: []string{"gds-v0.1.0-rc1"}, invalid: true},
		{name: "leading zero", args: []string{"01.1.0"}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := gdsReleaseSource(test.args, test.artifact)
			if (err != nil) != test.invalid || err == nil && got != test.want {
				t.Fatalf("source=%q err=%v", got, err)
			}
		})
	}
}

type gdsCLITransport func(*http.Request) (*http.Response, error)

func (f gdsCLITransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func directGDSCLI(t *testing.T, m *Manager, o cliOptions, args ...string) (int, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	c := CLI{In: strings.NewReader(""), Out: &out, Err: &stderr}
	code := c.gds(m, o, args)
	return code, out.String(), stderr.String()
}

func TestGDSCLIOfficialPreviewAndCancellationAreOffline(t *testing.T) {
	for _, args := range [][]string{{"install"}, {"install", "latest"}, {"install", "0.1.0"}, {"install", "gds-v0.1.0"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			m, _ := fixture(t)
			m.GDS.Platform = "linux/amd64"
			requests := 0
			m.GDS.HTTP = &http.Client{Transport: gdsCLITransport(func(r *http.Request) (*http.Response, error) {
				requests++
				return nil, fmt.Errorf("unexpected network request: %s", r.URL)
			})}
			code, out, stderr := directGDSCLI(t, m, cliOptions{dry: true, json: true}, args...)
			if code != 0 || !json.Valid([]byte(out)) || requests != 0 {
				t.Fatalf("preview: code=%d out=%s err=%s requests=%d", code, out, stderr, requests)
			}
			if _, err := os.Lstat(m.Root); !os.IsNotExist(err) {
				t.Fatal("preview initialized root", err)
			}
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			code, _, stderr = directGDSCLI(t, m, cliOptions{json: true}, args...)
			if code == 0 || requests != 0 || !strings.Contains(stderr, "API 功能未启用") {
				t.Fatalf("disabled before confirmation: code=%d err=%s requests=%d", code, stderr, requests)
			}
			if _, err = os.Lstat(filepath.Join(m.Root, "gds")); !os.IsNotExist(err) {
				t.Fatal("cancel created component directory", err)
			}
			after, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("cancel changed Nido configuration", err)
			}
		})
	}
}

func TestGDSCLIInstallSelectsOfficialVersion(t *testing.T) {
	for _, test := range []struct {
		args []string
		path string
	}{
		{args: []string{"install"}, path: "/repos/MiracleHYH/CDEToolkit/releases?per_page=100&page=1"},
		{args: []string{"install", "latest"}, path: "/repos/MiracleHYH/CDEToolkit/releases?per_page=100&page=1"},
		{args: []string{"install", "0.1.0"}, path: "/repos/MiracleHYH/CDEToolkit/releases/tags/gds-v0.1.0"},
		{args: []string{"install", "gds-v0.1.0"}, path: "/repos/MiracleHYH/CDEToolkit/releases/tags/gds-v0.1.0"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			m.GDS.Platform = "linux/amd64"
			var requests []string
			m.GDS.HTTP = &http.Client{Transport: gdsCLITransport(func(r *http.Request) (*http.Response, error) {
				requests = append(requests, r.URL.RequestURI())
				return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not published")), Header: make(http.Header), Request: r}, nil
			})}
			before, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			// This test isolates release selection from the separately tested
			// automatic mirror retry policy.
			proxyAddress := enabledAPIFixture(t, m, m.GDS.HTTP)
			code, out, stderr := directGDSCLI(t, m, cliOptions{yes: true, json: true, githubProxy: "direct", proxy: proxyAddress}, test.args...)
			if code == 0 || out != "" || !json.Valid([]byte(stderr)) || !reflect.DeepEqual(requests, []string{test.path}) {
				t.Fatalf("code=%d out=%s err=%s requests=%v", code, out, stderr, requests)
			}
			if _, err = os.Lstat(filepath.Join(m.Root, "gds", "gds")); !os.IsNotExist(err) {
				t.Fatal("failed download installed a program", err)
			}
			after, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed download changed Nido configuration", err)
			}
		})
	}
}

func TestGDSCLICheckUpdateIsReadOnly(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	m.GDS.Platform = "linux/amd64"
	dir := filepath.Join(m.Root, "gds")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// An ELF header is intentionally sufficient for static identity checks, and
	// cannot execute as a real service if the query accidentally invokes it.
	program := make([]byte, 64)
	copy(program, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(program[16:], 2)
	binary.LittleEndian.PutUint16(program[18:], 62)
	binary.LittleEndian.PutUint32(program[20:], 1)
	binary.LittleEndian.PutUint16(program[52:], 64)
	binary.LittleEndian.PutUint16(program[54:], 56)
	binary.LittleEndian.PutUint16(program[58:], 64)
	receipt, err := json.Marshal(map[string]any{"schema": 1, "product": "gds", "protocol": 1, "platform": "linux/amd64", "version": "0.1.0", "sha256": fmt.Sprintf("%x", sha256.Sum256(program)), "release_tag": "gds-v0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string][]byte{
		filepath.Join(dir, "gds"):            program,
		filepath.Join(dir, "installed.json"): receipt,
		filepath.Join(dir, "config.toml"):    []byte("existing private configuration"),
		filepath.Join(dir, "state.json"):     []byte("existing runtime history"),
		filepath.Join(home, "auth.json"):     []byte("private sentinel"),
	}
	for path, data := range paths {
		mode := os.FileMode(0600)
		if path == filepath.Join(dir, "gds") {
			mode = 0700
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	requests := 0
	m.GDS.HTTP = &http.Client{Transport: gdsCLITransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.RequestURI() != "/repos/MiracleHYH/CDEToolkit/releases?per_page=100&page=1" {
			t.Fatalf("unexpected release query %s", r.URL)
		}
		body := `[{"tag_name":"v99.0.0"},{"tag_name":"gds-v0.1.0"},{"tag_name":"gds-v0.2.0"}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	proxyAddress := enabledAPIFixture(t, m, m.GDS.HTTP)
	code, out, stderr := directGDSCLI(t, m, cliOptions{check: true, json: true, proxy: proxyAddress}, "update")
	var result struct {
		Current, Latest string
		Available       bool
	}
	if code != 0 || json.Unmarshal([]byte(out), &result) != nil || result.Current != "0.1.0" || result.Latest != "gds-v0.2.0" || !result.Available || requests != 1 {
		t.Fatalf("code=%d out=%s err=%s requests=%d", code, out, stderr, requests)
	}
	code, out, stderr = directGDSCLI(t, m, cliOptions{check: true, dry: true, json: true}, "update")
	if code != 0 || !json.Valid([]byte(out)) || requests != 1 || !strings.Contains(out, `"operation":"check-update"`) {
		t.Fatalf("preview: code=%d out=%s err=%s requests=%d", code, out, stderr, requests)
	}
	for path, want := range paths {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("read-only query modified %s: %v", path, err)
		}
	}
	for _, name := range []string{".lock", "process.json", "invocation.json"} {
		if _, err = os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("read-only query created %s: %v", name, err)
		}
	}
}

func TestGDSCLIOfficialOptionValidation(t *testing.T) {
	m, _ := fixture(t)
	for _, args := range [][]string{
		{"gds", "install", "0.1.0", "0.2.0", "--json"},
		{"gds", "install", "0.1.0", "--artifact", "/tmp/gds", "--json"},
		{"gds", "install", "--artifact", "latest", "--json"},
		{"gds", "install", "/tmp/gds", "--json"},
		{"gds", "update", "--check", "0.1.0", "--json"},
		{"gds", "update", "--check", "--artifact", "/tmp/gds", "--json"},
		{"gds", "update", "--check", "--sha256", strings.Repeat("a", 64), "--json"},
		{"gds", "install", "gds-v01.0.0", "--json"},
		{"gds", "status", "--proxy", "http://127.0.0.1:8080", "--json"},
		{"gds", "install", "--proxy", "file:///tmp/proxy", "--json"},
	} {
		code, out, stderr := isolatedCLI(t, m, args, "", nil)
		if code == 0 || out != "" || !json.Valid([]byte(stderr)) {
			t.Fatalf("invalid options accepted %v: code=%d out=%s err=%s", args, code, out, stderr)
		}
		if _, err := os.Lstat(m.Root); !os.IsNotExist(err) {
			t.Fatal("invalid options initialized root", err)
		}
	}
}

func TestGDSCLIProxyIsLimitedToThisRequest(t *testing.T) {
	m, _ := fixture(t)
	m.GDS.Platform = "linux/amd64"
	global := http.DefaultTransport.(*http.Transport)
	before := reflect.ValueOf(global.Proxy).Pointer()
	code, out, stderr := directGDSCLI(t, m, cliOptions{dry: true, json: true, proxy: "http://127.0.0.1:18443"}, "install", "0.1.0")
	if code != 0 || !json.Valid([]byte(out)) {
		t.Fatalf("code=%d out=%s err=%s", code, out, stderr)
	}
	transport, ok := m.GDS.HTTP.Transport.(*http.Transport)
	if !ok || transport == global {
		t.Fatal("request mutated default transport")
	}
	request, _ := http.NewRequest("GET", "https://api.github.com/repos/MiracleHYH/CDEToolkit/releases", nil)
	proxy, err := transport.Proxy(request)
	if err != nil || proxy.String() != "http://127.0.0.1:18443" {
		t.Fatalf("proxy=%v err=%v", proxy, err)
	}
	if reflect.ValueOf(global.Proxy).Pointer() != before {
		t.Fatal("global proxy function changed")
	}
	if _, err = os.Lstat(m.Root); !os.IsNotExist(err) {
		t.Fatal("proxy preview wrote root", err)
	}
}

func TestGDSCLIReadOnlyAndPreviewDoNotInitialize(t *testing.T) {
	m, _ := fixture(t)
	for _, args := range [][]string{{"gds", "status", "--json"}, {"gds", "config-path", "--json"}, {"gds", "configure", "--gpu", cliGPUUUID, "--dry-run", "--json"}} {
		code, out, stderr := isolatedCLI(t, m, args, "", nil)
		if code != 0 || !json.Valid([]byte(out)) {
			t.Fatalf("%v: %d %s %s", args, code, out, stderr)
		}
		if _, err := os.Lstat(m.Root); !os.IsNotExist(err) {
			t.Fatal("read-only GDS command initialized software root", err)
		}
	}
	for _, args := range [][]string{
		{"gds", "install", "--artifact", filepath.Join(t.TempDir(), "missing"), "--dry-run", "--json"},
		{"gds", "start", "--dry-run", "--json"},
		{"gds", "configure", "--gpu", "0", "--dry-run", "--json"},
		{"gds", "status", "--artifact", "/tmp/ignored", "--json"},
		{"gds", "status", "--gpu", cliGPUUUID, "--json"},
		{"gds", "probe", "--url", "https://example.com", "--json"},
		{"proxy", "status", "--gpu", cliGPUUUID},
	} {
		code, _, stderr := isolatedCLI(t, m, args, "", nil)
		if code == 0 {
			t.Fatalf("invalid preview accepted: %v", args)
		}
		if args[len(args)-1] == "--json" && !json.Valid([]byte(stderr)) {
			t.Fatalf("invalid structured error: %s", stderr)
		}
	}
}

func TestGDSCLIConfigurationAndUnknownFilePreservation(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("private sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := isolatedCLI(t, m, []string{"gds", "configure", "--gpu", cliGPUUUID, "--yes", "--json"}, "", nil)
	if code != 0 {
		t.Fatalf("configure: %s %s", out, stderr)
	}
	configPath := filepath.Join(m.Root, "gds", "config.toml")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = gdsapi.ParseConfig(before); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(m.Root, "gds", "gds")
	if err = os.WriteFile(unknown, []byte("user notes"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"gds", "remove", "--yes", "--json"}, {"gds", "configure", "--gpu", cliGPUUUID, "--yes", "--json"}, {"gds", "configure", "--gpu", cliGPUUUID, "--dry-run", "--json"}} {
		code, _, _ := isolatedCLI(t, m, args, "", nil)
		if code == 0 {
			t.Fatalf("unsafe operation accepted: %v", args)
		}
	}
	if got, err := os.ReadFile(unknown); err != nil || string(got) != "user notes" {
		t.Fatal("unknown program file removed")
	}
	if got, err := os.ReadFile(configPath); err != nil || !bytes.Equal(got, before) {
		t.Fatal("configuration changed")
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "private sentinel" {
		t.Fatal("external Codex data changed")
	}
}
