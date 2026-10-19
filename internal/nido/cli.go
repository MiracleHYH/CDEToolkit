package nido

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

const help = `Nido: Lightweight Development Environment Manager

nido                         打开交互管理菜单
nido [--root DIR] COMMAND     脚本与自动化入口
  init                         初始化单目录工具，不安装或启动组件
  shell setup [bash|zsh]       确认后注册真实软件路径到 Shell 启动文件
  update [--check]             更新 Nido 自身，仅替换受管程序
  download show               查看 GitHub Accelerate 和 Proxy
  download set none|proxy|URL  选择 GitHub 文件路线：直连 / 引用 Proxy / 加速前缀
  download proxy none|URL      设置 Nido 通用代理
  status / doctor              只读检查外部集成与内置组件
  env [bash|zsh]               输出 PATH 和已关联工具的环境
  codex setup [--home DIR]      发现并关联已有外部目录，不移动数据
  codex configure              交互选择目录关联、安装或代理配置
  codex status                 只读查看关联、命令及代理状态
  codex install [--home DIR]    选择外部目录，调用官方安装器
  codex update [--check]        委托官方更新；--check 只查版本
  codex proxy [URL|builtin|off] 选择或指定 .env 中的 Nido 代理
  codex detach                 解除关联，保留全部 Codex 程序和数据
  conda status / show / mirrors 查看 conda/pip 配置、激活状态及镜像预设
  conda configure              交互选择镜像、缓存或环境目录
  conda setup [conda|pip|all]   首次记录本机镜像为持久 default
  conda mirror conda|pip VALUE default / official / tuna / off
  conda cache conda|pip DIR|off 原生配置中直接设置包缓存路径
  conda envs DIR|off            原生配置中直接设置环境目录
  conda validate / reset       核对配置 / 释放管理，保留原镜像和路径
  proxy install / update       安装 Proxy / 原生更新内核
  proxy configure / quickstart 推荐配置；高级用户直接编辑 config.yaml
  proxy endpoints              只枚举 listeners，可任意命名
  proxy validate / config-path 核心验证 / 查询配置路径
  proxy start / stop / restart 只控制本实例并确认监听就绪
  proxy reload                 由受管 core 重载当前配置
  proxy groups / providers     查询选择链 / 可刷新的 Provider
  proxy provider-update all    手动刷新全部 Provider
  proxy provider-update proxy|rule NAME
                               手动刷新一个指定 Provider
  proxy status / endpoint [NAME] 查看自身状态 / 端点
  proxy update --check         查询当前与最新核心版本
  proxy probe --url URL [--endpoint NAME] / remove
                               显式测试目标 / 删除组件，保留配置数据
  path / version               输出软件目录 / CLI 版本

选项：
  --root DIR                   Nido 软件目录（NIDO_HOME，默认 ~/.nido）
  --home DIR                   外部 CODEX_HOME，不得位于 Nido 目录内
  --proxy URL                  仅为本次官方安装、更新或版本查询指定代理
  --github-accelerate VALUE    none、proxy 或 HTTPS GitHub 加速前缀
  --github-proxy VALUE         --github-accelerate 的旧名称
  --version X.Y.Z              Nido 自身更新到指定稳定版本
  --artifact FILE --sha256 HASH 内置组件程序来源与校验
  --endpoint NAME              外部工具选择代理 / Proxy 探测端点名称
  --file FILE / --url URL / --tail N
                               组件配置 / Proxy 探测目标 / 日志行数
  --yes                        确认显式操作，不猜测来源或迁移数据
  --dry-run                    只读预览，不下载、不安装、不写配置
  --json                       结构化输出

Codex 使用官方命令直接运行；Nido 不包装其运行、不移动目录、不管理 daemon。
配置环境后，新终端或重新加载 env.sh 生效。没有目录迁移或挂载修复命令。
`

type CLI struct {
	In              io.Reader
	Out, Err        io.Writer
	UI              terminalui.Driver
	uiSession       *terminalui.Session
	promptClosed    bool
	restartRequired bool
	// Runner is injected only by Go isolation tests, never a CLI flag.
	Runner      codex.Runner
	ProxyRunner proxy.Runner
	CondaRunner conda.Runner
	// UpdateHTTP is an isolated-test seam, not a configurable API endpoint.
	UpdateHTTP *http.Client
}

type cliOptions struct {
	root, home, proxy, artifact, checksum string
	file, url, tail                       string
	tool, endpoint                        string
	githubProxy, version                  string
	networkMode                           string
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
		case "--root", "--home", "--proxy", "--artifact", "--sha256", "--file", "--url", "--tail", "--tool", "--endpoint", "--github-proxy", "--github-accelerate", "--network-mode", "--version":
			if a == "--github-accelerate" {
				a = "--github-proxy"
			}
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
			case "--tool":
				o.tool = v
			case "--endpoint":
				o.endpoint = v
			case "--github-proxy":
				o.githubProxy = v
			case "--network-mode":
				o.networkMode = v
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
	return c.textPrompt(label, fallback)
}

