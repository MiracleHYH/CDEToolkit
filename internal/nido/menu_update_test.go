package nido

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSettingsMenuExitsAfterReplacingItsExecutable(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("release self-update executes only on Linux amd64")
	}
	binaries := updateCLIBinaries(t)
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(m.Root, "bin", "nido")
	if err := os.WriteFile(program, binaries["0.5.0"], 0755); err != nil {
		t.Fatal(err)
	}
	m.Executable = program
	m.executableInfo, _ = os.Stat(program)
	previous := Version
	Version = "0.5.0"
	t.Cleanup(func() { Version = previous })
	client, calls := updateCLIClient(t, updateCLIArchive(t, binaries["0.6.0"]), false)
	address := enabledAPIFixture(t, m, client)
	if err := m.setNetwork(DownloadConfig{GitHubAccelerate: GitHubAccelerateProxy, ProxyURL: address}); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	env, _ := os.ReadFile(filepath.Join(m.Root, "env.sh"))
	var out, stderr bytes.Buffer
	driver := &menuPlanDriver{plan: map[string][]string{"Nido": {"check-update", "github-accelerate"}}, choices: []int{1}}
	c := CLI{UI: driver, Out: &out, Err: &stderr, UpdateHTTP: client}
	if code := c.menu(m, cliOptions{}, nil); code != 0 || !c.restartRequired || !strings.Contains(out.String(), "重新运行 nido") {
		t.Fatal("menu continued using its old executable", code, out.String(), stderr.String())
	}
	if remaining := driver.plan["Nido"]; len(remaining) != 1 || remaining[0] != "github-accelerate" {
		t.Fatal("updated menu consumed another operation", remaining)
	}
	if got, err := os.ReadFile(program); err != nil || !bytes.Equal(got, binaries["0.6.0"]) || len(*calls) != 4 {
		t.Fatal("fixture did not complete the verified update", err, *calls)
	}
	for path, expected := range map[string][]byte{filepath.Join(m.Root, "config.toml"): config, filepath.Join(m.Root, "env.sh"): env} {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, expected) {
			t.Fatal("menu update changed configuration", path, err)
		}
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatal("menu update modified the external home", entries, err)
	}
}
