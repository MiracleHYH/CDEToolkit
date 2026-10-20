package nido

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"

	"go.yaml.in/yaml/v3"
)

func quickStartCLI(t *testing.T, m *Manager, r *cliProxyRunner, in string, args ...string) (int, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	reader := strings.NewReader(in)
	c := CLI{UI: terminalui.NewScriptDriver(reader, &stderr), In: reader, Out: &out, Err: &stderr, ProxyRunner: r}
	code := c.Run(append([]string{"--root", m.Root}, args...))
	return code, out.String(), stderr.String()
}

func TestProxyQuickStartDirectISPDoesNotAskForUnusedHealthTarget(t *testing.T) {
	input := "https://provider.invalid/sub\n.*\n17890\n1\n2\nss://aes-128-gcm:synthetic@192.0.2.1:8388#Direct-ISP\n17892\n1\n\n2\n"
	var out, stderr bytes.Buffer
	reader := strings.NewReader(input)
	c := CLI{UI: terminalui.NewScriptDriver(reader, &stderr), In: reader, Out: &out, Err: &stderr}
	data, err := c.proxyQuickStart(proxy.ControllerConfig{Address: proxy.DefaultControllerAddress, Secret: "synthetic-controller-secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"ISP 测试地址", "ISP 前置测试地址", "ISP 前置 filter"} {
		if strings.Contains(stderr.String(), label) {
			t.Fatalf("direct ISP asked for unused relay option %q", label)
		}
	}
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	listeners := config["listeners"].([]any)
	if len(config["proxy-groups"].([]any)) != 1 || listeners[1].(map[string]any)["proxy"] != "Direct-ISP" {
		t.Fatal("direct ISP must reference the node without a synthetic health-check group")
	}
}

// Capture the concurrent write at the Provider prompt, after the installation
// confirmation and after the component has captured its configuration revision.
type quickStartBeforeProvider struct {
	*terminalui.ScriptDriver
	before func()
}

func (d *quickStartBeforeProvider) InputSecret(label, fallback string) (string, error) {
	if label == "Provider URL" && d.before != nil {
		before := d.before
		d.before = nil
		before()
	}
	return d.ScriptDriver.InputSecret(label, fallback)
}

func TestProxyQuickStartInstallAndPrivateSummary(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	r := &cliProxyRunner{}
	input := "2\n2\nhttps://provider.invalid/sub?token=plain-preview\nHK\n17890\n2\nDownload\n17891\n2\nss://aes-128-gcm:p%2Dass@192.0.2.1:8388#ISP%2Dnode\n17892\n2\nRelay\nhttps://isp-check.invalid/check\n\n2\n"
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
	rules := config["rules"].([]any)
	if len(config["listeners"].([]any)) != 3 || len(rules) == 0 || rules[len(rules)-1] != "MATCH,DIRECT" {
		t.Fatalf("incorrect generated topology: %s", data)
	}
	if provider := config["proxy-providers"].(map[string]any)["subscription"].(map[string]any); provider["url"] != "https://provider.invalid/sub?token=plain-preview" || provider["interval"] != 0 {
		t.Fatal("summary redaction changed the saved provider")
	}
	if config["external-controller"] != "127.0.0.1:9090" || len(config["secret"].(string)) != 64 || strings.Contains(out+stderr, config["secret"].(string)) {
		t.Fatal("quick start did not persist a private controller secret")
	}
	if node := config["proxies"].([]any)[0].(map[string]any); node["password"] != "p-ass" {
		t.Fatal("summary redaction changed the saved node credential")
	}
	for _, name := range []string{"Proxy", "Download", "ISP-Relay"} {
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
	if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "configure", "--json"); code != 0 {
		t.Fatal(stderr)
	}
	data, err = os.ReadFile(filepath.Join(m.Root, "proxy", "config.yaml"))
	if err != nil || !bytes.Equal(before, data) {
		t.Fatal("existing native configuration was overwritten")
	}
}

func TestProxyQuickStartFailureKeepsInstalledProgramAndNoConfig(t *testing.T) {
	for _, input := range []string{"2\n2\n", "2\n2\nhttps://provider.invalid/sub\n.*\n0\n", "2\n2\nhttps://provider.invalid/sub\n.*\n17890\n2\nDL\n17890\n1\n\n2\n"} {
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
			answers := "https://provider.invalid/sub\n.*\n17890\n1\n1\n\n2\n"
			if action == "install" {
				answers = "2\n2\n" + answers
			}
			input := strings.NewReader(answers)
			var out, stderr bytes.Buffer
			driver := &quickStartBeforeProvider{ScriptDriver: terminalui.NewScriptDriver(input, &stderr), before: func() {
				if err := os.WriteFile(path, keep, 0600); err != nil {
					t.Fatal(err)
				}
			}}
			cli := CLI{UI: driver, In: input, Out: &out, Err: &stderr, ProxyRunner: r}
			args := []string{"--root", m.Root, "proxy", action}
			if action == "install" {
				args = append(args, "--artifact", artifact)
			}
			if code := cli.Run(args); code == 0 || !strings.Contains(stderr.String(), "配置已变化") {
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
		for _, answer := range []string{"", "\n", "1\n", "q\n"} {
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
				input := "https://provider.invalid/sub?token=cancel-secret\n.*\n17890\n1\n1\n\n" + answer
				if action == "install" {
					input = "2\n2\n" + input
				}
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
