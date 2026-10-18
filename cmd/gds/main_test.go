package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
)

func TestMetadataDoesNotLoadDrivers(t *testing.T) {
	// Poison driver paths: this is still a pure metadata operation.
	t.Setenv("LD_LIBRARY_PATH", t.TempDir())
	t.Setenv("CUDA_VISIBLE_DEVICES", "")
	var out, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version", "--json"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	var m gdsapi.Metadata
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Product != "gds" || m.Protocol != gdsapi.ProtocolVersion || m.Version == "" || m.Platform == "" {
		t.Fatalf("invalid metadata: %+v", m)
	}
}

func TestRejectInvalidCommandsWithoutSideEffects(t *testing.T) {
	for _, args := range [][]string{{"--version", "--unknown"}, {"run"}, {"run", "--dir", "relative"}, {"__probe", "--gpu", "0"}, {"__worker", "--gpu", "GPU-00000000-0000-0000-0000-000000000000"}, {"install"}} {
		var out, stderr bytes.Buffer
		if err := run(context.Background(), args, strings.NewReader(""), &out, &stderr); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
