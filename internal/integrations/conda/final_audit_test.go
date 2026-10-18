package conda

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestFinalAuditOriginalCustomChannelPreservesEscapedBase(t *testing.T) {
	for _, address := range []string{
		"https://private.invalid/team%2Fspecial/private?token=original",
		"https://private.invalid/team%2fspecial/%70rivate?token=original",
		"https://private.invalid/team%252Fspecial/private?token=original",
	} {
		t.Run(address, func(t *testing.T) {
			var original map[string]any
			if err := json.Unmarshal(defaultsCondaJSON(t), &original); err != nil {
				t.Fatal(err)
			}
			original["custom_channels"].(map[string]any)["private"] = address
			encoded, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			captured, err := captureCondaYAML(encoded)
			if err != nil {
				t.Fatal(err)
			}
			root, err := parseDefaultYAML(captured)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Replace(address, "/private?", "?", 1)
			want = strings.Replace(want, "/%70rivate?", "?", 1)
			if got := mirrorMapping(mirrorMapping(root, "custom_channels"), "private").Value; got != want {
				t.Fatalf("changed the original escaped repository path: got=%q want=%q", got, want)
			}
		})
	}
}

func TestFinalAuditPresetMatchesEscapedCustomBaseWithoutForwardingCredentials(t *testing.T) {
	var original map[string]any
	if err := json.Unmarshal(defaultsCondaJSON(t), &original); err != nil {
		t.Fatal(err)
	}
	original["channels"] = []string{"https://user:PRIVATE-PASSWORD@private.invalid/team%2Fspecial/conda-forge/label/dev?key=PRIVATE-QUERY"}
	original["custom_channels"].(map[string]any)["conda-forge"] = "https://user:PRIVATE-PASSWORD@private.invalid/team%2Fspecial/conda-forge?key=PRIVATE-QUERY"
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureCondaYAML(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, preset := range []string{"official", "tuna"} {
		got := PresetChannels(MirrorDefaults{CondaCaptured: true, CondaYAML: captured}, preset)
		if !reflect.DeepEqual(got, []string{"conda-forge/label/dev"}) {
			t.Fatalf("escaped private base was not remapped safely for %s: %q", preset, got)
		}
	}
}

func TestFinalAuditRejectsContradictoryPipMirrorSelections(t *testing.T) {
	s := fixture(t)
	for _, c := range []Config{
		{PipMirror: "tuna", PipIndexURL: "https://pypi.org/simple"},
		{PipMirror: "official", PipIndexURL: "https://custom.invalid/simple"},
		{PipMirror: "ustc"},
		{PipMirror: "custom"},
		{PipMirror: "default", PipIndexURL: "https://custom.invalid/simple", Defaults: MirrorDefaults{PipCaptured: true, PipIndexURL: "https://original.invalid/simple"}},
	} {
		if err := s.ValidateConfig(c); err == nil {
			t.Fatalf("accepted a contradictory pip selection: mirror=%q", c.PipMirror)
		}
	}
	for _, c := range []Config{
		{},
		{PipIndexURL: "https://old-draft.invalid/simple"},
		{PipMirror: "custom", PipIndexURL: "https://custom.invalid/simple"},
		{PipMirror: "official", PipIndexURL: "https://pypi.org/simple"},
		{PipMirror: "tuna", PipIndexURL: "https://pypi.tuna.tsinghua.edu.cn/simple"},
		{PipMirror: "ustc", PipIndexURL: "https://mirrors.ustc.edu.cn/pypi/simple"},
		{PipMirror: "default", Defaults: MirrorDefaults{PipCaptured: true, PipIndexURL: "http://user:original@private.invalid/simple?token=original"}},
	} {
		if err := s.ValidateConfig(c); err != nil {
			t.Fatalf("rejected a valid or compatible pip selection: mirror=%q: %v", c.PipMirror, err)
		}
	}
}

// This opt-in regression exercises Conda's own map-merging implementation.
// Every writable path is inside t.TempDir; it does not install packages or use
// the network. Standard test runs do not require an upstream installation.
func TestFinalAuditRuntimeDefaultRestoresLaterPrivateMappings(t *testing.T) {
	program := os.Getenv("NIDO_TEST_REAL_CONDA")
	if program == "" {
		t.Skip("set NIDO_TEST_REAL_CONDA to run the isolated upstream regression")
	}
	if !filepath.IsAbs(program) || !executable(program) {
		t.Fatal("NIDO_TEST_REAL_CONDA must name an executable absolute path")
	}
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-user-file", true: "explicit-source-and-user-file"}[explicit], func(t *testing.T) {
			s := fixture(t)
			s.Env = []string{"HOME=" + s.UserHome, "PATH=/usr/bin:/bin", "CONDA_EXE=" + program, "CONDA_NO_PLUGINS=true", "CONDA_OFFLINE=true", "XDG_CONFIG_HOME=" + filepath.Join(s.UserHome, "xdg-config"), "CONDA_PKGS_DIRS=" + filepath.Join(s.UserHome, "unused-pkgs"), "CONDA_ENVS_PATH=" + filepath.Join(s.UserHome, "unused-envs")}
			user := filepath.Join(s.UserHome, ".condarc")
			base := "# keep certificate and proxy settings\nchannels: [defaults, implicit]\nchannel_alias: https://original.invalid/cloud\nssl_verify: false\nproxy_servers:\n  https: https://proxy:ORIGINAL-PROXY-SECRET@proxy.invalid:8443\n"
			if err := os.WriteFile(user, []byte(base), 0600); err != nil {
				t.Fatal(err)
			}
			source := ""
			if explicit {
				source = filepath.Join(s.UserHome, "custom-source.yaml")
				if err := os.WriteFile(source, []byte(base), 0600); err != nil {
					t.Fatal(err)
				}
				s.Env = append(s.Env, "CONDARC="+source)
			}
			d, err := s.CaptureDefaults(MirrorDefaults{}, "conda")
			if err != nil {
				t.Fatal(err)
			}
			later := "custom_channels:\n  implicit: https://later.invalid/changes-original-resolution\n  later: https://later.invalid/private\ncustom_multichannels:\n  later: [https://later.invalid/private]\nmigrated_channel_aliases: [https://later.invalid/legacy]\nmigrated_custom_channels:\n  later: https://later.invalid/legacy\n"
			paths := []string{user}
			if source != "" {
				paths = append(paths, source)
			}
			for _, path := range paths {
				if err := os.WriteFile(path, []byte(base+later), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c := Config{CondaMirror: "default", SourceCONDARC: source, Defaults: d}
			if _, err := s.ChangePaths(Config{Defaults: d}, c, NativeScope{CondaMirror: true}); err != nil {
				t.Fatal(err)
			}
			profile, content := s.MirrorFile(c)
			if err := os.MkdirAll(filepath.Dir(profile), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(profile, content, 0600); err != nil {
				t.Fatal(err)
			}
			activate(&s, c)
			if _, err := s.Validate(c); err != nil {
				t.Fatal("default did not restore Conda's complete original mirror semantics", err)
			}
			for _, path := range paths {
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(after, []byte("# keep certificate and proxy settings")) || !bytes.Contains(after, []byte("ORIGINAL-PROXY-SECRET")) {
					t.Fatal("default restoration changed native certificate/proxy data")
				}
				var document map[string]any
				if err := yaml.Unmarshal(after, &document); err != nil || document["ssl_verify"] != false {
					t.Fatal("default restoration changed TLS verification", err)
				}
			}
			for _, path := range []string{filepath.Join(s.UserHome, "unused-pkgs"), filepath.Join(s.UserHome, "unused-envs")} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("read-only configuration validation created a package/environment directory", err)
				}
			}
		})
	}
}
