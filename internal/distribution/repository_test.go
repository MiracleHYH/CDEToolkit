package distribution

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReleaseRepositoryRequiresCleanTaggedHead(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v: %s", err, out)
		}
	}
	git("init", "-q")
	path := filepath.Join(dir, "source")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "source")
	git("commit", "-qm", "fixture")
	git("tag", "v1.2.3")
	if err := CheckRepository(dir, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := CheckRepository(dir, "v1.2.4"); err == nil {
		t.Fatal("nonexistent tag accepted")
	}
	if err := os.WriteFile(path, []byte("later"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckRepository(dir, "v1.2.3"); err == nil {
		t.Fatal("dirty source tree accepted")
	}
	git("add", "source")
	git("commit", "-qm", "later fixture")
	if err := CheckRepository(dir, "v1.2.3"); err == nil {
		t.Fatal("tag pointing at an older commit accepted")
	}
}
