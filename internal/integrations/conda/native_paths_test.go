package conda

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"go.yaml.in/yaml/v3"
)

func nativeWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func nativeRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNativePathsPersistWithoutResetRestore(t *testing.T) {
	s := fixture(t)
	condarc := filepath.Join(s.UserHome, ".condarc")
	pip, err := s.nativePipPath()
	if err != nil {
		t.Fatal(err)
	}
	condaOriginal := "# user configuration\nchannels:\n  - conda-forge # keep channel\nssl_verify: /custom/certificate.pem\nenvs_dirs: # chosen directory\n  - /old/envs\ncustom_channels:\n  private: https://example.test/channel\n"
	pipOriginal := "# keep header\r\n[global]\r\nindex-url = https://private.example/simple\r\ncache-dir = /old/cache\r\n\r\n[install]\r\ntrusted-host = private.example\r\n"
	nativeWrite(t, condarc, condaOriginal, 0640)
	nativeWrite(t, pip, pipOriginal, 0600)
	next := Config{EnvsDir: filepath.Join(s.UserHome, "new envs"), PkgsDir: filepath.Join(s.UserHome, "new pkgs"), PipCacheDir: filepath.Join(s.UserHome, "new pip")}
	if err := s.CheckPaths(Config{}, next); err != nil {
		t.Fatal(err)
	}
	if nativeRead(t, condarc) != condaOriginal || nativeRead(t, pip) != pipOriginal {
		t.Fatal("preflight wrote native configuration")
	}
	undo, err := s.ChangePaths(Config{}, next)
	if err != nil || undo == nil {
		t.Fatalf("change: %v", err)
	}
	changed := nativeRead(t, condarc)
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(changed), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["envs_dirs"].([]any)[0] != next.EnvsDir || parsed["pkgs_dirs"].([]any)[0] != next.PkgsDir || parsed["ssl_verify"] != "/custom/certificate.pem" {
		t.Fatalf("native paths or unrelated values lost: %s", changed)
	}
	for _, comment := range []string{"# user configuration", "# keep channel", "# chosen directory"} {
		if !strings.Contains(changed, comment) {
			t.Fatalf("comment lost: %s", changed)
		}
	}
	wantPip := strings.Replace(pipOriginal, "cache-dir = /old/cache", "cache-dir = "+next.PipCacheDir, 1)
	if got := nativeRead(t, pip); got != wantPip {
		t.Fatalf("unrelated INI bytes changed: %q", got)
	}
	if info, _ := os.Stat(condarc); info.Mode().Perm() != 0640 {
		t.Fatal("original permissions were not retained")
	}
	for _, path := range []string{next.EnvsDir, next.PkgsDir, next.PipCacheDir} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("data directory was created: %s", path)
		}
	}
	if _, err := s.ChangePaths(next, Config{}); err != nil {
		t.Fatal(err)
	}
	if nativeRead(t, condarc) != changed || nativeRead(t, pip) != wantPip {
		t.Fatal("reset restored or removed native configuration")
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if nativeRead(t, condarc) != condaOriginal || nativeRead(t, pip) != pipOriginal {
		t.Fatal("failed-save compensation did not restore exact original bytes")
	}
}

func TestNativePathsNoopNeverTouchesNativeConfiguration(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.UserHome, ".condarc")
	nativeWrite(t, path, "not valid: [\n", 0600)
	previous := Config{EnvsDir: filepath.Join(s.UserHome, "envs")}
	for _, next := range []Config{previous, {}, {CondaMirror: "tuna", EnvsDir: previous.EnvsDir}} {
		if err := s.CheckPaths(previous, next); err != nil {
			t.Fatal("untouched native file should not be parsed", err)
		}
		undo, err := s.ChangePaths(previous, next)
		if err != nil || undo() != nil {
			t.Fatal("no-op failed", err)
		}
	}
	if got := nativeRead(t, path); got != "not valid: [\n" {
		t.Fatal("no-op changed unrelated file")
	}
}

func TestNativePathsTargetsRespectActiveSearchChain(t *testing.T) {
	s := fixture(t)
	custom := filepath.Join(s.UserHome, "custom.yaml")
	s.Env = append(s.Env, "CONDARC="+custom)
	path, err := s.nativeCondaPath(Config{})
	if err != nil || path != custom {
		t.Fatalf("external CONDARC ignored: %s %v", path, err)
	}
	for _, mirror := range []string{"tuna", "official", "default"} {
		path, err := s.nativeCondaPath(Config{CondaMirror: mirror})
		if err != nil || path != filepath.Join(s.UserHome, ".condarc") {
			t.Fatalf("selected overlay must write ordinary user config: %s %v", path, err)
		}
	}
	s.Env = append(s.Env, "CONDARC="+filepath.Join(s.Root, "conda", "mirror-old.yaml"))
	path, err = s.nativeCondaPath(Config{})
	if err != nil || path != filepath.Join(s.UserHome, ".condarc") {
		t.Fatalf("must never edit an existing Nido overlay: %s %v", path, err)
	}
	pipCustom := filepath.Join(s.UserHome, "custom-pip.conf")
	nativeWrite(t, pipCustom, "[global]\ntimeout=30\n", 0600)
	s.Env = append(s.Env, "PIP_CONFIG_FILE="+pipCustom)
	if path, err := s.nativePipPath(); err != nil || path != pipCustom {
		t.Fatalf("explicit pip file ignored: %s %v", path, err)
	}
	s.Env = append(s.Env, "PIP_CONFIG_FILE="+os.DevNull)
	if _, err := s.nativePipPath(); err == nil {
		t.Fatal("accepted a disabled pip configuration")
	}
}

func TestNativePipConfigurationLocation(t *testing.T) {
	s := fixture(t)
	if runtime.GOOS == "darwin" {
		standard := filepath.Join(s.UserHome, ".config", "pip", "pip.conf")
		nativeWrite(t, standard, "[global]\n", 0600)
		if got, err := s.nativePipPath(); err != nil || got != standard {
			t.Fatalf("created macOS alternate user directory unnecessarily: %s %v", got, err)
		}
		library := filepath.Join(s.UserHome, "Library", "Application Support", "pip")
		if err := os.MkdirAll(library, 0700); err != nil {
			t.Fatal(err)
		}
		if got, err := s.nativePipPath(); err != nil || got != filepath.Join(library, "pip.conf") {
			t.Fatalf("existing macOS native user directory ignored: %s %v", got, err)
		}
		data := filepath.Join(s.UserHome, "xdg-data")
		if err := os.MkdirAll(filepath.Join(data, "pip"), 0700); err != nil {
			t.Fatal(err)
		}
		s.Env = append(s.Env, "XDG_DATA_HOME="+data)
		if got, err := s.nativePipPath(); err != nil || got != filepath.Join(data, "pip", "pip.conf") {
			t.Fatalf("existing XDG_DATA_HOME pip ignored: %s %v", got, err)
		}
	} else {
		base := filepath.Join(s.UserHome, "xdg-config")
		s.Env = append(s.Env, "XDG_CONFIG_HOME="+base)
		if got, err := s.nativePipPath(); err != nil || got != filepath.Join(base, "pip", "pip.conf") {
			t.Fatalf("XDG_CONFIG_HOME ignored: %s %v", got, err)
		}
	}
}

