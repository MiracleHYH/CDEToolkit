package nido

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/builtin"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
)

const help = `Nido: Lightweight Development Environment Manager

nido [--root DIR] COMMAND
  init                         初始化单目录工具，不安装或启动组件
  status / doctor              只读检查外部集成与内置组件
  env [bash|zsh]               输出 PATH 和已关联的 CODEX_HOME
  codex setup [--home DIR]      发现并关联已有外部目录，不移动数据
  codex status                 只读查看关联、命令及代理状态
  codex install --home DIR      在外部目录调用官方安装器
  codex update [--check]        委托官方更新；--check 只查版本
  codex proxy URL|builtin|off   配置 .env 中属于 Nido 的代理块
  codex detach                 解除关联，保留全部 Codex 程序和数据
  proxy install / update       安装或更新内置 Mihomo，不自动启动
  proxy start / stop / remove  只控制本实例；卸载保留配置和数据
  gds install / update / remove
                               GDS 显式可选，需本地程序；Go 调度启动待适配
  path / version               输出软件目录 / CLI 版本

选项：
  --root DIR                   Nido 软件目录（NIDO_HOME，默认 ~/.nido）
  --home DIR                   外部 CODEX_HOME，不得位于 Nido 目录内
  --proxy URL                  仅为本次官方安装、更新或版本查询指定代理
  --artifact FILE --sha256 HASH 内置组件程序来源与校验
  --yes                        确认显式操作，不猜测来源或迁移数据
  --dry-run                    只读预览，不下载、不安装、不写配置
  --json                       结构化输出

Codex 使用官方命令直接运行；Nido 不包装其运行、不移动目录、不管理 daemon。
配置环境后，新终端或重新加载 env.sh 生效。没有目录迁移或挂载修复命令。
`

type CLI struct {
	In        io.Reader
	Out, Err  io.Writer
	reader    *bufio.Reader
	promptOut io.Writer
	tty       *os.File
	// Runner is injected only by Go isolation tests, never a CLI flag.
	Runner codex.Runner
}

type cliOptions struct {
	root, home, proxy, artifact, checksum string
	yes, dry, json, check                 bool
}

func parse(args []string) (cliOptions, []string, error) {
	var o cliOptions
	var words []string
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--yes":
			o.yes = true
		case "--dry-run":
			o.dry = true
		case "--json":
			o.json = true
		case "--check":
			o.check = true
		case "--root", "--home", "--proxy", "--artifact", "--sha256":
			if seen[a] {
				return o, nil, fmt.Errorf("重复选项: %s", a)
			}
			seen[a] = true
			if i+1 == len(args) {
				return o, nil, fmt.Errorf("%s 缺少值", a)
			}
			i++
			v := args[i]
			if v == "" || strings.HasPrefix(v, "--") {
				return o, nil, fmt.Errorf("%s 不能为空", a)
			}
			switch a {
			case "--root":
				o.root = v
			case "--home":
				o.home = v
			case "--proxy":
				o.proxy = v
			case "--artifact":
				o.artifact = v
			case "--sha256":
				o.checksum = v
			}
		default:
			if strings.HasPrefix(a, "-") {
				return o, nil, fmt.Errorf("未知选项 %s；Nido 仅使用 --root，外部 Codex 使用 --home", a)
			}
			words = append(words, a)
		}
	}
	return o, words, nil
}

func (c *CLI) ask(label, fallback string) (string, error) {
	if c.reader == nil {
		if c.In != nil {
			c.reader = bufio.NewReader(c.In)
			c.promptOut = c.Err
		} else {
			f, e := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if e != nil {
				return "", fmt.Errorf("需要交互终端；请提供明确目录及 --yes")
			}
			c.tty = f
			c.reader = bufio.NewReader(f)
			c.promptOut = f
		}
	}
	fmt.Fprintf(c.promptOut, "%s [%s]: ", label, fallback)
	v, e := c.reader.ReadString('\n')
	if e != nil && len(v) == 0 {
		return "", fmt.Errorf("交互未完成")
	}
	v = strings.TrimSpace(v)
	if v == "" {
		v = fallback
	}
	return v, nil
}

