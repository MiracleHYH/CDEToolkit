package nido

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

// The shared page owns navigation, focus, task scheduling and terminal IO.
// Conda supplies only saved state and actions using its existing command path.
func (c *CLI) condaMenu(m *Manager, o cliOptions) int {
	if !c.interactive(o) {
		return c.fail(fmt.Errorf("Conda/pip 菜单需要交互终端；脚本请使用明确子命令"))
	}
	return c.componentPage("Conda / pip 配置", func() string { return condaPageStatus(m) }, c.condaActions(m, o))
}

func (c *CLI) condaActions(m *Manager, o cliOptions) []menuAction {
	run := func(args ...string) func() int {
		return func() int { return c.conda(m, o, args) }
	}
	return []menuAction{
		{id: "conda-mirror", label: "设置 Conda 镜像", group: "镜像", run: run("mirror", "conda")},
		{id: "pip-mirror", label: "设置 pip 镜像", group: "镜像", run: run("mirror", "pip")},
		{id: "envs-dir", label: "设置环境目录", group: "目录", run: run("envs")},
		{id: "conda-cache", label: "设置 Conda 缓存目录", group: "目录", run: run("cache", "conda")},
		{id: "pip-cache", label: "设置 pip 缓存目录", group: "目录", run: run("cache", "pip")},
		{id: "record-defaults", label: "保存首次镜像", group: "首次镜像记录", run: func() int {
			choice, err := c.condaChoice("记录哪些工具的首次镜像", []string{"Conda 和 pip", "仅 Conda", "仅 pip"}, 0)
			if err != nil {
				return c.condaInteractionError(err)
			}
			return c.conda(m, o, []string{"setup", []string{"all", "conda", "pip"}[choice]})
		}},
		{id: "validate", label: "核对当前生效配置（本地只读查询）", group: "核对", run: run("validate")},
		{id: "stop-conda-mirror", label: "停止 Conda 镜像管理", group: "停止管理（保留原生配置和数据）", run: run("mirror", "conda", "off")},
		{id: "stop-pip-mirror", label: "停止 pip 镜像管理", group: "停止管理（保留原生配置和数据）", run: run("mirror", "pip", "off")},
		{id: "stop-envs-dir", label: "停止环境目录跟踪", group: "停止管理（保留原生配置和数据）", run: run("envs", "off")},
		{id: "stop-conda-cache", label: "停止 Conda 缓存目录跟踪", group: "停止管理（保留原生配置和数据）", run: run("cache", "conda", "off")},
		{id: "stop-pip-cache", label: "停止 pip 缓存目录跟踪", group: "停止管理（保留原生配置和数据）", run: run("cache", "pip", "off")},
		{id: "stop-all", label: "停止全部 Conda/pip 管理", group: "停止管理（保留原生配置和数据）", run: run("reset")},
	}
}

// Saved state only: do not call status/validation services from rendering or
// cursor movement. The shared page refreshes this after an explicit action.
func condaPageStatus(m *Manager) string {
	cfg := m.Config.Conda
	var text strings.Builder
	fmt.Fprintln(&text, "以下是 Nido 保存值，未核对当前 Shell 或上游实际生效值。")
	fmt.Fprintln(&text, "Conda 镜像：", condaInteractiveMirror(cfg.CondaMirror))
	fmt.Fprintln(&text, "pip 镜像：", condaInteractivePipConfig(cfg))
	fmt.Fprintln(&text, "环境目录：", condaInteractiveValue(cfg.EnvsDir))
	fmt.Fprintln(&text, "Conda 缓存：", condaInteractiveValue(cfg.PkgsDir))
	fmt.Fprintln(&text, "pip 缓存：", condaInteractiveValue(cfg.PipCacheDir))
	fmt.Fprintf(&text, "首次镜像记录：Conda %s；pip %s\n", condaCaptured(cfg.Defaults.CondaCaptured), condaCaptured(cfg.Defaults.PipCaptured))
	fmt.Fprintln(&text, "停止管理保留原生配置和数据；只改使用位置，不创建、搬迁或清理环境与缓存。")
	fmt.Fprint(&text, "目录按原生配置生效；镜像环境需加载 env.sh。取消不会撤销此前已完成的独立操作。")
	return text.String()
}

