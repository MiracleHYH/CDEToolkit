package conda

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) Service {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Service{Root: filepath.Join(home, "nido"), UserHome: home, Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin"}}
}

func activate(s *Service, c Config) {
	for key, value := range s.Variables(c) {
		s.Env = append(s.Env, key+"="+value)
	}
}

func TestNormalizePathDoesNotCreateDataAndKeepsTildeNames(t *testing.T) {
	s := fixture(t)
	for input, expected := range map[string]string{
		"~/data/envs": filepath.Join(s.UserHome, "data/envs"),
		"~/~":         filepath.Join(s.UserHome, "~"),
		filepath.Join(s.UserHome, "one", "..", "two"): filepath.Join(s.UserHome, "two"),
	} {
		got, err := s.NormalizePath(input)
		if err != nil || got != expected {
			t.Fatalf("NormalizePath(%q) = %q, %v; want %q", input, got, err, expected)
		}
		if _, err := os.Stat(got); !os.IsNotExist(err) {
			t.Fatalf("normalization created %s", got)
		}
	}
	// Home itself contains the Nido software root, so it cannot be a data root.
	if _, err := s.NormalizePath("~"); err == nil {
		t.Fatal("accepted ancestor of Nido root")
	}
}

func TestNormalizePathRejectsExpansionListsFilesAndAliasedOwnership(t *testing.T) {
	s := fixture(t)
	file := filepath.Join(s.UserHome, "file")
	if err := os.WriteFile(file, []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.Root, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(s.UserHome, "alias")
	if err := os.Symlink(s.Root, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "relative", "/tmp/$HOME/envs", "/tmp/${USER}/envs", "/tmp/one:/tmp/two", "/tmp/new\nline", s.Root, filepath.Join(s.Root, "envs"), s.UserHome, file, filepath.Join(file, "missing", "envs"), filepath.Join(alias, "envs")} {
		if _, err := s.NormalizePath(path); err == nil {
			t.Fatalf("accepted invalid directory %q", path)
		}
	}
	data := filepath.Join(s.UserHome, "physical data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.UserHome, "data alias")
	if err := os.Symlink(data, link); err != nil {
		t.Fatal(err)
	}
	got, err := s.NormalizePath(filepath.Join(link, "envs"))
	if err != nil || got != filepath.Join(data, "envs") {
		t.Fatalf("physical ancestor not resolved: %q %v", got, err)
	}
}

func TestValidateConfigDoesNotDependOnDataDiskAvailability(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.UserHome, "data")
	c := Config{EnvsDir: path}
	if err := os.WriteFile(path, []byte("path was replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateConfig(c); err != nil {
		t.Fatalf("data path state blocked loading/reset: %v", err)
	}
	activate(&s, c)
	if issues := s.Issues(c); len(issues) == 0 || !strings.Contains(strings.Join(issues, ";"), "不是目录") {
		t.Fatalf("bad data path not diagnosed: %v", issues)
	}
	for _, c := range []Config{{PkgsDir: filepath.Join(s.UserHome, "a,b")}, {PkgsDir: filepath.Join(s.UserHome, "pkgs ")}, {EnvsDir: filepath.Join(s.UserHome, "envs ")}, {EnvsDir: s.Root}, {EnvsDir: "/tmp/$HOME"}, {EnvsDir: "/tmp/../tmp/envs"}, {CondaMirror: "ustc"}, {PipIndexURL: "tuna"}} {
		if err := s.ValidateConfig(c); err == nil {
			t.Fatalf("accepted invalid stored config: %#v", c)
		}
	}
	if err := s.ValidateConfig(Config{PipCacheDir: filepath.Join(s.UserHome, "pip cache "), EnvsDir: filepath.Join(s.UserHome, "envs,1")}); err != nil {
		t.Fatalf("comma restriction leaked beyond pkgs dirs: %v", err)
	}
}

func TestPipMirrorOnlyAcceptsHTTPSUnauthenticatedSimpleIndexes(t *testing.T) {
	for input, expected := range map[string]string{
		"official": "https://pypi.org/simple", "tuna": "https://pypi.tuna.tsinghua.edu.cn/simple", "ustc": "https://mirrors.ustc.edu.cn/pypi/simple",
		"https://example.test/pypi/simple/": "https://example.test/pypi/simple", "https://[::1]:8443/simple": "https://[::1]:8443/simple",
	} {
		got, err := PipMirror(input)
		if err != nil || got != expected {
			t.Fatalf("PipMirror(%q) = %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"", "http://example.test/simple", "https://user:secret@example.test/simple", "https://example.test/simple?token=secret", "https://example.test/simple#fragment", "https://example.test/packages", "https://example.test:0/simple", "https://example.test:99999/simple", "https://example.test:/simple", "https://example.test/si%6dple", "https://example.test/new\nline/simple", "https://example.test/pip'quote/simple"} {
		if _, err := PipMirror(input); err == nil {
			t.Fatalf("accepted invalid mirror %q", input)
		}
	}
}

func TestMirrorOverlayIsContentAddressedAndDoesNotChangeChannelSelection(t *testing.T) {
	s := fixture(t)
	if path, data := s.MirrorFile(Config{}); path != "" || data != nil {
		t.Fatal("empty mirror produced overlay")
	}
	paths := map[string]bool{}
	for _, mirror := range []string{"official", "tuna"} {
		path, data := s.MirrorFile(Config{CondaMirror: mirror})
		want := filepath.Join(s.Root, "conda", fmt.Sprintf("mirror-%x.yaml", sha256.Sum256(data)))
		if path != want || paths[path] {
			t.Fatal("overlay path not content-addressed or distinct")
		}
		paths[path] = true
		text := string(data)
		if !strings.Contains(text, "default_channels: #!final\n") || !strings.Contains(text, "custom_multichannels: #!final\n  defaults: #!final\n") || strings.Contains(text, "\nchannels:") || strings.Contains(text, "channel_alias:") {
			t.Fatalf("overlay altered selection or lost final-list semantics:\n%s", text)
		}
		for _, name := range []string{"main", "r", "msys2", "conda-forge", "bioconda"} {
			if !strings.Contains(text, name) {
				t.Fatalf("overlay omitted %s", name)
			}
		}
		otherPath, otherData := s.MirrorFile(Config{CondaMirror: mirror, EnvsDir: filepath.Join(s.UserHome, "envs")})
		if path != otherPath || !bytes.Equal(data, otherData) {
			t.Fatal("data directory changed mirror overlay")
		}
		if _, err := os.Lstat(s.Root); !os.IsNotExist(err) {
			t.Fatal("overlay calculation created files")
		}
	}
}

func TestIssuesChecksActivationAndPublicOverridesWithoutRunningTools(t *testing.T) {
	s := fixture(t)
	c := Config{CondaMirror: "tuna", PipIndexURL: "https://pypi.org/simple", EnvsDir: filepath.Join(s.UserHome, "envs"), PkgsDir: filepath.Join(s.UserHome, "pkgs"), PipCacheDir: filepath.Join(s.UserHome, "pip cache")}
	s.Runner = func(string, []string, []string) ([]byte, error) {
		t.Fatal("static check ran upstream")
		return nil, nil
	}
	if issues := s.Issues(c); len(issues) != 2 {
		t.Fatalf("missing activation not diagnosed per variable: %v", issues)
	}
	activate(&s, c)
	if issues := s.Issues(c); len(issues) != 0 {
		t.Fatalf("activated valid config has static issues: %v", issues)
	}
	s.Env = append(s.Env, "PIP_NO_CACHE_DIR=false", "PIP_EXTRA_INDEX_URL=https://private.test/simple", "PIP_NO_INDEX=1", "CONDA_DEFAULT_CHANNELS=https://old.test/main", "CONDA_ENVS_DIRS=/old/envs")
	issues := strings.Join(s.Issues(c), "\n")
	for _, key := range []string{"PIP_NO_CACHE_DIR", "PIP_EXTRA_INDEX_URL", "PIP_NO_INDEX", "CONDA_DEFAULT_CHANNELS", "CONDA_ENVS_DIRS"} {
		if !strings.Contains(issues, key) {
			t.Fatalf("override %s not diagnosed: %s", key, issues)
		}
	}
	if issues := s.Issues(Config{}); len(issues) != 0 {
		t.Fatalf("unmanaged config reported shell conflicts: %v", issues)
	}
}

func TestIssuesDiagnosesOnlyUnchangedWithdrawnShellExports(t *testing.T) {
	s := fixture(t)
	c := Config{CondaMirror: "tuna", PipIndexURL: "https://pypi.org/simple", EnvsDir: filepath.Join(s.UserHome, "envs"), PkgsDir: filepath.Join(s.UserHome, "pkgs"), PipCacheDir: filepath.Join(s.UserHome, "pip cache")}
	activate(&s, c)
	for key, value := range s.Variables(c) {
		s.Env = append(s.Env, "_NIDO_CONDA_"+key+"_ACTIVE=1", "_NIDO_CONDA_"+key+"_VALUE="+value)
	}
	if issues := s.Issues(c); len(issues) != 0 {
		t.Fatalf("current managed exports marked as withdrawn: %v", issues)
	}
	issues := s.Issues(Config{})
	if len(issues) != 2 {
		t.Fatalf("reset shell exports not diagnosed: %v", issues)
	}
	for _, key := range []string{"CONDARC", "PIP_INDEX_URL"} {
		if !strings.Contains(strings.Join(issues, "\n"), "已撤销的 "+key) {
			t.Fatalf("withdrawn %s missing from diagnostics: %v", key, issues)
		}
	}
	// A manually replaced value, an unset value, or a marker with no proven
	// prior value cannot authorize cleanup and must not be called stale.
	s.Env = append(s.Env, "PIP_INDEX_URL=https://user.test/simple", "_NIDO_CONDA_CONDA_PKGS_DIRS_ACTIVE=0")
	filtered := s.Env[:0]
	for _, row := range s.Env {
		if !strings.HasPrefix(row, "CONDA_ENVS_PATH=") && !strings.HasPrefix(row, "_NIDO_CONDA_PIP_CACHE_DIR_VALUE=") {
			filtered = append(filtered, row)
		}
	}
	s.Env = filtered
	issues = s.Issues(Config{})
	if len(issues) != 1 || !strings.Contains(issues[0], "已撤销的 CONDARC") {
		t.Fatalf("user-owned or unproven shell values reported stale: %v", issues)
	}
}

func configJSON(t *testing.T, mirror string, objects bool) []byte {
	t.Helper()
	defaults, custom := mirrorChannels(mirror)
	encode := func(value string) any {
		if !objects {
			return value
		}
		parts := strings.SplitN(strings.TrimPrefix(value, "https://"), "/", 2)
		return map[string]any{"scheme": "https", "location": parts[0], "name": parts[1], "auth": nil, "token": nil, "platform": nil, "package_filename": nil}
	}
	channels := []any{}
	for _, value := range defaults {
		channels = append(channels, encode(value))
	}
	customChannels := map[string]any{}
	for name, base := range custom {
		customChannels[name] = encode(base + "/" + name)
	}
	customChannels["private"] = "https://private.test/channel"
	data, err := json.Marshal(map[string]any{"channels": []string{"defaults", "conda-forge"}, "channel_alias": "https://conda.anaconda.org", "default_channels": channels, "custom_channels": customChannels, "custom_multichannels": map[string]any{"defaults": channels, "private": []string{"https://private.test/channel"}}, "migrated_channel_aliases": []string{}, "migrated_custom_channels": map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestValidateUsesCurrentEnvironmentAndAcceptsCondaChannelSerializationVersions(t *testing.T) {
	for _, objects := range []bool{false, true} {
		s := fixture(t)
		c := Config{CondaMirror: "tuna"}
		calls := 0
		s.Runner = func(name string, args, env []string) ([]byte, error) {
			calls++
			if name != "conda" || !reflect.DeepEqual(args, []string{"config", "--show", "channels", "default_channels", "custom_channels", "custom_multichannels", "channel_alias", "migrated_channel_aliases", "migrated_custom_channels", "--json"}) || !reflect.DeepEqual(env, s.Env) {
				t.Fatalf("unexpected command or desired environment injection: %s %v %v", name, args, env)
			}
			return configJSON(t, "tuna", objects), nil
		}
		if _, err := s.Validate(c); err == nil || calls != 0 {
			t.Fatal("unactivated config passed or ran command")
		}
		activate(&s, c)
		if report, err := s.Validate(c); err != nil || len(report) != 1 || calls != 1 {
			t.Fatalf("objects=%v report=%v calls=%d error=%v", objects, report, calls, err)
		}
	}
}

func TestValidateRejectsMixedOrOverriddenCondaMirrors(t *testing.T) {
	for _, field := range []string{"default_channels", "custom_channels", "custom_multichannels"} {
		s := fixture(t)
		c := Config{CondaMirror: "tuna"}
		activate(&s, c)
		var data map[string]any
		if err := json.Unmarshal(configJSON(t, "tuna", false), &data); err != nil {
			t.Fatal(err)
		}
		switch field {
		case "default_channels":
			data[field] = append(data[field].([]any), "https://old.test/main")
		case "custom_channels":
			data[field].(map[string]any)["bioconda"] = "https://old.test/bioconda"
		case "custom_multichannels":
			data[field].(map[string]any)["defaults"] = []string{"https://old.test/main"}
		}
		b, _ := json.Marshal(data)
		s.Runner = func(string, []string, []string) ([]byte, error) { return b, nil }
		if _, err := s.Validate(c); err == nil {
			t.Fatalf("accepted inconsistent %s", field)
		}
	}
}

func TestValidatePresetChecksEffectiveChannelsAgainstCapturedSelection(t *testing.T) {
	var original map[string]any
	if err := json.Unmarshal(configJSON(t, "official", false), &original); err != nil {
		t.Fatal(err)
	}
	original["channels"] = []string{"https://repo.anaconda.com/pkgs/main", "conda-forge"}
	data, _ := json.Marshal(original)
	baseline, err := captureCondaYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	d := MirrorDefaults{CondaCaptured: true, CondaYAML: baseline}
	for _, row := range []struct {
		name     string
		channels []string
		ok       bool
	}{{"old explicit URL survives final", []string{"https://repo.anaconda.com/pkgs/main", "conda-forge"}, false}, {"remapped selection", PresetChannels(d, "tuna"), true}, {"changed channel order", []string{"conda-forge", "https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/main"}, false}, {"missing query field", nil, false}} {
		t.Run(row.name, func(t *testing.T) {
			s := fixture(t)
			c := Config{CondaMirror: "tuna", Defaults: d}
			activate(&s, c)
			var effective map[string]any
			if err := json.Unmarshal(configJSON(t, "tuna", false), &effective); err != nil {
				t.Fatal(err)
			}
			effective["channels"] = row.channels
			if row.channels == nil {
				delete(effective, "channels")
			}
			data, _ := json.Marshal(effective)
			s.Runner = func(string, []string, []string) ([]byte, error) { return data, nil }
			_, err := s.Validate(c)
			if (err == nil) != row.ok {
				t.Fatalf("effective channels accepted=%v expected=%v err=%v", err == nil, row.ok, err)
			}
		})
	}
}

func TestValidatePipConfigurationAndCacheWithoutWriting(t *testing.T) {
	s := fixture(t)
	c := Config{PipIndexURL: "https://pypi.org/simple", PipCacheDir: filepath.Join(s.UserHome, "pip cache")}
	activate(&s, c)
	calls := []string{}
	s.Runner = func(name string, args, env []string) ([]byte, error) {
		if name != "python" || !reflect.DeepEqual(env, s.Env) {
			t.Fatalf("unexpected interpreter or modified shell environment: %s %v", name, env)
		}
		command := strings.Join(args, " ")
		calls = append(calls, command)
		switch command {
		case "-m pip config list":
			// Explicit env index overrides native command-specific indexes.
			return []byte(":env:.index-url='" + c.PipIndexURL + "'\n:env:.cache-dir='" + c.PipCacheDir + "'\ninstall.index-url='https://old.test/simple'\nglobal.cert=\"C:\\\\unrelated\\\\certificate.pem\"\n"), nil
		case "-m pip cache dir":
			return []byte(c.PipCacheDir + "\n"), nil
		default:
			t.Fatalf("unexpected pip operation: %v", args)
			return nil, nil
		}
	}
	if report, err := s.Validate(c); err != nil || len(report) != 2 || len(calls) != 2 {
		t.Fatalf("report=%v calls=%v err=%v", report, calls, err)
	}
	if _, err := os.Stat(c.PipCacheDir); !os.IsNotExist(err) {
		t.Fatal("validation created cache")
	}
}

func TestValidatePipDetectsNativeConflictsAndWrongEffectiveValues(t *testing.T) {
	for _, extra := range []string{"global.extra-index-url='https://private.test/simple'\n", "install.no-index='true'\n", "global.no-cache-dir='false'\n"} {
		s := fixture(t)
		c := Config{PipIndexURL: "https://pypi.org/simple", PipCacheDir: filepath.Join(s.UserHome, "pip cache")}
		activate(&s, c)
		s.Runner = func(string, []string, []string) ([]byte, error) {
			return []byte(":env:.index-url='" + c.PipIndexURL + "'\n" + extra), nil
		}
		if _, err := s.Validate(c); err == nil {
			t.Fatalf("accepted native pip conflict %q", extra)
		}
	}
	for _, data := range [][]byte{[]byte("not json"), bytes.Repeat([]byte("x"), maxOutput+1)} {
		s := fixture(t)
		c := Config{CondaMirror: "official"}
		activate(&s, c)
		s.Runner = func(string, []string, []string) ([]byte, error) { return data, nil }
		if _, err := s.Validate(c); err == nil {
			t.Fatal("accepted invalid or oversized upstream output")
		}
	}
}

func TestValidatePipCachePreservesTrailingSpacesAndRequiresOneOutputLine(t *testing.T) {
	for _, row := range []struct {
		name, suffix string
		ok           bool
	}{{"newline", " \n", true}, {"windows newline", " \r\n", true}, {"no newline", " ", true}, {"space missing", "\n", false}, {"extra empty line", " \n\n", false}, {"extra text line", " \nother\n", false}} {
		t.Run(row.name, func(t *testing.T) {
			s := fixture(t)
			base := filepath.Join(s.UserHome, "pip cache")
			c := Config{PipCacheDir: base + " "}
			activate(&s, c)
			s.Runner = func(name string, args, env []string) ([]byte, error) {
				if strings.Join(args, " ") == "-m pip config list" {
					return []byte(":env:.cache-dir='" + c.PipCacheDir + "'\n"), nil
				}
				return []byte(base + row.suffix), nil
			}
			_, err := s.Validate(c)
			if (err == nil) != row.ok {
				t.Fatalf("cache line %q accepted=%v, expected=%v, err=%v", base+row.suffix, err == nil, row.ok, err)
			}
		})
	}
}

func TestPipEnvironmentBooleanOverrideTakesPrecedence(t *testing.T) {
	options, err := pipOptions([]byte("global.no-index='true'\n:env:.no-index='false'\nglobal.extra-index-url='https://old.test/simple'\n:env:.extra-index-url='https://new.test/simple'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if pipConflict(options, "no-index", truthy) || !pipConflict(options, "extra-index-url", func(value string) bool { return value != "" }) {
		t.Fatal("pip environment override order is incorrect")
	}
}

func TestPipConflictUsesInstallSectionPrecedence(t *testing.T) {
	options, err := pipOptions([]byte("global.no-index='true'\ninstall.no-index='false'\ndownload.no-index='true'\ndownload.extra-index-url='https://download-only.test/simple'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if pipConflict(options, "no-index", truthy) || pipConflict(options, "extra-index-url", func(value string) bool { return value != "" }) {
		t.Fatal("unrelated or overridden pip command configuration reported as install conflict")
	}
}

func TestPipOptionsDecodesPythonReprExactly(t *testing.T) {
	for _, row := range []struct{ encoded, want string }{
		{`'https://private.test/a\\b/simple'`, `https://private.test/a\b/simple`},
		{`'https://private.test/a\x85b/simple'`, "https://private.test/a\u0085b/simple"},
		{`"https://private.test/a'b/simple"`, "https://private.test/a'b/simple"},
		{`'https://private.test/a\\\'b/simple'`, `https://private.test/a\'b/simple`},
		{`'\u2028'`, "\u2028"},
	} {
		got, err := pipValue(row.encoded)
		if err != nil || got != row.want {
			t.Fatalf("Python repr decoded incorrectly: %q want=%q err=%v", got, row.want, err)
		}
	}
}

func TestValidatePipCacheRejectsCommandSpecificOverrides(t *testing.T) {
	for _, section := range []string{"install", "download", "wheel", "cache"} {
		t.Run(section, func(t *testing.T) {
			s := fixture(t)
			c := Config{PipCacheDir: filepath.Join(s.UserHome, "desired cache")}
			s.Runner = func(_ string, args, _ []string) ([]byte, error) {
				if strings.Join(args, " ") == "-m pip config list" {
					return []byte("global.cache-dir='" + c.PipCacheDir + "'\n" + section + ".cache-dir='/old/command/cache'\n"), nil
				}
				return []byte(c.PipCacheDir + "\n"), nil
			}
			if _, err := s.Validate(c); err == nil {
				t.Fatal("pip cache dir masked a command-specific cache-dir override")
			}
		})
	}
}