func (c *CLI) confirm(o cliOptions, what string) error {
	if o.yes {
		return nil
	}
	v, e := c.ask(what+"，是否继续？y/N", "N")
	if e != nil {
		return e
	}
	if v != "y" && v != "Y" {
		return fmt.Errorf("操作已取消")
	}
	return nil
}

func (c CLI) Run(args []string) int {
	if c.Out == nil {
		c.Out = os.Stdout
	}
	if c.Err == nil {
		c.Err = os.Stderr
	}
	defer func() {
		if c.tty != nil {
			_ = c.tty.Close()
		}
	}()
	if len(args) == 0 {
		args = []string{"status"}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--root" || a == "--home" || a == "--proxy" || a == "--artifact" || a == "--sha256" {
			i++
			continue
		}
		if a == "--help" || a == "-h" || a == "help" {
			fmt.Fprint(c.Out, help)
			return 0
		}
	}
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(c.Out, "nido "+Version)
		return 0
	}
	o, words, e := parse(args)
	if e != nil {
		return c.fail(e)
	}
	if len(words) == 0 {
		return c.fail(fmt.Errorf("缺少命令"))
	}
	command := words[0]
	words = words[1:]
	component := command == "codex" || builtin.Known(command)
	if !component && (o.home != "" || o.proxy != "" || o.artifact != "" || o.checksum != "" || o.check) {
		return c.fail(fmt.Errorf("组件选项不能用于 %s", command))
	}
	if command == "version" {
		if len(words) != 0 {
			return c.fail(fmt.Errorf("version 不接受参数"))
		}
		if o.json {
			return c.output(o, map[string]string{"version": Version})
		}
		fmt.Fprintln(c.Out, "nido "+Version)
		return 0
	}
	m, e := New(o.root)
	if e != nil {
		return c.fail(e)
	}
	if c.Runner != nil {
		m.Codex.Run = c.Runner
	}
	switch command {
	case "path":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("path 不接受参数"))
		}
		fmt.Fprintln(c.Out, m.Root)
	case "init":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("init 不接受组件参数"))
		}
		if o.dry {
			return c.output(o, map[string]string{"operation": "init", "root": m.Root})
		}
		if e = m.Init(); e != nil {
			return c.fail(e)
		}
		return c.output(o, map[string]string{"root": m.Root, "environment": filepath.Join(m.Root, "env.sh"), "detail": "已初始化；未安装或启动组件。重新加载环境：. " + shellQuote(filepath.Join(m.Root, "env.sh"))})
	case "env":
		if o.json {
			return c.fail(fmt.Errorf("env 输出可直接加载的 Shell 代码，不支持 --json"))
		}
		if len(words) > 1 || (len(words) == 1 && words[0] != "bash" && words[0] != "zsh") {
			return c.fail(fmt.Errorf("env 仅支持 bash 或 zsh"))
		}
		fmt.Fprint(c.Out, m.Environment())
	case "status", "doctor":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("%s 不接受参数", command))
		}
		rows := m.Status()
		if command == "doctor" {
			issues := []string{}
			if m.configBytes == nil {
				issues = append(issues, "Nido 尚未初始化")
			}
			if _, err := os.Lstat(filepath.Join(m.Root, "env.sh")); os.IsNotExist(err) && m.configBytes != nil {
				issues = append(issues, "env.sh 缺失；可重新运行 init 生成")
			}
			if err := m.checkEnv(); err != nil {
				issues = append(issues, err.Error())
			}
			if h := os.Getenv("CODEX_HOME"); m.Config.Codex.Home != "" && h != m.Config.Codex.Home {
				issues = append(issues, "当前终端 CODEX_HOME 与关联目录不一致；请重新加载 Nido 环境")
			}
			for _, r := range rows {
				if r.Detail != "" {
					issues = append(issues, r.Name+": "+r.Detail)
				}
			}
			if o.json {
				if code := c.output(o, struct {
					Components []Status `json:"components"`
					Issues     []string `json:"issues"`
				}{rows, issues}); code != 0 {
					return code
				}
			} else {
				for _, r := range rows {
					fmt.Fprintf(c.Out, "%s\t%s\n", r.Name, r.State)
				}
				for _, issue := range issues {
					fmt.Fprintln(c.Out, issue)
				}
			}
			if len(issues) != 0 {
				return 1
			}
			return 0
		}
		if o.json {
			return c.output(o, rows)
		}
		for _, r := range rows {
			fmt.Fprintf(c.Out, "%s\t%s\t%s", r.Name, r.Kind, r.State)
			if r.Version != "" {
				fmt.Fprintf(c.Out, "\t%s", r.Version)
			}
			if r.Directory != "" {
				fmt.Fprintf(c.Out, "\t%s", r.Directory)
			}
			if r.Detail != "" {
				fmt.Fprintf(c.Out, "\t%s", r.Detail)
			}
			fmt.Fprintln(c.Out)
		}
	case "codex":
		return c.codex(m, o, words)
	default:
		if builtin.Known(command) {
			return c.builtin(m, o, command, words)
		}
		return c.fail(fmt.Errorf("未知命令: %s；使用 nido --help", command))
	}
	return 0
}