func TestNativeCondaRejectsIgnoredExplicitFilenames(t *testing.T) {
	for _, mirror := range []string{"", "tuna"} {
		for _, name := range []string{"custom.condarc", "custom.conf", "custom.YAML"} {
			t.Run(mirror+"/"+name, func(t *testing.T) {
				s := fixture(t)
				path := filepath.Join(s.UserHome, name)
				original := "envs_dirs: [/old/envs]\n"
				nativeWrite(t, path, original, 0600)
				s.Env = append(s.Env, "CONDARC="+path)
				next := Config{CondaMirror: mirror, EnvsDir: filepath.Join(s.UserHome, "new-envs")}
				if _, err := s.ChangePaths(Config{}, next); err == nil || !strings.Contains(err.Error(), "文件名") {
					t.Fatalf("accepted an ignored explicit CONDARC filename: %v", err)
				}
				if nativeRead(t, path) != original {
					t.Fatal("modified a file Conda ignores")
				}
				if _, err := os.Lstat(filepath.Join(s.UserHome, ".condarc")); !os.IsNotExist(err) {
					t.Fatal("unsafe explicit context partially changed standard config")
				}
			})
		}
	}
	for _, name := range []string{".condarc", "condarc", "custom.yaml", "custom.yml"} {
		s := fixture(t)
		path := filepath.Join(s.UserHome, name)
		s.Env = append(s.Env, "CONDARC="+path)
		if got, err := s.nativeCondaPath(Config{}); err != nil || got != path {
			t.Fatalf("supported explicit CONDARC rejected: %q %v", got, err)
		}
	}
}

func TestNativePathsRejectAmbiguousOrLinkedFiles(t *testing.T) {
	for _, text := range []string{"envs_dirs: []\nenvs_dirs: []\n", "channels: &channels [conda-forge]\ncopy: *channels\n", "channels: []\n---\nchannels: []\n", "[not, a, map]\n", "key:\n  duplicate: true\n  duplicate: false\n"} {
		if _, err := changeCondaPaths([]byte(text), map[string]string{"envs_dirs": "/new/envs"}); err == nil {
			t.Fatalf("accepted ambiguous YAML: %s", text)
		}
	}
	for _, text := range []string{"[global]\ncache-dir=a\ncache-dir=b\n", "[global]\na=1\n[global]\na=2\n", "[global]\ncache-dir=a\ncache_dir=b\n", "[global]\ncache-dir=a\n--cache-dir=b\n", "[global]\ncache-dir=\n  /first\n  /second\n"} {
		if _, err := changePipCache([]byte(text), "/new/cache"); err == nil {
			t.Fatalf("accepted ambiguous INI: %s", text)
		}
	}
	for _, kind := range []string{"symlink", "hardlink", "parent-link"} {
		t.Run(kind, func(t *testing.T) {
			s := fixture(t)
			sentinel := filepath.Join(s.UserHome, "sentinel")
			nativeWrite(t, sentinel, "channels: [conda-forge]\n", 0600)
			target := filepath.Join(s.UserHome, ".condarc")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(sentinel, target)
			case "hardlink":
				err = os.Link(sentinel, target)
			case "parent-link":
				link := filepath.Join(s.UserHome, "alias")
				err = os.Symlink(s.UserHome, link)
				s.Env = append(s.Env, "CONDARC="+filepath.Join(link, ".condarc"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CheckPaths(Config{}, Config{EnvsDir: filepath.Join(s.UserHome, "envs")}); err == nil {
				t.Fatal("accepted linked configuration")
			}
			if got := nativeRead(t, sentinel); got != "channels: [conda-forge]\n" {
				t.Fatal("linked target changed")
			}
		})
	}
}

func TestNativePathsCompensationPreservesConcurrentEdits(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.UserHome, ".condarc")
	nativeWrite(t, path, "channels: [conda-forge]\n", 0600)
	undo, err := s.ChangePaths(Config{}, Config{EnvsDir: filepath.Join(s.UserHome, "envs")})
	if err != nil {
		t.Fatal(err)
	}
	edited := nativeRead(t, path) + "# user edit after write\n"
	nativeWrite(t, path, edited, 0600)
	if err := undo(); err == nil {
		t.Fatal("compensation overwrote concurrent user edit")
	}
	if nativeRead(t, path) != edited {
		t.Fatal("concurrent edit lost")
	}
	before, err := s.readNativePath(path)
	if err != nil {
		t.Fatal(err)
	}
	nativeWrite(t, path, edited+"# newer\n", 0600)
	if _, err := before.replace([]byte("overwrite\n")); err == nil {
		t.Fatal("publication ignored snapshot change")
	}
}

func TestNativePathsCompensateWhenLaterFileWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the directory write permissions used by this failure case")
	}
	s := fixture(t)
	condarc := filepath.Join(s.UserHome, ".condarc")
	pip, err := s.nativePipPath()
	if err != nil {
		t.Fatal(err)
	}
	condaOriginal := "# preserve exact bytes\nchannels: [conda-forge]\nenvs_dirs: [/old/envs]\n"
	pipOriginal := "[global]\ncache-dir = /old/pip\n"
	nativeWrite(t, condarc, condaOriginal, 0640)
	nativeWrite(t, pip, pipOriginal, 0600)
	if err := os.Chmod(filepath.Dir(pip), 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(pip), 0700) })
	next := Config{EnvsDir: filepath.Join(s.UserHome, "new-envs"), PipCacheDir: filepath.Join(s.UserHome, "new-pip")}
	if _, err := s.ChangePaths(Config{}, next); err == nil {
		t.Fatal("later native file write unexpectedly succeeded")
	}
	if nativeRead(t, condarc) != condaOriginal || nativeRead(t, pip) != pipOriginal {
		t.Fatal("a later write failure retained a partial native change")
	}
	if info, _ := os.Stat(condarc); info.Mode().Perm() != 0640 {
		t.Fatal("compensation changed original permissions")
	}
}

func TestNativePathsPreserveGroupOwnershipAndDetectConcurrentChgrp(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.UserHome, ".condarc")
	original := "channels: [conda-forge]\n"
	nativeWrite(t, path, original, 0640)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	originalGroup := info.Sys().(*syscall.Stat_t).Gid
	groups, err := os.Getgroups()
	if err != nil {
		t.Skip(err)
	}
	alternate := -1
	for _, group := range groups {
		if uint32(group) != originalGroup {
			alternate = group
			break
		}
	}
	if alternate < 0 {
		t.Skip("a second group is required for the ownership preservation case")
	}
	if err := os.Chown(path, -1, alternate); err != nil {
		t.Skipf("temporary file group cannot be changed: %v", err)
	}
	undo, err := s.ChangePaths(Config{}, Config{EnvsDir: filepath.Join(s.UserHome, "new-envs")})
	if err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil || info.Sys().(*syscall.Stat_t).Gid != uint32(alternate) {
		t.Fatal("atomic native edit lost the original file group", err)
	}
	if err := undo(); err != nil || nativeRead(t, path) != original {
		t.Fatal("compensation did not preserve the original grouped file", err)
	}
	before, err := s.readNativePath(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, -1, int(originalGroup)); err != nil {
		t.Fatal(err)
	}
	if _, err := before.replace([]byte("overwrite\n")); err == nil {
		t.Fatal("native publication ignored a concurrent group ownership change")
	}
	if nativeRead(t, path) != original {
		t.Fatal("concurrent ownership change was overwritten")
	}
}

