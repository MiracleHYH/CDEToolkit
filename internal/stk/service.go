package stk

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (m *Manager) rewriteService(service Service, before []byte) ([]byte, error) {
	if err := validateService(service); err != nil {
		return nil, err
	}
	section := ""
	matched := false
	lines := strings.SplitAfter(string(before), "\n")
	mappings := m.pathMappings()
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			section = trim
		}
		wanted := "[program:" + service.Name + "]"
		if service.Backend == "systemd" {
			wanted = "[Service]"
		}
		if section != wanted {
			continue
		}
		matched = true
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		allowed := map[string]bool{"command": true, "directory": true, "stdout_logfile": true, "stderr_logfile": true}
		if service.Backend == "systemd" {
			allowed = map[string]bool{"ExecStart": true, "WorkingDirectory": true}
		}
		if !allowed[key] {
			continue
		}
		value = m.replaceServicePaths(value, mappings)
		prefix, _, _ := strings.Cut(line, "=")
		lines[i] = prefix + "=" + value
	}
	if !matched {
		return nil, fmt.Errorf("服务定义未找到已登记分组，拒绝修改")
	}
	return []byte(strings.Join(lines, "")), nil
}

func (m *Manager) replaceServicePaths(text string, mappings []pathMapping) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		matched := false
		if i == 0 || strings.ContainsRune(" \t'\"=", rune(text[i-1])) {
			for _, mapping := range mappings {
				old, current := mapping.old, mapping.current
				if current == "" || !strings.HasPrefix(text[i:], old) {
					continue
				}
				end := i + len(old)
				if end < len(text) && !strings.ContainsRune("/ \r\n\t'\"", rune(text[end])) {
					continue
				}
				out.WriteString(current)
				i = end
				matched = true
				break
			}
		}
		if !matched {
			out.WriteByte(text[i])
			i++
		}
	}
	return out.String()
}

func validateService(s Service) error {
	if s.Backend != "supervisor" && s.Backend != "systemd" {
		return fmt.Errorf("只支持 supervisor/systemd 服务后端")
	}
	if s.Name == "" || strings.HasPrefix(s.Name, "-") {
		return fmt.Errorf("服务名称无效")
	}
	for _, c := range s.Name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:", c)) {
			return fmt.Errorf("服务名称无效")
		}
	}
	return nil
}

func (m *Manager) serviceTemplate(name, binary, config string) (string, error) {
	data, _ := m.resolve(m.State.Modules[name].Home)
	for _, s := range []string{binary, config, m.Home, data} {
		if strings.ContainsAny(s, "\r\n%\"") {
			return "", fmt.Errorf("服务模板路径含不支持的字符")
		}
	}
	command := quote(binary)
	if name == "proxy" {
		data, _ := m.resolve(m.State.Modules[name].Home)
		command += " -d " + quote(data) + " -f " + quote(config)
	} else {
		command += " __run_monitor"
	}
	return "[program:stk-" + name + "]\ncommand=" + command + "\ndirectory=" + m.Home + "\nautostart=false\nautorestart=unexpected\nstopasgroup=true\nkillasgroup=true\nredirect_stderr=true\nstdout_logfile=" + m.Home + "/run/" + name + ".log\nstdout_logfile_maxbytes=5MB\nstdout_logfile_backups=2\n", nil
}

func (m *Manager) systemdTemplate(name, binary, config string) (string, error) {
	data, _ := m.resolve(m.State.Modules[name].Home)
	for _, s := range []string{binary, config, m.Home, data} {
		if strings.ContainsAny(s, "\r\n%\"\\$") {
			return "", fmt.Errorf("systemd 模板路径含不支持的字符")
		}
	}
	command := "\"" + binary + "\""
	if name == "proxy" {
		data, _ := m.resolve(m.State.Modules[name].Home)
		command += " -d \"" + data + "\" -f \"" + config + "\""
	} else {
		command += " __run_monitor"
	}
	return "[Unit]\nDescription=STK " + name + "\n\n[Service]\nType=simple\nExecStart=" + command + "\nWorkingDirectory=\"" + m.Home + "\"\nRestart=on-failure\n\n[Install]\nWantedBy=default.target\n", nil
}

