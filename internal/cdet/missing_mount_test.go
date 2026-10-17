package cdet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMountDisappearingAfterPreviewNeverRecreatesRetiredRoots(t *testing.T) {
	for _, root := range []string{"store", "install"} {
		t.Run(root, func(t *testing.T) {
			f := newFixture(t)
			f.init(t)
			m := f.manager(t, Options{})
			p, e := m.Install([]string{"codex"})
			if e != nil {
				t.Fatal(e)
			}
			retired := f.store
			if root == "install" {
				retired = f.prefix
			}
			moved := retired + "-moved"
			if e = os.Rename(retired, moved); e != nil {
				t.Fatal(e)
			}
			before := tree(t, moved).RawDigest
			if e = Apply(p); e == nil {
				t.Fatal("disappeared root accepted")
			}
			if !absent(retired) || tree(t, moved).RawDigest != before {
				t.Fatal("retired root recreated or moved files changed")
			}
			if !absent(filepath.Join(f.root, "curl-calls")) {
				t.Fatal("installer ran after root disappeared")
			}
		})
	}
}

func TestExecutionLeaseNeverRecreatesADisappearedStore(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	f.install(t, "codex")
	m := f.manager(t, Options{})
	moved := f.store + "-moved"
	if e := os.Rename(f.store, moved); e != nil {
		t.Fatal(e)
	}
	before := tree(t, moved).RawDigest
	if lease, e := m.ExecutionLease("codex"); e == nil {
		_ = lease.Close()
		t.Fatal("lease accepted missing store")
	}
	if !absent(f.store) || tree(t, moved).RawDigest != before {
		t.Fatal("execution recreated retired mount or changed data")
	}
}
