package distribution

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var publicPrivateKey = regexp.MustCompile(`(?m)^[\t ]*-----BEGIN (?:(?:OPENSSH|RSA|EC|DSA|ENCRYPTED) )?PRIVATE KEY-----[\t ]*\r?$`)
var publicAccessToken = regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{20,}`)

// AuditRepository checks the publishable working tree and, when available,
// invokes gitleaks on a private source snapshot and the entire Git history.
// It never stages files, edits the checkout or reads ignored runtime directories.
// Diagnostics identify files and check names, never matching source contents.
func AuditRepository(root, forbiddenPattern string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	var forbidden *regexp.Regexp
	if forbiddenPattern != "" {
		var err error
		forbidden, err = regexp.Compile(forbiddenPattern)
		if err != nil {
			return fmt.Errorf("自定义公开审查禁止模式无效")
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("公开审查要求有效仓库目录")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("公开审查要求现有仓库目录")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	files, err := exec.CommandContext(ctx, "git", "-C", root, "-c", "core.fsmonitor=false", "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return fmt.Errorf("无法列出 Git 公开审查候选文件")
	}
	snapshot, err := os.MkdirTemp("", "nido-public-audit-")
	if err != nil {
		return fmt.Errorf("无法创建公开审查临时目录")
	}
	defer os.RemoveAll(snapshot)
	failures, count := 0, 0
	report := func(name, check string) {
		failures++
		fmt.Fprintf(out, "公开审查失败: %s: %q\n", check, name)
	}
	for _, entry := range bytes.Split(files, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		name := string(entry)
		if !publicRelativePath(name) {
			report(name, "不安全候选路径")
			continue
		}
		file := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(file)
		if os.IsNotExist(err) {
			// Tracked files deleted in the working tree are not publishable.
			continue
		}
		if err != nil || !info.Mode().IsRegular() || !publicRegularParents(root, name) {
			report(name, "候选文件必须为仓库内普通文件")
			continue
		}
		count++
		if publicSensitivePath(name) {
			report(name, "敏感文件名")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			report(name, "无法读取候选文件")
			continue
		}
		if publicPrivateKey.Match(data) {
			report(name, "私钥内容")
		}
		if publicAccessToken.Match(data) {
			report(name, "疑似访问令牌")
		}
		if forbidden != nil && forbidden.Match(data) {
			report(name, "自定义禁止模式")
		}
		target := filepath.Join(snapshot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return fmt.Errorf("无法创建公开审查快照目录")
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return fmt.Errorf("无法创建公开审查文件快照")
		}
	}
	if executable, err := exec.LookPath("gitleaks"); err == nil {
		run := func(args ...string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, args...)
			cmd.Stdout, cmd.Stderr = out, out
			if err := cmd.Run(); err != nil {
				report("gitleaks", "凭据扫描失败")
			}
		}
		run("dir", "--no-banner", "--redact", snapshot)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := exec.CommandContext(ctx, "git", "-C", root, "-c", "core.fsmonitor=false", "rev-parse", "--verify", "HEAD").Run()
		cancel()
		if err == nil {
			run("git", "--no-banner", "--redact", "--log-opts=--all", root)
		}
	} else {
		fmt.Fprintln(out, "gitleaks 未安装：仅完成内置源码检查，未执行完整凭据与历史扫描")
	}
	if failures != 0 {
		return fmt.Errorf("public audit: %d issue(s)", failures)
	}
	fmt.Fprintf(out, "public audit: ok (%d publishable files)\n", count)
	return nil
}

func publicRelativePath(name string) bool {
	return name != "" && name != "." && !strings.Contains(name, "\\") && !path.IsAbs(name) && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../") && name != ".git" && !strings.HasPrefix(name, ".git/")
}

func publicRegularParents(root, name string) bool {
	for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(parent)))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

func publicSensitivePath(name string) bool {
	base := path.Base(name)
	for _, part := range strings.Split(name, "/") {
		if part == ".codex" || part == ".ssh" || part == ".aws" || part == "secrets" || part == "sessions" || part == "archived_sessions" {
			return true
		}
	}
	if name == "config.toml" || name == "config.conf" || name == "config.json" || name == "config.yaml" || name == "config.yml" || name == "proxy/config.yaml" || name == "proxy/config.yml" || base == ".env" || strings.HasPrefix(base, ".env.") && !strings.HasSuffix(base, ".example") || base == "auth.json" || base == "credentials.json" || base == "credentials.toml" || base == ".netrc" || base == ".npmrc" || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") {
		return true
	}
	for _, key := range []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"} {
		if base == key {
			return true
		}
	}
	return false
}