func TestNativePathsCreatesOnlyConfigurationParents(t *testing.T) {
	s := fixture(t)
	next := Config{PipCacheDir: filepath.Join(s.UserHome, "data", "pip")}
	path, err := s.nativePipPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckPaths(Config{}, next); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("preflight created configuration directory")
	}
	undo, err := s.ChangePaths(Config{}, next)
	if err != nil {
		t.Fatal(err)
	}
	if got := nativeRead(t, path); got != "[global]\ncache-dir = "+next.PipCacheDir+"\n" {
		t.Fatalf("new pip config: %s", got)
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != 0700 {
		t.Fatal("new configuration parent is not private")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatal("new configuration file is not private")
	}
	if _, err := os.Lstat(next.PipCacheDir); !os.IsNotExist(err) {
		t.Fatal("cache data directory was created")
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("compensation retained a newly created native file")
	}
}

func TestNativePipEditingPreservesUnrelatedMultilineAndIndentation(t *testing.T) {
	text := "# header\n[global] # section note\n  find-links =\n    https://first.example\n    https://second.example\n  cache-dir = /old\n  timeout = 25\n[install]\n  no-deps = false\n"
	got, err := changePipCache([]byte(text), "/new/cache")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(text, "  cache-dir = /old\n", "  cache-dir = /new/cache\n", 1)
	if !bytes.Equal(got, []byte(want)) {
		t.Fatalf("unrelated INI content changed:\n%s", got)
	}
}

func TestNativePipIndentedSectionSyntaxRemainsMultilineData(t *testing.T) {
	text := "[global]\nfind-links =\n  [not-section]\ncache-dir = /old\n"
	got, err := changePipCache([]byte(text), "/new/cache")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(text, "cache-dir = /old", "cache-dir = /new/cache", 1)
	if string(got) != want {
		t.Fatalf("section-like continuation was changed or moved: %q", got)
	}
	text = "[global]\ncache-dir = /old\n  [install]\n  timeout = 5\n"
	if _, err := changePipCache([]byte(text), "/new/cache"); err == nil {
		t.Fatal("section-like cache continuation was not rejected")
	}
	text = "[global]\n\u00a0find-links =\n  cache-dir = /a-link-value\n\u00a0cache-dir = /old\n"
	got, err = changePipCache([]byte(text), "/new/cache")
	if err != nil {
		t.Fatal(err)
	}
	want = strings.Replace(text, "\u00a0cache-dir = /old", "\u00a0cache-dir = /new/cache", 1)
	if string(got) != want {
		t.Fatalf("Unicode whitespace changed multiline parsing: %q", got)
	}
}

func TestNativePipSectionNamesMatchGreedyConfigParserHeader(t *testing.T) {
	text := "[global] # [annotation]\ncache-dir = /unrelated\n"
	got, err := changePipCache([]byte(text), "/new/cache")
	if err != nil {
		t.Fatal(err)
	}
	// ConfigParser treats everything through the last ']' as the section
	// name. The apparent [global] here is actually an unrelated section.
	if string(got) != text+"[global]\ncache-dir = /new/cache\n" {
		t.Fatalf("changed an unrelated section instead of global: %q", got)
	}
}

func TestNativePipSectionsRemainCaseSensitive(t *testing.T) {
	for _, text := range []string{
		"[GLOBAL]\ncache-dir = /ignored\n",
		"[Global]\ncache-dir = /ignored\n[GLOBAL]\ncache-dir = /also-ignored\n",
	} {
		got, err := changePipCache([]byte(text), "/new/cache")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != text+"[global]\ncache-dir = /new/cache\n" {
			t.Fatalf("case-distinct section was changed: %s", got)
		}
	}
	text := "[global]\ncache-dir = /old\n[GLOBAL]\ncache-dir = /ignored\n"
	got, err := changePipCache([]byte(text), "/new/cache")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.Replace(text, "cache-dir = /old", "cache-dir = /new/cache", 1) {
		t.Fatalf("wrong global section changed: %s", got)
	}
}

func TestNativePipCacheOptionAliasesMatchPip(t *testing.T) {
	for _, key := range []string{"cache_dir", "CACHE-DIR", "--cache-dir", "--CACHE_DIR"} {
		text := "[global]\n" + key + " = /old\ntimeout = 25\n"
		got, err := changePipCache([]byte(text), "/new/cache")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "[global]\ncache-dir = /new/cache\ntimeout = 25\n" {
			t.Fatalf("option alias was not replaced: %s", got)
		}
	}
}

func TestNativePathsUpdateBothCondaActivationContexts(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-source", true: "after-source"}[active], func(t *testing.T) {
			s := fixture(t)
			primary := filepath.Join(s.UserHome, ".condarc")
			explicit := filepath.Join(s.UserHome, "custom.yaml")
			originalPrimary := "channels: [conda-forge]\nenvs_dirs: [/old/standard]\n"
			originalExplicit := "ssl_verify: /custom/cert.pem\nenvs_dirs: [/old/explicit]\n"
			nativeWrite(t, primary, originalPrimary, 0640)
			nativeWrite(t, explicit, originalExplicit, 0600)
			if active {
				s.Env = append(s.Env,
					"CONDARC="+filepath.Join(s.Root, "conda", "mirror-active.yaml"),
					"_NIDO_CONDA_CONDARC_ORIGINAL_SET=x",
					"_NIDO_CONDA_CONDARC_ORIGINAL="+explicit)
			} else {
				s.Env = append(s.Env, "CONDARC="+explicit)
			}
			next := Config{CondaMirror: "tuna", EnvsDir: filepath.Join(s.UserHome, "selected envs")}
			files, err := s.NativePathFiles(Config{}, next)
			if err != nil || len(files) != 2 || files[0] != primary || files[1] != explicit {
				t.Fatalf("preview misses an activation context: %v %v", files, err)
			}
			if nativeRead(t, primary) != originalPrimary || nativeRead(t, explicit) != originalExplicit {
				t.Fatal("preview changed a native file")
			}
			undo, err := s.ChangePaths(Config{}, next)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{primary, explicit} {
				var parsed map[string]any
				if err := yaml.Unmarshal([]byte(nativeRead(t, path)), &parsed); err != nil {
					t.Fatal(err)
				}
				if parsed["envs_dirs"].([]any)[0] != next.EnvsDir {
					t.Fatalf("old directory remains in %s", path)
				}
			}
			changedPrimary, changedExplicit := nativeRead(t, primary), nativeRead(t, explicit)
			if _, err := s.ChangePaths(next, Config{}); err != nil {
				t.Fatal(err)
			}
			if nativeRead(t, primary) != changedPrimary || nativeRead(t, explicit) != changedExplicit {
				t.Fatal("reset restored an old directory in one activation context")
			}
			if err := undo(); err != nil {
				t.Fatal(err)
			}
			if nativeRead(t, primary) != originalPrimary || nativeRead(t, explicit) != originalExplicit {
				t.Fatal("failed-save compensation missed one native file")
			}
		})
	}
}