func (m *Manager) ServiceAction(name, action string) ([]byte, error) {
	if action != "start" && action != "stop" && action != "restart" && action != "status" && action != "logs" {
		return nil, fmt.Errorf("未知服务操作")
	}
	mod, ok := m.State.Modules[name]
	if !ok {
		return nil, fmt.Errorf("模块未管理")
	}
	if (action == "start" || action == "restart") && !m.Config.Modules[name].Enabled {
		return nil, fmt.Errorf("模块未启用: %s", name)
	}
	if action == "start" || action == "restart" {
		if err := m.requireCurrentRoots(); err != nil {
			return nil, err
		}
	}
	if name == "codex" {
		return nil, fmt.Errorf("Codex 是命令行程序；使用 exec codex 启动会话")
	}
	s := mod.Service
	if e := validateService(s); e != nil {
		return nil, fmt.Errorf("尚未接管有效服务；模板位于 STK_HOME/services，需另行注册")
	}
	controller, e := m.resolve(s.Controller)
	if e != nil {
		return nil, e
	}
	if controller == "" {
		command := "supervisorctl"
		if s.Backend == "systemd" {
			command = "systemctl"
		}
		controller, e = exec.LookPath(command)
		if e != nil {
			return nil, e
		}
	}
	var args []string
	if s.Backend == "supervisor" {
		cfg, e := m.resolve(s.Config)
		if e != nil {
			return nil, e
		}
		if cfg != "" {
			args = append(args, "-c", cfg)
		}
		if action == "logs" {
			args = append(args, "tail", s.Name)
		} else {
			args = append(args, action, s.Name)
		}
	} else {
		if !s.System {
			args = append(args, "--user")
		}
		if action == "logs" {
			return nil, fmt.Errorf("systemd 日志请使用 journalctl；STK 不启动日志守护")
		}
		args = append(args, action, s.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, controller, args...)
	cmd.Env = os.Environ()
	out, e := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, e
}

func (m *Manager) Command(name string, args []string) (string, []string, []string, error) {
	mod, ok := m.State.Modules[name]
	if !ok {
		return "", nil, nil, fmt.Errorf("模块未管理: %s", name)
	}
	if !m.Config.Modules[name].Enabled {
		return "", nil, nil, fmt.Errorf("模块未启用: %s", name)
	}
	if err := m.requireCurrentRoots(); err != nil {
		return "", nil, nil, err
	}
	path, e := m.resolve(mod.Binary)
	if e != nil {
		return "", nil, nil, e
	}
	if !executable(path) {
		return "", nil, nil, fmt.Errorf("组件入口不可用，请执行 repair: %s", path)
	}
	home, _ := m.resolve(mod.Home)
	env := os.Environ()
	if name == "proxy" {
		config, e := m.resolve(mod.Config)
		if e != nil {
			return "", nil, nil, e
		}
		if !regular(config) || !directory(home) {
			return "", nil, nil, fmt.Errorf("代理配置或数据目录不可用")
		}
		args = append([]string{"-d", home, "-f", config}, args...)
	}
	if name == "codex" {
		if !directory(home) {
			return "", nil, nil, fmt.Errorf("Codex 主目录不可用")
		}
		endpoint := m.Config.Modules[name].Endpoint
		if endpoint == "" && m.Config.Modules["proxy"].Enabled {
			endpoint = m.Config.Modules["proxy"].Endpoint
		}
		if endpoint != "" {
			if e = validEndpoint(endpoint); e != nil {
				return "", nil, nil, e
			}
		}
		env = scopedEnvironment(env, home, endpoint)
	}
	return path, append([]string{path}, args...), env, nil
}

func (m *Manager) Check(name string) error {
	path, argv, env, e := m.Command(name, nil)
	if e != nil {
		return e
	}
	arg := "--version"
	if name == "proxy" {
		arg = "-v"
	}
	if name == "gds" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, append(argv[1:], arg)...)
	cmd.Env = env
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("%s 可执行性检查失败（未输出组件配置）: %w", name, e)
	}
	return nil
}
