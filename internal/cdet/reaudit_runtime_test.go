package cdet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReauditOfflineProgramDoesNotDependOnProxyConfiguration(t *testing.T) {
	f := newFixture(t)
	m := f.manager(t, Options{Artifacts: map[string]string{"proxy": filepath.Join(f.proxy, "mihomo")}})
	config := filepath.Join(m.Store, "proxy", "config.yaml")
	write(t, config, "mixed-port: [invalid yaml", 0600)
	before := read(t, config)
	payload, e := m.downloadProgram("proxy")
	if e != nil || digest(payload) != digest([]byte(read(t, filepath.Join(f.proxy, "mihomo")))) {
		t.Fatal("offline artifact depended on a network proxy", e)
	}
	if read(t, config) != before {
		t.Fatal("offline artifact changed existing configuration")
	}
}

func TestReauditInvalidCodexConfigDoesNotExposeItsContents(t *testing.T) {
	f := newFixture(t)
	config := filepath.Join(f.codex, "config.toml")
	secret := "PRIVATE-CONFIG-SENTINEL"
	write(t, config, "model = "+secret+"\n", 0600)
	before := read(t, config)
	e := validateCodexSQLiteLocation(f.codex)
	if e == nil || strings.Contains(e.Error(), secret) {
		t.Fatal("invalid config was accepted or printed private content", e)
	}
	if read(t, config) != before {
		t.Fatal("configuration validation changed the source")
	}
}

func TestReauditBoundedWritesNeverCreateAMissingManagementRoot(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	missing := filepath.Join(root, "retired-mount")
	if e = atomicWriteIn(missing, filepath.Join(missing, ".cdet", "bin", "fixture"), []byte("fixture"), 0755); e == nil {
		t.Fatal("missing root recreated")
	}
	if !absent(missing) {
		t.Fatal("bounded write created retired root")
	}
	if e = os.Mkdir(missing, 0700); e != nil {
		t.Fatal(e)
	}
	if e = atomicWriteIn(missing, filepath.Join(missing, ".cdet", "bin", "fixture"), []byte("fixture"), 0755); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(root, "outside")
	if e = atomicWriteIn(missing, outside, []byte("forbidden"), 0600); e == nil || !absent(outside) {
		t.Fatal("bounded write escaped management root")
	}
}