func TestNativePathsAllCondaContextsPreflightBeforeAnyWrite(t *testing.T) {
	s := fixture(t)
	primary := filepath.Join(s.UserHome, ".condarc")
	explicit := filepath.Join(s.UserHome, "custom.yaml")
	original := "channels: [conda-forge]\n"
	nativeWrite(t, primary, original, 0600)
	nativeWrite(t, explicit, "envs_dirs: [\n", 0600)
	s.Env = append(s.Env, "CONDARC="+explicit)
	next := Config{CondaMirror: "tuna", EnvsDir: filepath.Join(s.UserHome, "new-envs")}
	if _, err := s.NativePathFiles(Config{}, next); err == nil {
		t.Fatal("accepted malformed original explicit CONDARC")
	}
	if _, err := s.ChangePaths(Config{}, next); err == nil {
		t.Fatal("accepted malformed original explicit CONDARC")
	}
	if nativeRead(t, primary) != original {
		t.Fatal("partially changed standard condarc before all-file preflight")
	}
	s.Env = append(s.Env, "CONDARC="+primary)
	files, err := s.NativePathFiles(Config{}, next)
	if err != nil || len(files) != 1 || files[0] != primary {
		t.Fatalf("same native file was not deduplicated: %v %v", files, err)
	}
}

func TestNativePathsAfterMirrorOffStillUpdateRestorableCondaContext(t *testing.T) {
	s := fixture(t)
	primary := filepath.Join(s.UserHome, ".condarc")
	explicit := filepath.Join(s.UserHome, "custom.yaml")
	nativeWrite(t, primary, "channels: [conda-forge]\nenvs_dirs: [/old/standard]\n", 0600)
	nativeWrite(t, explicit, "ssl_verify: /custom/cert.pem\nenvs_dirs: [/old/explicit]\n", 0600)
	// mirror off only updates generated env.sh; until source runs, the parent
	// shell still points to the previous overlay and can restore this file.
	s.Env = append(s.Env,
		"CONDARC="+filepath.Join(s.Root, "conda", "mirror-old.yaml"),
		"_NIDO_CONDA_CONDARC_ORIGINAL_SET=x",
		"_NIDO_CONDA_CONDARC_ORIGINAL="+explicit)
	next := Config{EnvsDir: filepath.Join(s.UserHome, "selected-envs")}
	files, err := s.NativePathFiles(Config{}, next)
	if err != nil || !slices.Equal(files, []string{primary, explicit}) {
		t.Fatalf("missed pending restore context: %v %v", files, err)
	}
	if _, err := s.ChangePaths(Config{}, next); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{primary, explicit} {
		var parsed struct {
			EnvsDirs []string `yaml:"envs_dirs"`
		}
		if err := yaml.Unmarshal([]byte(nativeRead(t, path)), &parsed); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(parsed.EnvsDirs, []string{next.EnvsDir}) {
			t.Fatalf("restoring %s reactivates the old path: %v", path, parsed.EnvsDirs)
		}
	}
}

func nativeMirrorDefaults(t *testing.T, channels []string) MirrorDefaults {
	t.Helper()
	defaults := []string{"https://repo.anaconda.com/pkgs/main", "https://repo.anaconda.com/pkgs/r", "https://repo.anaconda.com/pkgs/msys2"}
	data, err := json.Marshal(map[string]any{
		"channels": channels, "default_channels": defaults,
		"custom_channels":      map[string]string{"conda-forge": "https://conda.anaconda.org", "bioconda": "https://conda.anaconda.org"},
		"custom_multichannels": map[string][]string{"defaults": defaults}, "channel_alias": "https://conda.anaconda.org",
		"migrated_channel_aliases": []string{}, "migrated_custom_channels": map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := captureCondaYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	return MirrorDefaults{CondaCaptured: true, CondaYAML: content}
}

func nativeChannels(t *testing.T, path string) []string {
	t.Helper()
	var parsed struct {
		Channels []string `yaml:"channels"`
	}
	if err := yaml.Unmarshal([]byte(nativeRead(t, path)), &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.Channels
}

func TestNativeChannelsFollowSelectedMirrorAndKeepPathsAfterReset(t *testing.T) {
	s := fixture(t)
	primary, explicit := filepath.Join(s.UserHome, ".condarc"), filepath.Join(s.UserHome, "custom.yaml")
	original := []string{"https://repo.anaconda.com/pkgs/main", "conda-forge", "https://private.example/team"}
	defaults := nativeMirrorDefaults(t, original)
	for _, path := range []string{primary, explicit} {
		nativeWrite(t, path, "# native user settings\nchannels: [https://old.example/main]\nssl_verify: /custom/cert.pem\nenvs_dirs: [/native/envs]\n", 0600)
	}
	s.Env = append(s.Env, "CONDARC="+explicit, "CONDA_ENVS_PATH=/unrelated/override")
	// An unchanged tracked path must not trigger its override preflight while
	// synchronizing native channels for the selected mirror.
	previous := Config{EnvsDir: filepath.Join(s.UserHome, "tracked-envs")}
	selected := Config{CondaMirror: "tuna", EnvsDir: previous.EnvsDir, Defaults: defaults}
	files, err := s.NativePathFiles(previous, selected)
	if err != nil || len(files) != 2 {
		t.Fatalf("channel-only preview: %v %v", files, err)
	}
	if _, err := s.ChangePaths(previous, selected); err != nil {
		t.Fatal(err)
	}
	want := PresetChannels(defaults, "tuna")
	for _, path := range []string{primary, explicit} {
		if got := nativeChannels(t, path); !slices.Equal(got, want) {
			t.Fatalf("native channels retain old URLs: %v", got)
		}
		content := nativeRead(t, path)
		if !strings.Contains(content, "/custom/cert.pem") || !strings.Contains(content, "/native/envs") || !strings.Contains(content, "# native user settings") {
			t.Fatal("channel selection changed unrelated configuration")
		}
	}
	selectedDefault := selected
	selectedDefault.CondaMirror = "default"
	if _, err := s.ChangePaths(selected, selectedDefault); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{primary, explicit} {
		if got := nativeChannels(t, path); !slices.Equal(got, original) {
			t.Fatalf("default did not restore saved channel selection: %v", got)
		}
	}
	before := nativeRead(t, primary)
	if _, err := s.ChangePaths(selectedDefault, Config{Defaults: defaults}); err != nil {
		t.Fatal(err)
	}
	if nativeRead(t, primary) != before {
		t.Fatal("reset changed native channel or path settings")
	}
}

func TestNativeChannelsAlreadyEqualPreserveAllBytes(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.UserHome, ".condarc")
	defaults := nativeMirrorDefaults(t, []string{"conda-forge", "https://private.example/team"})
	text := "# preserve format\nchannels: ['conda-forge', https://private.example/team] # preserve selection note\nssl_verify: true\n"
	nativeWrite(t, path, text, 0644)
	selected := Config{CondaMirror: "tuna", Defaults: defaults}
	files, err := s.NativePathFiles(selected, selected)
	if err != nil || len(files) != 0 {
		t.Fatalf("unchanged channels would be rewritten: %v %v", files, err)
	}
	if _, err := s.ChangePaths(selected, selected); err != nil {
		t.Fatal(err)
	}
	if nativeRead(t, path) != text {
		t.Fatal("idempotent channel application changed comments or formatting")
	}
	// An unchanged saved selection is not permission to replay native edits
	// during an unrelated operation such as setup, cache or envs changes.
	nativeWrite(t, path, "channels: [old]\n", 0644)
	if _, err := s.ChangePaths(selected, selected); err != nil {
		t.Fatal(err)
	}
	if got := nativeChannels(t, path); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("unrelated operation changed user channels: %v", got)
	}
	if _, err := s.ChangePaths(selected, selected, NativeScope{CondaMirror: true}); err != nil {
		t.Fatal(err)
	}
	if got := nativeChannels(t, path); !slices.Equal(got, []string{"conda-forge", "https://private.example/team"}) {
		t.Fatalf("explicit repeated mirror selection did not reapply channels: %v", got)
	}
}

func TestNativePathChangePreservesLaterNativeChannelEdits(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.UserHome, ".condarc")
	text := "channels: [new-private-channel]\nssl_verify: true\n"
	nativeWrite(t, path, text, 0600)
	previous := Config{CondaMirror: "tuna", Defaults: nativeMirrorDefaults(t, []string{"conda-forge"})}
	next := previous
	next.EnvsDir = filepath.Join(s.UserHome, "selected-envs")
	if _, err := s.ChangePaths(previous, next); err != nil {
		t.Fatal(err)
	}
	if got := nativeChannels(t, path); !slices.Equal(got, []string{"new-private-channel"}) {
		t.Fatalf("path-only change overwrote user channel edits: %v", got)
	}
}

