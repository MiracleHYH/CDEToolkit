package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestNativeFixtureReplacementStaysPrivateAndKeepsOnlyLatestBackup(t *testing.T) {
	directory := fixtureDirectory(t)
	program := filepath.Join(directory, "mihomo")
	data := []byte("inert test bytes; this unit test never executes a component\n")
	if err := os.WriteFile(program, data, 0700); err != nil {
		t.Fatal(err)
	}
	previousArgs := os.Args
	os.Args = []string{program}
	t.Cleanup(func() { os.Args = previousArgs })
	for revision := 2; revision <= 3; revision++ {
		before, err := os.ReadFile(program)
		if err != nil {
			t.Fatal(err)
		}
		path, err := fakeNativeUpgrade(directory)
		if err != nil || path != program {
			t.Fatal(path, err)
		}
		after, err := os.ReadFile(program)
		if err != nil || fixtureRevision(after) != revision || bytes.Equal(before, after) {
			t.Fatal(revision, err)
		}
		backup, err := os.ReadFile(filepath.Join(directory, "meta-backup", "mihomo"))
		if err != nil || !bytes.Equal(backup, before) {
			t.Fatal("latest native backup differs", err)
		}
		if _, err := os.Lstat(filepath.Join(directory, "meta-update")); !os.IsNotExist(err) {
			t.Fatal("fixture left its update directory", err)
		}
	}
}

func TestNativeFixtureRefusesExternalOrUnknownTargets(t *testing.T) {
	for _, problem := range []string{"different-executable", "program-link", "existing-update", "backup-link", "backup-hardlink", "unknown-backup"} {
		t.Run(problem, func(t *testing.T) {
			directory, outside := fixtureDirectory(t), fixtureDirectory(t)
			program := filepath.Join(directory, "mihomo")
			external := filepath.Join(outside, "preserved")
			if err := os.WriteFile(program, []byte("private fake program"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(external, []byte("external sentinel"), 0700); err != nil {
				t.Fatal(err)
			}
			previousArgs := os.Args
			os.Args = []string{program}
			t.Cleanup(func() { os.Args = previousArgs })
			switch problem {
			case "different-executable":
				os.Args[0] = external
			case "program-link":
				if err := os.Remove(program); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, program); err != nil {
					t.Fatal(err)
				}
			case "existing-update":
				if err := os.Mkdir(filepath.Join(directory, "meta-update"), 0700); err != nil {
					t.Fatal(err)
				}
			case "backup-link":
				if err := os.Symlink(outside, filepath.Join(directory, "meta-backup")); err != nil {
					t.Fatal(err)
				}
			case "backup-hardlink":
				if err := os.Mkdir(filepath.Join(directory, "meta-backup"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(external, filepath.Join(directory, "meta-backup", "mihomo")); err != nil {
					t.Fatal(err)
				}
			case "unknown-backup":
				if err := os.Mkdir(filepath.Join(directory, "meta-backup"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "meta-backup", "unknown"), []byte("unknown sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fakeNativeUpgrade(directory); err == nil {
				t.Fatal("unsafe native fixture target accepted", problem)
			}
			if data, err := os.ReadFile(external); err != nil || string(data) != "external sentinel" {
				t.Fatal("external sentinel changed", err)
			}
		})
	}
}

func TestNativeFixtureControllerKeepsAnonymousNetworkAndRejectedActionsSeparate(t *testing.T) {
	directory := fixtureDirectory(t)
	config := configuration{Controller: "127.0.0.1:0", Secret: "isolated-controller-secret"}
	listener, err := fixtureController(config, directory, filepath.Join(directory, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	for _, item := range []struct {
		method, path, body, secret, mode string
		status                           int
	}{
		{http.MethodGet, "/version", "", "", "", http.StatusUnauthorized},
		{http.MethodGet, "/version", "", config.Secret, "", http.StatusOK},
		{http.MethodPost, "/upgrade?channel=release&force=true", "", config.Secret, "", http.StatusNotFound},
		{http.MethodPost, "/upgrade?channel=release", "", config.Secret, "reject", http.StatusServiceUnavailable},
		{http.MethodPut, "/configs?force=true", `{"path":"/outside"}`, config.Secret, "", http.StatusBadRequest},
	} {
		if err := os.WriteFile(filepath.Join(directory, "nido-test-native-mode"), []byte(item.mode), 0600); err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(item.method, "http://"+listener.Addr().String()+item.path, strings.NewReader(item.body))
		if err != nil {
			t.Fatal(err)
		}
		if item.secret != "" {
			request.Header.Set("Authorization", "Bearer "+item.secret)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != item.status {
			t.Fatal(item.path, response.StatusCode, err)
		}
	}
	log, err := os.ReadFile(filepath.Join(directory, "nido-test-controller.jsonl"))
	if err != nil || bytes.Contains(log, []byte(config.Secret)) {
		t.Fatal("controller evidence exposed secret", err)
	}
}
