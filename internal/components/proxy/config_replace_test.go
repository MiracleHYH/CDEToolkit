package proxy

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func configuredCore(t *testing.T) (*Service, *coreFakeRunner, string, string) {
	t.Helper()
	s, r, artifact := coreService(t)
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
		t.Fatal(err)
	}
	path, _ := s.ConfigPath()
	return s, r, artifact, path
}

func TestCoreConfigureBacksUpExactPriorBytesOnceAndKeepsOnlyOneBackup(t *testing.T) {
	s, _, _, path := configuredCore(t)
	backup := path + ".back"
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first configuration created backup: %v", err)
	}
	original := []byte("# custom comment\r\n" + strings.ReplaceAll(coreConfig, "\n", "\r\n"))
	coreWrite(t, path, original)
	next := []byte(strings.ReplaceAll(coreConfig, "7890", "7891"))
	if err := s.ConfigureBytes(next); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(backup); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("backup lost original YAML bytes: %q %v", got, err)
	}
	before, err := capture(backup, maxConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	if before.info.Mode().Perm() != 0600 {
		t.Fatal("backup is not private")
	}
	if err := s.ConfigureBytes(next); err != nil {
		t.Fatal(err)
	}
	if err := before.check(); err != nil {
		t.Fatalf("identical content changed backup: %v", err)
	}
	if err := s.ConfigureBytes([]byte(strings.ReplaceAll(coreConfig, "7890", "7892"))); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(backup); !bytes.Equal(got, next) {
		t.Fatalf("backup was not replaced with immediate prior configuration: %q", got)
	}
	if files, _ := filepath.Glob(path + ".back*"); len(files) != 1 || files[0] != backup {
		t.Fatalf("unexpected rotating backups: %v", files)
	}
}

func TestCoreConfigureFailureOrMissingOldConfigPreservesBackup(t *testing.T) {
	for _, operation := range []string{"first", "static-invalid", "core-invalid", "running"} {
		t.Run(operation, func(t *testing.T) {
			s, r, _, path := configuredCore(t)
			backup := path + ".back"
			coreWrite(t, backup, []byte("existing backup bytes"))
			before, err := capture(backup, maxConfig, false)
			if err != nil {
				t.Fatal(err)
			}
			data := []byte(strings.ReplaceAll(coreConfig, "7890", "7891"))
			switch operation {
			case "first":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "static-invalid":
				data = []byte("mixed-port: -1\n")
			case "core-invalid":
				r.run = func(Command) ([]byte, error) { return nil, errors.New("rejected") }
			case "running":
				record := coreStoppedRecord(t, s)
				r.process = record.Process
			}
			err = s.ConfigureBytes(data)
			if (operation == "first") != (err == nil) {
				t.Fatalf("unexpected configure result: %v", err)
			}
			if err := before.check(); err != nil {
				t.Fatalf("backup modified by %s: %v", operation, err)
			}
			if operation != "first" {
				if got, _ := os.ReadFile(path); string(got) != coreConfig {
					t.Fatal("failed configure replaced main configuration")
				}
			}
			if r.starts != 0 || r.stops != 0 {
				t.Fatal("configure implicitly controlled a process")
			}
		})
	}
}

