package stk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const help = `CDEToolkit — Cloud Development Environment Toolkit

cdet [--home DIR] COMMAND [选项]
  init                   只初始化管理工具，不接管或安装组件
  path                   输出管理程序路径
  status / scan          只读状态 / 候选发现
  setup proxy codex      接管已有组件；gds 必须明确选择
  install proxy codex    优先接管；缺失时从官方稳定版安装
  update [NAME...]       更新工具所有的程序（默认 proxy/codex）
  uninstall NAME...      删除受管文件；保留外部程序及所有用户数据
  uninstall cdet         移除 Go 管理入口、Shell 块和受管程序
  repair                 重建受管入口、环境、链接与服务模板
  repair --recover       恢复中断的文件操作
  rollback               回滚最近一次更新；文件或位置漂移时停止
  env bash               输出 Shell 环境（不修改 Shell 文件）
  exec codex -- ARGS     使用已登记程序与作用域环境
  start|stop|restart NAME 操作明确登记的已有服务
  logs NAME              查询登记服务的日志
  doctor                 本地路径诊断（不联网、不自动修复）

选项（可放在模块名后）：
  --home DIR             管理目录，或使用 CDET_HOME（兼容 STK_HOME）
  --entry FILE           init 或管理程序更新时登记 cdet 命令链接
  --root NAME=DIR        local/projects/data 根目录映射
  --search DIR           有限候选搜索；不会递归扫描数据目录
  --binary NAME=FILE     明确选择真实组件（不重复安装）
  --codex-home DIR       已有 Codex 主目录
  --proxy-config FILE    已有代理配置（原位使用）
  --proxy-data DIR       已有代理运行/缓存目录（原位使用）
  --endpoint NAME=URL    每个应用的 loopback HTTP 代理
  --install NAME=SOURCE  latest、X.Y.Z、本地文件或 HTTPS artifact
  --sha256 NAME=HASH     自定义 HTTPS 必需；官方包自动校验
  --takeover             update 改用 STK 副本，保留外部原安装
  --manage-config        明确接管已选配置的内部路径字段
  --shell-file FILE      明确登记 Shell 文件（受管块位于交互判断前）
  --exec-check           doctor 调用已选程序的版本命令（不联网）
  --service NAME=supervisor:PROGRAM 或 NAME=systemd:UNIT
  --controller FILE      已有服务控制器的绝对路径
  --service-config FILE  supervisorctl 使用的现有主配置
  --service-file FILE    显式接管服务定义内的路径（不加载服务）
  --system               systemd 系统级服务（默认用户级）
  --dry-run / --json     只预览 / 结构化输出

文件操作均不注册、停止、加载或启动服务，不改认证、会话或数据库。
在线 --dry-run 只读取发布元数据，不下载程序或写文件。
update cdet 需明确 --install cdet=FILE/HTTPS（HTTPS 还需摘要）。
`

type CLI struct{ Out, Err io.Writer }

// Version is set from VERSION by make build. Direct go builds stay identifiable.
var Version = "dev"

func assignment(value string) (string, string, error) {
	name, path, ok := strings.Cut(value, "=")
	if !ok || !validName(name) || path == "" {
		return "", "", fmt.Errorf("需要 NAME=VALUE")
	}
	return name, path, nil
}

