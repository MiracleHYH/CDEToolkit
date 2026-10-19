package nido

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
)

func (c *CLI) proxyQuickStart() ([]byte, error) {
	fmt.Fprintln(c.Err, "Proxy quick start：输入正常显示，请避免共享终端；确认摘要不展开订阅和节点凭据，保存后保持停止。")
	provider, err := c.ask("Provider URL", "")
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
	o := proxy.QuickStartOptions{ProviderURL: provider, ProxyFilter: filter, ProxyPort: port}
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
		node, err := c.ask("ISP 节点链接", "")
		if err != nil {
			return nil, err
		}
		port, err := c.proxyPort("ISP 端口", 7892)
		if err != nil {
			return nil, err
		}
		testURL, err := c.ask("ISP 测试地址", proxy.DefaultTestURL)
		if err != nil {
			return nil, err
		}
		o.ISP = &proxy.QuickStartISP{NodeURL: node, Port: port, TestURL: testURL}
		relay, err := c.proxyYes("ISP 是否需要前置代理")
		if err != nil {
			return nil, err
		}
		if relay {
			o.ISP.RelayFilter, err = c.ask("ISP 前置 filter", "")
			if err != nil || strings.TrimSpace(o.ISP.RelayFilter) == "" {
				return nil, fmt.Errorf("ISP 前置 filter 不能为空")
			}
		}
	}
	data, err := proxy.QuickStart(o)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(c.Err, "Proxy 配置摘要：")
	fmt.Fprintf(c.Err, "  日常入口：endpoint-proxy，127.0.0.1:%d → 订阅节点\n", o.ProxyPort)
	if o.Download != nil {
		fmt.Fprintf(c.Err, "  下载入口：endpoint-download，127.0.0.1:%d → 独立订阅节点组\n", o.Download.Port)
	}
	if o.ISP != nil {
		route := "服务器 → ISP 节点 → 目标"
		if o.ISP.RelayFilter != "" {
			route = "服务器 → 订阅前置节点 → ISP 节点 → 目标"
		}
		fmt.Fprintf(c.Err, "  ISP 入口：endpoint-isp，127.0.0.1:%d；%s\n", o.ISP.Port, route)
	}
	fmt.Fprintln(c.Err, "  订阅由核心直连获取；配置和凭据仅保存到私有主配置，不自动启动。")
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
	value, err := c.ask(label+"？y/N", "N")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(value) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("请选择 y 或 n")
	}
}