func TestNativeScopeReappliesOnlyExplicitPath(t *testing.T) {
	s := fixture(t)
	condarc := filepath.Join(s.UserHome, ".condarc")
	pip, err := s.nativePipPath()
	if err != nil {
		t.Fatal(err)
	}
	text := "channels: [new-private-channel]\nenvs_dirs: [/changed/envs]\npkgs_dirs: [/changed/pkgs]\n"
	nativeWrite(t, condarc, text, 0600)
	nativeWrite(t, pip, "[global]\ncache-dir = /changed/pip\n", 0600)
	selected := Config{CondaMirror: "tuna", EnvsDir: filepath.Join(s.UserHome, "envs"), PkgsDir: filepath.Join(s.UserHome, "pkgs"), PipCacheDir: filepath.Join(s.UserHome, "pip"), Defaults: nativeMirrorDefaults(t, []string{"conda-forge"})}
	scope := NativeScope{PipCacheDir: true}
	files, err := s.NativePathFiles(selected, selected, scope)
	if err != nil || !slices.Equal(files, []string{pip}) {
		t.Fatalf("scope affected unrelated native files: %v %v", files, err)
	}
	if _, err := s.ChangePaths(selected, selected, scope); err != nil {
		t.Fatal(err)
	}
	if nativeRead(t, condarc) != text || nativeRead(t, pip) != "[global]\ncache-dir = "+selected.PipCacheDir+"\n" {
		t.Fatal("explicit pip cache selection failed or changed another field")
	}
	if _, err := s.ChangePaths(selected, selected, NativeScope{EnvsDir: true}); err != nil {
		t.Fatal(err)
	}
	var root map[string][]string
	if err := yaml.Unmarshal([]byte(nativeRead(t, condarc)), &root); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(root["envs_dirs"], []string{selected.EnvsDir}) || !slices.Equal(root["pkgs_dirs"], []string{"/changed/pkgs"}) || !slices.Equal(root["channels"], []string{"new-private-channel"}) {
		t.Fatalf("explicit envs selection changed unrelated fields: %+v", root)
	}
}

func TestNativeChannelsDoNotCopySecretsIntoReadableFiles(t *testing.T) {
	for _, secret := range []string{
		"https://user:password@private.example/channel",
		"https://private.example/t/secret/channel",
		"https://private.example/channel?token=secret",
		"https://private.example/%74/encoded-secret/channel",
		"https://private.example/t%2Fembedded-secret/channel",
		"https://private.example/%2Ft%2Fembedded-secret/channel",
	} {
		t.Run(secret, func(t *testing.T) {
			s := fixture(t)
			path := filepath.Join(s.UserHome, ".condarc")
			text := "channels: [conda-forge]\n"
			nativeWrite(t, path, text, 0644)
			selected := Config{CondaMirror: "default", Defaults: nativeMirrorDefaults(t, []string{secret})}
			if _, err := s.NativePathFiles(Config{}, selected); err == nil || !strings.Contains(err.Error(), "权限") || strings.Contains(err.Error(), secret) {
				t.Fatalf("missing safe permission rejection: %v", err)
			}
			if _, err := s.ChangePaths(Config{}, selected); err == nil {
				t.Fatal("copied a secret channel into a group/other-readable file")
			}
			if nativeRead(t, path) != text {
				t.Fatal("permission failure partially edited native file")
			}
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ChangePaths(Config{}, selected); err != nil {
				t.Fatal("private native file refused secret channel", err)
			}
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ChangePaths(selected, selected); err != nil {
				t.Fatal("identical preexisting secret should not count as new disclosure", err)
			}
		})
	}
}

func TestNativeChannelsEmptySelectionPreservesFinalAnnotation(t *testing.T) {
	text := "channels: #!final\n  - old\nssl_verify: true\n"
	got, err := changeNativeConda([]byte(text), nil, []string{}, true)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Channels []string `yaml:"channels"`
	}
	if err := yaml.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("empty channel selection produced invalid YAML: %s\n%v", got, err)
	}
	if len(parsed.Channels) != 0 || !strings.Contains(string(got), "channels: [] #!final") {
		t.Fatalf("empty channel selection or annotation lost: %s", got)
	}
}

func TestNativeChannelsUseCurrentPresetSelectionAndCapturedDefault(t *testing.T) {
	for _, channels := range [][]string{{"conda-forge", "https://private.example/new-team"}, {}} {
		s := fixture(t)
		path := filepath.Join(s.UserHome, ".condarc")
		nativeWrite(t, path, "channels: [old]\n", 0600)
		selected := Config{CondaMirror: "tuna", Defaults: nativeMirrorDefaults(t, []string{"original-private"}), PresetChannels: channels, PresetChannelsSet: true}
		if _, err := s.ChangePaths(selected, selected, NativeScope{CondaMirror: true}); err != nil {
			t.Fatal(err)
		}
		if got := nativeChannels(t, path); !slices.Equal(got, channels) {
			t.Fatalf("preset lost the current channel range: %v", got)
		}
		next := selected
		next.CondaMirror = "default"
		if _, err := s.ChangePaths(selected, next, NativeScope{CondaMirror: true}); err != nil {
			t.Fatal(err)
		}
		if got := nativeChannels(t, path); !slices.Equal(got, []string{"original-private"}) {
			t.Fatalf("default used the later preset channel range: %v", got)
		}
	}
}

func TestNativePathsRejectOnlyOverridesOfChangingKeys(t *testing.T) {
	for _, key := range []string{"CONDA_ENVS_PATH", "CONDA_ENVS_DIRS", "CONDA_PKGS_DIRS", "PIP_CACHE_DIR"} {
		t.Run(key, func(t *testing.T) {
			s := fixture(t)
			path := filepath.Join(s.UserHome, "selected")
			next := Config{}
			switch key {
			case "CONDA_ENVS_PATH", "CONDA_ENVS_DIRS":
				next.EnvsDir = path
			case "CONDA_PKGS_DIRS":
				next.PkgsDir = path
			case "PIP_CACHE_DIR":
				next.PipCacheDir = path
			}
			s.Env = append(s.Env, key+"=/some/override")
			if err := s.CheckPaths(Config{}, next); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("accepted an override of changing native key: %v", err)
			}
			if err := s.CheckPaths(next, Config{}); err != nil {
				t.Fatal("override blocked reset", err)
			}
			mirror := next
			mirror.CondaMirror = "tuna"
			if err := s.CheckPaths(next, mirror); err != nil {
				t.Fatal("override blocked unrelated mirror change", err)
			}
			s.Env = append(s.Env, key+"="+path)
			if err := s.CheckPaths(Config{}, next); err != nil {
				t.Fatal("equal override should be accepted", err)
			}
		})
	}
}

