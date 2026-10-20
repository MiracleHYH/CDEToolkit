package nido

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
)

// Configure collects intent before taking mutation locks. The component owns
// all file snapshots and refuses to rebase that intent onto a concurrent edit.
func (c *CLI) proxyConfigure(m *Manager, o cliOptions) int {
	fail := func(err error) int { return c.proxyError(o, err) }
	if o.file != "" {
		return fail(fmt.Errorf("Proxy 不再导入 YAML；高级配置请直接编辑固定 config.yaml"))
	}
	path, err := m.Proxy.ConfigPath()
	if err != nil {
		return fail(err)
	}
	if !c.interactive(o) {
		if _, err := m.Proxy.ReadConfig(); err == nil {
			return c.output(o, map[string]string{"detail": "已有配置：" + path + "；交互运行 proxy quickstart 可重新配置，也可直接编辑后 reload 或 restart。"})
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		} else if !o.dry {
			return fail(fmt.Errorf("Quick Start 需要交互终端；高级配置请直接编辑 %s", path))
		}
	}
	if o.dry {
		if err := m.Proxy.Preflight("configure", "", ""); err != nil {
			return fail(err)
		}
		return c.output(o, map[string]string{"component": "proxy", "operation": "configure", "detail": "静态预检通过；未写入或执行核心，提交配置前仍要求停止。"})
	}
	if m.configBytes == nil {
		return fail(fmt.Errorf("尚未初始化，请先运行 nido init"))
	}
	revision, err := m.Proxy.CaptureConfigRevision()
	if err != nil {
		return fail(err)
	}
	if c.interactive(o) {
		fmt.Fprintln(c.Err, "Proxy 主配置：", path, "；高级用户可自行编辑此固定文件。")
	}
	generated, err := c.proxyQuickStartFor(m)
	if err != nil {
		return fail(err)
	}
	if err = c.proxyStopBeforeCommit(m, o, "保存 Proxy 配置"); err != nil {
		return fail(err)
	}
	err = m.mutate(false, func() error {
		return m.Proxy.ConfigureBytesIfUnchanged(generated, revision)
	})
	if err != nil {
		return fail(err)
	}
	return c.output(o, map[string]string{"component": "proxy", "operation": "configure", "detail": "操作完成；配置保持停止，仅内容变化时保存上一份原文到 config.yaml.back。"})
}

func (c *CLI) proxyFirstConfiguration(m *Manager, o cliOptions) error {
	path, err := m.Proxy.ConfigPath()
	if err != nil {
		return err
	}
	revision, err := m.Proxy.CaptureConfigRevision()
	if err != nil {
		return err
	}
	if revision.Exists() {
		fmt.Fprintln(c.Err, "保留已有 Proxy 配置：", path, "；可用 proxy configure 再次 Quick Start。")
		return nil
	}
	fmt.Fprintln(c.Err, "Proxy 主配置：", path, "；也可稍后自行编辑此固定文件。")
	choice, err := c.choose("Mihomo 已安装，尚未配置或启动", []string{"Skip（默认），稍后配置", "Quick Start，配置 provider、分流和 ISP"}, 0)
	if err != nil && c.promptClosed {
		// The optional next step never turns EOF into permission to configure.
		choice, err = 0, nil
	}
	if err != nil || choice == 0 {
		return err
	}
	generated, err := c.proxyQuickStartFor(m)
	if err != nil {
		return err
	}
	if err = c.proxyStopBeforeCommit(m, o, "保存 Proxy 配置"); err != nil {
		return err
	}
	return m.mutate(false, func() error { return m.Proxy.ConfigureBytesIfUnchanged(generated, revision) })
}

// A separate, explicit choice authorizes stopping; approving a candidate or
// passing --yes never implicitly stops or restarts the managed process.
func (c *CLI) proxyStopBeforeCommit(m *Manager, o cliOptions, operation string) error {
	status, err := m.Proxy.Runtime()
	if err == nil && status.State == "stopped" {
		return nil
	}
	if !c.interactive(o) {
		return fmt.Errorf("%s前必须停止 Proxy；请显式运行 proxy stop 后重试", operation)
	}
	choice, err := c.choose(operation+"前需要停止 Proxy；不会自动重启", []string{"取消本次提交，保持现状", "明确停止 Proxy，然后提交本次候选"}, 0)
	if err != nil {
		return err
	}
	if choice == 0 {
		return fmt.Errorf("操作已取消；未停止 Proxy，未提交候选")
	}
	if err = m.mutate(false, m.Proxy.Stop); err != nil {
		return err
	}
	fmt.Fprintln(c.Err, "Proxy 已按选择停止；本次操作完成后仍保持停止。")
	return nil
}

