package cdet

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var Version = "dev"

const help = `CDEToolkit — Cloud Development Environment Toolkit

cdet [--prefix DIR] COMMAND
  init                         初始化工具；仅选择安装目录、组件存储目录
  install [codex proxy]         新装或将已有程序及完整数据迁入存储目录
  scan                         只读发现可迁入的 Codex、Proxy
  status / doctor              查看受管状态和本机软件版本
  versions [codex proxy]       查看当前与上游最新版本
  repair [--store DIR]          重新定位原存储并重建受管入口
  update [codex proxy]          使用上游更新能力；更新前展示版本并确认
  remove NAME...               移除程序，保留数据；self 保留整个存储目录
  env bash                     输出安装目录的 PATH，不改 Shell 配置
  exec NAME -- ARGS            显式运行组件；Codex 使用受管 CODEX_HOME
  path / version               输出 CLI 路径 / 版本

目录选项：
  --prefix DIR                 工具安装目录（CDET_PREFIX，默认 ~/.cdet）
  --store DIR                  组件存储目录（CDET_STORE，首次默认 ~/.cdet-store）
一次性安装输入：
  --from NAME=DIR              迁入源目录；不会成为运行路径
  --fresh                      忽略外部安装，明确全新安装
  --artifact NAME=SOURCE       Proxy 包来源；GDS 必须提供本地包
  --sha256 NAME=HASH           自定义 HTTPS 包必需；Codex 不接受包选项
  --yes                        确认自动发现的迁入；init 接受默认目录
  --dry-run / --json           只读预览 / 结构化输出

Codex 使用官方安装脚本；Proxy 优先使用 Mihomo 官方控制 API 更新，该操作可能重启 Mihomo。
GDS 必须显式选择并提供独立程序包；没有默认在线源。
迁入完整数据目录并保留原件；不自动修改 Shell、注册或启动服务。
`

type CLI struct {
	In       io.Reader
	Out, Err io.Writer
}

func parse(args []string) (Options, []string, bool, error) {
	o := Options{From: map[string]string{}, Artifacts: map[string]string{}, Digests: map[string]string{}}
	var words []string
	jsonMode := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			words = append(words, args[i:]...)
			break
		}
		switch a {
		case "--dry-run":
			o.DryRun = true
			continue
		case "--yes":
			o.Yes = true
			continue
		case "--fresh":
			o.Fresh = true
			continue
		case "--json":
			jsonMode = true
			continue
		}
		if !strings.HasPrefix(a, "--") {
			words = append(words, a)
			continue
		}
		switch a {
		case "--prefix", "--store", "--from", "--artifact", "--sha256":
		default:
			return o, nil, false, fmt.Errorf("未知选项: %s；新版仅使用 --prefix、--store 两个目标目录", a)
		}
		if i+1 >= len(args) {
			return o, nil, false, fmt.Errorf("%s 缺少值", a)
		}
		i++
		value := args[i]
		switch a {
		case "--prefix":
			o.Prefix = expand(value)
		case "--store":
			o.Store = expand(value)
		default:
			name, v, ok := strings.Cut(value, "=")
			if !ok || !knownModule(name) || v == "" {
				return o, nil, false, fmt.Errorf("%s 需要有效的 NAME=VALUE", a)
			}
			target := o.Artifacts
			if a == "--from" {
				target = o.From
				v = expand(v)
			} else if a == "--sha256" {
				target = o.Digests
			}
			if _, exists := target[name]; exists {
				return o, nil, false, fmt.Errorf("重复选项: %s %s", a, name)
			}
			target[name] = v
		}
	}
	return o, words, jsonMode, nil
}

func expand(p string) string {
	if p == "~" {
		h, _ := os.UserHomeDir()
		return h
	}
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(p, "~/"))
	}
	return p
}

