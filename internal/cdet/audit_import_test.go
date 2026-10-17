package cdet

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestImportRejectsSeparateCodexSQLiteHome(t *testing.T) {
	f := newFixture(t)
	dbHome := filepath.Join(f.root, "external-databases")
	write(t, filepath.Join(dbHome, "state.sqlite"), "EXTERNAL-DB", 0600)
	t.Setenv("CODEX_SQLITE_HOME", dbHome)
	if _, e := f.manager(t, Options{}).Discover("codex"); e == nil {
		t.Fatal("separate database directory silently omitted")
	}
	t.Setenv("CODEX_SQLITE_HOME", f.codex)
	if _, e := f.manager(t, Options{}).Discover("codex"); e != nil {
		t.Fatal("database root already inside exact data source", e)
	}
}

func TestImportRejectsPinnedCodexSQLiteConfigEvenAtSourceRoot(t *testing.T) {
	for _, location := range []string{"source", "/external/database", "../database", ""} {
		f := newFixture(t)
		f.init(t)
		if location == "source" {
			location = f.codex
		}
		config := "model = 'preserved'\nsqlite_home = " + strconv.Quote(location) + "\n"
		write(t, filepath.Join(f.codex, "config.toml"), config, 0600)
		if _, e := f.manager(t, Options{}).Install([]string{"codex"}); e == nil {
			t.Fatal("pinned database source config accepted", location)
		}
		if read(t, filepath.Join(f.codex, "config.toml")) != config {
			t.Fatal("source config changed during rejected migration")
		}
	}
}

func TestImportDataChildDoesNotHideRootCredentials(t *testing.T) {
	for _, name := range []string{"codex", "proxy"} {
		for _, explicit := range []bool{false, true} {
			f := newFixture(t)
			f.init(t)
			source := f.codex
			if name == "proxy" {
				source = f.proxy
			}
			write(t, filepath.Join(source, "data", "nested-sentinel"), "NESTED-DATA", 0600)
			o := Options{}
			if explicit {
				o.From = map[string]string{name: source}
			}
			m := f.manager(t, o)
			candidate, e := m.Discover(name)
			if e != nil || candidate.Data != source {
				t.Fatalf("data child swallowed source root: %q, %v", candidate.Data, e)
			}
			p, e := m.Install([]string{name})
			apply(t, p, e)
			if tree(t, source).Digest != tree(t, filepath.Join(f.store, dataPath(name))).Digest {
				t.Fatal("full source tree was not imported")
			}
		}
	}
}

func TestImportSourceIsAlwaysExactDataRoot(t *testing.T) {
	f := newFixture(t)
	bundle := filepath.Join(f.root, "prepared")
	write(t, filepath.Join(bundle, "bin", "codex"), string(fakeNative("prepared")), 0755)
	write(t, filepath.Join(bundle, "data", "auth.json"), "PREPARED-AUTH", 0600)
	m := f.manager(t, Options{From: map[string]string{"codex": bundle}})
	candidate, e := m.Discover("codex")
	if e != nil || candidate.Data != bundle {
		t.Fatal("explicit source reinterpreted as preparation bundle", candidate.Data, e)
	}
}

func TestProxyImportRejectsResourcesOutsideTheCompleteRoot(t *testing.T) {
	for _, path := range []string{"/external/rules.yaml", "../outside.yaml", "rules/../../outside.yaml"} {
		f := newFixture(t)
		f.init(t)
		config := "rule-providers:\n  private:\n    path: " + strconv.Quote(path) + "\n"
		write(t, filepath.Join(f.proxy, "config.yaml"), config, 0600)
		before := tree(t, f.proxy).RawDigest
		if _, e := f.manager(t, Options{From: map[string]string{"proxy": f.proxy}}).Install([]string{"proxy"}); e == nil {
			t.Fatal("external resource accepted", path)
		}
		if tree(t, f.proxy).RawDigest != before || !absent(filepath.Join(f.store, "proxy")) {
			t.Fatal("resource rejection changed source or target")
		}
	}
}

