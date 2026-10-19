package nido

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
)

func condaInteractiveFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureConda(conda.Config{
		CondaMirror: "tuna", PipMirror: "tuna", PipIndexURL: "https://pypi.tuna.tsinghua.edu.cn/simple",
		EnvsDir: filepath.Join(home, "envs"), PkgsDir: filepath.Join(home, "pkgs"), PipCacheDir: filepath.Join(home, "pip-cache"),
	}); err != nil {
		t.Fatal(err)
	}
	return m, home
}

func condaInteractiveSnapshot(t *testing.T, m *Manager, home string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, path := range []string{filepath.Join(m.Root, "config.toml"), filepath.Join(m.Root, "env.sh"), filepath.Join(home, ".condarc"), filepath.Join(home, ".config", "pip", "pip.conf")} {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		files[path] = data
	}
	return files
}

func TestCondaInteractiveKeepDoesNotQueryOrWrite(t *testing.T) {
	for _, test := range []struct {
		name, input string
		args        []string
	}{
		{"configure", "\n", []string{"configure"}},
		{"mirror-tool-and-value", "\n\n", []string{"mirror"}},
		{"mirror-conda", "\n", []string{"mirror", "conda"}},
		{"mirror-pip", "\n", []string{"mirror", "pip"}},
		{"cache-tool-and-value", "\n\n", []string{"cache"}},
		{"cache-conda", "\n", []string{"cache", "conda"}},
		{"cache-pip", "\n", []string{"cache", "pip"}},
		{"envs", "\n", []string{"envs"}},
		{"configure-path-keep", "4\n\n", []string{"configure"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			before := condaInteractiveSnapshot(t, m, home)
			var out, stderr bytes.Buffer
			c := CLI{Interactive: true, In: strings.NewReader(test.input), Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
				t.Fatal("keep queried an upstream tool")
				return nil, nil
			}}
			if c.Run(append([]string{"--root", m.Root, "conda"}, test.args...)) != 0 {
				t.Fatal(stderr.String())
			}
			if !strings.Contains(out.String(), "已保留现有设置") || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
				t.Fatal("keep changed configuration", out.String())
			}
			if test.name == "envs" && !strings.Contains(stderr.String(), m.Config.Conda.EnvsDir) {
				t.Fatal("path prompt did not display the current managed value")
			}
		})
	}
}

func TestCondaInteractivePipChoicesReuseSavedDefault(t *testing.T) {
	for _, test := range []struct {
		name, input, mirror, index string
		args                       []string
	}{
		{"configure-official", "3\n3\ny\n", "official", "https://pypi.org/simple", []string{"configure"}},
		{"missing-tool", "2\n3\ny\n", "official", "https://pypi.org/simple", []string{"mirror"}},
		{"custom", "6\nhttps://packages.example/simple\ny\n", "custom", "https://packages.example/simple", []string{"mirror", "pip"}},
		{"default", "2\ny\n", "default", "", []string{"mirror", "pip"}},
		{"off", "7\ny\n", "", "", []string{"mirror", "pip"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			defaults := m.Config.Conda.Defaults
			nativeBefore := condaInteractiveSnapshot(t, m, home)
			var out, stderr bytes.Buffer
			c := CLI{Interactive: true, In: strings.NewReader(test.input), Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
				t.Fatal("recaptured an existing default while choosing pip mirror")
				return nil, nil
			}}
			if c.Run(append([]string{"--root", m.Root, "conda"}, test.args...)) != 0 {
				t.Fatal(stderr.String())
			}
			reloaded, err := New(m.Root)
			if err != nil {
				t.Fatal(err)
			}
			cfg := reloaded.Config.Conda
			if cfg.PipMirror != test.mirror || cfg.PipIndexURL != test.index || !reflect.DeepEqual(cfg.Defaults, defaults) || cfg.CondaMirror != "tuna" {
				t.Fatalf("unexpected saved choices: %#v", cfg)
			}
			for path, before := range nativeBefore {
				if filepath.Dir(path) == m.Root {
					continue
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("pip mirror changed native configuration", path, err)
				}
			}
		})
	}
}

