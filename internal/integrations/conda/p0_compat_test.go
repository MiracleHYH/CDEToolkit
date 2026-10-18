package conda

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestP0ValidateQueriesOnlyManagedCondaFields(t *testing.T) {
	for _, row := range []struct {
		name               string
		mirror, envs, pkgs bool
	}{
		{"mirror only", true, false, false},
		{"envs only", false, true, false},
		{"pkgs only", false, false, true},
		{"mirror and pkgs", true, false, true},
		{"all", true, true, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			s := fixture(t)
			c := Config{}
			want := []string{"config", "--show"}
			actual := map[string]any{}
			if row.mirror {
				c.CondaMirror = "tuna"
				want = append(want, defaultMirrorKeys...)
				if err := json.Unmarshal(configJSON(t, "tuna", true), &actual); err != nil {
					t.Fatal(err)
				}
			}
			if row.envs {
				c.EnvsDir = filepath.Join(s.UserHome, "data", "envs")
				want = append(want, "envs_dirs")
				actual["envs_dirs"] = []string{c.EnvsDir, "/upstream/fallback/envs"}
			}
			if row.pkgs {
				c.PkgsDir = filepath.Join(s.UserHome, "data", "pkgs")
				want = append(want, "pkgs_dirs")
				actual["pkgs_dirs"] = []string{c.PkgsDir, "/upstream/fallback/pkgs"}
			}
			want = append(want, "--json")
			activate(&s, c)
			calls := 0
			s.Runner = func(name string, args, env []string) ([]byte, error) {
				calls++
				if name != "conda" || !reflect.DeepEqual(args, want) || !reflect.DeepEqual(env, s.Env) {
					t.Fatalf("unexpected command or injected environment: %s %v %v; want %v", name, args, env, want)
				}
				return json.Marshal(actual)
			}
			if _, err := s.Validate(c); err != nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestP0ValidateEnvsRetainsUpstreamProbeAndOrdering(t *testing.T) {
	for _, row := range []struct {
		name   string
		output string
		err    error
	}{
		{"upstream refused probe", "", errors.New("runtime rejected write probe")},
		{"upstream preferred fallback", `{"envs_dirs":["/upstream/fallback/envs","TARGET"]}`, nil},
		{"upstream omitted directory", `{}`, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			s := fixture(t)
			c := Config{EnvsDir: filepath.Join(s.UserHome, "data", "envs")}
			output := strings.ReplaceAll(row.output, "TARGET", c.EnvsDir)
			calls := 0
			s.Runner = func(name string, args, env []string) ([]byte, error) {
				calls++
				if name != "conda" || !reflect.DeepEqual(args, []string{"config", "--show", "envs_dirs", "--json"}) {
					t.Fatalf("directory validation bypassed upstream query: %s %v", name, args)
				}
				return []byte(output), row.err
			}
			if _, err := s.Validate(c); err == nil || calls != 1 {
				t.Fatalf("accepted failed or different upstream resolution: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestP0PresetReplacesImplicitUserMirrorContext(t *testing.T) {
	s := fixture(t)
	user := filepath.Join(s.UserHome, ".condarc")
	var original map[string]any
	if err := json.Unmarshal(configJSON(t, "tuna", false), &original); err != nil {
		t.Fatal(err)
	}
	original["custom_channels"].(map[string]any)["nvidia"] = "https://mirrors.sustech.edu.cn/anaconda-extra/cloud/nvidia"
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := captureCondaYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	d := MirrorDefaults{CondaCaptured: true, CondaYAML: baseline}
	// This is the target's implicit user configuration shape: the old mirror
	// list has no merge annotations and must not survive beside the new one.
	text := "# private configuration\nssl_verify: /kept/cert.pem\nproxy_servers: {https: https://proxy.invalid:8443}\nunknown_setting: [one, two]\nenvs_dirs: [/kept/envs]\npkgs_dirs: [/kept/pkgs]\n" + strings.ReplaceAll(baseline, " #!final", "")
	nativeWrite(t, user, text, 0600)
	selected := Config{CondaMirror: "official", Defaults: d}
	undo, err := s.ChangePaths(Config{Defaults: d}, selected, NativeScope{CondaMirror: true})
	if err != nil {
		t.Fatal(err)
	}
	after := nativeRead(t, user)
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(after), &document); err != nil {
		t.Fatal(err)
	}
	root := document.Content[0]
	var actual []string
	if err := mirrorMapping(root, "default_channels").Decode(&actual); err != nil {
		t.Fatal(err)
	}
	want, _ := mirrorChannels("official")
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("implicit user file retained old default_channels: %v", actual)
	}
	custom := mirrorMapping(root, "custom_channels")
	if got := mirrorMapping(custom, "nvidia"); got == nil || got.Value != "https://mirrors.sustech.edu.cn/anaconda-extra/cloud" {
		t.Fatal("preset lost the original private channel mapping")
	}
	if !reflect.DeepEqual(nativeNonMirrorNodes(t, text), nativeNonMirrorNodes(t, after)) {
		t.Fatal("mirror selection changed non-mirror keys")
	}
	if selected.Defaults.CondaYAML != baseline {
		t.Fatal("preset selection replaced the original default snapshot")
	}
	if err := undo(); err != nil || nativeRead(t, user) != text {
		t.Fatal("failed-save compensation did not restore original bytes", err)
	}
}

func TestP0RuntimePresetReplacesImplicitUserDefaults(t *testing.T) {
	program := os.Getenv("NIDO_TEST_REAL_CONDA")
	if program == "" {
		t.Skip("set NIDO_TEST_REAL_CONDA to run the isolated upstream regression")
	}
	if !filepath.IsAbs(program) || !executable(program) {
		t.Fatal("NIDO_TEST_REAL_CONDA must name an executable absolute path")
	}
	s := fixture(t)
	s.Env = []string{
		"HOME=" + s.UserHome, "PATH=/usr/bin:/bin", "CONDA_EXE=" + program,
		"CONDA_ROOT=" + filepath.Join(s.UserHome, "runtime-root"),
		"CONDA_NO_PLUGINS=true", "CONDA_OFFLINE=true", "PYTHONDONTWRITEBYTECODE=1",
		"XDG_CONFIG_HOME=" + filepath.Join(s.UserHome, "xdg-config"),
	}
	user := filepath.Join(s.UserHome, ".condarc")
	before := "# target-like original user configuration\nchannels: [defaults]\nshow_channel_urls: true\ndefault_channels: [https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/main, https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/r, https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/msys2]\ncustom_channels:\n  nvidia: https://mirrors.sustech.edu.cn/anaconda-extra/cloud\n"
	nativeWrite(t, user, before, 0600)
	d, err := s.CaptureDefaults(MirrorDefaults{}, "conda")
	if err != nil {
		t.Fatal(err)
	}
	previous := Config{Defaults: d}
	for _, mirror := range []string{"official", "tuna", "default"} {
		selected := Config{CondaMirror: mirror, Defaults: d}
		if _, err := s.ChangePaths(previous, selected, NativeScope{CondaMirror: true}); err != nil {
			t.Fatal(err)
		}
		profile, content := s.MirrorFile(selected)
		if err := os.MkdirAll(filepath.Dir(profile), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(profile, content, 0600); err != nil {
			t.Fatal(err)
		}
		activate(&s, selected)
		if _, err := s.Validate(selected); err != nil {
			t.Fatalf("preset %s failed in the actual upstream merger: %v", mirror, err)
		}
		previous = selected
	}
}