func (c *CLI) proxyQuickStartFor(m *Manager) ([]byte, error) {
	controller := proxy.ControllerConfig{Address: proxy.DefaultControllerAddress}
	if existing, err := m.Proxy.ReadConfig(); err == nil && existing.Controller != nil {
		controller = *existing.Controller
	}
	if controller.Secret == "" {
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return nil, fmt.Errorf("无法生成 controller secret")
		}
		controller.Secret = hex.EncodeToString(secret[:])
	}
	return c.proxyQuickStart(controller)
}

func (c *CLI) proxyQuickStart(controller proxy.ControllerConfig) ([]byte, error) {
	fmt.Fprintln(c.Err, "Proxy quick start：订阅地址和节点链接使用隐藏输入，确认摘要不展开凭据。写入前要求停止，不隐式启停。")
	provider, err := c.secretPrompt("Provider URL", "")
	if err != nil {
		return nil, err
	}
	filter, err := c.ask("日常代理 filter（.* 表示全部）", "")
	if err != nil {
		return nil, err
	}
	port, err := c.proxyPort("日常代理端口", 7890)
	if err != nil {
		return nil, err
	}
	o := proxy.QuickStartOptions{ProviderURL: provider, ProxyFilter: filter, ProxyPort: port, Controller: &controller}
	download, err := c.proxyYes("是否启用独立下载入口")
	if err != nil {
		return nil, err
	}
	if download {
		filter, err := c.ask("下载 filter", "")
		if err != nil {
			return nil, err
		}
		port, err := c.proxyPort("下载端口", 7891)
		if err != nil {
			return nil, err
		}
		o.Download = &proxy.QuickStartRoute{Filter: filter, Port: port}
	}
	isp, err := c.proxyYes("是否启用 ISP")
	if err != nil {
		return nil, err
	}
	if isp {
		node, err := c.secretPrompt("ISP 节点链接", "")
		if err != nil {
			return nil, err
		}
		port, err := c.proxyPort("ISP 端口", 7892)
		if err != nil {
			return nil, err
		}
		o.ISP = &proxy.QuickStartISP{NodeURL: node, Port: port}
		relay, err := c.proxyYes("ISP 是否需要前置代理")
		if err != nil {
			return nil, err
		}
		if relay {
			o.ISP.RelayFilter, err = c.ask("ISP 前置 filter", "")
			if err != nil || strings.TrimSpace(o.ISP.RelayFilter) == "" {
				return nil, fmt.Errorf("ISP 前置 filter 不能为空")
			}
			o.ISP.TestURL, err = c.ask("ISP 前置测试地址", proxy.DefaultTestURL)
			if err != nil {
				return nil, err
			}
		}
	}
	address, err := c.ask("回环管理地址", controller.Address)
	if err != nil {
		return nil, err
	}
	controller.Address = address
	data, err := proxy.QuickStart(o)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(c.Err, "Proxy 配置摘要：")
	fmt.Fprintf(c.Err, "  管理入口：%s；secret 保存于私有配置，不在摘要中显示。\n", controller.Address)
	fmt.Fprintf(c.Err, "  日常入口：endpoint-proxy，127.0.0.1:%d → 订阅节点\n", o.ProxyPort)
	if o.Download != nil {
		fmt.Fprintf(c.Err, "  下载入口：endpoint-download，127.0.0.1:%d → 独立订阅节点组\n", o.Download.Port)
	}
	if o.ISP != nil {
		route := "服务器 → ISP 节点 → 目标"
		if o.ISP.RelayFilter != "" {
			route = "服务器 → 订阅前置节点 → ISP 节点 → 目标"
			fmt.Fprintln(c.Err, "  ISP-Relay 使用前置测试地址选择订阅节点；该测量不代表完整 ISP 链路。")
		}
		fmt.Fprintf(c.Err, "  ISP 入口：endpoint-isp，127.0.0.1:%d；%s\n", o.ISP.Port, route)
	}
	fmt.Fprintln(c.Err, "  HTTP provider 不做周期拉取（interval=0），首次缺少有效缓存仍可能下载；健康检查保留。不自动启动。")
	updateGroup := "Proxy"
	if o.Download != nil {
		updateGroup = "Download"
	}
	fmt.Fprintf(c.Err, "  内核 GitHub HTTPS 内部请求按规则使用 %s 组；其他内部目标保留 DIRECT，需已有可用节点。\n", updateGroup)
	if err := c.confirm(cliOptions{}, "保存 Proxy 配置（保持停止）"); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *CLI) proxyPort(label string, fallback int) (int, error) {
	value, err := c.ask(label, strconv.Itoa(fallback))
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s必须是 1 至 65535 的端口", label)
	}
	return port, nil
}

func (c *CLI) proxyYes(label string) (bool, error) {
	choice, err := c.choose(label, []string{"不启用", "启用"}, 0)
	return choice == 1, err
}
