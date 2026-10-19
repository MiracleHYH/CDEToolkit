package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallContextAlreadyCanceledCreatesNothingAndDoesNotExecute(t *testing.T) {
	s, r, artifact := coreService(t)
	s.Root = filepath.Join(s.Root, "absent")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.HTTP = &http.Client{Transport: coreRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("canceled install made network request")
		return nil, nil
	})}
	for _, source := range []string{"latest", artifact} {
		if err := s.InstallContext(ctx, source, ""); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled install: %v", err)
		}
	}
	if _, err := os.Lstat(s.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled install created software directory", err)
	}
	if len(r.commands) != 0 || r.starts != 0 || r.stops != 0 {
		t.Fatal("canceled install executed core")
	}
}

func TestLatestAndCheckUpdateContextCancelOnlyTheirOwnMetadataRequest(t *testing.T) {
	for _, action := range []string{"latest", "check-update"} {
		t.Run(action, func(t *testing.T) {
			s, r, artifact := coreService(t)
			if err := s.Install(artifact, ""); err != nil {
				t.Fatal(err)
			}
			_, program, _ := s.paths()
			before, err := capture(program, maxProgram, false)
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			s.HTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
				close(started)
				<-req.Context().Done()
				return nil, req.Context().Err()
			})}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				if action == "latest" {
					_, err := s.LatestContext(ctx)
					done <- err
				} else {
					_, err := s.CheckUpdateContext(ctx)
					done <- err
				}
			}()
			<-started
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("metadata cancellation lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("metadata cancellation did not return")
			}
			if err := before.check(); err != nil {
				t.Fatal("metadata query changed core", err)
			}
			client, api, _ := coreReleaseFixture(t, "")
			s.HTTP, s.API = client, api
			if version, err := s.Latest(); err != nil || version != "v1.2.4" {
				t.Fatalf("canceled ctx leaked into later action: %s %v", version, err)
			}
			if r.starts != 0 || r.stops != 0 {
				t.Fatal("version query controlled runtime")
			}
		})
	}
}

func TestInstallContextCancelsPinnedAssetWithoutMetadataOrCandidateExecution(t *testing.T) {
	for _, source := range []string{"", "latest"} {
		t.Run(source, func(t *testing.T) {
			s, r, _ := coreService(t)
			s.Platform = "linux/amd64"
			dir, program, _ := s.paths()
			config := filepath.Join(dir, "config.yaml")
			coreWrite(t, config, []byte(coreConfig))
			before, err := capture(config, maxConfig, false)
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			s.APIHTTP = &http.Client{Transport: coreRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Error("first install used APIHTTP")
				return nil, errors.New("metadata must not participate in install")
			})}
			s.HTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
				asset, err := pinnedInstallAsset("linux/amd64")
				if err != nil || req.URL.String() != asset.source() {
					t.Errorf("first install used unexpected URL %s: %v", req.URL, err)
				}
				close(started)
				<-req.Context().Done()
				return nil, req.Context().Err()
			})}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- s.InstallContext(ctx, source, "") }()
			select {
			case <-started:
			case <-time.After(time.Second):
				cancel()
				t.Fatal("first install never requested its pinned asset")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("download cancellation lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("install request did not cancel")
			}
			if len(r.commands) != 0 || r.starts != 0 || r.stops != 0 {
				t.Fatal("canceled download executed or controlled core")
			}
			if _, err := os.Lstat(program); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("canceled download installed core", err)
			}
			if files, _ := filepath.Glob(filepath.Join(dir, ".nido-core-*")); len(files) != 0 {
				t.Fatal("canceled download left candidate", files)
			}
			if err := before.check(); err != nil {
				t.Fatal("canceled download changed existing config", err)
			}
		})
	}
}

type cancelOnArtifactClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r cancelOnArtifactClose) Close() error { err := r.ReadCloser.Close(); r.cancel(); return err }

func TestInstallContextCancellationAfterAssetReadPreventsCandidateWrite(t *testing.T) {
	s, r, _ := coreService(t)
	s.Platform = "linux/amd64"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.HTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
		response := coreAPIResponse(req, []byte("inert response; cancellation precedes validation"))
		response.Body = cancelOnArtifactClose{ReadCloser: response.Body, cancel: cancel}
		return response, nil
	})}
	if err := s.InstallContext(ctx, "latest", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-download cancellation lost: %v", err)
	}
	dir, program, _ := s.paths()
	if _, err := os.Lstat(program); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled artifact committed", err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, ".nido-core-*")); len(files) != 0 {
		t.Fatal("post-download cancel wrote candidate", files)
	}
	if len(r.commands) != 0 {
		t.Fatal("post-download cancellation executed candidate")
	}
}

func TestInstallContextCancellationDuringCandidateChecksPreventsCommit(t *testing.T) {
	for _, stage := range []string{"version", "validation", "commit"} {
		t.Run(stage, func(t *testing.T) {
			s, r, artifact := coreService(t)
			dir, program, _ := s.paths()
			config := filepath.Join(dir, "config.yaml")
			coreWrite(t, config, []byte(coreConfig))
			coreWrite(t, config+".back", []byte("preserved backup"))
			before, err := capture(config, maxConfig, false)
			if err != nil {
				t.Fatal(err)
			}
			backup, err := capture(config+".back", maxConfig, false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			coreStoppedRecord(t, s)
			r.run = func(c Command) ([]byte, error) {
				if c.Context != ctx {
					t.Error("candidate command lost parent context")
				}
				if stage == "version" && c.Args[0] == "-v" || stage == "validation" && c.Args[0] == "-t" {
					cancel()
				}
				if c.Args[0] == "-v" {
					return []byte("Mihomo Meta v1.2.3"), nil
				}
				return nil, nil
			}
			if stage == "commit" {
				r.inspect = func(int) (Process, error) {
					if len(r.commands) > 0 && r.commands[len(r.commands)-1].Args[0] == "-t" {
						cancel()
					}
					return Process{}, os.ErrNotExist
				}
			}
			if err := s.InstallContext(ctx, artifact, ""); !errors.Is(err, context.Canceled) {
				t.Fatalf("candidate-stage cancellation lost: %v", err)
			}
			if _, err := os.Lstat(program); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("canceled candidate committed", err)
			}
			if files, _ := filepath.Glob(filepath.Join(dir, ".nido-core-*")); len(files) != 0 {
				t.Fatal("canceled candidate leaked", files)
			}
			if err := before.check(); err != nil {
				t.Fatal("canceled install changed config", err)
			}
			if err := backup.check(); err != nil {
				t.Fatal("canceled install changed backup", err)
			}
			if r.starts != 0 || r.stops != 0 {
				t.Fatal("canceled install controlled a process")
			}
		})
	}
}

func TestOSRunnerParentContextCancelsOnlyFakeValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = (OSRunner{}).Run(Command{Context: ctx, Program: program, Dir: t.TempDir(), Args: []string{"-t"}, Env: []string{"HOME=" + os.Getenv("HOME"), "NIDO_PROXY_RUNTIME_HELPER=1", "NIDO_PROXY_RUNTIME_MODE=validate-sleep"}, Timeout: time.Minute})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("parent context did not bound fake validation: %v", err)
	}
}