// These adapters deliberately do not interpret keys or textual exit words.
func (c *CLI) condaAsk(label, fallback string) (string, error) {
	return c.ask(label, fallback)
}

func (c *CLI) condaChoice(label string, options []string, fallback int) (int, error) {
	return c.choose(label, options, fallback)
}

func (c *CLI) condaConfirm(o cliOptions, what string) error {
	return c.confirm(o, what)
}

func (c *CLI) condaInteractionError(err error) int {
	if errors.Is(err, terminalui.ErrClosed) {
		c.promptClosed = true
		return 0
	}
	if errors.Is(err, terminalui.ErrCancelled) {
		fmt.Fprintln(c.Out, "已返回；本次尚未提交的选择未保存，此前完成的独立操作保留。")
		return 0
	}
	return c.fail(err)
}

func (c *CLI) condaOperationError(m *Manager, err error) int {
	// A simultaneous exit request must not hide an unrelated native write or
	// terminal failure. Only the operation's typed cancellation means closure.
	if contextErr := m.condaContextError(); contextErr != nil && errors.Is(err, contextErr) {
		return c.condaInteractionError(terminalui.ErrClosed)
	}
	return c.condaInteractionError(err)
}

// Interactive completion only supplies the existing command's arguments. The
// regular validation, confirmation and mutation path remains the sole writer.
func (c *CLI) completeCondaArguments(m *Manager, o cliOptions, args []string) ([]string, bool, error) {
	action, rest := args[0], args[1:]
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
	if m.configBytes == nil {
		return nil, false, fmt.Errorf("尚未初始化，请先运行 nido init")
	}
	if action != "envs" && len(rest) == 0 {
		choice, err := c.condaChoice("选择工具", []string{"Conda", "pip"}, 0)
		if err != nil {
			return nil, false, err
		}
		rest = []string{[]string{"conda", "pip"}[choice]}
		args = []string{action, rest[0]}
	}
	if action == "mirror" {
		tool := rest[0]
		current := m.Config.Conda.CondaMirror
		restore := "恢复首次保存的镜像设置"
		captured := m.Config.Conda.Defaults.CondaCaptured
		if tool == "pip" {
			captured = m.Config.Conda.Defaults.PipCaptured
		}
		if !captured {
			restore += "（尚未保存；确认后先记录当前镜像）"
		}
		options := []string{"保持当前选择", restore, "选择官方源", "选择清华镜像"}
		values := []string{"", "default", "official", "tuna"}
		if tool == "pip" {
			current = m.Config.Conda.PipMirror
			if current == "" && m.Config.Conda.PipIndexURL != "" {
				current = "custom"
			}
			options = append(options, "选择中科大镜像", "自定义 HTTPS simple index")
			values = append(values, "ustc", "custom")
		}
		choice, err := c.condaChoice(tool+" 镜像（Nido 当前选择："+condaInteractiveMirror(current)+"）", options, 0)
		if err != nil || choice == 0 {
			return args, choice == 0 && err == nil, err
		}
		value := values[choice]
		if value == "custom" {
			currentIndex := m.Config.Conda.PipIndexURL
			if _, err := conda.PipMirror(currentIndex); err != nil {
				currentIndex = ""
			}
			value, err = c.condaAsk("pip HTTPS simple index", currentIndex)
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
	value, err := c.condaAsk(label+"（仅修改配置，不迁移数据）", current)
	if err != nil {
		return nil, false, err
	}
	if value == current {
		return args, true, nil
	}
	if value == "off" {
		return nil, false, fmt.Errorf("目录需要绝对路径；停止跟踪请使用页面上的对应动作，脚本仍可显式使用 off")
	}
	return append(args, value), false, nil
}

func condaInteractiveValue(value string) string {
	if value == "" {
		return "未管理，保留原生设置"
	}
	return value
}

func condaInteractiveMirror(value string) string {
	switch value {
	case "":
		return "未管理，保留原生设置"
	case "default":
		return "首次保存的镜像设置"
	case "official", "conda", "pypi":
		return "官方源"
	case "tuna":
		return "清华镜像"
	case "ustc":
		return "中科大镜像"
	case "custom":
		return "自定义 HTTPS 源"
	default:
		return "无效选择，请重新设置"
	}
}

func condaInteractivePipMirror(m *Manager) string {
	return condaInteractivePipConfig(m.Config.Conda)
}

func condaInteractivePipConfig(cfg conda.Config) string {
	if cfg.PipMirror == "custom" || cfg.PipMirror == "" && cfg.PipIndexURL != "" {
		if index, err := conda.PipMirror(cfg.PipIndexURL); err == nil && strings.HasPrefix(cfg.PipIndexURL, "https://") {
			return "自定义 HTTPS 源（" + index + "）"
		}
		return "自定义 HTTPS 源（地址待检查）"
	}
	if cfg.PipMirror != "" {
		return condaInteractiveMirror(cfg.PipMirror)
	}
	return condaInteractiveMirror("")
}

// Preview only public selections. Defaults, original URLs and channel context
// can contain credentials and must never be formatted as a Go struct here.
func (c *CLI) previewCondaChange(m *Manager, action string, next conda.Config, files []string) {
	current := m.Config.Conda
	fmt.Fprintln(c.Err, "本次配置预览（当前值为 Nido 保存的选择，不代表上游实时值）：")
	row := func(label, before, after string) {
		fmt.Fprintf(c.Err, "  %s：%s → %s\n", label, before, after)
	}
	if action == "setup" {
		row("Conda 首次镜像", condaCaptured(current.Defaults.CondaCaptured), condaCaptured(next.Defaults.CondaCaptured))
		row("pip 首次镜像", condaCaptured(current.Defaults.PipCaptured), condaCaptured(next.Defaults.PipCaptured))
	} else {
		row("Conda 镜像", condaInteractiveMirror(current.CondaMirror), condaInteractiveMirror(next.CondaMirror))
		row("pip 镜像", condaInteractivePipMirror(m), condaInteractivePipConfig(next))
		row("环境目录", condaInteractiveValue(current.EnvsDir), condaInteractiveValue(next.EnvsDir))
		row("Conda 缓存目录", condaInteractiveValue(current.PkgsDir), condaInteractiveValue(next.PkgsDir))
		row("pip 缓存目录", condaInteractiveValue(current.PipCacheDir), condaInteractiveValue(next.PipCacheDir))
	}
	fmt.Fprintln(c.Err, "  Nido 状态/环境文件：", filepath.Join(m.Root, "config.toml"), "、", filepath.Join(m.Root, "env.sh"))
	if mirror, _ := m.Conda.MirrorFile(next); action != "setup" && mirror != "" {
		fmt.Fprintln(c.Err, "  Nido 镜像配置文件：", mirror)
	}
	if len(files) == 0 {
		fmt.Fprintln(c.Err, "  本次需改写的原生文件：无")
	} else {
		fmt.Fprintln(c.Err, "  本次需改写的原生文件：")
		for _, file := range files {
			fmt.Fprintln(c.Err, "   ", file)
		}
	}
	fmt.Fprintln(c.Err, "仅修改配置；不创建、搬迁或清理环境与缓存数据。目录设置原生生效；镜像环境仍需加载对应 env.sh。")
}

func condaCaptured(captured bool) string {
	if captured {
		return "已保存（不会覆盖）"
	}
	return "尚未保存"
}
