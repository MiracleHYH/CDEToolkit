package distribution

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func auditFixture(t *testing.T) (string, func(...string)) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v: %s", err, out)
		}
	}
	git("init", "-q")
	return root, git
}

func auditWrite(t *testing.T, root, name, text string) {
	t.Helper()
	target := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func auditTools(t *testing.T, gitleaksBody string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	if gitleaksBody != "" {
		if err := os.WriteFile(filepath.Join(bin, "gitleaks"), []byte("#!/bin/sh\n"+gitleaksBody), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

func TestPublicAuditCandidateBoundaries(t *testing.T) {
	root, git := auditFixture(t)
	auditWrite(t, root, ".gitignore", ".env\nruntime/\n")
	auditWrite(t, root, "source.go", "package fixture\n")
	auditWrite(t, root, "deleted.go", "package fixture\n")
	git("add", ".")
	git("commit", "-qm", "fixture")
	if err := os.Remove(filepath.Join(root, "deleted.go")); err != nil {
		t.Fatal(err)
	}
	auditWrite(t, root, ".env", "PRIVATE=ignored\n")
	auditWrite(t, root, "runtime/auth.json", "ignored credentials\n")
	auditWrite(t, root, "new file.go", "package fixture\n")
	auditTools(t, "")
	var out bytes.Buffer
	if err := AuditRepository(root, "", &out); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "3 publishable files") || !strings.Contains(out.String(), "gitleaks 未安装") {
		t.Fatal(out.String())
	}
	if strings.Contains(out.String(), "ignored credentials") {
		t.Fatal("audit exposed ignored file contents")
	}
}

func TestPublicAuditReportsFilesWithoutSecretContents(t *testing.T) {
	for _, test := range []struct{ name, data, pattern string }{
		{"nested/.env", "secret environment", ""},
		{"nested/.env.production", "secret environment", ""},
		{"nested/id_ed25519", "secret key", ""},
		{"nested/credentials.json", "secret auth", ""},
		{"config.toml", "private config", ""},
		{"saved/.codex/config.toml", "private codex config", ""},
		{"saved/sessions/session.jsonl", "private conversation", ""},
		{"ordinary.txt", "-----BEGIN " + "OPENSSH PRIVATE KEY-----\nprivate material\n", ""},
		{"ordinary.txt", "-----BEGIN " + "PRIVATE KEY-----\nprivate material\n", ""},
		{"ordinary.txt", "access = gh" + "p_" + strings.Repeat("x", 24), ""},
		{"ordinary.txt", "private marker not for publication", "private marker"},
	} {
		t.Run(test.name+test.data[:3], func(t *testing.T) {
			root, _ := auditFixture(t)
			auditWrite(t, root, test.name, test.data)
			auditTools(t, "")
			var out bytes.Buffer
			if err := AuditRepository(root, test.pattern, &out); err == nil {
				t.Fatal("sensitive source accepted")
			}
			if !strings.Contains(out.String(), test.name) || strings.Contains(out.String(), test.data) {
				t.Fatal("expected filename-only diagnostic", out.String())
			}
		})
	}
}

func TestPublicAuditDoesNotFollowCandidateLinks(t *testing.T) {
	root, _ := auditFixture(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("external private content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	auditTools(t, "")
	var out bytes.Buffer
	if err := AuditRepository(root, "external private content", &out); err == nil {
		t.Fatal("candidate symlink accepted")
	}
	if strings.Contains(out.String(), "自定义禁止模式") || strings.Contains(out.String(), "external private content") {
		t.Fatal("followed external link or exposed its contents", out.String())
	}
}

func TestPublicAuditGitleaksSnapshotAndHistory(t *testing.T) {
	root, git := auditFixture(t)
	auditWrite(t, root, "source", "public source\n")
	git("add", ".")
	git("commit", "-qm", "fixture")
	log := filepath.Join(t.TempDir(), "gitleaks-args")
	t.Setenv("NIDO_AUDIT_TEST_LOG", log)
	auditTools(t, `printf '%s\n' "$*" >> "$NIDO_AUDIT_TEST_LOG"
if [ "$1" = dir ]; then
  [ "$2" = --no-banner ] && [ "$3" = --redact ] && [ -f "$4/source" ] || exit 9
  [ ! -e "$4/.git" ] || exit 10
fi
exit 0
`)
	var out bytes.Buffer
	if err := AuditRepository(root, "", &out); err != nil {
		t.Fatal(err, out.String())
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "dir --no-banner --redact ") || lines[1] != "git --no-banner --redact --log-opts=--all "+canonicalRoot {
		t.Fatal("gitleaks source/history contract", string(data))
	}
	snapshot := strings.TrimPrefix(lines[0], "dir --no-banner --redact ")
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatal("temporary source snapshot left behind")
	}
}

func TestPublicAuditGitleaksFailureAndBadPattern(t *testing.T) {
	root, _ := auditFixture(t)
	auditWrite(t, root, "source", "public\n")
	auditTools(t, "exit 1\n")
	if err := AuditRepository(root, "", nil); err == nil {
		t.Fatal("failed scanner accepted")
	}
	if err := AuditRepository(root, "[", nil); err == nil || strings.Contains(err.Error(), "[") {
		t.Fatal("invalid pattern accepted or exposed", err)
	}
}
