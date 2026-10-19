package nido

import (
	"fmt"
	"os"
	"strings"
)

func (c *CLI) menu(m *Manager, o cliOptions, args []string) int {
	if len(args) != 0 || !c.interactive(o) {
		return c.fail(fmt.Errorf("nido 交互界面需要终端；自动化请使用明确子命令或 --json"))
	}
	actions := []menuAction{
		{id: "proxy", label: "Proxy", group: "组件", run: func() int { return c.proxyPage(m, o) }},
		{id: "codex", label: "Codex", group: "组件", run: func() int { return c.codexPage(m, o) }},
		{id: "conda", label: "Conda / pip", group: "组件", run: func() int { return c.condaMenu(m, o) }},
	}
	actions = append(actions, c.settingsActions(m, o)...)
	return c.componentPage("Nido", func() string {
		var text strings.Builder
		fmt.Fprintf(&text, "Nido %s · %s\n", Version, m.Root)
		if m.configBytes == nil {
			text.WriteString("尚未安装；请运行安装器完成目录与终端接入设置。\n")
		}
		observed := *m
		if c.uiSession != nil {
			observed.Codex.Ctx = c.uiSession.Context()
		}
		for _, row := range observed.Status() {
			fmt.Fprintf(&text, "%s: %s\n", row.Name, row.State)
		}
		if network, err := m.savedNetwork(); err != nil {
			fmt.Fprintf(&text, "网络配置需要重新选择：%s", err)
		} else {
			text.WriteString(networkSummary(network))
		}
		return strings.TrimSpace(text.String())
	}, actions)
}

func (c *CLI) settingsActions(m *Manager, o cliOptions) []menuAction {
	return []menuAction{
		{id: "github-accelerate", label: "GitHub Accelerate", group: "Nido", run: func() int { return c.githubAccelerateConfigure(m, o) }},
		{id: "nido-proxy", label: "Proxy", group: "Nido", run: func() int { return c.nidoProxyConfigure(m, o) }},
		{id: "check-update", label: "检查更新", group: "Nido", run: func() int {
			next := o
			next.check = false
			code := c.update(m, next, nil)
			if code == 0 {
				if current, err := os.Stat(m.Executable); err == nil && m.executableInfo != nil && !os.SameFile(current, m.executableInfo) {
					c.restartRequired = true
					fmt.Fprintln(c.Out, "Nido 已更新。请重新运行 nido 使用新版本。")
				}
			}
			return code
		}},
	}
}
