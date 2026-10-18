package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverPrefersValidEnvironmentOverDefault(t *testing.T) {
	s, external := fixture(t)
	defaultHome := filepath.Join(s.UserHome, ".codex")
	for _, path := range []string{external, defaultHome} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s.Env = append(s.Env, "CODEX_HOME="+external)
	candidates, err := s.Discover()
	want := []Candidate{{Home: external, Source: "CODEX_HOME"}}
	if err != nil || !reflect.DeepEqual(candidates, want) {
		t.Fatalf("got %+v, %v; want %+v", candidates, err, want)
	}
}

func TestDiscoverFallsBackToDefaultForUnavailableEnvironment(t *testing.T) {
	for _, state := range []string{"unset", "empty", "missing", "file", "broken-link", "unsafe-internal", "relative"} {
		t.Run(state, func(t *testing.T) {
			s, external := fixture(t)
			defaultHome := filepath.Join(s.UserHome, ".codex")
			if err := os.Mkdir(defaultHome, 0700); err != nil {
				t.Fatal(err)
			}
			path := external
			switch state {
			case "empty":
				path = ""
			case "file":
				if err := os.WriteFile(path, []byte("USER FILE"), 0600); err != nil {
					t.Fatal(err)
				}
			case "broken-link":
				if err := os.Symlink(external+"-missing", path); err != nil {
					t.Fatal(err)
				}
			case "unsafe-internal":
				path = s.Root
			case "relative":
				path = "relative-codex"
			}
			if state != "unset" {
				s.Env = append(s.Env, "CODEX_HOME="+path)
			}
			candidates, err := s.Discover()
			want := []Candidate{{Home: defaultHome, Source: "default"}}
			if err != nil || !reflect.DeepEqual(candidates, want) {
				t.Fatalf("got %+v, %v; want %+v", candidates, err, want)
			}
			if entries, err := os.ReadDir(defaultHome); err != nil || len(entries) != 0 {
				t.Fatalf("discovery changed default home: %v, %v", entries, err)
			}
			if state == "missing" {
				if _, err := os.Lstat(external); !os.IsNotExist(err) {
					t.Fatal("discovery created unavailable environment home")
				}
			}
			if state == "file" {
				if got, err := os.ReadFile(path); err != nil || string(got) != "USER FILE" {
					t.Fatal("discovery changed invalid environment file")
				}
			}
		})
	}
}

func TestDiscoverEquivalentPathsRetainEnvironmentSource(t *testing.T) {
	s, external := fixture(t)
	defaultHome := filepath.Join(s.UserHome, ".codex")
	if err := os.Mkdir(defaultHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(defaultHome, external); err != nil {
		t.Fatal(err)
	}
	s.Env = append(s.Env, "CODEX_HOME="+external)
	candidates, err := s.Discover()
	want := []Candidate{{Home: defaultHome, Source: "CODEX_HOME"}}
	if err != nil || !reflect.DeepEqual(candidates, want) {
		t.Fatalf("got %+v, %v; want %+v", candidates, err, want)
	}
	if target, err := os.Readlink(external); err != nil || target != defaultHome {
		t.Fatal("discovery changed the environment alias")
	}
}

func TestDiscoverMissingHomesDoesNotCreatePaths(t *testing.T) {
	s, external := fixture(t)
	s.Env = append(s.Env, "CODEX_HOME="+external)
	candidates, err := s.Discover()
	if err != nil || len(candidates) != 0 {
		t.Fatalf("unexpected candidate for missing homes: %+v, %v", candidates, err)
	}
	for _, path := range []string{external, filepath.Join(s.UserHome, ".codex")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("discovery created or altered missing path: %s, %v", path, err)
		}
	}
}
