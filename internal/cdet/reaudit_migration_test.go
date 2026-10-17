package cdet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReauditImportRejectsEntirePhysicalHomeWhenHomeIsSymlinked(t *testing.T) {
	f := newFixture(t)
	homeAlias := filepath.Join(f.root, "home-alias")
	if err := os.Symlink(f.user, homeAlias); err != nil {
		t.Fatal(err)
	}
	m := f.manager(t, Options{})
	m.UserHome = homeAlias
	before := tree(t, f.user).RawDigest
	for _, source := range []string{f.user, homeAlias} {
		if _, err := m.candidate("codex", source, nil); err == nil {
			t.Errorf("entire physical HOME accepted as Codex import root: %s", source)
		}
	}
	if tree(t, f.user).RawDigest != before || !absent(f.prefix) || !absent(f.store) {
		t.Fatal("candidate inspection modified source or destination")
	}
}
