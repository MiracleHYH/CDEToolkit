package nido

import (
	"fmt"
	"strings"
)

// Proxy only supplies state and actions. The shared UI owns keyboard input,
// confirmation, task scheduling, focus, progress and terminal restoration.
func (c *CLI) proxyPage(m *Manager, o cliOptions) int {
	return c.componentDynamicPage("Proxy", func() string { return c.proxyPageStatus(m) }, func() []menuAction { return c.proxyPageActions(m, o) })
}

func (c *CLI) proxyPageActions(m *Manager, o cliOptions) []menuAction {
	action := func(name string) func() int {
		return func() int { return c.proxy(m, o, []string{name}) }
	}
	status := m.Proxy.Status()
	if status.Program == "missing" && (status.Runtime.State == "stopped" || status.Runtime.State == "exited") {
		return []menuAction{{id: "install", label: "安装 Proxy", group: "安装", run: action("install")}}
	}
	return []menuAction{
		{id: "quickstart", label: "Quick Start", group: "配置", run: action("quickstart")},
		{id: "update", label: "更新内核", group: "更新", run: action("update")},
		{id: "provider-update", label: "更新订阅", group: "更新", run: action("provider-update")},
		{id: "reload", label: "重载配置", group: "运行", run: action("reload")},
		{id: "restart", label: "重启", group: "运行", run: action("restart")},
		{id: "stop", label: "停止", group: "运行", run: action("stop")},
		{id: "start", label: "启动", group: "运行", run: action("start")},
		{id: "remove", label: "删除 Proxy，保留配置与数据", group: "维护", run: action("remove")},
	}
}

func (c *CLI) proxyPageStatus(m *Manager) string {
	status := m.Proxy.Status()
	var out strings.Builder
	fmt.Fprintf(&out, "程序：%s  配置：%s  运行：%s\n目录：%s\n主配置：%s/config.yaml\n日志：%s/output.log\n", status.Program, status.Config, status.Runtime.State, status.Directory, status.Directory, status.Directory)
	if status.ConfigChanged {
		fmt.Fprintln(&out, "磁盘配置与已记录的应用配置不同。")
	}
	if status.Runtime.State == "stopped" || status.Runtime.State == "exited" || status.Program == "missing" {
		fmt.Fprintln(&out, "core 未运行；组选择与已有延迟不可查询。")
		return out.String()
	}
	overview, err := m.Proxy.OverviewContext(c.proxyTaskContext())
	if err != nil {
		fmt.Fprintf(&out, "core 状态不可读取：%s\n", err)
		return out.String()
	}
	fmt.Fprintf(&out, "core：%s\n选择关系（非网络追踪；只展示已有测量）：\n", overview.Version)
	if len(overview.Groups) == 0 {
		fmt.Fprintln(&out, "没有 core 返回的 proxy-group。")
	}
	for _, group := range overview.Groups {
		fmt.Fprintln(&out, group.String())
	}
	providers, err := m.Proxy.ProvidersContext(c.proxyTaskContext())
	if err != nil {
		fmt.Fprintf(&out, "订阅状态不可读取：%s\n", err)
		return out.String()
	}
	for _, provider := range providers {
		if !strings.EqualFold(provider.VehicleType, "http") && !strings.EqualFold(provider.VehicleType, "file") {
			continue
		}
		updated := provider.UpdatedAt
		if updated == "" {
			updated = "未报告"
		}
		fmt.Fprintf(&out, "订阅 %s/%q：%q；core 报告更新时间 %q\n", provider.Kind, provider.Name, provider.VehicleType, updated)
	}
	return out.String()
}
