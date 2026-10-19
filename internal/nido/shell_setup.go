package nido

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const shellBlockStart = "# >>> nido shell integration >>>"
const shellBlockEnd = "# <<< nido shell integration <<<"

type shellSetup struct {
	name, path string
	before     []byte
	after      []byte
	info       os.FileInfo
	mode       os.FileMode
	parent     string
	parentInfo os.FileInfo
}

func shellEnv(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], key+"=") {
			return strings.TrimPrefix(env[i], key+"=")
		}
	}
	return ""
}

func (m *Manager) shellProfile(name string) (string, error) {
	switch name {
	case "bash":
		return filepath.Join(m.UserHome, ".bashrc"), nil
	case "zsh":
		dir := shellEnv(m.Env, "ZDOTDIR")
		if dir == "" {
			dir = m.UserHome
		}
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || invalidShellPath(dir) {
			return "", fmt.Errorf("ZDOTDIR 需要规范的绝对路径")
		}
		return filepath.Join(dir, ".zshrc"), nil
	default:
		return "", fmt.Errorf("仅支持 bash 或 zsh；请显式指定 shell setup bash 或 shell setup zsh")
	}
}

func shellFile(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !info.Mode().IsRegular() || !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) {
		return nil, nil, fmt.Errorf("Shell 配置必须是当前用户拥有的无链接普通文件；请手动加载 env.sh")
	}
	b, err := readFile(path, 1<<20)
	if err != nil {
		return nil, nil, err
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) || info.Mode() != current.Mode() || !sameFileOwner(info, current) {
		return nil, nil, fmt.Errorf("Shell 配置在读取期间发生变化")
	}
	return b, info, nil
}

func (m *Manager) prepareShell(name string) (shellSetup, error) {
	var plan shellSetup
	if m.configBytes == nil {
		return plan, fmt.Errorf("尚未初始化，请先运行 nido init")
	}
	if err := m.checkRoot(); err != nil {
		return plan, err
	}
	if _, err := readFile(filepath.Join(m.Root, "env.sh"), 1<<20); err != nil {
		return plan, fmt.Errorf("env.sh 不可用，请先重新运行 nido init: %w", err)
	}
	if err := m.checkEnv(); err != nil {
		return plan, err
	}
	path, err := m.shellProfile(name)
	if err != nil {
		return plan, err
	}
	if err := safeParents(filepath.Dir(path)); err != nil {
		return plan, err
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	for os.IsNotExist(err) {
		parent = filepath.Dir(parent)
		parentInfo, err = os.Lstat(parent)
	}
	if err != nil || !parentInfo.IsDir() {
		return plan, fmt.Errorf("Shell 配置目录无效")
	}
	before, info, err := shellFile(path)
	if err != nil {
		return plan, err
	}
	source := shellQuote(filepath.Join(m.Root, "env.sh"))
	block := []byte(shellBlockStart + "\n[ ! -f " + source + " ] || . " + source + "\n" + shellBlockEnd + "\n")
	after := append([]byte(nil), before...)
	starts, ends := bytes.Count(before, []byte(shellBlockStart)), bytes.Count(before, []byte(shellBlockEnd))
	if starts != 0 || ends != 0 {
		at := bytes.Index(before, block)
		if starts != 1 || ends != 1 || at < 0 || at > 0 && before[at-1] != '\n' {
			return plan, fmt.Errorf("Shell 配置已有不同或不完整的 Nido 标记块；请检查后手动处理，不会覆盖")
		}
	} else {
		if len(after) > 0 && after[len(after)-1] != '\n' {
			after = append(after, '\n')
		}
		after = append(after, block...)
	}
	if len(after) > 1<<20 {
		return plan, fmt.Errorf("Shell 配置超过大小限制；请手动加载 env.sh")
	}
	mode := os.FileMode(0600)
	if info != nil {
		mode = info.Mode().Perm()
	}
	return shellSetup{name: name, path: path, before: before, after: after, info: info, mode: mode, parent: parent, parentInfo: parentInfo}, nil
}

func (m *Manager) applyShell(plan shellSetup) error {
	return m.mutate(false, func() error {
		check := func() error {
			if err := safeParents(filepath.Dir(plan.path)); err != nil {
				return err
			}
			parent, err := os.Lstat(plan.parent)
			if err != nil || !os.SameFile(parent, plan.parentInfo) || parent.Mode() != plan.parentInfo.Mode() || !sameFileOwner(parent, plan.parentInfo) {
				return fmt.Errorf("Shell 配置目录在确认期间发生变化")
			}
			current, info, err := shellFile(plan.path)
			if err != nil {
				return err
			}
			if !bytes.Equal(current, plan.before) || (info == nil) != (plan.info == nil) || info != nil && (!os.SameFile(info, plan.info) || info.Mode() != plan.info.Mode() || !sameFileOwner(info, plan.info)) {
				return fmt.Errorf("Shell 配置在确认期间发生变化，未覆盖用户编辑")
			}
			return nil
		}
		if err := check(); err != nil {
			return err
		}
		if bytes.Equal(plan.before, plan.after) {
			return nil
		}
		return replaceFileOwned(plan.path, plan.after, plan.mode, check, plan.info)
	})
}

// shell only opts a supported shell into the already generated environment.
// It never sources user profiles, changes the parent shell, or installs tools.
func (c *CLI) shell(m *Manager, o cliOptions, args []string) int {
	if len(args) < 1 || len(args) > 2 || args[0] != "setup" {
		return c.fail(fmt.Errorf("使用 shell setup [bash|zsh]"))
	}
	name := ""
	if len(args) == 2 {
		name = args[1]
	} else {
		if o.yes {
			return c.fail(fmt.Errorf("--yes 需要显式指定 bash 或 zsh，避免猜测要修改的 Shell 配置"))
		}
		name = filepath.Base(shellEnv(m.Env, "SHELL"))
	}
	plan, err := m.prepareShell(name)
	if err != nil {
		return c.fail(err)
	}
	result := map[string]string{"operation": "shell-setup", "shell": name, "profile": plan.path, "environment": filepath.Join(m.Root, "env.sh"), "activate": ". " + shellQuote(filepath.Join(m.Root, "env.sh"))}
	if o.dry {
		return c.output(o, result)
	}
	if err := c.confirm(o, "将 Nido 环境接入 "+plan.path); err != nil {
		return c.fail(err)
	}
	if err := m.applyShell(plan); err != nil {
		return c.fail(err)
	}
	result["detail"] = "读取 " + plan.path + " 的新 Shell 将加载 Nido；当前终端请执行 activate 显示的命令。不会修改父 Shell 环境。"
	return c.output(o, result)
}