func nativeNonMirrorNodes(t *testing.T, text string) map[string]string {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string)
	root := doc.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		key := root.Content[i].Value
		if slices.Contains(defaultMirrorKeys, key) {
			continue
		}
		node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{root.Content[i], root.Content[i+1]}}
		data, err := yaml.Marshal(node)
		if err != nil {
			t.Fatal(err)
		}
		result[key] = string(data)
	}
	return result
}

func TestNativeSourceMirrorUpdatesAllKeysAndPreservesOtherNodes(t *testing.T) {
	for _, sameAsUser := range []bool{false, true} {
		t.Run(map[bool]string{false: "separate-source", true: "source-is-user-file"}[sameAsUser], func(t *testing.T) {
			s := fixture(t)
			user := filepath.Join(s.UserHome, ".condarc")
			source := filepath.Join(s.UserHome, "corporate.yaml")
			originalUser := "channels: [defaults]\nssl_verify: /user/certificate.pem\n"
			nativeWrite(t, user, originalUser, 0640)
			if sameAsUser {
				source = user
			}
			originalSource := "# corporate configuration\nssl_verify: /corporate/certificate.pem #!final\n# proxy comment\nproxy_servers: #!final\n  https: 'https://proxy-user:proxy-secret@proxy.invalid:8443' # keep auth\nclient_ssl_cert: '$CONDARC/client.pem'\nclient_ssl_cert_key: '$CUSTOM_ROOT/client-key.pem'\ncustom_plugin_setting:\n  nested: ['unknown', {quoted: true}] # future setting\nenvs_dirs: [/unchanged/envs]\npkgs_dirs: [/unchanged/pkgs]\nchannels: #!final\n  - old-channel #!top\ndefault_channels: #!final\n  - https://old.invalid/pkgs/main\ncustom_channels: #!final\n  conda-forge: https://old.invalid/cloud\ncustom_multichannels: #!final\n  defaults: #!final\n    - https://old.invalid/pkgs/main\nchannel_alias: https://old.invalid/cloud #!final\nmigrated_channel_aliases: #!final\n  - https://old.invalid/alias\nmigrated_custom_channels: #!final\n  private: https://old.invalid/migrated\n"
			nativeWrite(t, source, originalSource, 0600)
			selected := Config{CondaMirror: "tuna", SourceCONDARC: source, Defaults: nativeMirrorDefaults(t, []string{"defaults", "conda-forge"})}
			files, err := s.NativePathFiles(Config{}, selected)
			wantFiles := 2
			if sameAsUser {
				wantFiles = 1
			}
			if err != nil || len(files) != wantFiles {
				t.Fatal("fresh shell did not use saved source exactly once", files, err)
			}
			if nativeRead(t, source) != originalSource {
				t.Fatal("preview wrote source")
			}
			undo, err := s.ChangePaths(Config{}, selected)
			if err != nil {
				t.Fatal(err)
			}
			changed := nativeRead(t, source)
			beforeOther, afterOther := nativeNonMirrorNodes(t, originalSource), nativeNonMirrorNodes(t, changed)
			if len(beforeOther) != len(afterOther) {
				t.Fatal("lost unrelated source nodes", changed)
			}
			for key, value := range beforeOther {
				if afterOther[key] != value {
					t.Fatalf("changed non-mirror field %s\nbefore: %s\nafter: %s", key, value, afterOther[key])
				}
			}
			_, overlay := s.MirrorFile(selected)
			expected, err := nativeMirrorFields(overlay)
			if err != nil {
				t.Fatal(err)
			}
			var parsed yaml.Node
			if err := yaml.Unmarshal([]byte(changed), &parsed); err != nil {
				t.Fatal(err)
			}
			root := parsed.Content[0]
			for i := 0; i < len(root.Content); i += 2 {
				if desired, ok := expected[root.Content[i].Value]; ok {
					if !nativeNodeEqual(root.Content[i+1], desired.value) || nativeMergeComment(root.Content[i].LineComment) != nativeMergeComment(desired.key.LineComment) {
						t.Fatalf("source mirror field failed to replace old merge rules: %s\n%s", root.Content[i].Value, changed)
					}
					delete(expected, root.Content[i].Value)
				}
			}
			if len(expected) != 0 {
				t.Fatal("source is missing mirror fields", expected)
			}
			if !sameAsUser {
				other := nativeNonMirrorNodes(t, nativeRead(t, user))
				if other["ssl_verify"] != nativeNonMirrorNodes(t, originalUser)["ssl_verify"] {
					t.Fatal("changed ordinary user's certificate")
				}
			}
			if err := undo(); err != nil || nativeRead(t, source) != originalSource {
				t.Fatal("failed-save compensation lost source bytes", err)
			}
			if !sameAsUser && nativeRead(t, user) != originalUser {
				t.Fatal("failed-save compensation lost ordinary user bytes")
			}
		})
	}
}

func TestNativeSourcePathOnlyScopeDoesNotReplayMirror(t *testing.T) {
	s := fixture(t)
	source := filepath.Join(s.UserHome, "source.yaml")
	text := "channels: [new-private]\nchannel_alias: https://later.invalid/cloud\nssl_verify: /later/cert.pem\n"
	nativeWrite(t, source, text, 0600)
	selected := Config{CondaMirror: "tuna", SourceCONDARC: source, Defaults: nativeMirrorDefaults(t, []string{"defaults"})}
	next := selected
	next.EnvsDir = filepath.Join(s.UserHome, "new-envs")
	if _, err := s.ChangePaths(selected, next, NativeScope{EnvsDir: true}); err != nil {
		t.Fatal(err)
	}
	changed := nativeRead(t, source)
	for _, value := range []string{"channels: [new-private]", "channel_alias: https://later.invalid/cloud", "ssl_verify: /later/cert.pem", next.EnvsDir} {
		if !strings.Contains(changed, value) {
			t.Fatal("path-only operation replayed mirror or lost settings", value, changed)
		}
	}
	if strings.Contains(changed, "default_channels") {
		t.Fatal("path-only operation inserted mirror fields")
	}
}