func (c CLI) prompt() (io.Reader, io.Writer, func(), error) {
	if c.In != nil {
		return c.In, c.Out, func() {}, nil
	}
	f, e := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if e != nil {
		return nil, nil, nil, fmt.Errorf("没有交互终端，请明确提供 --prefix、--store 或 --yes")
	}
	return f, f, func() { _ = f.Close() }, nil
}

func (c CLI) roots(o *Options) error {
	if o.Prefix == "" {
		if exe, e := os.Executable(); e == nil {
			if p, e := filepath.EvalSymlinks(exe); e == nil {
				exe = p
			}
			if filepath.Base(filepath.Dir(exe)) == "bin" && regular(filepath.Join(filepath.Dir(filepath.Dir(exe)), "config.toml")) {
				o.Prefix = filepath.Dir(filepath.Dir(exe))
			}
		}
	}
	if o.Prefix == "" {
		o.Prefix = os.Getenv("CDET_PREFIX")
	}
	h, _ := os.UserHomeDir()
	// Re-running an installer does not ask for an already registered store.
	if o.Prefix != "" && regular(filepath.Join(o.Prefix, "config.toml")) {
		return nil
	}
	if o.Store == "" {
		o.Store = os.Getenv("CDET_STORE")
	}
	if o.Prefix != "" && o.Store != "" {
		return nil
	}
	if o.Yes {
		if o.Prefix == "" {
			o.Prefix = filepath.Join(h, ".cdet")
		}
		if o.Store == "" {
			o.Store = filepath.Join(h, ".cdet-store")
		}
		return nil
	}
	in, out, close, e := c.prompt()
	if e != nil {
		return e
	}
	defer close()
	r := bufio.NewReader(in)
	for _, question := range []struct {
		label, fallback string
		target          *string
	}{{"cdet 安装目录", filepath.Join(h, ".cdet"), &o.Prefix}, {"组件存储目录", filepath.Join(h, ".cdet-store"), &o.Store}} {
		if *question.target != "" {
			continue
		}
		fmt.Fprintf(out, "%s [%s]: ", question.label, question.fallback)
		line, e := r.ReadString('\n')
		if e != nil {
			return fmt.Errorf("目录选择未完成")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			line = question.fallback
		}
		*question.target = expand(line)
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
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(c.Out, "cdet "+Version)
		return 0
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--help" || a == "-h" || a == "help" {
			fmt.Fprint(c.Out, help)
			return 0
		}
	}
	o, words, jsonMode, e := parse(args)
	if e != nil {
		return c.fail(e)
	}
	action := "status"
	if len(words) > 0 {
		action, words = words[0], words[1:]
	}
	allowed := map[string]bool{"init": true, "install": true, "scan": true, "status": true, "doctor": true, "repair": true, "update": true, "versions": true, "remove": true, "env": true, "exec": true, "path": true, "version": true}
	if !allowed[action] {
		return c.fail(fmt.Errorf("未知命令: %s", action))
	}
	if action == "version" {
		fmt.Fprintln(c.Out, "cdet "+Version)
		return 0
	}
	if (len(o.From) != 0 || o.Fresh) && action != "install" && action != "scan" {
		return c.fail(fmt.Errorf("--from、--fresh 仅用于安装或发现"))
	}
	if o.Fresh && len(o.From) != 0 {
		return c.fail(fmt.Errorf("--fresh 不能与 --from 同用"))
	}
	if (len(o.Artifacts) != 0 || len(o.Digests) != 0) && action != "install" && action != "update" {
		return c.fail(fmt.Errorf("安装包选项仅用于 install、update"))
	}
	if o.Artifacts["codex"] != "" || o.Digests["codex"] != "" {
		return c.fail(fmt.Errorf("Codex 使用官方安装脚本，不接受 --artifact 或 --sha256"))
	}
	if action == "update" {
		for _, name := range words {
			if name == "self" {
				return c.fail(fmt.Errorf("请使用 CDEToolkit 官方安装器更新 cdet"))
			}
		}
	}
	if o.DryRun && action == "exec" {
		return c.fail(fmt.Errorf("exec 不支持 --dry-run；未运行组件"))
	}
	if action == "init" {
		if e = c.roots(&o); e != nil {
			return c.fail(e)
		}
	}
	if action == "install" && len(words) == 0 {
		words = []string{"codex", "proxy"}
	}
	for _, inputs := range []map[string]string{o.From, o.Artifacts, o.Digests} {
		for name := range inputs {
			found := action == "scan"
			for _, word := range words {
				if word == name {
					found = true
				}
			}
			if !found {
				return c.fail(fmt.Errorf("%s 的选项未对应一个明确选择的组件", name))
			}
		}
	}
	for name := range o.Digests {
		if o.Artifacts[name] == "" {
			return c.fail(fmt.Errorf("--sha256 必须对应 --artifact"))
		}
	}
	m, e := New(o)
	if e != nil {
		return c.fail(e)
	}
	switch action {
	case "path":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("path 不接受组件"))
		}
		fmt.Fprintln(c.Out, m.executablePath())
		return 0
	case "status", "doctor":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("%s 不接受组件", action))
		}
		rows := m.Status()
		if jsonMode {
			_ = json.NewEncoder(c.Out).Encode(rows)
		} else {
			for _, r := range rows {
				fmt.Fprintf(c.Out, "%s\t%s\t%s\t%s\n", r.Module, r.State, r.Version, r.Detail)
			}
		}
		if action == "doctor" {
			for _, r := range rows {
				if r.State == "需要修复" || r.State == "路径待重建" || r.State == "操作未完成" {
					return 1
				}
			}
			if m.Relocated {
				return 1
			}
		}
		return 0
	case "versions":
		for _, name := range words {
			if name != "codex" && name != "proxy" {
				return c.fail(fmt.Errorf("versions 仅支持 codex、proxy"))
			}
		}
		rows, e := m.Versions(words)
		if e != nil {
			return c.fail(e)
		}
		if jsonMode {
			_ = json.NewEncoder(c.Out).Encode(rows)
		} else {
			c.printVersions(rows)
		}
		return 0
	case "scan":
		rows, e := m.Scan()
		if e != nil {
			return c.fail(e)
		}
		if jsonMode {
			_ = json.NewEncoder(c.Out).Encode(rows)
		} else {
			for _, r := range rows {
				fmt.Fprintf(c.Out, "%s\t程序=%s\t数据=%s\n", r.Module, r.Binary, r.Data)
			}
		}
		return 0
	case "env":
		if len(words) != 1 || words[0] != "bash" {
			return c.fail(fmt.Errorf("使用 env bash"))
		}
		if e = m.requireReady(); e != nil {
			return c.fail(e)
		}
		fmt.Fprintln(c.Out, "export PATH="+quote(filepath.Join(m.Prefix, "bin"))+":\"$PATH\"")
		return 0
	case "exec":
		if len(words) < 1 {
			return c.fail(fmt.Errorf("需要组件名称"))
		}
		name, argv := words[0], words[1:]
		if len(argv) > 0 && argv[0] == "--" {
			argv = argv[1:]
		}
		lease, e := m.ExecutionLease(name)
		if e != nil {
			return c.fail(e)
		}
		defer lease.Close()
		path, argv, env, e := m.Command(name, argv)
		if e != nil {
			return c.fail(e)
		}
		return execProcess(path, append([]string{path}, argv...), env, c.Err)
	}
	var p *Plan
	switch action {
	case "init":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("init 不安装组件，请使用 install"))
		}
		p, e = m.Init()
	case "install":
		p, e = m.Install(words)
	case "repair":
		if len(words) != 0 {
			return c.fail(fmt.Errorf("repair 不接受组件"))
		}
		p, e = m.Repair()
	case "update":
		p, e = m.Update(words)
	case "remove":
		if len(words) == 0 {
			return c.fail(fmt.Errorf("需要明确选择移除组件"))
		}
		p, e = m.Remove(words)
	}
	if e != nil {
		return c.fail(e)
	}
	if o.DryRun {
		if jsonMode {
			_ = json.NewEncoder(c.Out).Encode(p)
		} else {
			c.printPlan(p)
		}
		return 0
	}
	if action == "install" && !o.Yes {
		needConfirm := false
		for _, item := range p.Imports {
			if o.From[item.Module] == "" {
				needConfirm = true
			}
		}
		if needConfirm {
			c.printPlan(p)
			in, out, close, e := c.prompt()
			if e != nil {
				return c.fail(fmt.Errorf("自动发现已有安装；请先 --dry-run 查看，再加 --yes 或使用 --from 明确迁入源"))
			}
			fmt.Fprint(out, "按以上方案迁入组件数据，保留源副本，继续？[y/N]: ")
			line, er := bufio.NewReader(in).ReadString('\n')
			close()
			if er != nil || strings.TrimSpace(strings.ToLower(line)) != "y" {
				return c.fail(fmt.Errorf("迁入未确认，未执行"))
			}
		}
	}
	if action == "update" && len(p.Steps) != 0 {
		if !jsonMode {
			c.printPlan(p)
		}
		if !o.Yes {
			in, out, close, e := c.prompt()
			if e != nil {
				return c.fail(fmt.Errorf("更新需要确认；请先 --dry-run 查看，再使用 --yes"))
			}
			if jsonMode {
				out = c.Err
			}
			fmt.Fprint(out, "按以上方案更新？[y/N]: ")
			line, er := bufio.NewReader(in).ReadString('\n')
			close()
			if er != nil || strings.TrimSpace(strings.ToLower(line)) != "y" {
				return c.fail(fmt.Errorf("更新未确认，未执行"))
			}
		}
	}
	if e = Apply(p); e != nil {
		return c.fail(e)
	}
	if jsonMode {
		_ = json.NewEncoder(c.Out).Encode(p)
	} else {
		fmt.Fprintf(c.Out, "完成 %d 项操作\n", len(p.Steps))
		for _, w := range p.Warnings {
			fmt.Fprintln(c.Out, w)
		}
		if action == "init" {
			fmt.Fprintln(c.Out, "PATH: "+filepath.Join(m.Prefix, "bin"))
		}
	}
	return 0
}

