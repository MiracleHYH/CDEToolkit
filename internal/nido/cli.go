package nido

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/components/gds"
	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
)

const help = `Nido: Lightweight Development Environment Manager

nido [--root DIR] COMMAND
  init                         初始化单目录工具，不安装或启动组件
  update [--check]             更新 Nido 自身，仅替换受管程序
  download show / set VALUE    查看或设置 GitHub 下载加速策略
  status / doctor              只读检查外部集成与内置组件
  env [bash|zsh]               输出 PATH 和已关联工具的环境
  codex setup [--home DIR]      发现并关联已有外部目录，不移动数据
  codex status                 只读查看关联、命令及代理状态
  codex install --home DIR      在外部目录调用官方安装器
  codex update [--check]        委托官方更新；--check 只查版本
  codex proxy URL|builtin|off   配置 .env 中属于 Nido 的代理块
  codex detach                 解除关联，保留全部 Codex 程序和数据
  conda status / show / mirrors 查看 conda/pip 配置、激活状态及镜像预设
  conda setup [conda|pip|all]   首次记录本机镜像为持久 default
  conda mirror conda|pip VALUE default / official / tuna / off
  conda cache conda|pip DIR|off 原生配置中直接设置包缓存路径
  conda envs DIR|off            原生配置中直接设置环境目录
  conda validate / reset       核对配置 / 释放管理，保留原镜像和路径
  proxy install / update       安装或更新内置 Mihomo，不自动启动
  proxy configure [--file FILE] 配置原生 YAML，替换前要求停止
  proxy endpoints              只枚举 listeners，可任意命名
  proxy bind [ai|daily|download] 为类别选择 listener 或自定义代理
  proxy bind --tool codex|git   只配置一个工具
  proxy validate / config-path 核心验证 / 查询配置路径
  proxy start / stop / restart 只控制本实例并确认监听就绪
  proxy status / endpoint [NAME] / logs [--tail N]
  proxy update --check         查询当前与最新核心版本
  proxy probe --url URL [--endpoint NAME] / remove
                               显式测试目标 / 卸载保留配置数据
  gds install / update [gds-vX.Y.Z]
                               官方稳定版 GDS；缺省最新，不自动启动
  gds install / update --artifact FILE [--sha256 HASH]
                               显式安装本地 Linux amd64 GDS
  gds update --check           只读查询当前与官方最新 GDS 版本
  gds configure --gpu UUID / --file FILE
  gds validate / config-path / probe
  gds start / stop / restart   单卡空闲显存保留，stop 确认 worker 退出
  gds status / logs [--tail N] / remove
  path / version               输出软件目录 / CLI 版本

选项：
  --root DIR                   Nido 软件目录（NIDO_HOME，默认 ~/.nido）
  --home DIR                   外部 CODEX_HOME，不得位于 Nido 目录内
  --proxy URL                  仅为本次官方安装、更新或版本查询指定代理
  --github-proxy VALUE         auto、direct 或 HTTPS 加速前缀；不用于 Codex
  --version X.Y.Z              Nido 自身更新到指定稳定版本
  --artifact FILE --sha256 HASH 内置组件程序来源与校验
  --gpu UUID                   GDS 首次配置的完整 GPU UUID
  --tool NAME --endpoint NAME   快捷代理工具 / bind 或 probe 的端点名称
  --file FILE / --url URL / --tail N
                               组件配置 / Proxy 探测目标 / 日志行数
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
	Runner      codex.Runner
	ProxyRunner proxy.Runner
	GDSRunner   gds.Runner
	CondaRunner conda.Runner
	// GDSHTTP is injected only by Go isolation tests, never a CLI flag.
	GDSHTTP *http.Client
	// UpdateHTTP is an isolated-test seam, not a configurable API endpoint.
	UpdateHTTP *http.Client
}

type cliOptions struct {
	root, home, proxy, artifact, checksum string
	file, url, tail, gpu                  string
	tool, endpoint                        string
	githubProxy, version                  string
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
		case "--root", "--home", "--proxy", "--artifact", "--sha256", "--file", "--url", "--tail", "--gpu", "--tool", "--endpoint", "--github-proxy", "--version":
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
			case "--file":
				o.file = v
			case "--url":
				o.url = v
			case "--tail":
				o.tail = v
			case "--gpu":
				o.gpu = v
			case "--tool":
				o.tool = v
			case "--endpoint":
				o.endpoint = v
			case "--github-proxy":
				o.githubProxy = v
			case "--version":
				o.version = v
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
		} else if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
			c.reader = bufio.NewReader(os.Stdin)
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
		if a == "--root" || a == "--home" || a == "--proxy" || a == "--artifact" || a == "--sha256" || a == "--file" || a == "--url" || a == "--tail" || a == "--gpu" || a == "--tool" || a == "--endpoint" || a == "--github-proxy" || a == "--version" {
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
	component := command == "codex" || command == "proxy" || command == "gds"
	if o.githubProxy != "" && command != "init" && command != "update" && command != "proxy" && command != "gds" {
		return c.fail(fmt.Errorf("--github-proxy 仅用于 Nido 或内置组件下载，不用于 Codex"))
	}
	if o.version != "" && command != "update" {
		return c.fail(fmt.Errorf("--version X.Y.Z 仅用于 Nido 自身更新"))
	}
	if command != "proxy" && (o.tool != "" || o.endpoint != "") {
		return c.fail(fmt.Errorf("--tool 和 --endpoint 仅用于 proxy bind"))
	}
	if command != "proxy" && command != "gds" && (o.file != "" || o.url != "" || o.tail != "") {
		return c.fail(fmt.Errorf("--file、--url、--tail 仅用于对应组件操作"))
	}
	if o.gpu != "" && command != "gds" {
		return c.fail(fmt.Errorf("--gpu 仅用于 GDS 配置"))
	}
	if !component && (o.home != "" || o.artifact != "" || o.checksum != "" || command != "update" && (o.proxy != "" || o.check)) {
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
	if c.ProxyRunner != nil {
		m.Proxy.Runner = c.ProxyRunner
	}
	if c.GDSRunner != nil {
		m.GDS.Runner = c.GDSRunner
	}
	if c.CondaRunner != nil {
		m.Conda.Runner = c.CondaRunner
	}
	if c.GDSHTTP != nil {
		m.GDS.HTTP = c.GDSHTTP
	}
	switch command {
	case "update":
		return c.update(m, o, words)
	case "download":
		return c.download(m, o, words)
	case "path":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("path 不接受参数"))
		}
		fmt.Fprintln(c.Out, m.Root)
	case "init":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("init 不接受组件参数"))
		}
		if _, err := m.githubPolicy(o.githubProxy); err != nil {
			return c.fail(err)
		}
		if o.dry {
			return c.output(o, map[string]string{"operation": "init", "root": m.Root})
		}
		if e = m.InitWithDownload(o.githubProxy); e != nil {
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
			if _, err := m.githubPolicy(""); err != nil {
				issues = append(issues, "GitHub 下载配置无效；使用 download set 修正或 --github-proxy 覆盖")
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
	case "conda":
		return c.conda(m, o, words)
	case "proxy":
		return c.proxy(m, o, words)
	case "gds":
		return c.gds(m, o, words)
	default:
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
			if len(candidates) > 0 {
				home = candidates[0].Home
			} else {
				if o.yes {
					return c.fail(fmt.Errorf("未发现可用 Codex 目录，请用 --home 指定已有目录；新装请使用 codex install"))
				}
				var e error
				home, e = c.ask("未发现已有 CODEX_HOME，请输入自定义目录；没有则输入 install，跳过输入 skip", "skip")
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
		var builtinEndpoint *proxy.Endpoint
		if endpoint == "builtin" {
			var choice proxyChoice
			var e error
			listeners, readErr := m.Proxy.ListenerEndpoints()
			if readErr == nil && len(listeners) == 1 && (o.yes || o.json || o.dry) {
				choice, e = c.listenerChoice(listeners[0], true)
			} else {
				choice, e = c.chooseProxy(m, o, "Codex")
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
	default:
		return c.fail(fmt.Errorf("未知 Codex 操作: %s", action))
	}
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
		for _, k := range []string{"component", "operation", "root", "home", "environment", "installer", "version", "current", "latest", "saved", "effective", "github_proxy", "detail"} {
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
