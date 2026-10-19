package conda

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestServiceContextPreventsRunnerAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	s := fixture(t)
	s.Ctx = ctx
	s.Runner = func(string, []string, []string) ([]byte, error) {
		calls++
		return nil, fmt.Errorf("cancelled runner should not start")
	}
	if data, err := s.run("conda"); data != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query=%q %v", data, err)
	}
	if got, err := s.CaptureDefaults(MirrorDefaults{}, "all"); err == nil || !reflect.DeepEqual(got, MirrorDefaults{}) {
		t.Fatal("cancelled capture returned a baseline", err)
	}
	if checked, err := s.Validate(Config{PipCacheDir: filepath.Join(s.UserHome, "pip-cache")}); !errors.Is(err, context.Canceled) || len(checked) != 0 {
		t.Fatal("cancelled validation continued", err)
	}
	if calls != 0 {
		t.Fatal("cancelled service invoked a runner", calls)
	}
}

func TestServiceContextDiscardsRunnerOutputAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := fixture(t)
	s.Ctx = ctx
	s.Runner = func(string, []string, []string) ([]byte, error) {
		cancel()
		return []byte("PRIVATE-LATE-OUTPUT"), errors.New("PRIVATE-LATE-DIAGNOSTIC")
	}
	data, err := s.run("conda")
	if data != nil || !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "PRIVATE-LATE") {
		t.Fatalf("late runner result was accepted or leaked: %v", err)
	}
}

func TestCaptureContextCancellationStopsBeforeNextQueryAndKeepsBaseline(t *testing.T) {
	for _, cancelAt := range []int{1, 2} {
		t.Run(strconv.Itoa(cancelAt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := fixture(t)
			s.Ctx = ctx
			calls := 0
			s.Runner = func(name string, args, env []string) ([]byte, error) {
				calls++
				if calls == cancelAt {
					cancel()
				}
				if name == "conda" {
					return defaultsCondaJSON(t), nil
				}
				return []byte("global.index-url='https://fixture.invalid/simple'\n"), nil
			}
			got, err := s.CaptureDefaults(MirrorDefaults{}, "all")
			if err == nil || calls != cancelAt || !reflect.DeepEqual(got, MirrorDefaults{}) {
				t.Fatalf("cancelled capture published partial data or continued: calls=%d err=%v", calls, err)
			}
		})
	}
	// Adding a missing tool must also keep the already captured tool intact.
	t.Run("existing-conda-baseline", func(t *testing.T) {
		s, original := defaultsFixture(t)
		original.PipCaptured = false
		original.PipIndexURL = ""
		original.PipVariables = nil
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s.Ctx = ctx
		calls := 0
		s.Runner = func(name string, args, env []string) ([]byte, error) {
			calls++
			if name != "python" {
				t.Fatal("existing Conda baseline was recaptured")
			}
			cancel()
			return []byte("global.index-url='https://fixture.invalid/simple'\n"), nil
		}
		got, err := s.CaptureDefaults(original, "all")
		if err == nil || calls != 1 || !reflect.DeepEqual(got, original) {
			t.Fatal("cancellation changed an existing baseline", err)
		}
	})
}

func TestValidateContextCancellationStopsBeforeNextQuery(t *testing.T) {
	for _, cancelAt := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(cancelAt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := fixture(t)
			s.Ctx = ctx
			cfg := Config{PkgsDir: filepath.Join(s.UserHome, "pkgs"), PipCacheDir: filepath.Join(s.UserHome, "pip-cache")}
			calls := 0
			s.Runner = func(name string, args, env []string) ([]byte, error) {
				calls++
				if calls == cancelAt {
					cancel()
				}
				switch calls {
				case 1:
					if name != "conda" {
						t.Fatal("unexpected first validation query", name)
					}
					return []byte(`{"pkgs_dirs":[` + strconv.Quote(cfg.PkgsDir) + `]}`), nil
				case 2:
					if name != "python" || strings.Join(args, " ") != "-m pip config list" {
						t.Fatal("unexpected pip configuration query", name, args)
					}
					return []byte("global.cache-dir=" + strconv.Quote(cfg.PipCacheDir) + "\n"), nil
				case 3:
					if name != "python" || strings.Join(args, " ") != "-m pip cache dir" {
						t.Fatal("unexpected pip cache query", name, args)
					}
					return []byte(cfg.PipCacheDir + "\n"), nil
				default:
					t.Fatal("validation started an extra query")
					return nil, nil
				}
			}
			if _, err := s.Validate(cfg); !errors.Is(err, context.Canceled) || calls != cancelAt {
				t.Fatalf("cancelled validation continued or accepted late data: calls=%d err=%v", calls, err)
			}
			for _, path := range []string{cfg.PkgsDir, cfg.PipCacheDir} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("read-only validation created a data directory", err)
				}
			}
		})
	}
}