func TestCoreConfigRevisionRejectsConcurrentMainBackupAndDirectoryChanges(t *testing.T) {
	for _, mutation := range []string{"main", "identical-main", "backup", "new-backup", "directory", "lock", "zero", "other-root", "initial-appeared"} {
		t.Run(mutation, func(t *testing.T) {
			s, r, _, path := configuredCore(t)
			if mutation != "new-backup" {
				coreWrite(t, path+".back", []byte("prior backup"))
			}
			if mutation == "initial-appeared" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			revision, err := s.CaptureConfigRevision()
			if err != nil || revision.Exists() == (mutation == "initial-appeared") {
				t.Fatalf("bad revision: exists=%v err=%v", revision.Exists(), err)
			}
			switch mutation {
			case "main", "initial-appeared":
				coreWrite(t, path, []byte("# external\n"+coreConfig))
			case "identical-main":
				if err := os.Rename(path, path+".external-old"); err != nil {
					t.Fatal(err)
				}
				coreWrite(t, path, []byte(coreConfig))
			case "backup", "new-backup":
				coreWrite(t, path+".back", []byte("external backup"))
			case "directory":
				dir := filepath.Dir(path)
				if err := os.Rename(dir, dir+"-external"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			case "lock":
				lock := filepath.Join(filepath.Dir(path), ".lock")
				if err := os.Rename(lock, lock+"-external"); err != nil {
					t.Fatal(err)
				}
				coreWrite(t, lock, []byte("external lock"))
			case "zero":
				revision = ConfigRevision{}
			case "other-root":
				other, _, _, _ := configuredCore(t)
				revision, err = other.CaptureConfigRevision()
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			backup, _ := os.ReadFile(path + ".back")
			commands := len(r.commands)
			if err := s.ConfigureBytesIfUnchanged([]byte(strings.ReplaceAll(coreConfig, "7890", "7891")), revision); err == nil {
				t.Fatal("stale revision accepted")
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, before) {
				t.Fatal("concurrent main configuration overwritten")
			}
			if got, _ := os.ReadFile(path + ".back"); !bytes.Equal(got, backup) {
				t.Fatal("concurrent backup overwritten")
			}
			if len(r.commands) != commands {
				t.Fatal("stale revision reached core validation")
			}
		})
	}
}

func TestCoreConfigSourceBindsExactFileBeforeConfirmation(t *testing.T) {
	s, r, _, path := configuredCore(t)
	revision, err := s.CaptureConfigRevision()
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "import.yaml")
	coreWrite(t, input, []byte(strings.ReplaceAll(coreConfig, "7890", "7891")))
	source, err := s.CaptureConfigSource(input)
	if err != nil {
		t.Fatal(err)
	}
	coreWrite(t, input, []byte(strings.ReplaceAll(coreConfig, "7890", "7892")))
	commands := len(r.commands)
	if err := s.ConfigureSourceIfUnchanged(source, revision); err == nil {
		t.Fatal("modified import source accepted after confirmation")
	}
	if got, _ := os.ReadFile(path); string(got) != coreConfig || len(r.commands) != commands {
		t.Fatal("stale source reached configuration commit or validation")
	}
	if _, err := os.Lstat(path + ".back"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale source modified backup")
	}
	source, err = s.CaptureConfigSource(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureSourceIfUnchanged(source, revision); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path + ".back"); string(got) != coreConfig {
		t.Fatal("full YAML import did not retain original bytes")
	}
}

func TestCoreConfigSourceCanRestoreBackupWhileRetainingReplacedMain(t *testing.T) {
	s, _, _, path := configuredCore(t)
	previous := []byte("# previous configuration\n" + strings.ReplaceAll(coreConfig, "7890", "7891"))
	coreWrite(t, path+".back", previous)
	revision, err := s.CaptureConfigRevision()
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CaptureConfigSource(path + ".back")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureSourceIfUnchanged(source, revision); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, previous) {
		t.Fatal("backup import did not restore captured YAML")
	}
	if got, _ := os.ReadFile(path + ".back"); string(got) != coreConfig {
		t.Fatal("backup import did not preserve replaced main YAML")
	}
}

