package nido

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestProxyQuickStartInstallAndVisiblePreview(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	r := &cliProxyRunner{}
	input := "https://provider.invalid/sub?token=plain-preview\nHK\n17890\ny\nDownload\n17891\ny\nss://aes-128-gcm:p%2Dass@192.0.2.1:8388#ISP%2Dnode\n17892\nhttps://isp-check.invalid/check\ny\nRelay\n"
	code, out, stderr := proxyCLI(t, m, r, input, "proxy", "install", "--artifact", proxyCLIArtifact(t), "--json")
	if code != 0 || !strings.Contains(stderr, "plain-preview") || !strings.Contains(stderr, "p-ass") || !strings.Contains(out, "操作完成") {
		t.Fatalf("install quick start: %d %s %s", code, out, stderr)
	}
	data, err := os.ReadFile(filepath.Join(m.Root, "proxy", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if len(config["listeners"].([]any)) != 3 || config["rules"].([]any)[0] != "MATCH,DIRECT" {
		t.Fatalf("incorrect generated topology: %s", data)
	}
	for _, name := range []string{"proxy", "download", "isp-relay", "isp"} {
		found := false
		for _, value := range config["proxy-groups"].([]any) {
			group := value.(map[string]any)
			if group["name"] == name {
				found = true
				if group["type"] != "url-test" {
					t.Fatalf("group %s did not use url-test", name)
				}
			}
		}
		if !found {
			t.Fatalf("missing group %s", name)
		}
	}
	if r.starts != 0 || r.stops != 0 {
		t.Fatal("quick start controlled a service")
	}
	before := append([]byte(nil), data...)
	if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "configure"); code != 0 {
		t.Fatal(stderr)
	}
	data, err = os.ReadFile(filepath.Join(m.Root, "proxy", "config.yaml"))
	if err != nil || !bytes.Equal(before, data) {
		t.Fatal("existing native configuration was overwritten")
	}
}

func TestProxyQuickStartFailureKeepsInstalledProgramAndNoConfig(t *testing.T) {
	for _, input := range []string{"", "https://provider.invalid/sub\n.*\n0\n", "https://provider.invalid/sub\n.*\n17890\ny\nDL\n17890\nn\n"} {
		t.Run(input, func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			r := &cliProxyRunner{}
			if code, _, _ := proxyCLI(t, m, r, input, "proxy", "install", "--artifact", proxyCLIArtifact(t)); code == 0 {
				t.Fatal("incomplete or invalid quick start succeeded")
			}
			if _, err := os.Stat(filepath.Join(m.Root, "proxy", "mihomo")); err != nil {
				t.Fatal("failed quick start discarded installed program")
			}
			if _, err := os.Stat(filepath.Join(m.Root, "proxy", "config.yaml")); !os.IsNotExist(err) {
				t.Fatal("failed quick start committed a configuration")
			}
			if r.starts != 0 {
				t.Fatal("incomplete quick start started core")
			}
		})
	}
}

func TestProxyQuickStartPreservesConfigCreatedDuringInput(t *testing.T) {
	for _, action := range []string{"install", "configure"} {
		t.Run(action, func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			r := &cliProxyRunner{}
			artifact := proxyCLIArtifact(t)
			if action == "configure" {
				if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "install", "--artifact", artifact, "--yes"); code != 0 {
					t.Fatal(stderr)
				}
			}
			path := filepath.Join(m.Root, "proxy", "config.yaml")
			keep := []byte("mixed-port: 18888\n# created while entering quick start\n")
			input := &beforeRead{reader: strings.NewReader("https://provider.invalid/sub\n.*\n17890\nn\nn\n"), before: func() {
				if err := os.WriteFile(path, keep, 0600); err != nil {
					t.Fatal(err)
				}
			}}
			var out, stderr bytes.Buffer
			cli := CLI{In: input, Out: &out, Err: &stderr, ProxyRunner: r}
			args := []string{"--root", m.Root, "proxy", action}
			if action == "install" {
				args = append(args, "--artifact", artifact)
			}
			if code := cli.Run(args); code == 0 || !strings.Contains(stderr.String(), "配置已创建") {
				t.Fatalf("concurrent config accepted: %d %s %s", code, out.String(), stderr.String())
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, keep) {
				t.Fatal("quick start overwrote concurrent user configuration")
			}
		})
	}
}
