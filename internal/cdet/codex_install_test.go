package cdet

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These bytes deliberately are not interpreted as SQLite or Codex session
// records. Unknown future formats must be copied just like any other file.
func opaqueCodexFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	for path, payload := range map[string]string{
		"state_99999.sqlite":           "SQLite format 3\x00UNKNOWN-PRIVATE-DATABASE\xff" + f.codex,
		"state_99999.sqlite-wal":       "UNKNOWN-WAL\x00\xfe" + f.codex,
		"state_99999.sqlite-shm":       "UNKNOWN-SHM\x00\xfd",
		"state_99999.sqlite-journal":   "UNKNOWN-JOURNAL\x00\xfc",
		"thread_history_future.sqlite": "UNKNOWN-HISTORY\x00" + f.codex,
		"session_index.jsonl":          "{opaque future index with PRIVATE-TITLE-SENTINEL}\x00",
		"sessions/future.jsonl":        "{unknown history format}\x00" + f.codex,
		"rollout-migrations/.pending":  "UNKNOWN-MIGRATION-STATE",
		"versions/user-version/notes":  "HOME-OWNED-VERSIONS",
		"current/session.meta":         "HOME-OWNED-CURRENT",
		"data/config.toml":             "HOME-OWNED-DATA",
		"unknown-format/payload.bin":   "\x00\x01\xffBINARY-DATA",
	} {
		write(t, filepath.Join(f.codex, path), payload, 0600)
	}
	return f
}

func TestCodexInstallCopiesTheCompleteHomeWithoutInterpretingItsFiles(t *testing.T) {
	f := opaqueCodexFixture(t)
	f.init(t)
	source := tree(t, f.codex)
	p, err := f.manager(t, Options{From: map[string]string{"codex": f.codex}}).Install([]string{"codex"})
	apply(t, p, err)
	target := filepath.Join(f.store, dataPath("codex"))
	if len(p.Imports) != 1 || tree(t, f.codex).RawDigest != source.RawDigest || tree(t, target).Digest != source.Digest {
		t.Fatal("full HOME import changed or omitted opaque source contents")
	}
	managed := f.manager(t, Options{})
	if !managed.State.Components["codex"].Installed || within(target, filepath.Join(f.store, programPath("codex"))) {
		t.Fatal("program management paths overlap complete HOME data")
	}
	for _, entry := range source.entries {
		if entry.Kind != "file" {
			continue
		}
		path := filepath.Join(target, entry.Path)
		if read(t, path) != read(t, filepath.Join(f.codex, entry.Path)) {
			t.Fatal("file bytes were interpreted or changed", entry.Path)
		}
		info, err := os.Stat(path)
		if err != nil || uint32(info.Mode().Perm()) != entry.Mode || !info.ModTime().Equal(entry.ModTime) {
			t.Fatal("copied file mode or modification time changed", entry.Path, err)
		}
	}
}

func TestCodexFullHomeCLIPreviewIsReadOnlyAndDoesNotPrintFileContents(t *testing.T) {
	f := opaqueCodexFixture(t)
	f.init(t)
	before := tree(t, f.codex).RawDigest
	var out, err bytes.Buffer
	args := []string{"--prefix", f.prefix, "--store", f.store, "install", "codex", "--from", "codex=" + f.codex, "--dry-run", "--json"}
	if (CLI{Out: &out, Err: &err}).Run(args) != 0 {
		t.Fatal(err.String())
	}
	if strings.Contains(out.String(), "PRIVATE-TITLE-SENTINEL") || strings.Contains(out.String(), "UNKNOWN-PRIVATE-DATABASE") || strings.Contains(out.String(), `"mode":"home"`) || strings.Contains(out.String(), `"mode":"conversations"`) {
		t.Fatal("preview exposed file contents or a removed import mode")
	}
	if tree(t, f.codex).RawDigest != before || !absent(filepath.Join(f.store, dataPath("codex"))) {
		t.Fatal("preview changed source or installed managed data")
	}
}
