package nido

import (
	"fmt"

	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

func (c *CLI) githubAccelerateConfigure(m *Manager, o cliOptions) int {
	next, err := m.savedNetwork()
	if err != nil {
		next = DownloadConfig{GitHubAccelerate: GitHubAccelerateNone, ProxyURL: m.Config.Download.ProxyURL}
	}
	selected := 0
	if next.GitHubAccelerate == GitHubAccelerateProxy {
		selected = 2
	} else if next.GitHubAccelerate != GitHubAccelerateNone {
		selected = 1
	}
	choice, err := c.choose("GitHub Accelerate：仅控制 GitHub 文件下载；三条路线不叠加", []string{"None — 官方地址直连", "加速前缀 — 仅拼接 URL，直连加速站", "Proxy — 官方地址，通过下方 Proxy"}, selected)
	if err != nil {
		return c.fail(err)
	}
	switch choice {
	case 0:
		next.GitHubAccelerate = GitHubAccelerateNone
	case 1:
		fallback := githubdownload.DefaultPrefix
		if selected == 1 {
			fallback = next.GitHubAccelerate
		}
		next.GitHubAccelerate, err = c.setting("GitHub HTTPS 加速前缀", fallback)
	case 2:
		if next.ProxyURL == "" {
			return c.fail(fmt.Errorf("请先配置 Nido 的 Proxy，再选择引用 Proxy"))
		}
		next.GitHubAccelerate = GitHubAccelerateProxy
	}
	if err != nil {
		return c.fail(err)
	}
	return c.saveNetworkChoice(m, o, next, nil)
}

func (c *CLI) nidoProxyConfigure(m *Manager, o cliOptions) int {
	next, err := m.savedNetwork()
	if err != nil {
		next = DownloadConfig{GitHubAccelerate: GitHubAccelerateNone}
	}
	selected := 0
	if next.ProxyURL != "" {
		selected = 1
	}
	choice, err := c.choose("Proxy：用于 Nido API 与本次启动的外部命令；GitHub 文件仅在引用 Proxy 时使用", []string{"None — 不使用通用代理", "配置通用代理 URL"}, selected)
	if err != nil {
		return c.fail(err)
	}
	if choice == 0 {
		next.ProxyURL = ""
	} else {
		fallback := next.ProxyURL
		label := "Proxy URL（隐藏输入；留空保持）"
		if fallback == "" {
			fallback = "http://127.0.0.1:7890"
			label = "Proxy URL（默认 http://127.0.0.1:7890；隐藏输入）"
		}
		next.ProxyURL, err = c.secretPrompt(label, fallback)
		if err != nil {
			return c.fail(err)
		}
	}
	return c.saveNetworkChoice(m, o, next, nil)
}
