package nido

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func TestBootstrapInstallationPreviewAndCancellationDoNotWrite(t *testing.T) {
	for _, scenario := range []string{"cancel", "quit", "preview", "preview-token", "invalid-proxy", "invalid-private-proxy", "auto", "json-needs-confirmation"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, "nido ' literal $value")
			t.Setenv("HOME", home)
			t.Setenv("NIDO_HOME", "")
			t.Setenv("SHELL", "/bin/bash")
			profile := filepath.Join(home, ".bashrc")
			if err := os.WriteFile(profile, []byte("USER PROFILE\n"), 0600); err != nil {
				t.Fatal(err)
			}
			options := cliOptions{root: root, networkMode: "DIRECT"}
			answers, wantSuccess := "1\n1\n", false
			switch scenario {
			case "quit":
				answers = "q\n"
			case "preview":
				options.dry, options.json = true, true
				options.networkMode, options.proxy = "PROXY", "http://user:private-password@127.0.0.1:7890"
				wantSuccess = true
			case "preview-token":
				options.dry, options.json = true, true
				options.networkMode, options.proxy = "PROXY", "http://private-access-token@127.0.0.1:7890"
				wantSuccess = true
			case "invalid-proxy":
				options.networkMode, options.proxy = "PROXY", "http://localhost:0"
			case "invalid-private-proxy":
				options.networkMode, options.proxy = "PROXY", "http://private-access-token@localhost:0"
			case "auto":
				options.networkMode = "AUTO"
			case "json-needs-confirmation":
				options.json = true
			}
			var out, errors bytes.Buffer
			c := CLI{UI: terminalui.NewScriptDriver(strings.NewReader(answers), &errors), Out: &out, Err: &errors}
			code := c.bootstrapInstall(options, nil)
			if (code == 0) != wantSuccess {
				t.Fatal("unexpected outcome", code, out.String(), errors.String())
			}
			if strings.Contains(out.String()+errors.String(), "private-password") || strings.Contains(out.String()+errors.String(), "private-access-token") {
				t.Fatal("proxy userinfo leaked into output")
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatal("preview/cancellation created software directory", err)
			}
			if got, err := os.ReadFile(profile); err != nil || string(got) != "USER PROFILE\n" {
				t.Fatal("preview/cancellation changed profile", err)
			}
		})
	}
}

func TestBootstrapNetworkSelectionStaysInGoAndCancellationWritesNothing(t *testing.T) {
	for _, scenario := range []struct{ name, answers, want string }{
		{"direct", "1\n1\n\n1\n1\n", "GitHub Accelerate：None"},
		{"github-preset", "2\n\n1\n\n1\n1\n", "https://gh-proxy.org"},
		{"github-custom", "2\nhttps://mirror.example/prefix\n1\n\n1\n1\n", "https://mirror.example/prefix"},
		{"general-proxy", "3\n2\nhttp://private-token@127.0.0.1:7890\n\n1\n1\n", "http://REDACTED@127.0.0.1:7890"},
		{"prefix-and-independent-proxy", "2\n\n2\nhttp://private-token@127.0.0.1:7890\n\n1\n1\n", "http://REDACTED@127.0.0.1:7890"},
		{"proxy-default-not-enabled", "3\n1\n", "Proxy"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, "proposed nido")
			t.Setenv("HOME", home)
			t.Setenv("NIDO_HOME", root)
			t.Setenv("SHELL", "/bin/bash")
			profile := filepath.Join(home, ".bashrc")
			if err := os.WriteFile(profile, []byte("PROFILE"), 0600); err != nil {
				t.Fatal(err)
			}
			var out, errors bytes.Buffer
			c := CLI{UI: terminalui.NewScriptDriver(strings.NewReader(scenario.answers), &errors), Out: &out, Err: &errors}
			if code := c.bootstrapInstall(cliOptions{}, nil); code == 0 {
				t.Fatal("final cancel accepted", out.String(), errors.String())
			}
			text := out.String() + errors.String()
			if !strings.Contains(text, scenario.want) {
				t.Fatal("policy selection not reached", text)
			}
			if strings.Contains(text, "private-token") {
				t.Fatal("userinfo leak", text)
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatal("cancel wrote installation directory", err)
			}
			if data, err := os.ReadFile(profile); err != nil || string(data) != "PROFILE" {
				t.Fatal("cancel changed shell profile", err, string(data))
			}
		})
	}
}

func TestBootstrapIndependentNetworkFlagsRemainExplicitAndReadOnly(t *testing.T) {
	for _, scenario := range []struct {
		options           cliOptions
		accelerate, proxy string
	}{
		{cliOptions{githubProxy: "none"}, "none", ""},
		{cliOptions{githubProxy: "none", proxy: "none"}, "none", ""},
		{cliOptions{proxy: "http://localhost:7890"}, "none", "http://localhost:7890"},
		{cliOptions{githubProxy: "https://mirror.example"}, "https://mirror.example", ""},
		{cliOptions{githubProxy: "https://mirror.example", proxy: "http://localhost:7890"}, "https://mirror.example", "http://localhost:7890"},
		{cliOptions{githubProxy: "proxy", proxy: "http://localhost:7890"}, "proxy", "http://localhost:7890"},
		{cliOptions{networkMode: "GITHUB_PROXY", githubProxy: "https://mirror.example", proxy: "http://localhost:7890"}, "https://mirror.example", "http://localhost:7890"},
	} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		o := scenario.options
		o.root, o.dry, o.json = filepath.Join(home, "nido"), true, true
		var out, errors bytes.Buffer
		c := CLI{UI: terminalui.NewScriptDriver(strings.NewReader(""), &errors), Out: &out, Err: &errors}
		if code := c.bootstrapInstall(o, nil); code != 0 {
			t.Fatal("independent settings rejected", code, out.String(), errors.String())
		}
		if strings.Contains(errors.String(), "三条路线互斥") {
			t.Fatal("explicit settings unexpectedly opened interaction")
		}
		var result map[string]string
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err, out.String())
		}
		wantProxy := scenario.proxy
		if wantProxy == "" {
			wantProxy = "None"
		}
		if result["github_accelerate"] != scenario.accelerate || result["proxy"] != wantProxy {
			t.Fatal("independent settings were mixed or discarded", result)
		}
		if _, err := os.Lstat(o.root); !os.IsNotExist(err) {
			t.Fatal("preview changed target", err)
		}
	}
}
func TestBootstrapProxyReferenceRequiresConfiguredURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := cliOptions{root: filepath.Join(home, "nido"), githubProxy: "proxy", dry: true}
	var out, errors bytes.Buffer
	c := CLI{Out: &out, Err: &errors}
	if code := c.bootstrapInstall(o, nil); code == 0 || !strings.Contains(errors.String(), "Proxy") {
		t.Fatal("missing proxy accepted", code, out.String(), errors.String())
	}
	if _, err := os.Lstat(o.root); !os.IsNotExist(err) {
		t.Fatal("invalid reference created target", err)
	}
}