func parse(args []string) (Options, []string, bool, bool, error) {
	opts := Options{Roots: map[string]string{}, Binaries: map[string]string{}, Artifacts: map[string]string{}, Digests: map[string]string{}, Endpoints: map[string]string{}, Services: map[string]Service{}}
	var words []string
	jsonMode, recoverMode := false, false
	controller, serviceConfig, serviceFile := "", "", ""
	system := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			words = append(words, args[i:]...)
			break
		}
		switch a {
		case "--dry-run":
			opts.DryRun = true
			continue
		case "--json":
			jsonMode = true
			continue
		case "--recover":
			recoverMode = true
			continue
		case "--manage-config":
			opts.ManageConfig = true
			continue
		case "--system":
			system = true
			continue
		case "--exec-check":
			opts.ExecCheck = true
			continue
		case "--takeover":
			opts.Takeover = true
			continue
		}
		if !strings.HasPrefix(a, "--") {
			words = append(words, a)
			continue
		}
		if i+1 >= len(args) {
			return opts, nil, false, false, fmt.Errorf("%s 缺少值", a)
		}
		i++
		value := args[i]
		switch a {
		case "--home":
			opts.Home = value
		case "--entry":
			opts.Entry = value
		case "--codex-home":
			opts.CodexHome = value
		case "--proxy-config":
			opts.ProxyConfig = value
		case "--proxy-data":
			opts.ProxyData = value
		case "--search":
			opts.Search = append(opts.Search, value)
		case "--shell-file":
			opts.ShellFiles = append(opts.ShellFiles, value)
		case "--controller":
			controller = value
		case "--service-config":
			serviceConfig = value
		case "--service-file":
			serviceFile = value
		case "--root", "--binary", "--install", "--sha256", "--endpoint", "--service":
			name, v, e := assignment(value)
			if e != nil {
				return opts, nil, false, false, e
			}
			if (a == "--install" || a == "--sha256") && name == "cdet" {
				name = "stk"
			}
			if a != "--root" && !knownModule(name) && !((a == "--install" || a == "--sha256") && name == "stk") {
				return opts, nil, false, false, fmt.Errorf("未知模块: %s", name)
			}
			switch a {
			case "--root":
				opts.Roots[name] = v
			case "--binary":
				opts.Binaries[name] = v
			case "--install":
				opts.Artifacts[name] = v
			case "--sha256":
				opts.Digests[name] = v
			case "--endpoint":
				opts.Endpoints[name] = v
			case "--service":
				backend, n, ok := strings.Cut(v, ":")
				if !ok {
					return opts, nil, false, false, fmt.Errorf("服务需要 BACKEND:NAME")
				}
				opts.Services[name] = Service{Backend: backend, Name: n}
			}
		default:
			return opts, nil, false, false, fmt.Errorf("未知选项: %s", a)
		}
	}
	if serviceFile != "" && len(opts.Services) != 1 {
		return opts, nil, false, false, fmt.Errorf("--service-file 必须对应一个明确服务")
	}
	for name, s := range opts.Services {
		s.Controller = Ref{Path: controller}
		s.Config = Ref{Path: serviceConfig}
		s.Definition = Ref{Path: serviceFile}
		s.System = system
		opts.Services[name] = s
	}
	for _, path := range []string{opts.Entry, opts.CodexHome, opts.ProxyConfig, opts.ProxyData, controller, serviceConfig, serviceFile} {
		if path != "" {
			if e := absolute(path); e != nil {
				return opts, nil, false, false, e
			}
		}
	}
	if opts.Home == "" {
		opts.Home = os.Getenv("CDET_HOME")
	}
	if opts.Home == "" {
		opts.Home = os.Getenv("STK_HOME")
	}
	if opts.Home == "" {
		if root := opts.Roots["local"]; root != "" {
			opts.Home = filepath.Join(root, "cdetoolkit")
		} else {
			exe, _ := os.Executable()
			if physical, err := filepath.EvalSymlinks(exe); err == nil {
				exe = physical
			}
			dir := filepath.Dir(exe)
			if regular(filepath.Join(dir, "state.json")) {
				opts.Home = dir
			} else {
				h, _ := os.UserHomeDir()
				opts.Home = filepath.Join(h, ".local", "cdetoolkit")
				legacy := filepath.Join(h, ".local", "stk")
				_, stateErr := os.Lstat(filepath.Join(opts.Home, "state.json"))
				_, configErr := os.Lstat(filepath.Join(opts.Home, "config.toml"))
				_, legacyErr := os.Lstat(filepath.Join(legacy, "state.json"))
				if os.IsNotExist(stateErr) && os.IsNotExist(configErr) && !os.IsNotExist(legacyErr) {
					opts.Home = legacy
				}
			}
		}
	}
	return opts, words, jsonMode, recoverMode, nil
}

