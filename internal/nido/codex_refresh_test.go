package nido

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func TestCodexRefreshCommandPreservesExternalHomeAndProxy(t *testing.T) {
	for _, scenario := range []string{"discover", "cancel", "missing-saved", "available-custom"} {
		t.Run(scenario, func(t *testing.T) {
			m, userHome := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(userHome, "external codex")
			if err := os.MkdirAll(filepath.Join(home, "sessions"), 0700); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(userHome, "custom program bin", "codex")
			target := filepath.Join(home, "packages", "standalone", "synthetic", "bin", "codex")
			createCommand := func() {
				t.Helper()
				for _, dir := range []string{filepath.Dir(target), filepath.Dir(entry)} {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(target, []byte("INERT-STANDALONE-PROGRAM"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, entry); err != nil {
					t.Fatal(err)
				}
			}
			m.Codex.Env = []string{"PATH=/nonexistent"}
			m.Codex.Run = func(string, []string, []string, []byte) ([]byte, error) {
				t.Fatal("association refresh executed an external command")
				return nil, fmt.Errorf("unexpected execution")
			}
			if scenario == "missing-saved" || scenario == "available-custom" {
				createCommand()
				m.Codex.Env = []string{"PATH=" + filepath.Dir(entry)}
			}
			if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(home) }); err != nil {
				t.Fatal(err)
			}
			if err := m.ConfigureProxy("http://127.0.0.1:17890", false); err != nil {
				t.Fatal(err)
			}
			block := m.Config.Codex.ProxyBlock
			if scenario == "missing-saved" {
				if err := os.Remove(entry); err != nil {
					t.Fatal(err)
				}
			}
			m.Codex.Env = []string{"PATH=/nonexistent"}
			if scenario == "discover" || scenario == "cancel" {
				createCommand()
				m.Codex.Env = append(m.Codex.Env, "CODEX_INSTALL_DIR="+filepath.Dir(entry))
			}
			protected := map[string][]byte{}
			for _, name := range []string{"auth.json", "config.toml", "sessions/example.jsonl"} {
				path := filepath.Join(home, name)
				data := []byte("UNCHANGED " + name)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				protected[path] = data
			}
			for _, path := range []string{filepath.Join(home, ".env"), target} {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				protected[path] = data
			}
			configBefore, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			envBefore, _ := os.ReadFile(filepath.Join(m.Root, "env.sh"))
			input := "y\n"
			if scenario == "cancel" {
				input = "\n"
			}
			var output bytes.Buffer
			c := CLI{UI: terminalui.NewScriptDriver(strings.NewReader(input), &output), Out: &output, Err: &output}
			code := c.codex(m, cliOptions{}, []string{"setup"})
			if scenario == "cancel" {
				if code == 0 || m.Config.Codex.Command != "" {
					t.Fatal("cancelled refresh changed the saved command", output.String())
				}
				for path, want := range map[string][]byte{filepath.Join(m.Root, "config.toml"): configBefore, filepath.Join(m.Root, "env.sh"): envBefore} {
					got, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatal("cancelled refresh wrote Nido configuration", err)
					}
				}
			} else {
				if code != 0 {
					t.Fatal(code, output.String())
				}
				wantCommand := entry
				if scenario == "missing-saved" {
					wantCommand = ""
				}
				if m.Config.Codex.Command != wantCommand {
					t.Fatal("refresh did not save the confirmed command state")
				}
				if wantCommand == "" {
					if m.Config.Codex.Channel != "" || !strings.Contains(output.String(), "尚未发现 Codex 命令") {
						t.Fatal("missing command was reported as ready to use")
					}
				} else if m.Config.Codex.Channel != "standalone" || !strings.Contains(m.Environment(), filepath.Dir(entry)) {
					t.Fatal("refresh did not register the actual standalone command directory")
				}
			}
			if m.Config.Codex.Home != home || m.Config.Codex.ProxyBlock != block {
				t.Fatal("refresh changed the associated data directory or proxy ownership")
			}
			for path, want := range protected {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal("refresh changed external Codex data", path, err)
				}
			}
		})
	}
}
