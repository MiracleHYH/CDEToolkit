package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsapi"
)

func TestHelperGateSubprocess(t *testing.T) {
	if os.Getenv("GDS_HELPER_GATE_TEST") == "1" {
		args := []string{"__helper", "--", "--version", "--json"}
		if os.Getenv("GDS_HELPER_GATE_PROBE") == "1" {
			args = []string{"__helper", "--", "probe", "--uuid", "GPU-01234567-89ab-cdef-0123-456789abcdef"}
		}
		if err := run(context.Background(), args, os.Stdin, os.Stdout, os.Stderr); err != nil {
			io.WriteString(os.Stderr, err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}
	for _, mode := range []string{"authorized", "parent-death", "denied", "probe-parent-death"} {
		t.Run(mode, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestHelperGateSubprocess$")
			cmd.Env = append(os.Environ(), "GDS_HELPER_GATE_TEST=1")
			if mode == "probe-parent-death" {
				cmd.Env = append(cmd.Env, "GDS_HELPER_GATE_PROBE=1")
			}
			cmd.ExtraFiles = []*os.File{reader}
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			// Withhold authorization: metadata must not be emitted and the
			// probe must not dispatch, even though its full arguments are known.
			select {
			case err := <-done:
				t.Fatalf("helper executed before authorization: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			switch mode {
			case "authorized":
				_, err = writer.Write([]byte{1})
			case "denied":
				_, err = writer.Write([]byte{0})
			default:
				err = writer.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("helper gate did not settle")
			}
			if mode == "authorized" {
				var metadata gdsapi.Metadata
				if err != nil || json.Unmarshal(out.Bytes(), &metadata) != nil || metadata.Product != "gds" {
					t.Fatalf("metadata after authorization: %v %s %s", err, out.String(), stderr.String())
				}
			} else if err == nil || out.Len() != 0 || (mode == "probe-parent-death" && !strings.Contains(stderr.String(), "EOF")) {
				t.Fatalf("unauthorized helper dispatched: %v %s %s", err, out.String(), stderr.String())
			}
		})
	}
}

func TestHelperRejectsLifecycleAndNestedCommands(t *testing.T) {
	for _, command := range []string{"run", "__worker", "__helper", "help"} {
		if err := run(context.Background(), []string{"__helper", "--", command}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatalf("helper accepted command %s", command)
		}
	}
}

func TestHelperGateCancelable(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := readHelperGate(ctx, reader); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("helper gate cancellation: %v", err)
	}
}

func TestCanceledHelperRejectsQueuedAuthorization(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = writer.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = readHelperGate(ctx, reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued authorization revived canceled helper: %v", err)
	}
	// Recursive dispatch must also reject cancellation after a successful
	// gate read; metadata provides observable dispatch without native calls.
	var out bytes.Buffer
	if err = run(ctx, []string{"--version", "--json"}, strings.NewReader(""), &out, io.Discard); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatalf("canceled helper dispatched metadata: %v %s", err, out.String())
	}
}
