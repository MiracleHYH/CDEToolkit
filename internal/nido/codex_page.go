package nido

import (
	"fmt"
	"strings"
)

// codexPage is an external integration page. Status is a local, read-only
// snapshot; only the explicit version-check action queries the release API.
// The shared terminal driver owns all rendering, prompts and keyboard handling.
func (c *CLI) codexPage(m *Manager, o cliOptions) int {
	action := func(name string, check bool) func() int {
		return func() int {
			next := o
			next.check = check
			return c.codex(m, next, []string{name})
		}
	}
	return c.componentPage("Codex · 外部辅助集成", func() string { return c.codexPageStatus(m) }, []menuAction{
		{id: "associate", label: "关联已有目录", group: "配置", run: action("setup", false)},
		{id: "proxy", label: "配置 Codex 运行代理", group: "配置", run: action("proxy", false)},
		{id: "install", label: "官方安装", group: "官方软件", run: action("install", false)},
		{id: "update", label: "官方更新", group: "官方软件", run: action("update", false)},
		{id: "check", label: "查询当前与最新版本", group: "官方软件", run: action("update", true)},
		{id: "detach", label: "解除关联，保留程序与数据", group: "关联", run: action("detach", false)},
	})
}

func (c *CLI) codexPageStatus(m *Manager) string {
	service := m.Codex
	if c.uiSession != nil {
		service.Ctx = c.uiSession.Context()
	}
	st := service.Status(m.Config.Codex)
	state := map[string]string{"unlinked": "未关联", "linked": "已关联", "unavailable": "目录不可用"}[st.State]
	if state == "" {
		state = "状态未知"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "状态：%s\n", state)
	if st.Home == "" {
		text.WriteString("CODEX_HOME：未关联；可发现已有目录或选择目录后官方安装。\n")
	} else {
		fmt.Fprintf(&text, "CODEX_HOME：%s\n", st.Home)
	}
	if st.Command != "" {
		fmt.Fprintf(&text, "命令：%s\n", st.Command)
	} else {
		text.WriteString("命令：未发现\n")
	}
	if st.Version != "" {
		fmt.Fprintf(&text, "版本：%s\n", st.Version)
	} else {
		text.WriteString("版本：未知\n")
	}
	// Never echo proxy endpoints or read authentication/session storage. A
	// boolean ownership receipt is sufficient for this page's local status.
	if st.ProxyConfigured {
		text.WriteString("运行代理：已保存 Codex .env 代理配置\n")
	} else {
		text.WriteString("运行代理：未由 Nido 配置\n")
	}
	if st.Detail != "" {
		fmt.Fprintf(&text, "提示：%s\n", st.Detail)
	}
	text.WriteString("目录保持原位；官方安装更新使用 Nido Proxy，运行代理单独配置。\nNido 不管理 Codex 进程、认证和会话。")
	return text.String()
}
