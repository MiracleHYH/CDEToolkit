package nido

import (
	"fmt"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
)

type codexPageState struct {
	status                codex.Status
	installErr, updateErr error
}

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
	var snapshot codexPageState
	return c.componentDynamicPage("Codex · 外部辅助集成", func() string {
		service := m.Codex
		if c.uiSession != nil {
			service.Ctx = c.uiSession.Context()
		}
		snapshot = codexPageState{status: service.Status(m.Config.Codex)}
		if snapshot.status.State == "linked" {
			_, snapshot.installErr = service.CheckInstall(m.Config.Codex, false)
			_, snapshot.updateErr = service.CheckInstall(m.Config.Codex, true)
		}
		return codexPageSummary(snapshot)
	}, func() []menuAction {
		// The shared page calls this immediately after Status in the same
		// worker. Reuse that observation rather than executing --version again.
		st := snapshot.status
		associateLabel := "关联已有目录"
		if st.State == "linked" {
			associateLabel = "重新发现 Codex 命令"
		}
		actions := []menuAction{{id: "associate", label: associateLabel, group: "配置", run: action("setup", false)}}
		if st.State == "unlinked" {
			return append(actions, menuAction{id: "install", label: "官方安装", group: "官方软件", run: action("install", false)})
		}
		if st.State == "linked" {
			actions = append(actions, menuAction{id: "proxy", label: "配置 Codex 运行代理", group: "配置", run: action("proxy", false)})
			if snapshot.updateErr == nil {
				actions = append(actions, menuAction{id: "update", label: "官方更新", group: "官方软件", run: action("update", false)})
			} else if snapshot.installErr == nil {
				actions = append(actions, menuAction{id: "install", label: "官方安装到当前 CODEX_HOME", group: "官方软件", run: action("install", false)})
			}
			checkLabel := "查询当前与最新版本"
			if st.Command == "" {
				checkLabel = "查询最新版本"
			}
			actions = append(actions, menuAction{id: "check", label: checkLabel, group: "官方软件", run: action("update", true)})
		}
		if st.Home != "" {
			actions = append(actions, menuAction{id: "detach", label: "解除关联，保留程序与数据", group: "关联", run: action("detach", false)})
		}
		return actions
	})
}

func codexPageSummary(snapshot codexPageState) string {
	st := snapshot.status
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
		switch st.Channel {
		case "standalone":
			text.WriteString("来源：官方 standalone\n")
		case "npm":
			text.WriteString("来源：npm；请使用 npm 等原安装方式更新，Nido 不覆盖现有命令。\n")
		case "homebrew":
			text.WriteString("来源：Homebrew；请使用 Homebrew 更新，Nido 不覆盖现有命令。\n")
		default:
			text.WriteString("来源：未确认；请确认原安装方式或重新发现命令，Nido 不覆盖未知来源的命令。\n")
		}
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
	if st.State == "linked" {
		if st.Command == "" {
			text.WriteString("提示：已关联数据目录不代表已安装 Codex 命令。\n")
			if snapshot.installErr == nil {
				text.WriteString("可在当前 CODEX_HOME 调用官方安装器；数据目录保持原位。\n")
			}
		}
		if st.Channel == "standalone" && snapshot.updateErr != nil {
			fmt.Fprintf(&text, "官方更新暂不可用：%s\n", snapshot.updateErr)
		}
		if st.Command == "" && snapshot.installErr != nil {
			fmt.Fprintf(&text, "官方安装暂不可用：%s\n", snapshot.installErr)
		}
	} else if st.State == "unavailable" {
		text.WriteString("请先检查已有 CODEX_HOME 和命令位置；当前不提供安装、更新或运行代理修改，不自动新建或迁移目录。\n")
	}
	text.WriteString("目录保持原位；官方安装更新使用 Nido Proxy，运行代理单独配置。\nNido 不管理 Codex 进程、认证和会话。")
	return text.String()
}