func (c *CLI) confirm(o cliOptions, what string) error {
	if o.yes {
		return nil
	}
	if o.json || !c.interactive(o) {
		return fmt.Errorf("此操作需要交互确认；自动化请明确指定 --yes")
	}
	return c.confirmPrompt(what)
}

func (c CLI) Run(args []string) int {
	if c.Out == nil {
		c.Out = os.Stdout
	}
	if c.Err == nil {
		c.Err = os.Stderr
	}
	if len(args) == 0 {
		args = []string{"menu"}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--root" || a == "--home" || a == "--proxy" || a == "--artifact" || a == "--sha256" || a == "--file" || a == "--url" || a == "--tail" || a == "--tool" || a == "--endpoint" || a == "--github-proxy" || a == "--github-accelerate" || a == "--network-mode" || a == "--version" {
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
		words = []string{"menu"}
	}
	command := words[0]
	words = words[1:]
	if command == "bootstrap-install" {
		if !o.yes && !o.dry && !o.json {
			return c.runInteractiveTask(func() int { return c.bootstrapInstall(o, words) })
		}
		return c.bootstrapInstall(o, words)
	}
	component := command == "codex" || command == "proxy"
	if o.githubProxy != "" && command != "init" && command != "update" && command != "proxy" && command != "download" {
		return c.fail(fmt.Errorf("--github-proxy 仅用于 Nido 或内置组件下载，不用于 Codex"))
	}
	if o.networkMode != "" && command != "init" {
		return c.fail(fmt.Errorf("--network-mode 仅用于初始化；网络设置使用 download set MODE"))
	}
	if o.version != "" && command != "update" {
		return c.fail(fmt.Errorf("--version X.Y.Z 仅用于 Nido 自身更新"))
	}
	if o.tool != "" {
		return c.fail(fmt.Errorf("Proxy 工具代理管理已移除；请在对应工具或 Nido 中配置代理"))
	}
	if o.endpoint != "" && command != "proxy" && !(command == "codex" && len(words) > 0 && words[0] == "proxy") {
		return c.fail(fmt.Errorf("--endpoint 仅用于 Codex 运行代理或 Proxy 自身端点操作"))
	}
	if command != "proxy" && (o.file != "" || o.url != "" || o.tail != "") {
		return c.fail(fmt.Errorf("--file、--url、--tail 仅用于对应组件操作"))
	}
	if !component && (o.home != "" || o.artifact != "" || o.checksum != "" || o.check && command != "update" || o.proxy != "" && command != "update" && command != "download" && command != "init") {
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
	if c.CondaRunner != nil {
		m.Conda.Runner = c.CondaRunner
	}
	dispatch := func() int {
		switch command {
		case "menu":
			return c.menu(m, o, words)
		case "update":
			return c.update(m, o, words)
		case "download":
			return c.download(m, o, words)
		case "shell":
			return c.shell(m, o, words)
		case "path":
			if len(words) != 0 {
				return c.fail(fmt.Errorf("path 不接受参数"))
			}
			fmt.Fprintln(c.Out, m.Root)
		case "init":
			if len(words) != 0 {
				return c.fail(fmt.Errorf("init 不接受组件参数"))
			}
			network, explicit, err := networkOptions(o)
			if err != nil {
				return c.fail(err)
			}
			if o.dry {
				return c.output(o, map[string]string{"operation": "init", "root": m.Root})
			}
			if explicit {
				e = m.InitWithNetwork(network)
			} else {
				e = m.Init()
			}
			if e != nil {
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
				if _, err := m.githubPolicy(""); err != nil && !errors.Is(err, ErrNetworkChoice) {
					issues = append(issues, "GitHub 下载配置无效；使用 download set 修正或 --github-proxy 覆盖")
				}
				if _, err := m.downloadAPIClient(""); err != nil && !errors.Is(err, ErrNetworkChoice) {
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
		case "conda":
			return c.conda(m, o, words)
		case "proxy":
			return c.proxy(m, o, words)
		default:
			return c.fail(fmt.Errorf("未知命令: %s；使用 nido --help", command))
		}
		return 0
	}
	if c.interactive(o) && command != "menu" && command != "status" && command != "doctor" && command != "env" && command != "path" {
		return c.runInteractiveTask(dispatch)
	}
	return dispatch()
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
		for _, k := range []string{"component", "operation", "root", "home", "shell", "profile", "environment", "activate", "installer", "version", "current", "latest", "saved", "effective", "mode", "network", "listener", "proxy", "github_accelerate", "github_proxy", "detail"} {
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