func (c CLI) printPlan(p *Plan) {
	fmt.Fprintf(c.Out, "安装目录: %s\n组件存储: %s\n", p.Prefix, p.Store)
	for _, i := range p.Imports {
		mode := ""
		if i.Module == "codex" {
			mode = " [完整 CODEX_HOME]"
		}
		fmt.Fprintf(c.Out, "迁入 %s%s: %s（%d 项，%d 字节；源副本保留）\n", i.Module, mode, i.Source, i.Files, i.Bytes)
	}
	c.printVersions(p.Versions)
	for _, step := range p.Steps {
		fmt.Fprintf(c.Out, "%s\t%s\t%s\n", step.Module, step.Action, step.Detail)
	}
	for _, warning := range p.Warnings {
		fmt.Fprintln(c.Out, warning)
	}
}
func (c CLI) printVersions(rows []VersionInfo) {
	for _, row := range rows {
		current, latest := row.Current, row.Latest
		if current == "" {
			current = "未知"
		}
		if latest == "" {
			latest = "未知"
		}
		fmt.Fprintf(c.Out, "%s\t当前=%s\t最新=%s", row.Module, current, latest)
		if row.Error != "" {
			fmt.Fprintf(c.Out, "\t%s", row.Error)
		}
		fmt.Fprintln(c.Out)
	}
}
func (c CLI) fail(e error) int { fmt.Fprintln(c.Err, "错误:", e); return 1 }
