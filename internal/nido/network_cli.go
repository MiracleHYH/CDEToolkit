package nido

import (
	"fmt"
	"strings"
)

func (c *CLI) download(m *Manager, o cliOptions, args []string) int {
	if o.check || o.endpoint != "" || o.networkMode != "" {
		return c.fail(fmt.Errorf("请用 download set none|proxy|HTTPS前缀，或 download proxy none|URL"))
	}
	if len(args) == 0 || len(args) == 1 && args[0] == "show" {
		if o.githubProxy != "" || o.proxy != "" {
			return c.fail(fmt.Errorf("查看网络设置不接受临时覆盖参数"))
		}
		next, err := m.savedNetwork()
		if err != nil {
			return c.output(o, map[string]string{"detail": err.Error(), "github_accelerate": "需要配置", "proxy": redactedNetworkProxy(m.Config.Download.ProxyURL)})
		}
		return c.output(o, map[string]string{"github_accelerate": next.GitHubAccelerate, "proxy": redactedNetworkProxy(next.ProxyURL)})
	}
	if len(args) == 1 && args[0] == "configure" {
		return c.networkConfigure(m, o)
	}
	if len(args) != 2 || args[1] == "" || (args[0] != "set" && args[0] != "proxy") {
		return c.fail(fmt.Errorf("使用 download show、download set none|proxy|HTTPS前缀 或 download proxy none|URL"))
	}
	next, err := m.savedNetwork()
	if err != nil {
		next = DownloadConfig{GitHubAccelerate: GitHubAccelerateNone, ProxyURL: m.Config.Download.ProxyURL}
	}
	if o.githubProxy != "" || o.proxy != "" {
		return c.fail(fmt.Errorf("网络设置请直接提供值，不接受 --github-proxy/--proxy 覆盖"))
	}
	if args[0] == "set" {
		chosen, e := normalizeDownload(args[1])
		if e != nil {
			return c.fail(e)
		}
		next.GitHubAccelerate = chosen.GitHubAccelerate
	} else {
		next.ProxyURL = args[1]
		switch strings.ToLower(next.ProxyURL) {
		case "none", "direct", "off":
			next.ProxyURL = ""
		}
	}
	if err := ValidateNetwork(next); err != nil {
		return c.fail(err)
	}
	if o.dry {
		return c.output(o, map[string]string{"operation": "network-set", "github_accelerate": next.GitHubAccelerate, "proxy": redactedNetworkProxy(next.ProxyURL)})
	}
	if err := m.setNetwork(next); err != nil {
		return c.fail(err)
	}
	return c.output(o, map[string]string{"detail": "网络设置已保存；不修改 Codex、Git 或系统代理。"})
}
