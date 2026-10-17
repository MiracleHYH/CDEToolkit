package nido

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

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
	name := ""
	if len(args) == 1 {
		name = args[0]
	}
	switch action {
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
			if _, err := m.Proxy.CheckProbe("", o.url); err != nil {
				return fail(err)
			}
			return c.output(o, map[string]string{"operation": "probe", "detail": "仅检查目标与本地端点；未联网"})
		}
		result, err := m.Proxy.Probe("", o.url)
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
			if _, err = m.Proxy.ReadConfig(); err != nil {
				return fail(err)
			}
			return c.output(o, map[string]string{"detail": "已有配置：" + path + "；编辑后运行 proxy validate，再显式 restart。"})
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
	if action == "update" && o.check {
		info, err := m.Proxy.CheckUpdate()
		if err != nil {
			return fail(err)
		}
		return c.output(o, info)
	}
	if action == "configure" && o.file == "" {
		if o.yes {
			return fail(fmt.Errorf("首次非交互配置需要 --file 私有 YAML 文件"))
		}
	}
	if m.configBytes == nil {
		return fail(fmt.Errorf("尚未初始化，请先运行 nido init"))
	}
	if action == "configure" && o.file != "" {
		if err := m.Proxy.Preflight(action, o.file, ""); err != nil {
			return fail(err)
		}
	}
	if action != "validate" {
		if err := c.confirm(o, action+" 内置 Proxy"); err != nil {
			return fail(err)
		}
	}
	var generated []byte
	if action == "configure" && o.file == "" {
		subscription, err := c.askSecret("Mihomo provider 订阅地址")
		if err != nil {
			return fail(err)
		}
		generated, err = proxy.Template(subscription)
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
			return m.Proxy.ConfigureBytes(generated)
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
	return c.output(o, map[string]string{"component": "proxy", "operation": action, "detail": "操作完成；配置、安装和更新不自动启动，卸载保留配置与数据。"})
}

// A test-provided reader never echoes. A real interactive terminal temporarily
// disables echo and restores its exact prior mode even on input failure.
func (c *CLI) askSecret(label string) (string, error) {
	if c.reader == nil {
		if c.In != nil {
			c.reader = bufio.NewReader(c.In)
			c.promptOut = c.Err
		} else {
			f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if err != nil {
				return "", fmt.Errorf("需要交互终端，或使用 --file 私有配置文件")
			}
			c.tty = f
			c.reader = bufio.NewReader(f)
			c.promptOut = f
		}
	}
	var interrupt chan os.Signal
	if c.tty != nil {
		interrupt = make(chan os.Signal, 1)
		signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(interrupt)
		state := exec.Command("/bin/stty", "-g")
		state.Stdin = c.tty
		mode, err := state.Output()
		if err != nil {
			return "", fmt.Errorf("无法安全读取终端模式")
		}
		defer func() {
			restore := exec.Command("/bin/stty", strings.TrimSpace(string(mode)))
			restore.Stdin = c.tty
			_ = restore.Run()
			fmt.Fprintln(c.promptOut)
		}()
		disable := exec.Command("/bin/stty", "-echo")
		disable.Stdin = c.tty
		if disable.Run() != nil {
			return "", fmt.Errorf("无法隐藏终端输入")
		}
	}
	fmt.Fprint(c.promptOut, label+"：")
	var value string
	var err error
	if c.tty != nil {
		type input struct {
			value string
			err   error
		}
		result := make(chan input, 1)
		go func() { value, err := c.reader.ReadString('\n'); result <- input{value, err} }()
		select {
		case <-interrupt:
			return "", fmt.Errorf("配置输入已取消")
		case read := <-result:
			value, err = read.value, read.err
		}
	} else {
		value, err = c.reader.ReadString('\n')
	}
	if err != nil && value == "" {
		return "", fmt.Errorf("配置输入未完成")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("配置输入不能为空")
	}
	return value, nil
}
