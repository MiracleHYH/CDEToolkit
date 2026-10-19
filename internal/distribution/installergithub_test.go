package distribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Download policy belongs to Go after bootstrap. The bootstrap has no HTTP
// client, proxy parser, retry/fallback loop, or interactive Shell implementation.
func TestInstallerLeavesNetworkAndInteractionToGo(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"curl ", "wget ", "read -r", "选择编号", "case \"$1\"", "GITHUB_PROXY", "https://gh-proxy", "/dev/tty"} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("Shell bootstrap contains Go business logic: %q", forbidden)
		}
	}
	if !strings.Contains(string(source), `bootstrap-install "$@" </dev/null`) {
		t.Fatal("installer may consume piped source")
	}
	for _, required := range []string{"ulimit -f 131072", "ulimit -f 262144", "release_sha256", "release_size", "compressed_size", "trap cleanup EXIT"} {
		if !strings.Contains(string(source), required) {
			t.Fatalf("missing bootstrap safety check %q", required)
		}
	}
}
