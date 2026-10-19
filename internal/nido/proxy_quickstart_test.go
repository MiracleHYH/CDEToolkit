package nido

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func quickStartCLI(t *testing.T, m *Manager, r *cliProxyRunner, in string, args ...string) (int, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	c := CLI{In: strings.NewReader(in), Out: &out, Err: &stderr, ProxyRunner: r, Interactive: true}
	code := c.Run(append([]string{"--root", m.Root}, args...))
	return code, out.String(), stderr.String()
}

func TestProxyQuickStartInstallAndPrivateSummary(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	r := &cliProxyRunner{}
	input := "https://provider.invalid/sub?token=plain-preview\nHK\n17890\ny\nDownload\n17891\ny\nss://aes-128-gcm:p%2Dass@192.0.2.1:8388#ISP%2Dnode\n17892\nhttps://isp-check.invalid/check\ny\nRelay\ny\n"
	code, out, stderr := quickStartCLI(t, m, r, input, "proxy", "install", "--artifact", proxyCLIArtifact(t))
	if code != 0 || !strings.Contains(stderr, "Proxy 配置摘要") || !strings.Contains(out, "操作完成") {
		t.Fatalf("install quick start: %d %s %s", code, out, stderr)
	}
	for _, secret := range []string{"provider.invalid", "plain-preview", "ss://", "p%2Dass", "p-ass", "isp-check.invalid", "192.0.2.1"} {
		if strings.Contains(stderr+out, secret) {
			t.Fatalf("quick start output disclosed a private field %q", secret)
		}
	}
	for _, visible := range []string{"17890", "17891", "17892", "服务器 → 订阅前置节点 → ISP 节点 → 目标", "保存 Proxy 配置（保持停止）"} {
		if !strings.Contains(stderr, visible) {
			t.Fatalf("quick start summary missing %q: %s", visible, stderr)
		}
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
	if provider := config["proxy-providers"].(map[string]any)["subscription"].(map[string]any); provider["url"] != "https://provider.invalid/sub?token=plain-preview" {
		t.Fatal("summary redaction changed the saved provider")
	}
	if node := config["proxies"].([]any)[0].(map[string]any); node["password"] != "p-ass" {
		t.Fatal("summary redaction changed the saved node credential")
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
			if code, _, _ := quickStartCLI(t, m, r, input, "proxy", "install", "--artifact", proxyCLIArtifact(t)); code == 0 {
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
			input := &beforeRead{reader: strings.NewReader("https://provider.invalid/sub\n.*\n17890\nn\nn\ny\n"), before: func() {
				if err := os.WriteFile(path, keep, 0600); err != nil {
					t.Fatal(err)
				}
			}}
			var out, stderr bytes.Buffer
			cli := CLI{In: input, Out: &out, Err: &stderr, ProxyRunner: r, Interactive: true}
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

func TestProxyQuickStartFinalConfirmationRequired(t *testing.T) {
	for _, action := range []string{"install", "configure"} {
		for _, answer := range []string{"", "\n", "n\n"} {
			t.Run(action+"/"+strings.ReplaceAll(answer, "\n", "newline"), func(t *testing.T) {
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
				before := map[string][]byte{}
				for _, name := range []string{"config.toml", "env.sh"} {
					data, err := os.ReadFile(filepath.Join(m.Root, name))
					if err != nil {
						t.Fatal(err)
					}
					before[name] = data
				}
				input := "https://provider.invalid/sub?token=cancel-secret\n.*\n17890\nn\nn\n" + answer
				args := []string{"proxy", action}
				if action == "install" {
					args = append(args, "--artifact", artifact)
				}
				code, out, stderr := quickStartCLI(t, m, r, input, args...)
				if code == 0 || !strings.Contains(stderr, "保存 Proxy 配置（保持停止）") {
					t.Fatalf("configuration did not require final confirmation: %d %s %s", code, out, stderr)
				}
				if strings.Contains(out+stderr, "cancel-secret") {
					t.Fatal("cancelled preview disclosed provider credentials")
				}
				if _, err := os.Lstat(filepath.Join(m.Root, "proxy", "config.yaml")); !os.IsNotExist(err) {
					t.Fatal("cancelled quick start created configuration")
				}
				for name, want := range before {
					if got, err := os.ReadFile(filepath.Join(m.Root, name)); err != nil || !bytes.Equal(got, want) {
						t.Fatalf("cancelled quick start changed %s", name)
					}
				}
				if r.starts != 0 || r.stops != 0 {
					t.Fatal("cancelled quick start controlled a service")
				}
			})
		}
	}
}