func TestImportRejectsSymlinkEscapeAfterPhysicalParentResolution(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.user, "outside"), "OUTSIDE", 0600)
	if e := os.Symlink(".", filepath.Join(f.codex, "a")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("a/../outside", filepath.Join(f.codex, "b")); e != nil {
		t.Fatal(e)
	}
	if _, e := snapshotTree(f.codex); e == nil {
		t.Fatal("physical symlink escape accepted")
	}
}

func TestImportPreservesRelativeLinkTraversalSemantics(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.codex, "nested", "outside"), "RIGHT", 0600)
	write(t, filepath.Join(f.codex, "nested", "deep", "file"), "DEEP", 0600)
	write(t, filepath.Join(f.codex, "outside"), "WRONG", 0600)
	if e := os.Symlink("nested/deep", filepath.Join(f.codex, "a")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("a/../outside", filepath.Join(f.codex, "b")); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(f.root, "copy")
	if e := copyTree(tree(t, f.codex), target); e != nil {
		t.Fatal(e)
	}
	if got := read(t, filepath.Join(target, "b")); got != "RIGHT" {
		t.Fatal("link semantics changed while normalizing", got)
	}
}

func TestImportPreservesInternalDanglingLinkAndRejectsCycles(t *testing.T) {
	f := newFixture(t)
	if e := os.Symlink("future/file", filepath.Join(f.codex, "dangling")); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(f.root, "copy")
	if e := copyTree(tree(t, f.codex), target); e != nil {
		t.Fatal(e)
	}
	if got, e := os.Readlink(filepath.Join(target, "dangling")); e != nil || got != "future/file" {
		t.Fatal("dangling link changed", got, e)
	}
	if e := os.Symlink("loop", filepath.Join(f.codex, "loop")); e != nil {
		t.Fatal(e)
	}
	if _, e := snapshotTree(f.codex); e == nil {
		t.Fatal("cyclic link accepted")
	}
}

func TestImportRejectsUnmarkedExternalShellEntrypoints(t *testing.T) {
	for _, name := range []string{"proxy"} {
		f := newFixture(t)
		program := filepath.Join(f.codex, "bin", "codex")
		if name == "proxy" {
			program = filepath.Join(f.proxy, "mihomo")
		}
		write(t, program, "#!/bin/sh\nexec /retired/private/program \"$@\"\n", 0755)
		if _, e := f.manager(t, Options{}).Discover(name); e == nil {
			t.Fatal("external shell wrapper accepted", name)
		}
	}
}

func TestLocalProxyArtifactsRequireNativePrograms(t *testing.T) {
	f := newFixture(t)
	program := filepath.Join(f.root, "shell-entry")
	write(t, program, "#!/bin/sh\nexec /retired/private/program \"$@\"\n", 0755)
	if _, e := newReleaseClient().artifactBytes(Artifact{Source: program}, "proxy"); e == nil {
		t.Fatal("external shell artifact accepted")
	}
}

func elfWithInterpreter(loader string) []byte {
	b := fakeELF("")
	binary.LittleEndian.PutUint64(b[32:], 64)
	binary.LittleEndian.PutUint16(b[54:], 56)
	binary.LittleEndian.PutUint16(b[56:], 1)
	program := make([]byte, 56)
	binary.LittleEndian.PutUint32(program, uint32(elf.PT_INTERP))
	binary.LittleEndian.PutUint64(program[8:], 120)
	binary.LittleEndian.PutUint64(program[32:], uint64(len(loader)+1))
	b = append(b, program...)
	return append(b, append([]byte(loader), 0)...)
}

