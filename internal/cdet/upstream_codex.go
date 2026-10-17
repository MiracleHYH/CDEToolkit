package cdet

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const codexInstallerURL = "https://chatgpt.com/codex/install.sh"

func (m *Manager) codexInstallerPathsOK() error {
	if m.Initialized {
		if err := m.checkManagedRoots(); err != nil {
			return err
		}
		if err := m.dataRootOK("codex"); err != nil {
			return err
		}
	}
	data := filepath.Join(m.Store, dataPath("codex"))
	bin := filepath.Join(m.Store, ".cdet", "bin")
	// These are the upstream installer's write roots, including fresh installs
	// where componentOK cannot yet validate an existing managed program.
	for _, path := range []string{filepath.Join(bin, "sentinel"), filepath.Join(data, "packages", "standalone", "releases", "sentinel")} {
		if err := noSymlinkParents(path); err != nil {
			return err
		}
	}
	if !absent(filepath.Join(bin, "codex")) {
		return m.codexLinks(false)
	}
	if !absent(filepath.Join(bin, "codex-code-mode-host")) {
		return fmt.Errorf("Codex 主入口缺失但辅助入口仍存在，请检查该文件；未运行安装器")
	}
	return nil
}

func (m *Manager) installCodex() (result error) {
	if m.Options.DryRun {
		return fmt.Errorf("预览不会运行 Codex 官方安装器")
	}
	if err := m.codexInstallerPathsOK(); err != nil {
		return err
	}
	data := filepath.Join(m.Store, dataPath("codex"))
	bin := filepath.Join(m.Store, ".cdet", "bin")
	env, err := m.networkEnv()
	if err != nil {
		return err
	}
	metadata := filepath.Join(m.Store, ".cdet")
	if err = noSymlinkParents(filepath.Join(metadata, "sentinel")); err != nil {
		return err
	}
	home, err := os.MkdirTemp(metadata, "installer-*")
	if err != nil {
		return fmt.Errorf("无法创建官方安装器临时目录")
	}
	defer func() {
		if err := os.RemoveAll(home); err != nil {
			if result == nil {
				result = fmt.Errorf("Codex 安装器临时目录清理失败")
			} else {
				result = fmt.Errorf("%w；安装器临时目录清理失败", result)
			}
		}
	}()
	// The upstream installer may update a shell profile even in noninteractive
	// mode. Isolate only that operation's HOME; the actual Codex data root is
	// still explicit. Never carry daemon/updater-only CODEX_* switches into it.
	var clean []string
	for _, entry := range env {
		if !strings.HasPrefix(entry, "CODEX_") {
			clean = append(clean, entry)
		}
	}
	for key, value := range map[string]string{
		"HOME": home, "TMPDIR": home,
		// curl also searches these inherited directories for a curlrc. Keep
		// downloads, including the upstream script's child curl, independent
		// of user settings that can redirect output into arbitrary files.
		"CURL_HOME": home, "XDG_CONFIG_HOME": home,
		"CODEX_HOME":        data,
		"CODEX_INSTALL_DIR": bin, "CODEX_NON_INTERACTIVE": "1",
		"PATH": bin + string(os.PathListSeparator) + envValue(clean, "PATH"),
	} {
		clean = setEnvValue(clean, key, value)
	}
	script, err := m.run(CommandSpec{Path: "curl", Args: []string{"-q", "-fsSL", "--proto", "=https", "--proto-redir", "=https", "--connect-timeout", "10", "--max-time", "60", codexInstallerURL}, Env: clean})
	if err != nil {
		return fmt.Errorf("获取 Codex 官方安装器失败: %w", err)
	}
	if len(bytes.TrimSpace(script)) == 0 {
		return fmt.Errorf("Codex 官方安装器内容为空，未执行")
	}
	// Downloading can take a minute. Do not run the script against a data root
	// or mount that disappeared or changed into a link in the meantime.
	if err = m.codexInstallerPathsOK(); err != nil {
		return err
	}
	if m.Initialized {
		if err = checkInactive(m.activePaths([]string{"codex"})); err != nil {
			return err
		}
	}
	if _, err = m.run(CommandSpec{Path: "/bin/sh", Args: []string{"-s"}, Env: clean, Input: script}); err != nil {
		return fmt.Errorf("Codex 官方安装器执行失败: %w", err)
	}
	return m.codexInstallerPathsOK()
}
