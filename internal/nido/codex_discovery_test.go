package nido

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func TestCLISetupDiscoveryPriorityAndCustomFallback(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		envExists, defaultExists bool
		emptyEnv, interactive    bool
		explicit, want           string
		fail                     bool
	}{
		{name: "environment-before-default", envExists: true, defaultExists: true, want: "environment"},
		{name: "missing-environment-falls-back", defaultExists: true, want: "default"},
		{name: "empty-environment-falls-back", defaultExists: true, emptyEnv: true, want: "default"},
		{name: "custom-only-after-discovery-misses", interactive: true, want: "custom"},
		{name: "explicit-custom-overrides-discovery", envExists: true, defaultExists: true, explicit: "custom", want: "custom"},
		{name: "invalid-explicit-does-not-fall-back", envExists: true, defaultExists: true, explicit: "missing", fail: true},
		{name: "noninteractive-miss-does-not-guess", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			paths := map[string]string{
				"environment": filepath.Join(home, "environment codex"),
				"default":     filepath.Join(home, ".codex"),
				"custom":      filepath.Join(home, "custom codex"),
				"missing":     filepath.Join(home, "missing explicit directory"),
			}
			protected := map[string][]byte{}
			for name, exists := range map[string]bool{"environment": tc.envExists, "default": tc.defaultExists, "custom": true} {
				if !exists {
					continue
				}
				if err := os.Mkdir(paths[name], 0700); err != nil {
					t.Fatal(err)
				}
				for _, file := range []string{"auth.json", ".env"} {
					path := filepath.Join(paths[name], file)
					data := []byte(name + " unchanged " + file + "\n")
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
					protected[path] = data
				}
			}
			if tc.emptyEnv {
				t.Setenv("CODEX_HOME", "")
			} else {
				t.Setenv("CODEX_HOME", paths["environment"])
			}
			configBefore, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"codex", "setup", "--json"}
			input := ""
			if tc.interactive {
				input = "2\n" + paths["custom"] + "\ny\n"
			} else {
				args = append(args, "--yes")
			}
			if tc.explicit != "" {
				args = append(args, "--home", paths[tc.explicit])
			}
			called := false
			runner := func(string, []string, []string, []byte) ([]byte, error) {
				called = true
				return nil, fmt.Errorf("setup must not execute a component or installer")
			}
			var code int
			var out, stderr string
			if tc.interactive {
				var stdout, errors bytes.Buffer
				c := CLI{UI: terminalui.NewScriptDriver(strings.NewReader(input), &errors), Out: &stdout, Err: &errors, Runner: runner}
				code = c.Run([]string{"--root", m.Root, "codex", "setup"})
				out, stderr = stdout.String(), errors.String()
				if strings.Count(stderr, "已有自定义目录 [") != 1 || strings.Contains(stderr, "CODEX_HOME（目录保持原位）") {
					t.Fatal("custom directory prompted more than once", stderr)
				}
			} else {
				code, out, stderr = isolatedCLI(t, m, args, input, runner)
			}
			if (code != 0) != tc.fail || called {
				t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, out, stderr)
			}
			reloaded, err := New(m.Root)
			if err != nil {
				t.Fatal(err)
			}
			if tc.fail {
				current, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
				if err != nil || !bytes.Equal(current, configBefore) || reloaded.Config.Codex.Home != "" {
					t.Fatal("failed discovery changed the association")
				}
			} else {
				var result struct{ Home string }
				if !tc.interactive && (json.Unmarshal([]byte(out), &result) != nil || result.Home != paths[tc.want]) || reloaded.Config.Codex.Home != paths[tc.want] {
					t.Fatalf("wrong home: stdout=%q stderr=%q config=%+v", out, stderr, reloaded.Config.Codex)
				}
				if !tc.interactive && stderr != "" {
					t.Fatalf("automatic discovery unexpectedly prompted: %q", stderr)
				}
			}
			for path, expected := range protected {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatalf("setup modified external data: %s", path)
				}
			}
			if _, err := os.Lstat(paths["missing"]); !os.IsNotExist(err) {
				t.Fatalf("setup created a missing explicit home: %v", err)
			}
		})
	}
}
