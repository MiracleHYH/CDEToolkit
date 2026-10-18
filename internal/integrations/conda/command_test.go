package conda

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRunnerUsesProvidedEnvironmentAndExplicitCondaExecutable(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "fixture conda")
	if err := os.WriteFile(program, []byte("#!/bin/sh\n[ \"$#\" = 2 ] && [ \"$1\" = config ] && [ \"$2\" = --show ] || exit 9\nprintf '%s' \"$CONDA_ENVS_PATH\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=/nonexistent", "CONDA_EXE=" + program, "CONDA_ENVS_PATH=/chosen/envs"}
	data, err := runCommand("conda", []string{"config", "--show"}, env)
	if err != nil || string(data) != "/chosen/envs" {
		t.Fatalf("default command ignored explicit env: %q %v", data, err)
	}
	if _, err := runCommand("python", nil, env); err == nil {
		t.Fatal("command lookup leaked ambient process PATH")
	}
	if _, err := commandPath("conda", []string{"CONDA_EXE=./relative", "PATH=/usr/bin"}); err == nil {
		t.Fatal("accepted relative CONDA_EXE")
	}
}

func TestDefaultRunnerBoundsOutputAndHidesDiagnostics(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "conda")
	script := "#!/bin/sh\ni=0\nwhile [ \"$i\" -lt 20000 ]; do printf '%080d\\n' \"$i\"; i=$((i+1)); done\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if data, err := runCommand("conda", nil, []string{"PATH=" + dir}); err == nil || data != nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("unbounded output: bytes=%d err=%v", len(data), err)
	}
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf 'secret-token' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand("conda", nil, []string{"PATH=" + dir}); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("upstream diagnostics leaked: %v", err)
	}
}

func TestBoundedOutputCancelsWithoutRetainingOversizedPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := boundedOutput{cancel: cancel}
	data := bytes.Repeat([]byte("x"), maxOutput+100)
	if n, err := out.Write(data); n != len(data) || err != nil || out.data.Len() != maxOutput || !out.full || ctx.Err() == nil {
		t.Fatalf("output bound failed: n=%d len=%d full=%v ctx=%v", n, out.data.Len(), out.full, ctx.Err())
	}
}
