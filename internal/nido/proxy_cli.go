package nido

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
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
	action := "status"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	fail := func(err error) int { return c.proxyError(o, err) }
	if o.githubProxy != "" && (action != "install" && action != "update" || o.artifact != "" && o.artifact != "latest") {
		return fail(fmt.Errorf("--github-proxy 仅用于在线安装、更新或版本查询"))
	}
	if action == "bind" {
		return c.proxyBind(m, o, args)
	}
	if o.tool != "" || o.endpoint != "" && action != "probe" {
		return fail(fmt.Errorf("--tool 仅用于 proxy bind，--endpoint 用于 bind 或 probe"))
	}
	if o.home != "" {
		return fail(fmt.Errorf("Proxy 不接受 --home"))
	}
	if len(args) > 0 && (action != "endpoint" || len(args) != 1) {
		return fail(fmt.Errorf("该 Proxy 操作不接受位置参数"))
	}
	if o.file != "" && action != "configure" || o.url != "" && action != "probe" || o.tail != "" && action != "logs" || o.check && action != "update" {
		return fail(fmt.Errorf("Proxy 选项与操作不匹配"))
	}
	if (o.artifact != "" || o.checksum != "") && action != "install" && action != "update" {
		return fail(fmt.Errorf("程序包选项仅用于安装或更新"))
	}
	if o.check && (o.artifact != "" || o.checksum != "") {
		return fail(fmt.Errorf("版本查询不接受程序包选项"))
	}
	if o.proxy != "" {
		if action != "install" && action != "update" {
			return fail(fmt.Errorf("--proxy 仅用于程序下载和版本查询"))
		}
		if err := codex.ValidateProxyURL(o.proxy); err != nil {
			return fail(err)
		}
		u, _ := url.Parse(o.proxy)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(u)
		m.Proxy.HTTP = &http.Client{Transport: transport}
	}
	sourceChosen := false
	if (action == "install" || action == "update") && !o.check && o.artifact == "" && c.interactive(o) {
		var keep bool
		var err error
		o, keep, err = c.artifactSource("Proxy", o)
		if err != nil {
			return fail(err)
		}
		if keep {
			return c.output(o, map[string]string{"detail": "保持 Proxy 程序不变"})
		}
		sourceChosen = true
		if o.githubProxy != "" && o.artifact != "" {
			return fail(fmt.Errorf("--github-proxy 仅用于在线安装、更新或版本查询"))
		}
	}
	if (action == "install" || action == "update") && (o.artifact == "" || o.artifact == "latest") {
		policy, err := m.githubPolicy(o.githubProxy)
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
	case "logs":
		tail := 100
		if o.tail != "" {
			n, err := strconv.Atoi(o.tail)
			if err != nil {
				return fail(fmt.Errorf("--tail 需要整数"))
			}
			tail = n
		}
		if tail < 1 || tail > 10000 {
			return fail(fmt.Errorf("日志行数必须为 1 到 10000"))
		}
		if o.dry {
			return c.output(o, map[string]string{"operation": "logs", "detail": "只读查询日志；预览不读取日志内容"})
		}
		log, err := m.Proxy.Logs(tail)
		if err != nil {
			return fail(err)
		}
		if o.json {
			return c.output(o, map[string]string{"log": log})
		}
		fmt.Fprintln(c.Out, log)
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
	case "validate", "install", "update", "configure", "start", "stop", "restart", "remove":
	default:
		return fail(fmt.Errorf("未知 Proxy 操作"))
	}
	// With no source, configure is a read-only path query once a config exists.
	// Preview must use the same branch even while the component is running.
	if action == "configure" && o.file == "" {
		path, err := m.Proxy.ConfigPath()
		if err != nil {
			return fail(err)
		}
		if _, err := os.Lstat(path); err == nil {
			if c.interactive(o) {
				choice, err := c.choose("已有 Proxy 配置："+path, []string{"保持现有配置", "导入完整私有 YAML（整体替换，要求停止）"}, 0)
				if err != nil {
					return fail(err)
				}
				if choice == 0 {
					return c.output(o, map[string]string{"detail": "保持 Proxy 配置不变"})
				}
				o.file, err = c.setting("私有 YAML 文件的绝对路径", "")
				if err != nil {
					return fail(err)
				}
				if !filepath.IsAbs(o.file) {
					return fail(fmt.Errorf("配置文件必须是绝对路径"))
				}
			} else {
				if _, err = m.Proxy.ReadConfig(); err != nil {
					return fail(err)
				}
				return c.output(o, map[string]string{"detail": "已有配置：" + path + "；编辑后运行 proxy validate，再显式 restart。"})
			}
		} else if !os.IsNotExist(err) {
			return fail(err)
		}
	}
	if o.dry {
		if action == "update" && o.check {
			if _, err := m.Proxy.ConfigPath(); err != nil {
				return fail(err)
			}
			return c.output(o, map[string]string{"component": "proxy", "operation": "check-update", "detail": "未调用核心或联网；当前与远端版本未知"})
		}
		source := o.artifact
		if action == "configure" {
			source = o.file
		}
		if err := m.Proxy.Preflight(action, source, o.checksum); err != nil {
			return fail(err)
		}
		return c.output(o, map[string]string{"component": "proxy", "operation": action, "detail": "静态预检通过；核心验证和远端版本尚未查询，未写入或启动"})
	}
	if (action == "install" || action == "update") && (o.artifact == "" || o.artifact == "latest") {
		var err error
		m.Proxy.APIHTTP, err = m.downloadAPIClient(o.proxy)
		if err != nil {
			return fail(err)
		}
	}
	if action == "update" && o.check {
		info, err := m.Proxy.CheckUpdate()
		if err != nil {
			return fail(err)
		}
		return c.output(o, info)
	}
	if action == "configure" && o.file == "" {
		if !c.interactive(o) {
			return fail(fmt.Errorf("首次配置需要交互终端；非交互模式请提供 --file 私有 YAML 文件"))
		}
	}
	if m.configBytes == nil {
		return fail(fmt.Errorf("尚未初始化，请先运行 nido init"))
	}
	if sourceChosen {
		if err := m.Proxy.Preflight(action, o.artifact, o.checksum); err != nil {
			return fail(err)
		}
	}
	if action == "configure" && o.file != "" {
		if err := m.Proxy.Preflight(action, o.file, ""); err != nil {
			return fail(err)
		}
	}
	if action != "validate" && (action != "install" || sourceChosen) && !(action == "configure" && o.file == "") {
		if err := c.confirm(o, action+" 内置 Proxy"); err != nil {
			return fail(err)
		}
	}
	var generated []byte
	if action == "configure" && o.file == "" {
		var err error
		generated, err = c.proxyQuickStart()
		if err != nil {
			return fail(err)
		}
	}
	err := m.mutate(false, func() error {
		switch action {
		case "install":
			return m.Proxy.Install(o.artifact, o.checksum)
		case "update":
			return m.Proxy.Update(o.artifact, o.checksum)
		case "configure":
			if o.file != "" {
				return m.Proxy.Configure(o.file)
			}
			return m.Proxy.ConfigureInitialBytes(generated)
		case "validate":
			return m.Proxy.Validate()
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
	if action == "install" && c.interactive(o) {
		path, err := m.Proxy.ConfigPath()
		if err != nil {
			return fail(err)
		}
		if _, err = os.Lstat(path); os.IsNotExist(err) {
			generated, err := c.proxyQuickStart()
			if err != nil {
				return fail(fmt.Errorf("Mihomo 已安装，quick start 未完成；可运行 proxy configure 重试：%w", err))
			}
			if err = m.mutate(false, func() error { return m.Proxy.ConfigureInitialBytes(generated) }); err != nil {
				return fail(err)
			}
		} else if err != nil {
			return fail(err)
		}
	}
	return c.output(o, map[string]string{"component": "proxy", "operation": action, "detail": "操作完成；配置、安装和更新不自动启动，卸载保留配置与数据。"})
}