func TestCondaInteractivePathUsesExistingMutationWithoutCreatingData(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	defaults := m.Config.Conda.Defaults
	target := filepath.Join(home, "new cache", "pkgs")
	var out, stderr bytes.Buffer
	c := CLI{Interactive: true, In: strings.NewReader("5\n" + target + "\ny\n"), Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
		t.Fatal("cache choice queried upstream")
		return nil, nil
	}}
	if c.Run([]string{"--root", m.Root, "conda", "configure"}) != 0 {
		t.Fatal(stderr.String())
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Config.Conda.PkgsDir != target || reloaded.Config.Conda.EnvsDir != m.Config.Conda.EnvsDir || !reflect.DeepEqual(reloaded.Config.Conda.Defaults, defaults) {
		t.Fatal("path choice changed unrelated selections")
	}
	native, err := os.ReadFile(filepath.Join(home, ".condarc"))
	if err != nil || !bytes.Contains(native, []byte(target)) {
		t.Fatal("existing native path writer was not used", err)
	}
	if _, err := os.Lstat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Fatal("created a data directory", err)
	}
}

func TestCondaInteractiveCondaMirrorPreservesOriginalDefault(t *testing.T) {
	for _, test := range []struct{ input, mirror string }{{"3\ny\n", "official"}, {"2\ny\n", "default"}, {"5\ny\n", ""}} {
		t.Run(condaInteractiveValue(test.mirror), func(t *testing.T) {
			m, _ := condaInteractiveFixture(t)
			defaults := m.Config.Conda.Defaults
			var out, stderr bytes.Buffer
			c := CLI{Interactive: true, In: strings.NewReader(test.input), Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
			if c.Run([]string{"--root", m.Root, "conda", "mirror", "conda"}) != 0 {
				t.Fatal(stderr.String())
			}
			reloaded, err := New(m.Root)
			if err != nil || reloaded.Config.Conda.CondaMirror != test.mirror || !reflect.DeepEqual(reloaded.Config.Conda.Defaults, defaults) || reloaded.Config.Conda.PipMirror != "tuna" {
				t.Fatal("Conda choice overwrote original default or unrelated mirror", err)
			}
		})
	}
}

func TestCondaInteractiveMissingPathsAndOffKeepNativeSemantics(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{"envs", []string{"envs"}}, {"cache-pip", []string{"cache"}}, {"envs-off", []string{"envs"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			target := filepath.Join(home, "new-data", "directory")
			input := target + "\ny\n"
			if test.name == "cache-pip" {
				input = "2\n" + input
			} else if test.name == "envs-off" {
				input = "off\ny\n"
			}
			nativeBefore, err := os.ReadFile(filepath.Join(home, ".condarc"))
			if err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			c := CLI{Interactive: true, In: strings.NewReader(input), Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
				t.Fatal("path completion queried upstream")
				return nil, nil
			}}
			if c.Run(append([]string{"--root", m.Root, "conda"}, test.args...)) != 0 {
				t.Fatal(stderr.String())
			}
			reloaded, err := New(m.Root)
			if err != nil {
				t.Fatal(err)
			}
			cfg := reloaded.Config.Conda
			switch test.name {
			case "envs":
				if cfg.EnvsDir != target {
					t.Fatal("environment directory was not applied")
				}
			case "cache-pip":
				if cfg.PipCacheDir != target {
					t.Fatal("pip cache directory was not applied")
				}
			case "envs-off":
				after, err := os.ReadFile(filepath.Join(home, ".condarc"))
				if cfg.EnvsDir != "" || err != nil || !bytes.Equal(after, nativeBefore) {
					t.Fatal("off removed the native path", err)
				}
			}
			if !reflect.DeepEqual(cfg.Defaults, m.Config.Conda.Defaults) {
				t.Fatal("path choice changed default snapshot")
			}
			if _, err := os.Lstat(filepath.Dir(target)); !os.IsNotExist(err) {
				t.Fatal("path selection created data directories", err)
			}
		})
	}
}