func TestNativeSourceMirrorProtectsSecretsInAllMirrorKeys(t *testing.T) {
	for _, secret := range []string{"https://user:secret@private.invalid/cloud", "https://private.invalid/t/secret/cloud", "https://private.invalid/cloud?token=secret"} {
		t.Run(secret, func(t *testing.T) {
			s := fixture(t)
			source := filepath.Join(s.UserHome, "source.yaml")
			user := filepath.Join(s.UserHome, ".condarc")
			text := "channels: [defaults]\nssl_verify: /corporate/cert.pem\n"
			nativeWrite(t, source, text, 0644)
			nativeWrite(t, user, text, 0644)
			defaults := nativeMirrorDefaults(t, []string{"defaults"})
			defaults.CondaYAML = strings.ReplaceAll(defaults.CondaYAML, "https://conda.anaconda.org", secret)
			selected := Config{CondaMirror: "default", SourceCONDARC: source, Defaults: defaults}
			if _, err := s.ChangePaths(Config{}, selected); err == nil || !strings.Contains(err.Error(), "权限") || strings.Contains(err.Error(), secret) {
				t.Fatal("source mirror copied protected secret or leaked it in error", err)
			}
			if nativeRead(t, source) != text || nativeRead(t, user) != text {
				t.Fatal("secret rejection partially changed native files")
			}
			if err := os.Chmod(source, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ChangePaths(Config{}, selected); err == nil {
				t.Fatal("private source bypassed secret protection for readable ordinary user file")
			}
			if err := os.Chmod(user, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ChangePaths(Config{}, selected); err != nil {
				t.Fatal("private original source rejected secret", err)
			}
			if !strings.Contains(nativeRead(t, source), secret) {
				t.Fatal("default lost private mirror mapping")
			}
			if err := os.Chmod(source, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ChangePaths(selected, selected, NativeScope{CondaMirror: true}); err != nil {
				t.Fatal("preexisting exact secret was mistaken for new disclosure", err)
			}
		})
	}
}

func TestNativeSourceMirrorFailureCompensatesEarlierUserWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the directory permissions used to fail the source write")
	}
	s := fixture(t)
	user := filepath.Join(s.UserHome, ".condarc")
	source := filepath.Join(s.UserHome, "readonly", "source.yaml")
	userText := "# ordinary user\nchannels: [old-user-channel]\nssl_verify: /user/cert.pem\n"
	sourceText := "# corporate original\nchannels: [old-source-channel]\nssl_verify: /corporate/cert.pem\nproxy_servers: {https: https://proxy.invalid}\n"
	nativeWrite(t, user, userText, 0640)
	nativeWrite(t, source, sourceText, 0600)
	if err := os.Chmod(filepath.Dir(source), 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(source), 0700) })
	selected := Config{CondaMirror: "tuna", SourceCONDARC: source, Defaults: nativeMirrorDefaults(t, []string{"defaults"})}
	if _, err := s.ChangePaths(Config{}, selected); err == nil {
		t.Fatal("source write unexpectedly succeeded")
	}
	if nativeRead(t, user) != userText || nativeRead(t, source) != sourceText {
		t.Fatal("failed source write left an earlier user-file change or changed source")
	}
	if info, _ := os.Stat(user); info.Mode().Perm() != 0640 {
		t.Fatal("failed source write changed user-file permissions")
	}
}

func TestNativeSourceAssociationChangeReappliesSameMirrorWithoutScope(t *testing.T) {
	s := fixture(t)
	sourceA := filepath.Join(s.UserHome, "source-a.yaml")
	sourceB := filepath.Join(s.UserHome, "source-b.yaml")
	nativeWrite(t, sourceA, "channels: [old-a]\nssl_verify: /a/cert.pem\n", 0600)
	nativeWrite(t, sourceB, "channels: [old-b]\nchannel_alias: https://old-b.invalid/cloud\nssl_verify: /b/cert.pem\n", 0600)
	previous := Config{CondaMirror: "tuna", SourceCONDARC: sourceA, Defaults: nativeMirrorDefaults(t, []string{"defaults"})}
	if _, err := s.ChangePaths(Config{}, previous); err != nil {
		t.Fatal(err)
	}
	beforeA := nativeRead(t, sourceA)
	next := previous
	next.SourceCONDARC = sourceB
	if _, err := s.ChangePaths(previous, next); err != nil {
		t.Fatal(err)
	}
	changedB := nativeRead(t, sourceB)
	for _, value := range []string{"https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/main", "custom_channels:", "custom_multichannels:", "migrated_channel_aliases:", "migrated_custom_channels:", "ssl_verify: /b/cert.pem"} {
		if !strings.Contains(changedB, value) {
			t.Fatal("changing source association did not apply full mirror", value, changedB)
		}
	}
	if nativeRead(t, sourceA) != beforeA {
		t.Fatal("changing source association rewrote the previous source")
	}
}

func TestNativeSourceAssociationChangeInPipScopeDoesNotReplayMirror(t *testing.T) {
	s := fixture(t)
	sourceA := filepath.Join(s.UserHome, "source-a.yaml")
	sourceB := filepath.Join(s.UserHome, "source-b.yaml")
	textA := "channels: [current-a]\nssl_verify: /a/cert.pem\n"
	textB := "channels: [current-b]\nchannel_alias: https://current-b.invalid/cloud\nssl_verify: /b/cert.pem\n"
	nativeWrite(t, sourceA, textA, 0600)
	nativeWrite(t, sourceB, textB, 0600)
	previous := Config{CondaMirror: "tuna", SourceCONDARC: sourceA, Defaults: nativeMirrorDefaults(t, []string{"defaults"})}
	next := previous
	next.SourceCONDARC = sourceB
	next.PipCacheDir = filepath.Join(s.UserHome, "pip-cache")
	files, err := s.NativePathFiles(previous, next, NativeScope{PipCacheDir: true})
	pipPath, pipErr := s.nativePipPath()
	if err != nil || pipErr != nil || len(files) != 1 || files[0] != pipPath {
		t.Fatal("pip-only scope included a mirror write after source association change", files, err, pipErr)
	}
	if _, err := s.ChangePaths(previous, next, NativeScope{PipCacheDir: true}); err != nil {
		t.Fatal(err)
	}
	if nativeRead(t, sourceA) != textA || nativeRead(t, sourceB) != textB {
		t.Fatal("pip-only scope replayed mirror after source association change")
	}
	if _, err := os.Lstat(filepath.Join(s.UserHome, ".condarc")); !os.IsNotExist(err) {
		t.Fatal("pip-only scope created ordinary user mirror configuration", err)
	}
}

func TestNativePreflightRejectsOversizedGeneratedConfigurations(t *testing.T) {
	for _, tool := range []string{"conda", "pip", "source"} {
		t.Run(tool, func(t *testing.T) {
			s := fixture(t)
			path := filepath.Join(s.UserHome, ".condarc")
			next := Config{EnvsDir: filepath.Join(s.UserHome, strings.Repeat("a", 180))}
			text := "#" + strings.Repeat("a", nativePathLimit-80) + "\nssl_verify: false\n"
			if tool == "pip" {
				var err error
				path, err = s.nativePipPath()
				if err != nil {
					t.Fatal(err)
				}
				next = Config{PipCacheDir: filepath.Join(s.UserHome, strings.Repeat("a", 180))}
				text = "#" + strings.Repeat("a", nativePathLimit-80) + "\n[global]\nindex-url = https://pypi.org/simple\n"
			} else if tool == "source" {
				path = filepath.Join(s.UserHome, "source.yaml")
				next = Config{CondaMirror: "tuna", SourceCONDARC: path, Defaults: nativeMirrorDefaults(t, []string{"defaults"})}
				nativeWrite(t, filepath.Join(s.UserHome, ".condarc"), "channels: [old-user]\n", 0600)
			}
			nativeWrite(t, path, text, 0600)
			if len(text) > nativePathLimit {
				t.Fatal("fixture already exceeds source limit")
			}
			if err := s.CheckPaths(Config{}, next); err == nil {
				t.Fatal("preflight accepted a generated native file larger than 1 MiB")
			}
			if _, err := s.NativePathFiles(Config{}, next); err == nil {
				t.Fatal("preview accepted a generated native file larger than 1 MiB")
			}
			if nativeRead(t, path) != text {
				t.Fatal("failed preflight changed original configuration")
			}
		})
	}
}

