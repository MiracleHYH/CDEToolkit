package nido

import "fmt"

// Interactive completion only supplies the existing command's arguments. The
// regular validation, confirmation and mutation path remains the sole writer.
func (c *CLI) completeCondaArguments(m *Manager, o cliOptions, args []string) ([]string, bool, error) {
	action, rest := args[0], args[1:]
	if action == "configure" {
		if len(rest) != 0 {
			return nil, false, fmt.Errorf("conda configure 不接受位置参数")
		}
		if !c.interactive(o) {
			return nil, false, fmt.Errorf("conda configure 需要交互终端；请使用 mirror、cache 或 envs 并提供明确参数")
		}
	} else {
		if !c.interactive(o) {
			return args, false, nil
		}
		switch action {
		case "mirror", "cache":
			if len(rest) >= 2 || len(rest) == 1 && rest[0] != "conda" && rest[0] != "pip" {
				return args, false, nil
			}
		case "envs":
			if len(rest) != 0 {
				return args, false, nil
			}
		default:
			return args, false, nil
		}
	}
	if m.configBytes == nil {
		return nil, false, fmt.Errorf("尚未初始化，请先运行 nido init")
	}
	if action == "configure" {
		cfg := m.Config.Conda
		choice, err := c.choose("选择一个配置项", []string{
			"保持现有设置",
			"Conda 镜像（当前：" + condaInteractiveValue(cfg.CondaMirror) + "）",
			"pip 镜像（当前：" + condaInteractivePipMirror(m) + "）",
			"Conda 环境目录（当前：" + condaInteractiveValue(cfg.EnvsDir) + "）",
			"Conda 包缓存目录（当前：" + condaInteractiveValue(cfg.PkgsDir) + "）",
			"pip 缓存目录（当前：" + condaInteractiveValue(cfg.PipCacheDir) + "）",
		}, 0)
		if err != nil || choice == 0 {
			return args, choice == 0 && err == nil, err
		}
		commands := [][]string{{"mirror", "conda"}, {"mirror", "pip"}, {"envs"}, {"cache", "conda"}, {"cache", "pip"}}
		args = commands[choice-1]
		action, rest = args[0], args[1:]
	}
	if action != "envs" && len(rest) == 0 {
		choice, err := c.choose("选择工具", []string{"Conda", "pip"}, 0)
		if err != nil {
			return nil, false, err
		}
		rest = []string{[]string{"conda", "pip"}[choice]}
		args = []string{action, rest[0]}
	}
	if action == "mirror" {
		tool := rest[0]
		current := m.Config.Conda.CondaMirror
		options := []string{"保持现有设置", "default：恢复首次记录的本机镜像", "official：官方源", "tuna：清华镜像"}
		values := []string{"", "default", "official", "tuna"}
		if tool == "pip" {
			current = condaInteractivePipMirror(m)
			options = append(options, "ustc：中科大镜像", "自定义 HTTPS simple index")
			values = append(values, "ustc", "custom")
		}
		options = append(options, "off：停止管理，保留 default 和原生配置")
		values = append(values, "off")
		choice, err := c.choose(tool+" 镜像（当前："+condaInteractiveValue(current)+"）", options, 0)
		if err != nil || choice == 0 {
			return args, choice == 0 && err == nil, err
		}
		value := values[choice]
		if value == "custom" {
			value, err = c.setting("pip HTTPS simple index（回车保持）", m.Config.Conda.PipIndexURL)
			if err != nil {
				return nil, false, err
			}
			if value == "" || value == m.Config.Conda.PipIndexURL {
				return args, true, nil
			}
		}
		return append(args, value), false, nil
	}
	current, label := m.Config.Conda.EnvsDir, "Conda 环境目录"
	if action == "cache" {
		current, label = m.Config.Conda.PkgsDir, "Conda 包缓存目录"
		if rest[0] == "pip" {
			current, label = m.Config.Conda.PipCacheDir, "pip 缓存目录"
		}
	}
	value, err := c.setting(label+"（回车保持，off 停止管理）", current)
	if err != nil {
		return nil, false, err
	}
	if value == current {
		return args, true, nil
	}
	return append(args, value), false, nil
}

func condaInteractiveValue(value string) string {
	if value == "" {
		return "未管理"
	}
	return value
}

func condaInteractivePipMirror(m *Manager) string {
	if m.Config.Conda.PipMirror != "" {
		return m.Config.Conda.PipMirror
	}
	if m.Config.Conda.PipIndexURL != "" {
		return "custom"
	}
	return "未管理"
}
