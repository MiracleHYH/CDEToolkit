package cdet

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFullHomeTreePreservesModificationTimeAndChecksTheCompleteSource(t *testing.T) {
	f := opaqueCodexFixture(t)
	path := filepath.Join(f.codex, "sessions", "future.jsonl")
	old := time.Unix(1600000000, 123456789)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := tree(t, f.codex)
	target := filepath.Join(f.root, "copy")
	if err := copyTree(snapshot, target); err != nil {
		t.Fatal(err)
	}
	copied, err := os.Stat(filepath.Join(target, "sessions", "future.jsonl"))
	if err != nil || !copied.ModTime().Equal(before.ModTime()) {
		t.Fatal("copy lost the source modification time", err)
	}
	if err := os.Chtimes(path, old.Add(time.Hour), old.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	again, err := snapshot.sourceSnapshot()
	if err != nil || again.RawDigest != snapshot.RawDigest || again.Digest != snapshot.Digest {
		t.Fatal("mtime incorrectly changed the content fingerprint", err)
	}
	write(t, filepath.Join(f.codex, "unknown-format", "payload.bin"), "CHANGED-OPAQUE-DATA", 0600)
	changed, err := snapshot.sourceSnapshot()
	if err != nil || changed.RawDigest == snapshot.RawDigest {
		t.Fatal("an unknown source file escaped the complete baseline", err)
	}
	if err := copyTree(snapshot, filepath.Join(f.root, "stale-copy")); err == nil {
		t.Fatal("copy accepted source data changed after the snapshot")
	}
}