func (c *CLI) codex(m *Manager, o cliOptions, args []string) int {
	if len(args) == 0 {
		if o.home != "" || o.proxy != "" || o.artifact != "" || o.checksum != "" || o.check {
			return c.fail(fmt.Errorf("Codex 状态不接受修改选项"))
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
		if e := codex.ValidateProxyURL(o.proxy); e != nil {
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
		if registeredDefault {
			home = m.Config.Codex.Home
		}
		if home == "" {
			candidates, e := m.Codex.Discover()
			if e != nil {
				return c.fail(e)
			}
			if len(candidates) == 1 {
				home = candidates[0].Home
			} else {
				if o.yes {
					return c.fail(fmt.Errorf("未找到唯一 Codex 目录，请用 --home 指定已有目录；新装请使用 codex install"))
				}
				for _, p := range candidates {
					fmt.Fprintf(c.Err, "候选: %s (%s)\n", p.Home, p.Source)
				}
				var e error
				home, e = c.ask("已有 CODEX_HOME（不搬目录）；没有则输入 install，跳过输入 skip", "skip")
				if e != nil {
					return c.fail(e)
				}
				if home == "skip" {
					return 0
				}
				if home == "install" {
					home, e = c.ask("新 Codex HOME", filepath.Join(m.UserHome, ".codex"))
					if e != nil {
						return c.fail(e)
					}
					o.home = home
					return c.codex(m, o, []string{"install"})
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
			if registeredDefault && m.Config.Codex.Command != "" {
				next.Command = m.Config.Codex.Command
				next.Channel = m.Config.Codex.Channel
			}
		}
		if o.dry {
			return c.output(o, map[string]string{"operation": "associate", "home": next.Home})
		}
		if e = c.confirm(o, "关联外部 Codex 目录 "+next.Home); e != nil {
			return c.fail(e)
		}
		e = m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return next, nil })
		if e != nil {
			return c.fail(e)
		}
		return c.output(o, map[string]string{"home": next.Home, "detail": "已关联；目录未移动。重新加载 env.sh 后直接使用 codex。"})
	case "install", "update":
		cfg := m.Config.Codex
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
		proxy, e := m.Codex.ProxyEnv(cfg)
		if e != nil {
			return c.fail(e)
		}
		if o.proxy != "" {
			if proxy == nil {
				proxy = map[string]string{}
			}
			for _, key := range []string{"http_proxy", "https_proxy", "all_proxy"} {
				proxy[key] = o.proxy
			}
		}
		if o.check {
			latest, e := m.Codex.Latest(proxy)
			if e != nil {
				return c.fail(e)
			}
			return c.output(o, map[string]string{"current": m.Codex.Status(cfg).Version, "latest": latest})
		}
		current, latest := "", ""
		if action == "update" {
			current = m.Codex.Status(cfg).Version
			if !o.json {
				fmt.Fprintln(c.Out, "当前 Codex 版本:", current)
			}
			if v, e := m.Codex.Latest(proxy); e == nil {
				latest = v
				if !o.json {
					fmt.Fprintln(c.Out, "最新 Codex 版本:", latest)
				}
			} else {
				fmt.Fprintln(c.Err, "最新版本查询失败；官方更新脚本仍可在确认后执行。")
			}
		}
		if e = c.confirm(o, "调用 Codex 官方脚本执行 "+action+"，数据目录 "+cfg.Home); e != nil {
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
		return c.output(o, map[string]string{"current": current, "latest": latest, "detail": "官方操作已完成；重新加载 Nido 环境后直接使用 codex。"})
	case "proxy":
		if len(rest) != 1 {
			return c.fail(fmt.Errorf("使用 codex proxy URL|builtin|off"))
		}
		endpoint := rest[0]
		if endpoint == "builtin" {
			v, e := m.Builtins.ProxyEndpoint()
			if e != nil {
				return c.fail(e)
			}
			endpoint = v
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
		e := m.ConfigureProxy(endpoint, false)
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
	default:
		return c.fail(fmt.Errorf("未知 Codex 操作: %s", action))
	}
}

func (c *CLI) builtin(m *Manager, o cliOptions, name string, args []string) int {
	if o.home != "" || o.proxy != "" || o.check {
		return c.fail(fmt.Errorf("内置组件不接受 Codex 选项"))
	}
	if (len(args) == 0 || len(args) == 1 && args[0] == "status") && (o.artifact != "" || o.checksum != "") {
		return c.fail(fmt.Errorf("状态不接受程序包选项"))
	}
	if len(args) == 0 {
		return c.output(o, m.Builtins.Status(name))
	}
	if len(args) != 1 {
		return c.fail(fmt.Errorf("内置组件只接受一个操作"))
	}
	a := args[0]
	if a == "status" {
		return c.output(o, m.Builtins.Status(name))
	}
	if a != "install" && a != "update" && a != "start" && a != "stop" && a != "remove" {
		return c.fail(fmt.Errorf("未知内置组件操作: %s", a))
	}
	if (o.artifact != "" || o.checksum != "") && a != "install" && a != "update" {
		return c.fail(fmt.Errorf("程序包选项仅用于安装或更新"))
	}
	if name == "gds" && (a == "install" || a == "update") && o.artifact == "" {
		return c.fail(fmt.Errorf("GDS 保持可选，请显式提供 --artifact 本地程序"))
	}
	if o.dry {
		return c.output(o, map[string]string{"operation": a, "component": name, "root": m.Root})
	}
	if e := c.confirm(o, a+" 内置 "+name); e != nil {
		return c.fail(e)
	}
	e := m.mutate(false, func() error {
		switch a {
		case "install":
			return m.Builtins.Install(name, o.artifact, o.checksum)
		case "update":
			return m.Builtins.Update(name, o.artifact, o.checksum)
		case "start":
			return m.Builtins.Start(name)
		case "stop":
			return m.Builtins.Stop(name)
		case "remove":
			return m.Builtins.Remove(name)
		}
		return nil
	})
	if e != nil {
		return c.fail(e)
	}
	return c.output(o, map[string]string{"component": name, "operation": a, "detail": "已完成；remove 保留配置与数据，install/update 不自动启动"})
}

func (c *CLI) output(o cliOptions, value any) int {
	if o.json {
		if e := json.NewEncoder(c.Out).Encode(value); e != nil {
			return c.fail(e)
		}
		return 0
	}
	if values, ok := value.(map[string]string); ok {
		// Stable order makes terminal output and scripts predictable.
		for _, k := range []string{"component", "operation", "root", "home", "environment", "installer", "version", "current", "latest", "detail"} {
			if v := values[k]; v != "" {
				fmt.Fprintf(c.Out, "%s: %s\n", k, v)
			}
		}
	} else {
		b, _ := json.Marshal(value)
		fmt.Fprintln(c.Out, string(b))
	}
	return 0
}

func (c *CLI) fail(e error) int { fmt.Fprintln(c.Err, "nido:", e); return 1 }
