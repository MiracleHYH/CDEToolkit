//go:build linux && (amd64 || arm64)

package gdsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileSnapshot(t *testing.T, path, data string) Snapshot {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Capture(path, 1024, false)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCommitPreservesReplacementAfterLastSnapshotCheck(t *testing.T) {
	dir := t.TempDir()
	before := fileSnapshot(t, filepath.Join(dir, "config"), "old")
	candidate := fileSnapshot(t, filepath.Join(dir, "candidate"), "requested")
	edited := fileSnapshot(t, filepath.Join(dir, "editor"), "late user edit")
	// Exact editor interleaving after CommitCandidate's final checks.
	if err := os.Rename(edited.Path, before.Path); err != nil {
		t.Fatal(err)
	}
	_, err := commitCandidateChecked(candidate, before)
	if err == nil || !strings.Contains(err.Error(), candidate.Path) {
		t.Fatal("late edit was not reported with its recovery path:", err)
	}
	if got, _ := os.ReadFile(candidate.Path); string(got) != "late user edit" {
		t.Fatal("displaced user edit was lost")
	}
	if got, _ := os.ReadFile(before.Path); string(got) != "requested" {
		t.Fatal("unexpected committed target")
	}
	// The caller's ordinary candidate cleanup must not unlink displaced data.
	RemoveCreated(candidate.Path, candidate.Info)
	if got, _ := os.ReadFile(candidate.Path); string(got) != "late user edit" {
		t.Fatal("candidate defer removed the retained user edit")
	}
}

func TestCommitNeverOverwritesLateCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	before, err := Capture(path, 1024, true)
	if err != nil {
		t.Fatal(err)
	}
	candidate := fileSnapshot(t, filepath.Join(dir, "candidate"), "requested")
	fileSnapshot(t, path, "late user creation")
	if _, err := commitCandidateChecked(candidate, before); err == nil {
		t.Fatal("late creation was overwritten")
	}
	if got, _ := os.ReadFile(path); string(got) != "late user creation" {
		t.Fatal("late creation was lost")
	}
}

func TestCommitPreservesLateSymlinkWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	before := fileSnapshot(t, filepath.Join(dir, "config"), "old")
	candidate := fileSnapshot(t, filepath.Join(dir, "candidate"), "requested")
	secret := fileSnapshot(t, filepath.Join(dir, "secret"), "user data")
	if err := os.Remove(before.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret.Path, before.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := commitCandidateChecked(candidate, before); err == nil || !strings.Contains(err.Error(), candidate.Path) {
		t.Fatal("late symlink was not retained:", err)
	}
	if got, err := os.Readlink(candidate.Path); err != nil || got != secret.Path {
		t.Fatal("late symlink evidence was lost:", err)
	}
	if got, _ := os.ReadFile(secret.Path); string(got) != "user data" {
		t.Fatal("symlink destination was modified")
	}
}

func TestRemovePreservesReplacementAfterLastSnapshotCheck(t *testing.T) {
	dir := t.TempDir()
	before := fileSnapshot(t, filepath.Join(dir, "gds"), "owned program")
	edited := fileSnapshot(t, filepath.Join(dir, "editor"), "late user program")
	if err := os.Rename(edited.Path, before.Path); err != nil {
		t.Fatal(err)
	}
	recovery := filepath.Join(dir, "recovery")
	if err := removeExpectedChecked(before, recovery); err == nil || !strings.Contains(err.Error(), recovery) {
		t.Fatal("late program was not retained:", err)
	}
	if got, _ := os.ReadFile(recovery); string(got) != "late user program" {
		t.Fatal("late program was deleted")
	}
}
