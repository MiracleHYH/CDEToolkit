// The two independent test packages keep this small fixture local; production
// binaries never accept or interpret the race-report input.
package proxy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type isolatedRaceReports struct {
	path string
	info os.FileInfo
}

func prepareIsolatedRaceReports(path string) (isolatedRaceReports, error) {
	if path == "" {
		return isolatedRaceReports{}, nil
	}
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return isolatedRaceReports{}, err
	}
	// A single child of the canonical temporary root avoids following any
	// caller-selected parent symlink. The fixed prefix belongs only to tests.
	name := filepath.Base(path)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != base ||
		!strings.HasPrefix(name, "nido-proxy-race-") || name == "nido-proxy-race-" || strings.ContainsAny(path, " \t\r\n") {
		return isolatedRaceReports{}, fmt.Errorf("race 报告目录必须是临时根目录下独有的 nido-proxy-race-* 绝对路径")
	}
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return isolatedRaceReports{}, err
	}
	reports := isolatedRaceReports{path: path}
	dir, err := reports.open()
	if err != nil {
		return isolatedRaceReports{}, err
	}
	defer dir.Close()
	reports.info, err = dir.Stat()
	return reports, err
}

func (reports isolatedRaceReports) open() (*os.File, error) {
	before, err := os.Lstat(reports.path)
	if err != nil {
		return nil, err
	}
	stat, ok := before.Sys().(*syscall.Stat_t)
	if !before.IsDir() || before.Mode().Perm() != 0700 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("race 报告目录必须属于当前用户、权限为 0700 且不能是符号链接")
	}
	dir, err := os.OpenFile(reports.path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	after, err := dir.Stat()
	if err != nil || !os.SameFile(before, after) || after.Mode().Perm() != 0700 || reports.info != nil && !os.SameFile(reports.info, after) {
		dir.Close()
		return nil, fmt.Errorf("race 报告目录在检查期间被替换")
	}
	return dir, nil
}

func (reports isolatedRaceReports) check() error {
	if reports.path == "" {
		return nil
	}
	dir, err := reports.open()
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "race.") {
			return fmt.Errorf("race 报告目录出现未知项 %q", entry.Name())
		}
		path := filepath.Join(reports.path, entry.Name())
		file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return fmt.Errorf("无法安全读取 race 报告 %q: %w", entry.Name(), err)
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
			file.Close()
			return fmt.Errorf("race 报告不是本测试用户独占的普通文件")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 64<<10))
		file.Close()
		if readErr != nil {
			return readErr
		}
		// Even an empty report means the detector opened a report file.
		return fmt.Errorf("检测到子进程 race 报告 %q:\n%s", entry.Name(), data)
	}
	return nil
}

func raceReportTestDirectory(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path, err := os.MkdirTemp(base, "nido-proxy-race-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	return path
}

func TestIsolatedRaceReportsRejectChildReportAfterSuccessfulExit(t *testing.T) {
	path := raceReportTestDirectory(t)
	reports, err := prepareIsolatedRaceReports(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reports.check(); err != nil {
		t.Fatalf("empty report directory rejected: %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The detector in a detached child can leave a report even when that
	// child's exit is no longer being watched by a lifecycle operation.
	child := exec.Command(executable, "-test.run=^TestIsolatedRaceReportChildFixture$")
	child.Env = append(os.Environ(), "NIDO_PROXY_RACE_CHILD_FIXTURE="+path)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("report fixture did not exit successfully: %v\n%s", err, out)
	}
	if err := reports.check(); err == nil || !strings.Contains(err.Error(), "子进程 race 报告") {
		t.Fatalf("a successful child exit hid its report: %v", err)
	}
}

func TestIsolatedRaceReportChildFixture(t *testing.T) {
	path := os.Getenv("NIDO_PROXY_RACE_CHILD_FIXTURE")
	if path == "" {
		return
	}
	if _, err := prepareIsolatedRaceReports(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "race.child-fixture"), []byte("WARNING: DATA RACE\nfixture only\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestIsolatedRaceReportsRejectUnsafeDirectories(t *testing.T) {
	path := raceReportTestDirectory(t)
	for _, candidate := range []string{"relative", filepath.Dir(path), path + "/nested", filepath.Join(filepath.Dir(path), "nido-proxy-race-")} {
		if _, err := prepareIsolatedRaceReports(candidate); err == nil {
			t.Fatalf("unsafe directory accepted: %q", candidate)
		}
	}
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareIsolatedRaceReports(path); err == nil {
		t.Fatal("public directory accepted")
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	link := raceReportTestDirectory(t)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareIsolatedRaceReports(link); err == nil {
		t.Fatal("symlink directory accepted")
	}
	reports, err := prepareIsolatedRaceReports(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(path, "missing"), filepath.Join(path, "race.link")); err != nil {
		t.Fatal(err)
	}
	if err := reports.check(); err == nil {
		t.Fatal("symlink report accepted")
	}
}