func (c CLI) Run(args []string) int {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" {
			fmt.Fprint(c.Out, help)
			return 0
		}
	}
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprint(c.Out, help)
		return 0
	}
	if len(args) > 0 && args[0] == "version" {
		fmt.Fprintln(c.Out, "cdet "+Version)
		return 0
	}
	opts, words, jsonMode, recoverMode, err := parse(args)
	if err != nil {
		return c.fail(err)
	}
	action := "status"
	if len(words) > 0 {
		action = words[0]
		words = words[1:]
	}
	if action == "update" || action == "uninstall" {
		for i, word := range words {
			if word == "cdet" {
				words[i] = "stk"
			}
		}
	}
	if action == "version" {
		fmt.Fprintln(c.Out, "cdet "+Version)
		return 0
	}
	if recoverMode {
		if action != "repair" || opts.DryRun {
			return c.fail(fmt.Errorf("--recover 仅用于 repair 且不能与 --dry-run 同用"))
		}
		if err = Recover(opts.Home); err != nil {
			return c.fail(err)
		}
		fmt.Fprintln(c.Out, "文件操作已恢复；未改变服务运行态")
		return 0
	}
	if (action == "install" || action == "update") && len(words) == 0 && len(opts.Artifacts) > 0 {
		for name := range opts.Artifacts {
			words = append(words, name)
		}
		sort.Strings(words)
	}
	if action == "install" && len(words) == 0 {
		words = []string{"proxy", "codex"}
	}
	if err = lifecycleOptions(action, words, opts); err != nil {
		return c.fail(err)
	}
	m, err := New(opts)
	if err == nil && action == "scan" && !m.Initialized && len(opts.Search) > 0 {
		var home string
		home, err = FindHome(opts.Home, "", opts.Search)
		if err == nil {
			opts.Home = home
			m, err = New(opts)
		}
	}
	if err != nil {
		return c.fail(err)
	}
	if opts.DryRun && (action == "start" || action == "stop" || action == "restart" || action == "exec" || action == "logs" || opts.ExecCheck) {
		return c.fail(fmt.Errorf("该操作不支持 --dry-run，未执行"))
	}
	switch action {
	case "path":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("path 不接受参数"))
		}
		fmt.Fprintln(c.Out, filepath.Join(m.Home, m.selfPath()))
		return 0
	case "status", "doctor":
		if len(words) > 0 {
			return c.fail(fmt.Errorf("多余参数"))
		}
		rows := m.Status()
		bad := false
		for i, s := range rows {
			if s.Managed && s.State != "可用（运行态未查询）" && s.State != "未启用" {
				bad = true
			}
			if action == "doctor" && opts.ExecCheck && s.Managed && m.Config.Modules[s.Module].Enabled {
				if e := m.Check(s.Module); e != nil {
					bad = true
					rows[i].State = "可执行性检查失败"
				}
			}
		}
		if jsonMode {
			code := c.encode(rows)
			if code == 0 && action == "doctor" && bad {
				return 1
			}
			return code
		}
		fmt.Fprintf(c.Out, "CDET_HOME %s\n", m.Home)
		if !m.Initialized {
			fmt.Fprintln(c.Out, "尚未初始化；只读检查未创建文件")
		}
		for _, s := range rows {
			fmt.Fprintf(c.Out, "%-8s %s %s\n", s.Module, s.State, s.Binary)
		}
		if action == "doctor" && bad {
			return 1
		}
		return 0
	case "scan":
		if len(words) == 0 {
			words = []string{"proxy", "codex"}
		}
		found := map[string][]Candidate{}
		for _, name := range words {
			cs, e := m.Discover(name)
			if e != nil {
				return c.fail(e)
			}
			found[name] = cs
		}
		if jsonMode {
			return c.encode(found)
		}
		for _, name := range words {
			fmt.Fprintf(c.Out, "%s: %v\n", name, candidatePaths(found[name]))
		}
		return 0
	case "init", "setup", "repair", "install", "update", "uninstall", "rollback":
		var plan *Plan
		switch action {
		case "init":
			if len(words) != 0 {
				return c.fail(fmt.Errorf("init 不接受组件参数"))
			}
			plan, err = m.Init()
		case "setup":
			plan, err = m.Setup(words)
		case "install":
			plan, err = m.Install(words)
		case "update":
			plan, err = m.Update(words)
		case "uninstall":
			plan, err = m.Uninstall(words)
		case "rollback":
			if len(words) != 0 {
				return c.fail(fmt.Errorf("rollback 不接受模块名，只回滚最近一批更新"))
			}
			plan, err = m.Rollback()
		case "repair":
			if len(words) > 0 {
				return c.fail(fmt.Errorf("repair 不接受模块参数"))
			}
			plan, err = m.Repair()
		}
		if err != nil {
			return c.fail(err)
		}
		if !opts.DryRun {
			if err = Apply(plan); err != nil {
				return c.fail(err)
			}
		}
		if jsonMode {
			return c.encode(plan)
		}
		for _, d := range plan.Downloads {
			state := "待校验安装"
			if !d.Needed {
				state = "已是所选发布版本"
			}
			fmt.Fprintf(c.Out, "%-8s %s %s %s\n", d.Module, d.Version, d.Asset, state)
		}
		for _, change := range plan.Changes {
			fmt.Fprintf(c.Out, "%-5s %s — %s\n", change.Kind, change.Path, change.Reason)
		}
		for _, warning := range plan.Warnings {
			fmt.Fprintln(c.Out, "提示:", warning)
		}
		if opts.DryRun {
			fmt.Fprintln(c.Out, "仅预览，未写入文件或改变运行态")
		} else {
			fmt.Fprintf(c.Out, "完成 %d 项文件变更；未改变服务运行态\n", len(plan.Changes))
		}
		return 0
	case "env":
		if len(words) != 1 || words[0] != "bash" {
			return c.fail(fmt.Errorf("用法: env bash"))
		}
		if err = m.requireCurrentRoots(); err != nil {
			return c.fail(err)
		}
		p := filepath.Join(m.Home, "env.sh")
		b, e := readRegular(p)
		if e != nil {
			return c.fail(e)
		}
		if digest(b) != m.State.Generated["env.sh"] {
			return c.fail(fmt.Errorf("环境文件已被外部修改"))
		}
		fmt.Fprint(c.Out, string(b))
		return 0
	case "exec":
		if len(words) < 1 {
			return c.fail(fmt.Errorf("用法: exec NAME -- ARGS"))
		}
		name := words[0]
		argv := words[1:]
		if len(argv) > 0 && argv[0] == "--" {
			argv = argv[1:]
		}
		path, argv, env, e := m.Command(name, argv)
		if e != nil {
			return c.fail(e)
		}
		return execProcess(path, argv, env, c.Err)
	case "start", "stop", "restart", "logs":
		if len(words) != 1 {
			return c.fail(fmt.Errorf("需要一个服务模块名"))
		}
		out, e := m.ServiceAction(words[0], action)
		fmt.Fprint(c.Out, string(out))
		if e != nil {
			return c.fail(e)
		}
		return 0
	default:
		return c.fail(fmt.Errorf("未知或尚未实现命令: %s；执行 help 查看当前边界", action))
	}
}

