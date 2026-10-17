package cdet

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func versionTestMetadata(m *Manager, fail bool) {
	m.Releases = &ReleaseClient{API: "https://isolated-metadata.invalid", HTTP: &http.Client{Transport: networkRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if fail {
			return nil, errors.New("PRIVATE-METADATA-SECRET")
		}
		body := `{"tag_name":"rust-v0.130.0"}`
		if strings.Contains(r.URL.Path, "MetaCubeX/mihomo") {
			body = `{"tag_name":"v1.19.30"}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
}

func versionTestPrograms(t *testing.T, m *Manager) {
	t.Helper()
	for name := range m.State.Components {
		if err := os.MkdirAll(filepath.Join(m.Store, dataPath(name)), 0700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(m.Store, programPath(name)), "VERSION-PROBE-FIXTURE", 0755)
	}
}

func TestVersionsReadSoftwareVersionsAndMetadataWithoutTreatingHashesAsVersions(t *testing.T) {
	m := networkTestManager(t)
	m.State.Components = map[string]Component{
		"codex": {Installed: true, Backend: "official", Digest: strings.Repeat("a", 64)},
		"proxy": {Installed: true, Backend: "file", Digest: strings.Repeat("b", 64)},
	}
	versionTestPrograms(t, m)
	versionTestMetadata(m, false)
	var probes []CommandSpec
	m.Runner = func(spec CommandSpec) ([]byte, error) {
		probes = append(probes, spec)
		if spec.Path == filepath.Join(m.Store, programPath("codex")) && reflect.DeepEqual(spec.Args, []string{"--version"}) {
			data := filepath.Join(m.Store, dataPath("codex"))
			if envValue(spec.Env, "HOME") != data || envValue(spec.Env, "CODEX_HOME") != data || envValue(spec.Env, "CODEX_SQLITE_HOME") != data {
				t.Fatal("version probe inherited an external HOME or SQLite path")
			}
			return []byte("codex-cli 0.127.0\n"), nil
		}
		if spec.Path == filepath.Join(m.Store, programPath("proxy")) && reflect.DeepEqual(spec.Args, []string{"-v"}) {
			return []byte("Mihomo Meta v1.19.31 linux amd64 with go1.25.0\n"), nil
		}
		t.Fatal("version lookup ran an installer or unsupported command")
		return nil, nil
	}
	rows, err := m.Versions(nil)
	if err != nil || len(rows) != 2 || len(probes) != 2 {
		t.Fatal("software version lookup failed", err)
	}
	if rows[0].Current != "0.127.0" || rows[0].Latest != "0.130.0" || !rows[0].UpdateAvailable || rows[0].Error != "" || rows[1].Current != "1.19.31" || rows[1].Latest != "1.19.30" || rows[1].UpdateAvailable || rows[1].Error != "" {
		t.Fatal("software comparison was replaced with hashes or downgraded a newer local version", rows)
	}
}

func TestVersionQueryFailuresRemainReadOnlyAndDoNotHideCurrentVersions(t *testing.T) {
	m := networkTestManager(t)
	m.State.Components = map[string]Component{"codex": {Installed: true, Backend: "official"}}
	versionTestPrograms(t, m)
	versionTestMetadata(m, true)
	m.Runner = func(CommandSpec) ([]byte, error) { return []byte("codex-cli 0.127.0\n"), nil }
	rows, err := m.Versions([]string{"codex"})
	if err != nil || len(rows) != 1 || rows[0].Current != "0.127.0" || rows[0].Latest != "" || rows[0].Error == "" || strings.Contains(rows[0].Error, "PRIVATE-METADATA-SECRET") || !m.State.Components["codex"].Installed {
		t.Fatal("metadata failure changed management state or hid a usable installed version", rows, err)
	}
	versionTestMetadata(m, false)
	m.Runner = func(CommandSpec) ([]byte, error) { return nil, errors.New("PRIVATE-COMMAND-SECRET") }
	rows, err = m.Versions([]string{"codex"})
	if err != nil || rows[0].Current != "" || rows[0].Latest != "0.130.0" || rows[0].Error == "" || strings.Contains(rows[0].Error, "PRIVATE-COMMAND-SECRET") || !m.State.Components["codex"].Installed {
		t.Fatal("local query failure became a destructive operation", rows, err)
	}
	delete(m.State.Components, "codex")
	m.Runner = func(CommandSpec) ([]byte, error) { t.Fatal("uninstalled software was executed"); return nil, nil }
	rows, err = m.Versions([]string{"codex"})
	if err != nil || rows[0].Current != "" || rows[0].Latest != "0.130.0" {
		t.Fatal("uninstalled latest version lookup failed", rows, err)
	}
	if _, err := m.Versions([]string{"codex", "codex"}); err == nil {
		t.Fatal("duplicate version selection accepted")
	}
}

func TestVersionsNeverExecutesRelocatedOrExternalProgramLinks(t *testing.T) {
	m := networkTestManager(t)
	m.State.Components = map[string]Component{"codex": {Installed: true, Backend: "official"}}
	versionTestPrograms(t, m)
	versionTestMetadata(m, false)
	m.Runner = func(CommandSpec) ([]byte, error) {
		t.Fatal("version probe bypassed the component boundary")
		return nil, nil
	}
	m.Relocated = true
	rows, err := m.Versions([]string{"codex"})
	if err != nil || rows[0].Current != "" || rows[0].Latest != "0.130.0" || !strings.Contains(rows[0].Error, "repair") {
		t.Fatal("relocated instance ran a version probe", rows, err)
	}
	m.Relocated = false
	program := filepath.Join(m.Store, programPath("codex"))
	if err := os.Remove(program); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(filepath.Dir(m.Store), "external-codex")
	write(t, external, "OUTSIDE-PROGRAM", 0755)
	if err := os.Symlink(external, program); err != nil {
		t.Fatal(err)
	}
	rows, err = m.Versions([]string{"codex"})
	if err != nil || rows[0].Current != "" || rows[0].Latest != "0.130.0" || !strings.Contains(rows[0].Error, "repair") {
		t.Fatal("external program link ran a version probe", rows, err)
	}
}

func TestSoftwareVersionParsingAndComparison(t *testing.T) {
	for _, scenario := range []struct{ name, output, want string }{
		{"codex", "codex-cli 0.130.0-alpha.12\n", "0.130.0-alpha.12"},
		{"proxy", "Mihomo Meta v1.19.30 linux amd64\n", "1.19.30"},
		{"proxy", "Mihomo Meta alpha-abcdef linux amd64\n", "alpha-abcdef"},
	} {
		got, err := parseSoftwareVersion(scenario.name, []byte(scenario.output))
		if err != nil || got != scenario.want {
			t.Fatal("actual software version was not parsed", scenario.name, got, err)
		}
	}
	if _, err := parseSoftwareVersion("codex", []byte(strings.Repeat("a", 64))); err == nil {
		t.Fatal("binary hash was accepted as a software version")
	}
	for _, scenario := range []struct {
		a, b string
		sign int
	}{
		{"1.9.0", "1.10.0", -1}, {"2.0.0", "1.99.99", 1},
		{"v1.2.3", "1.2.3", 0}, {"1.2.3-alpha.2", "1.2.3-alpha.10", -1},
		{"1.2.3-beta", "1.2.3", -1}, {"1.2.3+build", "1.2.3", 0},
	} {
		got, err := compareSoftwareVersions(scenario.a, scenario.b)
		if err != nil || got != scenario.sign {
			t.Fatal("software version comparison failed", scenario, got, err)
		}
	}
	if _, err := compareSoftwareVersions("alpha-abcdef", "1.19.30"); err == nil {
		t.Fatal("unknown development build was silently ordered as a stable release")
	}
}