func TestCoreConfigureRechecksCommittedBackupBeforeReplacingMain(t *testing.T) {
	s, r, _, path := configuredCore(t)
	coreStoppedRecord(t, s)
	inspects := 0
	mutated := false
	r.inspect = func(int) (Process, error) {
		inspects++
		if data, _ := os.ReadFile(path + ".back"); string(data) == coreConfig && !mutated {
			mutated = true
			coreWrite(t, path+".back", []byte("external backup after save"))
		}
		return Process{}, os.ErrNotExist
	}
	if err := s.ConfigureBytes([]byte(strings.ReplaceAll(coreConfig, "7890", "7891"))); err == nil || !mutated {
		t.Fatalf("concurrent backup replacement accepted: inspects=%d err=%v", inspects, err)
	}
	if got, _ := os.ReadFile(path); string(got) != coreConfig {
		t.Fatal("main replaced after saved backup changed")
	}
	if got, _ := os.ReadFile(path + ".back"); string(got) != "external backup after save" {
		t.Fatal("external backup overwritten")
	}
}

func TestCoreConfigureRejectsUnsafeOrConcurrentlyChangedBackupBeforeReplacingMain(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "directory", "changed-during-validation"} {
		t.Run(kind, func(t *testing.T) {
			s, r, _, path := configuredCore(t)
			backup := path + ".back"
			external := filepath.Join(t.TempDir(), "sentinel")
			coreWrite(t, external, []byte("external sentinel"))
			switch kind {
			case "symlink":
				if err := os.Symlink(external, backup); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(external, backup); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(backup, 0700); err != nil {
					t.Fatal(err)
				}
			case "changed-during-validation":
				coreWrite(t, backup, []byte("old backup"))
				r.run = func(Command) ([]byte, error) {
					coreWrite(t, backup, []byte("external replacement"))
					return nil, nil
				}
			}
			if err := s.ConfigureBytes([]byte(strings.ReplaceAll(coreConfig, "7890", "7891"))); err == nil {
				t.Fatal("unsafe or modified backup accepted")
			}
			if got, _ := os.ReadFile(path); string(got) != coreConfig {
				t.Fatal("main configuration replaced despite failed backup")
			}
			if got, _ := os.ReadFile(external); string(got) != "external sentinel" {
				t.Fatal("external data changed")
			}
		})
	}
}

func TestCoreConfigureRejectsPublicOrSpecialModeBackupWithoutChangingFiles(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode os.FileMode
	}{
		{"public", 0644},
		{"sticky", 0600 | os.ModeSticky},
	} {
		for _, missingMain := range []bool{false, true} {
			name := mode.name + "/existing-main"
			if missingMain {
				name = mode.name + "/missing-main"
			}
			t.Run(name, func(t *testing.T) {
				s, r, _, path := configuredCore(t)
				backupPath := path + ".back"
				coreWrite(t, backupPath, []byte("private original backup bytes"))
				if missingMain {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				revision, err := s.CaptureConfigRevision()
				if err != nil {
					t.Fatal(err)
				}
				data := []byte(strings.ReplaceAll(coreConfig, "7890", "7891"))
				input := filepath.Join(t.TempDir(), "import.yaml")
				coreWrite(t, input, data)
				source, err := s.CaptureConfigSource(input)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(backupPath, mode.mode); err != nil {
					t.Fatal(err)
				}
				backup, err := capture(backupPath, maxConfig, false)
				if err != nil || backup.info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != mode.mode {
					t.Fatalf("unsafe-mode fixture not established: info=%v err=%v", backup.info, err)
				}
				main, err := capture(path, maxConfig, true)
				if err != nil {
					t.Fatal(err)
				}
				commands := len(r.commands)
				for _, action := range []struct {
					name string
					run  func() error
				}{
					{"capture", func() error { _, err := s.CaptureConfigRevision(); return err }},
					{"bytes", func() error { return s.ConfigureBytes(data) }},
					{"initial", func() error { return s.ConfigureInitialBytes(data) }},
					{"file", func() error { return s.Configure(input) }},
					{"revision-bytes", func() error { return s.ConfigureBytesIfUnchanged(data, revision) }},
					{"revision-file", func() error { return s.ConfigureIfUnchanged(input, revision) }},
					{"revision-source", func() error { return s.ConfigureSourceIfUnchanged(source, revision) }},
					{"preflight", func() error { return s.Preflight("configure", "", "") }},
					{"preflight-file", func() error { return s.Preflight("configure", input, "") }},
				} {
					if err := action.run(); err == nil {
						t.Fatalf("%s accepted unsafe backup", action.name)
					}
					if err := main.check(); err != nil {
						t.Fatalf("%s changed main bytes/metadata: %v", action.name, err)
					}
					if err := backup.check(); err != nil {
						t.Fatalf("%s changed original backup bytes/metadata: %v", action.name, err)
					}
				}
				if len(r.commands) != commands || r.starts != 0 || r.stops != 0 {
					t.Fatal("unsafe backup reached core execution or process control")
				}
			})
		}
	}
}