func lifecycleOptions(action string, names []string, o Options) error {
	if o.Entry != "" && action != "init" && !(action == "update" && len(names) == 1 && names[0] == "stk") {
		return fmt.Errorf("--entry 仅用于 init 或 update cdet")
	}
	if o.Takeover && action != "update" {
		return fmt.Errorf("--takeover 仅用于 update")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] && (action == "setup" || action == "install" || action == "update" || action == "uninstall") {
			return fmt.Errorf("重复模块: %s", name)
		}
		seen[name] = true
	}
	if action == "update" || action == "uninstall" || action == "rollback" {
		if o.CodexHome != "" || o.ProxyConfig != "" || o.ProxyData != "" || o.Endpoint != "" || len(o.Binaries)+len(o.Endpoints)+len(o.Services)+len(o.ShellFiles) > 0 || o.ManageConfig {
			return fmt.Errorf("update/uninstall 不接管或迁移配置；位置变更请单独使用 setup/repair")
		}
	}
	if action == "uninstall" && len(o.Artifacts)+len(o.Digests) > 0 {
		return fmt.Errorf("uninstall 不接受安装源或摘要")
	}
	if action != "setup" && action != "install" && action != "update" && len(o.Artifacts)+len(o.Digests) > 0 {
		return fmt.Errorf("安装源/摘要仅用于 setup/install/update")
	}
	if o.ExecCheck && action != "doctor" {
		return fmt.Errorf("--exec-check 仅用于 doctor")
	}
	for name := range o.Artifacts {
		if len(names) > 0 && !seen[name] {
			return fmt.Errorf("安装源 %s 不在本次模块列表中", name)
		}
		if o.Binaries[name] != "" {
			return fmt.Errorf("%s 不能同时选择已有程序和安装源", name)
		}
	}
	for name := range o.Digests {
		if len(names) > 0 && !seen[name] {
			return fmt.Errorf("摘要 %s 不在本次模块列表中", name)
		}
		if !validDigest(o.Digests[name]) {
			return fmt.Errorf("SHA-256 必须是 64 位十六进制摘要")
		}
	}
	return nil
}

func (c CLI) encode(v any) int {
	e := json.NewEncoder(c.Out)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil {
		return c.fail(err)
	}
	return 0
}
func (c CLI) fail(err error) int {
	if errors.Is(err, os.ErrPermission) {
		fmt.Fprintln(c.Err, "权限不足；未自动提升权限")
	}
	fmt.Fprintln(c.Err, "错误:", err)
	return 1
}
