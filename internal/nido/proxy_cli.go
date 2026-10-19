package nido

import (
	"encoding/json"
	"fmt"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
)

func (c *CLI) proxyError(o cliOptions, err error) int {
	if o.json {
		_ = json.NewEncoder(c.Err).Encode(struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}{"proxy_operation_failed", err.Error()})
		return 1
	}
	return c.fail(err)
}

func (c *CLI) proxy(m *Manager, o cliOptions, args []string) int {
	// The menu reuses its Manager. A one-operation route must never become the
	// next action's base transport or retain a prior network binding snapshot.
	httpClient, apiClient, githubPolicy := m.Proxy.HTTP, m.Proxy.APIHTTP, m.Proxy.GitHubProxy
	defer func() {
		if m.Proxy.HTTP != nil && m.Proxy.HTTP != httpClient {
			m.Proxy.HTTP.CloseIdleConnections()
		}
		if m.Proxy.APIHTTP != nil && m.Proxy.APIHTTP != apiClient {
			m.Proxy.APIHTTP.CloseIdleConnections()
		}
		m.Proxy.HTTP, m.Proxy.APIHTTP, m.Proxy.GitHubProxy = httpClient, apiClient, githubPolicy
	}()
	action := "status"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	fail := func(err error) int { return c.proxyError(o, err) }
	fileNetwork := action == "install" || action == "update" && o.check
	if o.githubProxy != "" && (!fileNetwork || o.artifact != "" && o.artifact != "latest") {
		return fail(fmt.Errorf("--github-proxy 仅用于首次在线安装或版本查询；原生更新走 core 自身规则"))
	}
	if action == "bind" {
		return fail(fmt.Errorf("Proxy 不再配置外部工具代理；请在相应工具中选择代理"))
	}
	if o.tool != "" || o.endpoint != "" && action != "probe" {
		return fail(fmt.Errorf("Proxy 不接受 --tool；--endpoint 仅用于只读 probe"))
	}
	if o.home != "" {
		return fail(fmt.Errorf("Proxy 不接受 --home"))
	}
	if len(args) > 0 && (action != "endpoint" || len(args) != 1) && action != "provider-update" {
		return fail(fmt.Errorf("该 Proxy 操作不接受位置参数"))
	}
	if action == "provider-update" && !(len(args) == 0 || len(args) == 1 && args[0] == "all" || len(args) == 2 && (args[0] == "proxy" || args[0] == "rule")) {
		return fail(fmt.Errorf("provider-update 使用 all 或 proxy|rule NAME"))
	}
	if o.file != "" || o.tail != "" || o.url != "" && action != "probe" || o.check && action != "update" {
		return fail(fmt.Errorf("Proxy 选项与操作不匹配"))
	}
	if (o.artifact != "" || o.checksum != "") && action != "install" {
		return fail(fmt.Errorf("程序包选项仅用于首次安装；更新委托运行中的 core 原生接口"))
	}
	if o.check && (o.artifact != "" || o.checksum != "") {
		return fail(fmt.Errorf("版本查询不接受程序包选项"))
	}
	if o.proxy != "" {
		if !fileNetwork {
			return fail(fmt.Errorf("--proxy 仅用于首次在线安装或版本查询；原生更新走 core 自身规则"))
		}
		if _, err := m.generalProxyURL(o.proxy); err != nil {
			return fail(err)
		}
		if o.artifact != "" && o.artifact != "latest" {
			return fail(fmt.Errorf("--proxy 仅用于在线安装、更新或版本查询"))
		}
	}
	// File acceleration is independent from the general API proxy. Validate
	// explicit input without downloading or selecting a local artifact.
	if o.githubProxy != "" {
		if _, err := normalizeDownload(o.githubProxy); err != nil {
			return fail(err)
		}
	}
	if action == "install" && (o.artifact == "" || o.artifact == "latest") && (!o.dry || o.githubProxy != "" || o.proxy != "") {
		policy, err := m.networkPolicy(o.githubProxy, o.proxy)
		if err != nil {
			return fail(err)
		}
		m.Proxy.GitHubProxy = policy
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	}
	switch action {
	case "endpoints":
		endpoints, err := m.Proxy.ListenerEndpoints()
		if err != nil {
			fmt.Fprintln(c.Err, "没有可用的 listener 端点：", err)
			endpoints = []proxy.Endpoint{}
		}
		return c.output(o, endpoints)
	case "status":
		return c.output(o, m.Proxy.Status())
	case "groups":
		if o.dry {
			return c.output(o, map[string]string{"detail": "仅读取本地状态；预览未请求 core 状态或测速"})
		}
		overview, err := m.Proxy.OverviewContext(c.proxyTaskContext())
		if err != nil {
			return fail(err)
		}
		return c.output(o, overview)
	case "providers":
		if o.dry {
			return c.output(o, map[string]string{"detail": "预览未请求或刷新 provider"})
		}
		providers, err := m.Proxy.ProvidersContext(c.proxyTaskContext())
		if err != nil {
			return fail(err)
		}
		return c.output(o, providers)
	case "provider-update":
		return c.proxyProviderUpdate(m, o, args)
	case "config-path":
		path, err := m.Proxy.ConfigPath()
		if err != nil {
			return fail(err)
		}
		if o.json {
			return c.output(o, map[string]string{"path": path})
		}
		fmt.Fprintln(c.Out, path)
		return 0
	case "endpoint":
		endpoint, err := m.Proxy.Endpoint(name)
		if err != nil {
			return fail(err)
		}
		if endpoint.Authentication {
			return fail(fmt.Errorf("该入口要求认证；未导出带凭据地址"))
		}
		if o.json {
			return c.output(o, endpoint)
		}
		fmt.Fprintln(c.Out, endpoint.URL())
		return 0
	case "probe":
		if o.url == "" {
			return fail(fmt.Errorf("probe 需要 --url 指定 HTTP 或 HTTPS 目标"))
		}
		if o.dry {
			if _, err := m.Proxy.CheckProbe(o.endpoint, o.url); err != nil {
				return fail(err)
			}
			return c.output(o, map[string]string{"operation": "probe", "detail": "仅检查目标与本地端点；未联网"})
		}
		result, err := m.Proxy.Probe(o.endpoint, o.url)
		if err != nil {
			return fail(err)
		}
		return c.output(o, result)
	case "configure", "quickstart":
		return c.proxyConfigure(m, o)
	case "validate", "install", "update", "reload", "start", "stop", "restart", "remove":
	default:
		return fail(fmt.Errorf("未知 Proxy 操作"))
	}
	if o.dry {
		if action == "update" && o.check {
			if _, err := m.Proxy.ConfigPath(); err != nil {
				return fail(err)
			}
			return c.output(o, map[string]string{"component": "proxy", "operation": "check-update", "detail": "未调用核心或联网；当前与远端版本未知"})
		}
		if err := m.Proxy.Preflight(action, o.artifact, o.checksum); err != nil {
			return fail(err)
		}
		return c.output(o, map[string]string{"component": "proxy", "operation": action, "detail": "静态预检通过；核心验证和远端版本尚未查询，未写入或启动"})
	}
	if action == "install" && (o.artifact == "" || o.artifact == "latest") {
		var err error
		m.Proxy.HTTP, err = m.networkHTTP(m.Proxy.HTTP, o.githubProxy, o.proxy)
		if err != nil {
			return fail(err)
		}
	}
	if action == "update" && o.check {
		var err error
		m.Proxy.APIHTTP, err = m.networkAPIClient("", o.proxy)
		if err != nil {
			return fail(err)
		}
		info, err := m.Proxy.CheckUpdateContext(c.proxyTaskContext())
		if err != nil {
			return fail(err)
		}
		return c.output(o, info)
	}
	if m.configBytes == nil {
		return fail(fmt.Errorf("尚未初始化，请先运行 nido init"))
	}
	if action == "install" {
		if err := m.Proxy.Preflight(action, o.artifact, o.checksum); err != nil {
			return fail(err)
		}
	}
	if action != "validate" {
		if action == "update" && !o.json {
			fmt.Fprintln(c.Err, "由运行中的 core 使用自身规则下载稳定版，管理 meta-update/meta-backup 并重新执行；可能中断连接。Nido 下载模式不会传入此操作。")
		}
		if action == "reload" && !o.json {
			fmt.Fprintln(c.Err, "重载固定 config.yaml，可能重建监听和影响连接；不自动重启或恢复配置。")
		}
		confirmation := action + " Proxy"
		if action == "install" {
			confirmation = "安装 Proxy"
		} else if action == "remove" {
			confirmation = "停止并删除 Proxy，保留配置和数据"
		}
		if err := c.confirm(o, confirmation); err != nil {
			return fail(err)
		}
	}
	err := m.mutate(false, func() error {
		switch action {
		case "install":
			return m.Proxy.InstallContext(c.proxyTaskContext(), o.artifact, o.checksum)
		case "validate":
			return m.Proxy.Validate()
		case "update":
			return m.Proxy.UpgradeContext(c.proxyTaskContext())
		case "reload":
			return m.Proxy.ReloadContext(c.proxyTaskContext())
		case "start":
			return m.Proxy.Start()
		case "stop":
			return m.Proxy.Stop()
		case "restart":
			return m.Proxy.Restart()
		case "remove":
			return m.Proxy.Remove()
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	if action == "update" {
		return c.output(o, map[string]string{"component": "proxy", "operation": action, "detail": "core 原生更新及受管实例观测完成；下载使用 core 自身规则，不继承 Nido 下载模式。"})
	}
	if action == "reload" {
		return c.output(o, map[string]string{"component": "proxy", "operation": action, "detail": "固定主配置已由 core 重载，已复核受管实例及代理监听；不代表所有 provider 或网站均可用。"})
	}
	if action == "install" && c.interactive(o) {
		if err := c.proxyFirstConfiguration(m, o); err != nil {
			return fail(fmt.Errorf("Mihomo 已安装，配置未完成；可运行 proxy configure 重试：%w", err))
		}
	}
	return c.output(o, map[string]string{"component": "proxy", "operation": action, "detail": "操作完成；配置、安装和更新不自动启动，卸载保留配置与数据。"})
}