func TestCondaInteractiveEmptyPathKeepsUnmanagedState(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	before := condaInteractiveSnapshot(t, m, home)
	var out, stderr bytes.Buffer
	c := CLI{Interactive: true, In: strings.NewReader("\n"), Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
		t.Fatal("keeping an unmanaged path queried upstream")
		return nil, nil
	}}
	if c.Run([]string{"--root", m.Root, "conda", "envs"}) != 0 || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
		t.Fatal("empty path unexpectedly enabled management", stderr.String())
	}
}

func TestCondaInteractiveCancellationDoesNotQueryOrWrite(t *testing.T) {
	for _, test := range []struct {
		args  []string
		input string
	}{
		{[]string{"configure"}, "q\n"}, {[]string{"configure"}, ""},
		{[]string{"mirror"}, "q\n"}, {[]string{"mirror", "pip"}, "quit\n"},
		{[]string{"mirror", "pip"}, "6\nq\n"}, {[]string{"envs"}, "q\n"},
		{[]string{"cache", "pip"}, ""}, {[]string{"configure", "extra"}, "\n"},
	} {
		t.Run(strings.Join(test.args, "-")+fmt.Sprint(len(test.input)), func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			before := condaInteractiveSnapshot(t, m, home)
			var out, stderr bytes.Buffer
			c := CLI{Interactive: true, In: strings.NewReader(test.input), Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
				t.Fatal("cancelled prompt queried upstream")
				return nil, nil
			}}
			if c.Run(append([]string{"--root", m.Root, "conda"}, test.args...)) == 0 {
				t.Fatal("cancelled or invalid interaction succeeded")
			}
			if !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
				t.Fatal("cancelled interaction changed configuration")
			}
		})
	}
}

func TestCondaInteractiveMissingArgumentsStayStrictInScripts(t *testing.T) {
	for _, flags := range [][]string{nil, {"--yes"}, {"--json"}, {"--dry-run"}} {
		for _, args := range [][]string{{"configure"}, {"mirror"}, {"mirror", "pip"}, {"cache"}, {"cache", "conda"}, {"envs"}} {
			t.Run(strings.Join(append(append([]string{}, args...), flags...), "-"), func(t *testing.T) {
				m, home := condaInteractiveFixture(t)
				before := condaInteractiveSnapshot(t, m, home)
				input := strings.NewReader("1\n/home/should-not-be-read\ny\n")
				var out, stderr bytes.Buffer
				c := CLI{Interactive: len(flags) != 0, In: input, Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
					t.Fatal("missing script arguments queried upstream")
					return nil, nil
				}}
				command := append(append([]string{"--root", m.Root, "conda"}, args...), flags...)
				if c.Run(command) == 0 || input.Len() != len("1\n/home/should-not-be-read\ny\n") {
					t.Fatal("script supplied missing values from stdin", stderr.String())
				}
				if !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
					t.Fatal("missing script arguments changed configuration")
				}
			})
		}
	}
}

func TestCondaInteractiveExplicitArgumentsKeepYesBehavior(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	target := filepath.Join(home, "explicit-pip-cache")
	input := strings.NewReader("unused\n")
	var out, stderr bytes.Buffer
	c := CLI{Interactive: true, In: input, Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
	if c.Run([]string{"--root", m.Root, "conda", "cache", "pip", target, "--yes"}) != 0 {
		t.Fatal(stderr.String())
	}
	reloaded, err := New(m.Root)
	if err != nil || reloaded.Config.Conda.PipCacheDir != target || input.Len() != len("unused\n") {
		t.Fatal("explicit --yes path changed behavior", err)
	}
}