func elfWithDynamicPath(tag elf.DynTag, value string) []byte {
	b := fakeELF("")
	stringsOffset := len(b)
	strings := append([]byte{0}, append([]byte(value), 0)...)
	b = append(b, strings...)
	dynamicOffset := len(b)
	dynamic := make([]byte, 32)
	binary.LittleEndian.PutUint64(dynamic, uint64(tag))
	binary.LittleEndian.PutUint64(dynamic[8:], 1)
	b = append(b, dynamic...)
	sectionOffset := len(b)
	binary.LittleEndian.PutUint64(b[40:], uint64(sectionOffset))
	binary.LittleEndian.PutUint16(b[58:], 64)
	binary.LittleEndian.PutUint16(b[60:], 3)
	sections := make([]byte, 3*64)
	str := sections[64:]
	binary.LittleEndian.PutUint32(str[4:], uint32(elf.SHT_STRTAB))
	binary.LittleEndian.PutUint64(str[24:], uint64(stringsOffset))
	binary.LittleEndian.PutUint64(str[32:], uint64(len(strings)))
	dyn := sections[128:]
	binary.LittleEndian.PutUint32(dyn[4:], uint32(elf.SHT_DYNAMIC))
	binary.LittleEndian.PutUint64(dyn[24:], uint64(dynamicOffset))
	binary.LittleEndian.PutUint64(dyn[32:], uint64(len(dynamic)))
	binary.LittleEndian.PutUint32(dyn[40:], 1)
	binary.LittleEndian.PutUint64(dyn[56:], 16)
	return append(b, sections...)
}

func machoWithDependency(command uint32, dependency string) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b, 0xfeedfacf)
	binary.LittleEndian.PutUint32(b[4:], 0x01000007)
	binary.LittleEndian.PutUint32(b[12:], 2)
	binary.LittleEndian.PutUint32(b[16:], 1)
	pathOffset := 24
	if command == 0xe || command == 0x8000001c {
		pathOffset = 12
	}
	size := (pathOffset + len(dependency) + 1 + 7) &^ 7
	binary.LittleEndian.PutUint32(b[20:], uint32(size))
	load := make([]byte, size)
	binary.LittleEndian.PutUint32(load, command)
	binary.LittleEndian.PutUint32(load[4:], uint32(size))
	binary.LittleEndian.PutUint32(load[8:], uint32(pathOffset))
	copy(load[pathOffset:], dependency)
	return append(b, load...)
}

func TestNativeMetadataRejectsRetiredRuntimeDependencies(t *testing.T) {
	for _, loader := range []string{"/retired/lib/ld-linux.so.2", "/lib/../retired/loader", "$ORIGIN/loader"} {
		if e := verifyPlatform(elfWithInterpreter(loader), "linux/amd64"); e == nil {
			t.Fatal("custom ELF loader accepted", loader)
		}
	}
	if e := verifyPlatform(elfWithInterpreter("/lib64/ld-linux-x86-64.so.2"), "linux/amd64"); e != nil {
		t.Fatal("standard ELF loader rejected", e)
	}
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		for _, path := range []string{"$ORIGIN/lib", "/retired/lib", "/usr/lib:/retired/lib", "lib", ":/usr/lib"} {
			if e := verifyPlatform(elfWithDynamicPath(tag, path), "linux/amd64"); e == nil {
				t.Fatal("external ELF search path accepted", tag, path)
			}
		}
		if e := verifyPlatform(elfWithDynamicPath(tag, "/usr/lib:/lib64"), "linux/amd64"); e != nil {
			t.Fatal("system ELF search path rejected", e)
		}
	}
	if e := verifyPlatform(elfWithDynamicPath(elf.DT_NEEDED, "/retired/lib/private.so"), "linux/amd64"); e == nil {
		t.Fatal("external explicit ELF library accepted")
	}
	if e := verifyPlatform(elfWithDynamicPath(elf.DT_NEEDED, "libc.so.6"), "linux/amd64"); e != nil {
		t.Fatal("system-resolved ELF library rejected", e)
	}
	for _, command := range []uint32{0xc, 0xe, 0x80000018, 0x8000001f, 0x20, 0x80000023, 0x8000001c} {
		for _, library := range []string{"/retired/lib/private.dylib", "@loader_path/lib.dylib", "@rpath/lib.dylib"} {
			if e := verifyPlatform(machoWithDependency(command, library), "darwin/amd64"); e == nil {
				t.Fatal("external Mach-O dependency accepted", command, library)
			}
		}
		if e := verifyPlatform(machoWithDependency(command, "/usr/lib/libSystem.B.dylib"), "darwin/amd64"); e != nil {
			t.Fatal("system Mach-O dependency rejected", command, e)
		}
	}
}