type configPermissionInfo struct {
	os.FileInfo
	mode os.FileMode
	uid  uint32
}

func (i configPermissionInfo) Mode() os.FileMode { return i.mode }
func (i configPermissionInfo) Sys() any {
	stat := *i.FileInfo.Sys().(*syscall.Stat_t)
	stat.Uid = i.uid
	return &stat
}

func TestCorePrivateConfigBackupRejectsUnsafePermissionAndOwnershipMetadata(t *testing.T) {
	// Some temporary filesystems clear set-id bits even after a successful
	// chmod. Exercise these permission/ownership invariants on a captured
	// ordinary inode without changing real ownership or requiring privileges.
	path := filepath.Join(t.TempDir(), "config.yaml.back")
	coreWrite(t, path, []byte("unchanged original backup"))
	before, err := capture(path, maxConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name string
		mode os.FileMode
		uid  uint32
	}{
		{"public", 0644, uint32(os.Geteuid())},
		{"setuid", 0600 | os.ModeSetuid, uint32(os.Geteuid())},
		{"setgid", 0600 | os.ModeSetgid, uint32(os.Geteuid())},
		{"sticky", 0600 | os.ModeSticky, uint32(os.Geteuid())},
		{"different-owner", 0600, uint32(os.Geteuid()) + 1},
	} {
		t.Run(item.name, func(t *testing.T) {
			unsafe := before
			unsafe.info = configPermissionInfo{FileInfo: before.info, mode: item.mode, uid: item.uid}
			if err := privateConfigBackup(unsafe); err == nil {
				t.Fatal("unsafe backup metadata accepted")
			}
			if err := before.check(); err != nil {
				t.Fatalf("permission check changed original backup bytes/metadata: %v", err)
			}
		})
	}
	if err := privateConfigBackup(before); err != nil {
		t.Fatalf("private backup rejected: %v", err)
	}
}

func TestCoreExistingPublicMainConfigRemainsReadableAndCanBeReplaced(t *testing.T) {
	s, _, _, path := configuredCore(t)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	revision, err := s.CaptureConfigRevision()
	if err != nil {
		t.Fatalf("existing main-file read contract changed: %v", err)
	}
	if _, err := s.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBytesIfUnchanged([]byte(strings.ReplaceAll(coreConfig, "7890", "7891")), revision); err != nil {
		t.Fatal(err)
	}
	backup, err := capture(path+".back", maxConfig, false)
	if err != nil || string(backup.data) != coreConfig || backup.info.Mode().Perm() != 0600 {
		t.Fatalf("original public main not preserved as private backup: %v", err)
	}
}

func TestCoreConfigBackupCannotBeProviderOrReferencedResource(t *testing.T) {
	for _, data := range []string{
		"mixed-port: 7890\nproxy-providers: {p: {type: http, url: https://example.invalid, path: config.yaml.back}}\n",
		"mixed-port: 7890\nrule-providers: {p: {type: file, path: config.yaml.back}}\n",
		"mixed-port: 7890\nproxy-providers: {p: {type: http, url: https://example.invalid, path: CONFIG.YAML.BACK/file}}\n",
	} {
		if _, err := ParseConfig([]byte(data), t.TempDir()); err == nil {
			t.Fatal("reserved backup path accepted as writable core resource")
		}
	}
}
