package nido

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
)

func (c *CLI) codex(m *Manager, o cliOptions, args []string) int {
	previousContext := m.Codex.Ctx
	if c.uiSession != nil {
		m.Codex.Ctx = c.uiSession.Context()
	}
	defer func() { m.Codex.Ctx = previousContext }()
	if len(args) == 0 {
		if o.home != "" || o.proxy != "" || o.artifact != "" || o.checksum != "" || o.check {
			return c.fail(fmt.Errorf("Codex 状态不接受修改选项"))
		}
		if c.interactive(o) {
			return c.codexPage(m, o)
		}
		return c.output(o, m.Codex.Status(m.Config.Codex))
	}
	action := args[0]
	rest := args[1:]
	if o.artifact != "" || o.checksum != "" {
		return c.fail(fmt.Errorf("Codex 只使用官方安装更新，不接受程序包选项"))
	}
	if len(rest) != 0 && action != "proxy" {
		return c.fail(fmt.Errorf("%s 不接受位置参数", action))
	}
	if o.home != "" && action != "setup" && action != "install" {
		return c.fail(fmt.Errorf("更换关联目录请使用 codex setup --home"))
	}
	if o.proxy != "" && action != "install" && action != "update" {
		return c.fail(fmt.Errorf("--proxy 仅用于本次安装或更新；持久配置使用 codex proxy"))
	}
	if o.proxy != "" {
		if _, e := m.generalProxyURL(o.proxy); e != nil {
			return c.fail(e)
		}
	}
	if o.check && action != "update" {
		return c.fail(fmt.Errorf("--check 仅用于 codex update"))
	}
	if (action == "setup" || action == "install" || action == "proxy" || action == "detach" || action == "update" && !o.check) && !o.dry && m.configBytes == nil {
		return c.fail(fmt.Errorf("尚未初始化，请先运行 nido init"))
	}
	switch action {
	case "status":
		return c.output(o, m.Codex.Status(m.Config.Codex))
	case "setup":
		home := o.home
		registeredDefault := home == "" && m.Config.Codex.Home != ""
		discovered, source := false, "默认目录"
		if registeredDefault {
			home = m.Config.Codex.Home
		}
		if home == "" {
			candidates, e := m.Codex.Discover()
			if e != nil {
				return c.fail(e)
			}
			if len(candidates) > 0 {
				home = candidates[0].Home
				discovered = true
				if candidates[0].Source == "CODEX_HOME" {
					source = "CODEX_HOME 环境变量"
				}
			} else {
				if o.yes {
					return c.fail(fmt.Errorf("未发现可用 Codex 目录，请用 --home 指定已有目录；新装请使用 codex install"))
				}
				if !c.interactive(o) {
					return c.fail(fmt.Errorf("未发现可用 Codex 目录，请用 --home 指定已有目录；新装请使用 codex install --home DIR"))
				}
				var e error
				choice, e := c.choose("未发现已有 CODEX_HOME", []string{"跳过，保持不变", "指定已有自定义目录", "在新目录调用官方安装器"}, 0)
				if e != nil {
					return c.fail(e)
				}
				if choice == 0 {
					return 0
				}
				if choice == 2 {
					home, e = c.setting("新 Codex HOME", filepath.Join(m.UserHome, ".codex"))
					if e != nil {
						return c.fail(e)
					}
					o.home = home
					return c.codex(m, o, []string{"install"})
				}
				home, e = c.setting("已有自定义目录", "")
				if e != nil {
					return c.fail(e)
				}
			}
		}
		if discovered && c.interactive(o) {
			choice, err := c.choose("发现 "+source+"："+home, []string{"使用此目录，保持目录原位", "指定其他已有目录", "跳过，保持不变"}, 0)
			if err != nil {
				return c.fail(err)
			}
			if choice == 2 {
				return 0
			}
			if choice == 1 {
				home, err = c.setting("已有自定义目录", "")
				if err != nil {
					return c.fail(err)
				}
			}
		}
		home = expand(home, m.UserHome)
		next, e := m.Codex.Associate(home)
		if e != nil {
			return c.fail(e)
		}
		if m.Config.Codex.Home != "" && m.Config.Codex.Home != next.Home {
			return c.fail(fmt.Errorf("已有其他 Codex 关联，请先 detach；不会遗留旧目录代理配置"))
		}
		// Repeating setup must retain our proxy ownership receipt.
		if m.Config.Codex.Home == next.Home {
			next.ProxyBlock = m.Config.Codex.ProxyBlock
			if registeredDefault && next.Command == "" && m.Config.Codex.Command != "" {
				// Keep an available custom entry if it is outside the current
				// PATH. Explicit refresh may clear a missing saved entry, but
				// never removes or replaces the external program itself.
				if info, err := os.Stat(m.Config.Codex.Command); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
					next.Command = m.Config.Codex.Command
					next.Channel = m.Config.Codex.Channel
				}
			}
		}
		if o.dry {
			return c.output(o, map[string]string{"operation": "associate", "home": next.Home})
		}
		if c.interactive(o) {
			if next.Command == "" {
				fmt.Fprintln(c.Err, "未发现 Codex 命令；此次只关联数据目录，可随后选择官方安装。")
			} else {
				fmt.Fprintf(c.Err, "Codex 命令：%s；安装渠道：%s\n", next.Command, next.Channel)
			}
		}
		if e = c.confirm(o, "关联外部 Codex 目录 "+next.Home); e != nil {
			return c.fail(e)
		}
		e = m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return next, nil })
		if e != nil {
			return c.fail(e)
		}
		detail := "已关联；目录未移动。重新加载 env.sh 后直接使用 codex。"
		if next.Command == "" {
			detail = "已关联数据目录；尚未发现 Codex 命令，可选择官方安装到此目录。"
		}
		return c.output(o, map[string]string{"home": next.Home, "detail": detail})
	case "install", "update":
		cfg := m.Config.Codex
		if action == "install" && cfg.Home == "" && o.home == "" && c.interactive(o) {
			var err error
			o.home, err = c.setting("Codex 数据目录（独立于 Nido）", filepath.Join(m.UserHome, ".codex"))
			if err != nil {
				return c.fail(err)
			}
		}
		if action == "install" && o.home != "" {
			h := expand(o.home, m.UserHome)
			if p, e := physical(h); e == nil {
				h = p
			}
			if cfg.Home != "" && cfg.Home != h {
				return c.fail(fmt.Errorf("已有不同关联目录，请先 detach"))
			}
			cfg.Home = h
		}
		if cfg.Home == "" {
			return c.fail(fmt.Errorf("请先关联已有目录，或 codex install --home DIR 显式选择新目录"))
		}
		if !o.check {
			checked, e := m.Codex.CheckInstall(cfg, action == "update")
			if e != nil {
				return c.fail(e)
			}
			cfg = checked
		}
		if o.dry {
			return c.output(o, map[string]string{"operation": action, "home": cfg.Home, "installer": "https://chatgpt.com/codex/install.sh"})
		}
		if o.check {
			apiHTTP, e := m.codexAPIClient(o.proxy)
			if e != nil {
				return c.fail(e)
			}
			defer apiHTTP.CloseIdleConnections()
			latest, e := m.Codex.LatestHTTP(apiHTTP)
			if e != nil {
				return c.fail(e)
			}
			return c.output(o, map[string]string{"current": m.Codex.Status(cfg).Version, "latest": latest})
		}
		// Official installer commands are Nido operations. Their temporary
		// network environment comes from Nido's Proxy setting, never from
		// Codex's independent runtime .env or an ambient shell proxy.
		proxyEnv, e := m.commandProxyEnv(nil, o.proxy)
		if e != nil {
			return c.fail(e)
		}
		proxy := make(map[string]string, len(proxyEnv))
		for _, entry := range proxyEnv {
			key, value, _ := strings.Cut(entry, "=")
			proxy[key] = value
		}
		current := ""
		if action == "update" {
			current = m.Codex.Status(cfg).Version
			if !o.json {
				fmt.Fprintln(c.Out, "当前 Codex 版本:", current)
			}
		}
		operation := "安装"
		if action == "update" {
			operation = "更新"
		}
		if e = c.confirm(o, "通过 Codex 官方脚本"+operation+"到官方最新版，CODEX_HOME："+cfg.Home); e != nil {
			return c.fail(e)
		}
		e = m.ConfigureCodex(func(codex.Config) (codex.Config, error) {
			if action == "update" {
				return m.Codex.Update(cfg, proxy)
			}
			return m.Codex.Install(cfg, proxy)
		})
		if e != nil {
			return c.fail(e)
		}
		return c.output(o, map[string]string{"current": current, "detail": "官方操作已完成；重新加载 Nido 环境后直接使用 codex。"})
	case "proxy":
		if len(rest) != 1 && !(len(rest) == 0 && c.interactive(o)) {
			return c.fail(fmt.Errorf("使用 codex proxy URL|builtin|off"))
		}
		endpoint := "builtin"
		if len(rest) == 1 {
			endpoint = rest[0]
		}
		var builtinEndpoint *proxy.Endpoint
		if endpoint == "builtin" {
			var choice proxyChoice
			var e error
			listeners, readErr := m.Proxy.ListenerEndpoints()
			if len(rest) != 0 && readErr == nil && len(listeners) == 1 && (o.yes || o.json || o.dry) {
				choice, e = c.listenerChoice(listeners[0], true)
			} else {
				choice, e = c.chooseProxy(m, o, "Codex 运行")
			}
			if e != nil {
				return c.fail(e)
			}
			if choice.keep {
				return c.output(o, map[string]string{"detail": "保持 Codex 代理配置不变"})
			}
			endpoint = choice.address
			builtinEndpoint = choice.endpoint
			if !o.dry && builtinEndpoint != nil && m.Proxy.Status().Runtime.State != "running" {
				fmt.Fprintln(c.Err, "内置 Proxy 尚未运行；仅保存端点，不启动服务。")
			}
		}
		if o.dry {
			checkEndpoint := endpoint
			if checkEndpoint == "off" {
				checkEndpoint = ""
			}
			if e := m.Codex.CheckProxy(m.Config.Codex, checkEndpoint); e != nil {
				return c.fail(e)
			}
			return c.output(o, map[string]string{"operation": "configure-proxy", "detail": "仅修改 Codex .env 中的 Nido 代理块；不启动服务"})
		}
		if e := c.confirm(o, "修改关联 Codex 的 Nido 代理配置"); e != nil {
			return c.fail(e)
		}
		if endpoint == "off" {
			endpoint = ""
		}
		var e error
		if builtinEndpoint != nil {
			e = m.configureProxy(endpoint, false, builtinEndpoint)
		} else {
			e = m.ConfigureProxy(endpoint, false)
		}
		if e != nil {
			return c.fail(e)
		}
		return c.output(o, map[string]string{"detail": "代理配置已更新；未启动或重启组件，后续启动生效。"})
	case "detach":
		if m.Config.Codex.Home == "" {
			return c.output(o, map[string]string{"detail": "Codex 尚未关联，无需解除；程序和数据未改动。"})
		}
		if o.dry {
			if m.Config.Codex.ProxyBlock != "" {
				if e := m.Codex.CheckProxy(m.Config.Codex, ""); e != nil {
					return c.fail(e)
				}
			}
			return c.output(o, map[string]string{"operation": "detach", "detail": "只移除 Nido 的关联及代理块，保留 Codex 程序和数据"})
		}
		if e := c.confirm(o, "解除 Codex 关联并保留程序与数据"); e != nil {
			return c.fail(e)
		}
		e := m.ConfigureProxy("", true)
		if e != nil {
			return c.fail(e)
		}
		return c.output(o, map[string]string{"detail": "已解除关联；Codex 程序和数据保留。当前终端仍可能保留旧 CODEX_HOME，请按需 unset CODEX_HOME。"})
	case "configure":
		return c.codexConfigure(m, o)
	default:
		return c.fail(fmt.Errorf("未知 Codex 操作: %s", action))
	}
}

func (c *CLI) codexConfigure(m *Manager, o cliOptions) int {
	if !c.interactive(o) || o.home != "" || o.proxy != "" || o.check {
		return c.fail(fmt.Errorf("codex configure 需要交互终端；脚本请使用 setup --home、install --home 或 proxy URL 并提供 --yes"))
	}
	return c.codexPage(m, o)
}
