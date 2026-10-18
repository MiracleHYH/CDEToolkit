package conda

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func defaultsCondaJSON(t *testing.T) []byte {
	t.Helper()
	channel := func(name string) map[string]any {
		return map[string]any{"scheme": "http", "location": "original.test", "name": "pkgs/" + name, "auth": "user:password", "token": "private-token", "platform": nil, "package_filename": nil}
	}
	data, err := json.Marshal(map[string]any{
		"channels":         []string{"http://user:password@original.test/t/private-token/pkgs/main", "defaults", "https://person:secret@cloud.test/cloud/t/token/conda-forge/label/dev", "https://untouched.private.test/repo", "bioconda"},
		"default_channels": []any{channel("main"), channel("r"), channel("msys2")},
		"custom_channels": map[string]any{
			"conda-forge": map[string]any{"scheme": "https", "location": "cloud.test/cloud", "name": "conda-forge", "auth": "person:secret", "token": "token"},
			"private":     "https://internal.test/base/private",
		},
		"custom_multichannels":     map[string]any{"defaults": []any{channel("main"), channel("r"), channel("msys2")}, "team": []string{"http://staff:pwd@intranet.test/team?key=source"}, "local": []any{map[string]any{"scheme": "file", "location": "", "name": "/original/local-channel"}}},
		"channel_alias":            map[string]any{"scheme": "https", "location": "alias.test/cloud?key=alias", "name": nil, "auth": "alias:secret", "token": "alias-token"},
		"migrated_channel_aliases": []any{map[string]any{"scheme": "https", "location": "legacy-alias.test/cloud", "name": nil, "auth": "legacy:secret", "token": "legacy-token"}},
		"migrated_custom_channels": map[string]string{"team": "https://legacy-private.test/team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func defaultsFixture(t *testing.T) (Service, MirrorDefaults) {
	t.Helper()
	s := fixture(t)
	s.Env = append(s.Env, "CONDA_CHANNELS=", "CONDA_CHANNEL_ALIAS=https://env-alias.test", "PIP_INDEX_URL=http://pip:user@original-pip.test/simple?access=private", "CONDA_ENVS_PATH=/not-a-mirror", "PIP_CACHE_DIR=/not-a-mirror", "PIP_EXTRA_INDEX_URL=https://extra.test/simple")
	s.Runner = func(name string, args, env []string) ([]byte, error) {
		if !reflect.DeepEqual(env, s.Env) {
			t.Fatal("snapshot injected a desired shell environment")
		}
		if name == "conda" {
			if !reflect.DeepEqual(args, []string{"config", "--show", "channels", "default_channels", "custom_channels", "custom_multichannels", "channel_alias", "migrated_channel_aliases", "migrated_custom_channels", "--json"}) {
				t.Fatalf("unexpected Conda snapshot command: %v", args)
			}
			return defaultsCondaJSON(t), nil
		}
		if name == "python" && reflect.DeepEqual(args, []string{"-m", "pip", "config", "list"}) {
			return []byte("global.index-url='https://global.test/simple'\ninstall.index-url='http://native:user@install.test/simple?token=original'\nglobal.extra-index-url='https://extra.test/simple'\n"), nil
		}
		t.Fatalf("unexpected snapshot operation: %s %v", name, args)
		return nil, nil
	}
	d, err := s.CaptureDefaults(MirrorDefaults{}, "all")
	if err != nil {
		t.Fatal(err)
	}
	return s, d
}

func TestDefaultsCapturePreservesPrivateAddressesAndMirrorVariablesOnly(t *testing.T) {
	s, d := defaultsFixture(t)
	if !d.CondaCaptured || !d.PipCaptured || d.PipIndexURL != "http://pip:user@original-pip.test/simple?access=private" {
		t.Fatal("capture state or effective pip default lost")
	}
	for _, address := range []string{"http://user:password@original.test/t/private-token/pkgs/main", "https://person:secret@cloud.test/cloud/t/token", "http://staff:pwd@intranet.test/team?key=source", "https://alias:secret@alias.test/cloud/t/alias-token?key=alias", "file:///original/local-channel", "https://legacy:secret@legacy-alias.test/cloud/t/legacy-token", "https://legacy-private.test/team"} {
		if !strings.Contains(d.CondaYAML, address) {
			t.Fatalf("original authenticated source was lost: expected source count=%d", len(address))
		}
	}
	if value, ok := d.CondaVariables["CONDA_CHANNELS"]; !ok || value != "" || len(d.CondaVariables) != 2 {
		t.Fatal("explicit empty mirror variable was not preserved exactly")
	}
	if len(d.PipVariables) != 1 || d.PipVariables["PIP_INDEX_URL"] != d.PipIndexURL {
		t.Fatal("pip snapshot included unrelated configuration or lost original index")
	}
	if err := ValidateDefaults(d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(s.Root); !os.IsNotExist(err) {
		t.Fatal("capture created an overlay or data directory")
	}
	data, err := json.Marshal(d)
	if err != nil || bytes.Contains(data, []byte("secret")) || bytes.Contains(data, []byte("original")) || bytes.Contains(data, []byte("password")) {
		t.Fatal("JSON view exposed private baseline values")
	}
	if string(data) != `{"conda_captured":true,"pip_captured":true}` {
		t.Fatalf("unexpected public capture state: %s", data)
	}
}

func TestDefaultsNewCaptureRequiresMigrationRulesAndLegacySnapshotIsImmutable(t *testing.T) {
	s, d := defaultsFixture(t)
	root, err := parseDefaultYAML(d.CondaYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Content) != len(defaultMirrorKeys)*2 {
		t.Fatal("new snapshot did not retain migration rules")
	}
	root.Content = root.Content[:legacyDefaultMirrorKeys*2]
	legacy, err := encodeDefaultYAML(root)
	if err != nil {
		t.Fatal(err)
	}
	d.CondaYAML = legacy
	if err := ValidateDefaults(d); err != nil {
		t.Fatalf("existing five-key snapshot rejected: %v", err)
	}
	s.Runner = func(string, []string, []string) ([]byte, error) {
		t.Fatal("existing snapshot was recaptured")
		return nil, nil
	}
	got, err := s.CaptureDefaults(d, "all")
	if err != nil || !reflect.DeepEqual(got, d) {
		t.Fatal("legacy first-captured default was replaced")
	}
	var raw map[string]any
	if err := json.Unmarshal(defaultsCondaJSON(t), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"migrated_channel_aliases", "migrated_custom_channels"} {
		complete := map[string]any{}
		for key, value := range raw {
			complete[key] = value
		}
		delete(complete, key)
		data, _ := json.Marshal(complete)
		if _, err := captureCondaYAML(data); err == nil {
			t.Fatalf("new capture silently omitted %s", key)
		}
	}
}

func TestDefaultsValidateChecksMigrationRulesAndReadsLegacySnapshots(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, changed := range []string{"", "migrated_channel_aliases", "migrated_custom_channels"} {
			s, d := defaultsFixture(t)
			if legacy {
				root, err := parseDefaultYAML(d.CondaYAML)
				if err != nil {
					t.Fatal(err)
				}
				root.Content = root.Content[:legacyDefaultMirrorKeys*2]
				d.CondaYAML, err = encodeDefaultYAML(root)
				if err != nil {
					t.Fatal(err)
				}
			}
			var actual map[string]any
			if err := json.Unmarshal(defaultsCondaJSON(t), &actual); err != nil {
				t.Fatal(err)
			}
			switch changed {
			case "migrated_channel_aliases":
				actual[changed] = []string{}
			case "migrated_custom_channels":
				actual[changed] = map[string]string{}
			}
			data, _ := json.Marshal(actual)
			s.Runner = func(string, []string, []string) ([]byte, error) { return data, nil }
			c := Config{CondaMirror: "default", Defaults: d}
			activate(&s, c)
			_, err := s.Validate(c)
			if want := legacy || changed == ""; (err == nil) != want {
				t.Fatalf("legacy=%v changed=%q accepted=%v expected=%v err=%v", legacy, changed, err == nil, want, err)
			}
		}
	}
}

func TestDefaultsCaptureIsIdempotentAndAllFailureKeepsOriginal(t *testing.T) {
	s, d := defaultsFixture(t)
	s.Runner = func(string, []string, []string) ([]byte, error) {
		t.Fatal("repeated capture read upstream")
		return nil, nil
	}
	got, err := s.CaptureDefaults(d, "all")
	if err != nil || !reflect.DeepEqual(got, d) {
		t.Fatal("repeated capture replaced original baseline")
	}
	s.Runner = func(name string, _, _ []string) ([]byte, error) {
		if name == "conda" {
			return defaultsCondaJSON(t), nil
		}
		return nil, fmt.Errorf("sensitive-token-value")
	}
	got, err = s.CaptureDefaults(MirrorDefaults{}, "all")
	if err == nil || !reflect.DeepEqual(got, MirrorDefaults{}) || strings.Contains(err.Error(), "sensitive-token-value") {
		t.Fatal("partial capture was committed or error leaked upstream values")
	}
	partial := MirrorDefaults{PipCaptured: true, PipIndexURL: "http://original-pip.test/simple"}
	got, err = s.CaptureDefaults(partial, "conda")
	if err != nil || !got.CondaCaptured || got.PipIndexURL != partial.PipIndexURL {
		t.Fatal("single-tool capture did not retain existing other-tool baseline")
	}
}

func TestDefaultsPipEffectiveIndexUsesNonemptyEnvironmentThenInstallThenGlobal(t *testing.T) {
	for _, row := range []struct {
		env    []string
		config string
		want   string
	}{
		{[]string{"PIP_INDEX_URL=http://u:p@env.test/simple?token=one"}, "install.index-url='https://install.test/simple'\n", "http://u:p@env.test/simple?token=one"},
		{[]string{"PIP_INDEX_URL="}, "global.index-url='https://global.test/simple'\ninstall.index-url='http://u:p@install.test/simple?token=two'\n", "http://u:p@install.test/simple?token=two"},
		{nil, "global.index-url='https://global.test/simple'\n", "https://global.test/simple"},
		{nil, "", "https://pypi.org/simple"},
	} {
		s := fixture(t)
		s.Env = append(s.Env, row.env...)
		s.Runner = func(name string, _, _ []string) ([]byte, error) {
			if name != "python" {
				t.Fatal("pip-only capture required Conda")
			}
			return []byte(row.config), nil
		}
		d, err := s.CaptureDefaults(MirrorDefaults{}, "pip")
		if err != nil || d.PipIndexURL != row.want || d.CondaCaptured {
			t.Fatalf("pip precedence mismatch: err=%v captured=%v", err, d.PipCaptured)
		}
		if len(row.env) != 0 {
			if value, ok := d.PipVariables["PIP_INDEX_URL"]; !ok || value != strings.TrimPrefix(row.env[0], "PIP_INDEX_URL=") {
				t.Fatal("pip explicit environment presence was lost")
			}
		}
	}
}

func TestDefaultsOverlayIsStableAndRestoresAllCapturedMirrorKeys(t *testing.T) {
	s, d := defaultsFixture(t)
	path, data := s.DefaultMirrorFile(d)
	if !strings.HasPrefix(path, filepath.Join(s.Root, "conda", "mirror-")) || !strings.HasSuffix(path, ".yaml") || string(data) != d.CondaYAML {
		t.Fatal("default overlay did not use captured source")
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(root.Content[0].Content); i += 2 {
		if root.Content[0].Content[i].LineComment != "#!final" && root.Content[0].Content[i+1].LineComment != "#!final" {
			t.Fatal("default overlay allows original lists/maps to merge with a later source")
		}
		if root.Content[0].Content[i].Value == "custom_multichannels" {
			multichannels := root.Content[0].Content[i+1]
			for j := 0; j < len(multichannels.Content); j += 2 {
				if multichannels.Content[j].LineComment != "#!final" && multichannels.Content[j+1].LineComment != "#!final" {
					t.Fatal("multichannel entry allows older source lists to merge into the captured baseline")
				}
			}
		}
	}
	first, second := s.DefaultMirrorFile(d)
	if first != path || !bytes.Equal(second, data) {
		t.Fatal("captured overlay changed across reuse")
	}
	if path, data := s.DefaultMirrorFile(MirrorDefaults{}); path != "" || data != nil {
		t.Fatal("uncaptured default emitted an overlay")
	}
}

func TestDefaultsPresetChannelsPreservesOrderPrivateSourcesAndSubchannels(t *testing.T) {
	_, d := defaultsFixture(t)
	for _, mirror := range []string{"official", "tuna"} {
		defaults, _ := mirrorChannels(mirror)
		want := []string{defaults[0], "defaults", "conda-forge/label/dev", "https://untouched.private.test/repo", "bioconda"}
		if got := PresetChannels(d, mirror); !reflect.DeepEqual(got, want) {
			t.Fatalf("preset %s changed channel order/private source or missed full URL", mirror)
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(defaultsCondaJSON(t), &raw); err != nil {
		t.Fatal(err)
	}
	raw["channels"] = []string{}
	data, _ := json.Marshal(raw)
	content, err := captureCondaYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	d.CondaYAML = content
	if _, err := parseDefaultYAML(content); err != nil {
		t.Fatal(err)
	}
	if got := PresetChannels(d, "tuna"); got == nil || len(got) != 0 {
		t.Fatal("empty baseline channels acquired implicit defaults")
	}
}

func TestDefaultsPresetChannelsRecognizesChannelsOnlyStandardMirrorURLs(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(configJSON(t, "official", false), &raw); err != nil {
		t.Fatal(err)
	}
	original := []string{
		"https://user:secret@mirrors.aliyun.com/anaconda/pkgs/main/t/suffix-token/linux-64?token=source",
		"https://mirrors.aliyun.com/anaconda/pkgs/r/label/legacy",
		"http://mirrors.aliyun.com/anaconda/pkgs/msys2",
		"https://mirrors.tuna.tsinghua.edu.cn/t/private-token/anaconda/cloud/conda-forge/label/nightly?access=private#fragment",
		"https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud/bioconda/linux-64",
		"https://private.example/team?token=keep-private",
		"https://private.example/pkgs/main-extra",
		"file:///private/pkgs/main",
	}
	raw["channels"] = original
	data, _ := json.Marshal(raw)
	content, err := captureCondaYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	d := MirrorDefaults{CondaCaptured: true, CondaYAML: content}
	for _, mirror := range []string{"official", "tuna"} {
		defaults, _ := mirrorChannels(mirror)
		want := []string{defaults[0] + "/linux-64", defaults[1] + "/label/legacy", defaults[2], "conda-forge/label/nightly", "bioconda/linux-64", original[5], original[6], original[7]}
		got := PresetChannels(d, mirror)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s did not remap standard channels-only mirrors while preserving private sources", mirror)
		}
		for _, value := range got[:5] {
			if strings.Contains(value, "secret") || strings.Contains(value, "token") || strings.Contains(value, "?") || strings.Contains(value, "#") || strings.Contains(value, "@") {
				t.Fatal("a public preset inherited authentication from its original mirror")
			}
		}
	}
	root, err := parseDefaultYAML(d.CondaYAML)
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range root.Content[1].Content {
		if value.Value != original[i] {
			t.Fatal("default snapshot lost an original address or its authentication")
		}
	}
}

func TestDefaultsPresetSnapshotMappingTakesPrecedenceAndDropsOldQuery(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(configJSON(t, "official", false), &raw); err != nil {
		t.Fatal(err)
	}
	raw["custom_channels"].(map[string]any)["conda-forge"] = "https://user:secret@private.test/pkgs/main/conda-forge?key=source"
	raw["channels"] = []string{"https://user:secret@private.test/pkgs/main/conda-forge/label/team?key=source"}
	data, _ := json.Marshal(raw)
	content, err := captureCondaYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	got := PresetChannels(MirrorDefaults{CondaCaptured: true, CondaYAML: content}, "tuna")
	if !reflect.DeepEqual(got, []string{"conda-forge/label/team"}) {
		t.Fatal("generic standard-name detection overrode the explicit captured channel map or retained an old query")
	}
}

func TestDefaultsPresetDoesNotGuessPrivateRepositoriesFromPathNames(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(configJSON(t, "official", false), &raw); err != nil {
		t.Fatal(err)
	}
	original := []string{
		"https://private.example/team/pkgs/main",
		"https://private.example/anaconda/cloud/conda-forge/label/team",
		"https://repo.anaconda.com.private.example/pkgs/main",
		"https://repo.anaconda.com/team/pkgs/main",
		"https://repo.anaconda.com:8443/pkgs/main",
	}
	raw["channels"] = original
	data, _ := json.Marshal(raw)
	content, err := captureCondaYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	d := MirrorDefaults{CondaCaptured: true, CondaYAML: content}
	if got := PresetChannels(d, "tuna"); !reflect.DeepEqual(got, original) {
		t.Fatalf("unregistered private repository rewritten to public preset: %v", got)
	}
}

func TestDefaultsPresetRemovesEncodedTokenSegments(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(configJSON(t, "official", false), &raw); err != nil {
		t.Fatal(err)
	}
	raw["channels"] = []string{"https://repo.anaconda.com/pkgs/main/%74/encoded-secret/linux-64", "https://repo.anaconda.com/pkgs/r/t%2Fembedded-secret/linux-64"}
	data, _ := json.Marshal(raw)
	content, err := captureCondaYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	defaults, _ := mirrorChannels("tuna")
	got := PresetChannels(MirrorDefaults{CondaCaptured: true, CondaYAML: content}, "tuna")
	want := []string{defaults[0] + "/linux-64", defaults[1] + "/linux-64"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a public preset inherited encoded source authentication: %v", got)
	}
}

func TestDefaultsPipCaptureRejectsEscapedControlCharacters(t *testing.T) {
	s := fixture(t)
	s.Runner = func(string, []string, []string) ([]byte, error) {
		return []byte("global.index-url='https://private.example/simple\\nother'\n"), nil
	}
	if _, err := s.CaptureDefaults(MirrorDefaults{}, "pip"); err == nil {
		t.Fatal("captured a Python repr escape instead of rejecting the actual newline")
	}
}

func TestDefaultsValidationRejectsArbitraryConfigurationAndUnsafeSchemas(t *testing.T) {
	_, original := defaultsFixture(t)
	for _, change := range []func(*MirrorDefaults){
		func(d *MirrorDefaults) { d.CondaCaptured = false },
		func(d *MirrorDefaults) {
			d.CondaYAML = strings.Replace(d.CondaYAML, "channels: #!final", "channels:", 1)
		},
		func(d *MirrorDefaults) { d.CondaYAML += "ssl_verify: false\n" },
		func(d *MirrorDefaults) { d.CondaYAML += "---\nchannels: []\n" },
		func(d *MirrorDefaults) { d.CondaVariables = map[string]string{"CONDA_ENVS_PATH": "/not-mirror"} },
		func(d *MirrorDefaults) {
			d.PipVariables = map[string]string{"PIP_EXTRA_INDEX_URL": "https://extra.test/simple"}
		},
		func(d *MirrorDefaults) { d.PipIndexURL = "https://user:password@private.test/simple\ninvalid" },
	} {
		d := original
		change(&d)
		if err := ValidateDefaults(d); err == nil {
			t.Fatal("accepted unsafe or malformed default snapshot")
		}
	}
	keys := MirrorVariableNames()
	keys[0] = "CONDA_ENVS_PATH"
	if MirrorVariableNames()[0] == "CONDA_ENVS_PATH" {
		t.Fatal("caller changed exported ownership whitelist")
	}
}