func TestNativeMissingPersistedSourceNeverBecomesMirrorOnlyFile(t *testing.T) {
	s := fixture(t)
	user := filepath.Join(s.UserHome, ".condarc")
	text := "channels: [original-user]\nssl_verify: /user/cert.pem\n"
	nativeWrite(t, user, text, 0600)
	next := Config{CondaMirror: "tuna", SourceCONDARC: filepath.Join(s.UserHome, "missing-source.yaml"), Defaults: nativeMirrorDefaults(t, []string{"defaults"})}
	if err := s.CheckPaths(Config{}, next); err == nil {
		t.Fatal("preflight accepted a missing persisted original source")
	}
	if _, err := s.ChangePaths(Config{}, next); err == nil {
		t.Fatal("mutation recreated an absent original source as mirror-only configuration")
	}
	if nativeRead(t, user) != text {
		t.Fatal("missing source failure partially changed ordinary user configuration")
	}
	if _, err := os.Lstat(next.SourceCONDARC); !os.IsNotExist(err) {
		t.Fatal("missing source was created", err)
	}
}

func TestNativeDefaultReplacesEntireSnapshotInOrdinaryUserAndSource(t *testing.T) {
	for _, source := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("source=%t/legacy=%t", source, legacy), func(t *testing.T) {
				s := fixture(t)
				user := filepath.Join(s.UserHome, ".condarc")
				later := "channels: [later-channel]\ndefault_channels: [https://later.invalid/pkgs/main]\ncustom_channels: {later-private: https://later.invalid/cloud}\ncustom_multichannels: {later-team: [https://later.invalid/team]}\nchannel_alias: https://later.invalid/cloud\nmigrated_channel_aliases: [https://later.invalid/alias]\nmigrated_custom_channels: {later-migration: https://later.invalid/migrated}\nssl_verify: /kept/cert.pem #!final\nproxy_servers: {https: https://proxy.invalid} # keep proxy\n"
				nativeWrite(t, user, later, 0600)
				defaults := nativeMirrorDefaults(t, []string{"defaults"})
				if legacy {
					root, err := parseDefaultYAML(defaults.CondaYAML)
					if err != nil {
						t.Fatal(err)
					}
					root.Content = root.Content[:2*legacyDefaultMirrorKeys]
					content, err := encodeDefaultYAML(root)
					if err != nil {
						t.Fatal(err)
					}
					defaults.CondaYAML = content
				}
				selected := Config{CondaMirror: "default", Defaults: defaults}
				paths := []string{user}
				if source {
					selected.SourceCONDARC = filepath.Join(s.UserHome, "original-source.yaml")
					nativeWrite(t, selected.SourceCONDARC, later, 0600)
					paths = append(paths, selected.SourceCONDARC)
				}
				if _, err := s.ChangePaths(Config{}, selected); err != nil {
					t.Fatal(err)
				}
				for _, path := range paths {
					changed := nativeRead(t, path)
					for _, suffix := range []string{"later-private", "later-team", "https://later.invalid/pkgs/main", "https://later.invalid/cloud"} {
						if strings.Contains(changed, suffix) {
							t.Fatal("default retained later mirror mapping in native file", suffix, changed)
						}
					}
					for key, expected := range nativeNonMirrorNodes(t, later) {
						if nativeNonMirrorNodes(t, changed)[key] != expected {
							t.Fatal("default changed unrelated node", key, changed)
						}
					}
					if legacy && (!strings.Contains(changed, "later-migration") || !strings.Contains(changed, "https://later.invalid/alias")) {
						t.Fatal("legacy default altered migration fields it never captured", changed)
					}
					if !legacy && strings.Contains(changed, "later-migration") {
						t.Fatal("default kept later migration map", changed)
					}
					laterAgain := strings.Replace(changed, "https://conda.anaconda.org", "https://second-edit.invalid/cloud", 1)
					nativeWrite(t, path, laterAgain, 0600)
					if _, err := s.ChangePaths(selected, selected); err != nil {
						t.Fatal(err)
					}
					if nativeRead(t, path) != laterAgain {
						t.Fatal("unscoped unrelated no-op reapplied default")
					}
				}
			})
		}
	}
}

func TestNativeNewFilePublicationCompensatesTemporaryUnlinkFailure(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.UserHome, ".condarc")
	before, err := s.readNativePath(path)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	_, err = before.replaceWithRemove([]byte("ssl_verify: /kept/cert.pem\n"), func(name string) error {
		if strings.HasPrefix(filepath.Base(name), ".nido-native-") && failures == 0 {
			failures++
			return syscall.EPERM
		}
		return os.Remove(name)
	})
	if err == nil || failures != 1 {
		t.Fatal("did not exercise post-Link unlink failure", failures, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("failed new-file publication left a committed destination outside undo tracking", err)
	}
	entries, err := os.ReadDir(s.UserHome)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed publication left its own temporary hardlink", entries, err)
	}
}

func TestNativeTemporaryCleanupPreservesConcurrentReplacement(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.UserHome, ".condarc")
	before, err := s.readNativePath(path)
	if err != nil {
		t.Fatal(err)
	}
	var replaced string
	_, err = before.replaceWithRemove([]byte("ssl_verify: /original/cert.pem\n"), func(name string) error {
		if strings.HasPrefix(filepath.Base(name), ".nido-native-") && replaced == "" {
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			nativeWrite(t, name, "concurrent user content", 0600)
			replaced = name
			return syscall.EPERM
		}
		return os.Remove(name)
	})
	if err == nil || replaced == "" {
		t.Fatal("did not exercise concurrent temporary replacement", err)
	}
	if nativeRead(t, replaced) != "concurrent user content" {
		t.Fatal("deferred cleanup removed or changed concurrent user file")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("failed publication was not compensated", err)
	}
}

func TestNativeTemporaryCleanupPersistentFailureReportsPublishedDestination(t *testing.T) {
	s := fixture(t)
	path := filepath.Join(s.UserHome, ".condarc")
	before, err := s.readNativePath(path)
	if err != nil {
		t.Fatal(err)
	}
	text := "ssl_verify: /original/cert.pem\n"
	_, err = before.replaceWithRemove([]byte(text), func(name string) error {
		if strings.HasPrefix(filepath.Base(name), ".nido-native-") {
			return syscall.EPERM
		}
		return os.Remove(name)
	})
	if err == nil || !strings.Contains(err.Error(), "已创建") || !strings.Contains(err.Error(), "无法安全撤销") {
		t.Fatal("failure hid that publication already happened", err)
	}
	if nativeRead(t, path) != text {
		t.Fatal("unsafe compensation altered a hardlinked publication")
	}
}

func TestNativeCaptureGuardFailurePreventsAllWrites(t *testing.T) {
	s := fixture(t)
	user := filepath.Join(s.UserHome, ".condarc")
	source := filepath.Join(s.UserHome, "source.yaml")
	userText := "channels: [original-user]\nssl_verify: /user/cert.pem\n"
	sourceText := "channels: [original-source]\nssl_verify: /source/cert.pem\n"
	nativeWrite(t, user, userText, 0600)
	nativeWrite(t, source, sourceText, 0600)
	next := Config{CondaMirror: "tuna", SourceCONDARC: source, Defaults: nativeMirrorDefaults(t, []string{"defaults"})}
	rejected := errors.New("prior capture changed")
	calls := 0
	undo, err := s.ChangePathsChecked(Config{}, next, func() error {
		calls++
		return rejected
	})
	if !errors.Is(err, rejected) || undo != nil || calls != 1 {
		t.Fatal("capture guard did not stop native publication", err, calls)
	}
	if nativeRead(t, user) != userText || nativeRead(t, source) != sourceText {
		t.Fatal("capture guard failure partially changed native files")
	}
}
