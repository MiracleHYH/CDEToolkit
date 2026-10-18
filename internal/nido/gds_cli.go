package nido

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
)

var gdsVersionArgument = regexp.MustCompile(`^(?:gds-v)?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func gdsReleaseSource(args []string, artifact string) (string, error) {
	if artifact != "" {
		if len(args) != 0 {
			return "", fmt.Errorf("官方版本与 --artifact 不能同时使用")
		}
		if !filepath.IsAbs(artifact) {
			return "", fmt.Errorf("本地 GDS 程序必须通过 --artifact 指定绝对路径")
		}
		return artifact, nil
	}
	if len(args) == 0 || args[0] == "latest" {
		return "", nil
	}
	version := args[0]
	if !gdsVersionArgument.MatchString(version) {
		return "", fmt.Errorf("GDS 版本需要 latest、X.Y.Z 或 gds-vX.Y.Z 稳定版本")
	}
	if version[0] >= '0' && version[0] <= '9' {
		version = "gds-v" + version
	}
	return version, nil
}

func (c *CLI) gds(m *Manager, o cliOptions, args []string) int {
	fail := func(err error) int {
		if o.json {
			_ = json.NewEncoder(c.Err).Encode(map[string]string{"code": "gds_operation_failed", "error": err.Error()})
			return 1
		}
		return c.fail(err)
	}
	action := "status"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	if len(args) > 1 || len(args) == 1 && action != "install" && action != "update" {
		return fail(fmt.Errorf("GDS 操作不接受位置参数"))
	}
	if o.home != "" || o.url != "" {
		return fail(fmt.Errorf("GDS 不接受 --home 或 --url"))
	}
	if o.gpu != "" && action != "configure" || o.file != "" && action != "configure" || o.tail != "" && action != "logs" || o.check && action != "update" {
		return fail(fmt.Errorf("GDS 选项与操作不匹配"))
	}
	if o.gpu != "" && o.file != "" {
		return fail(fmt.Errorf("--gpu 与 --file 不能同时使用"))
	}
	if (o.artifact != "" || o.checksum != "") && action != "install" && action != "update" {
		return fail(fmt.Errorf("程序包选项仅用于安装或更新"))
	}
	if o.check && (len(args) != 0 || o.artifact != "" || o.checksum != "") {
		return fail(fmt.Errorf("版本查询不接受指定版本或程序包选项"))
	}
	if o.githubProxy != "" && (action != "install" && action != "update" || o.artifact != "") {
		return fail(fmt.Errorf("--github-proxy 仅用于在线安装、更新或版本查询"))
	}
	if (action == "install" || action == "update") && o.artifact == "" {
		policy, err := m.githubPolicy(o.githubProxy)
		if err != nil {
			return fail(err)
		}
		m.GDS.GitHubProxy = policy
	}
	if o.proxy != "" {
		if action != "install" && action != "update" {
			return fail(fmt.Errorf("--proxy 仅用于程序下载和版本查询"))
		}
		if err := codex.ValidateProxyURL(o.proxy); err != nil {
			return fail(err)
		}
		u, _ := url.Parse(o.proxy)
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(u)
		m.GDS.HTTP = &http.Client{Transport: transport}
	}
	switch action {
	case "status":
		return c.output(o, m.GDS.Status())
	case "config-path":
		path, err := m.GDS.ConfigPath()
		if err != nil {
			return fail(err)
		}
		if o.json {
			return c.output(o, map[string]string{"path": path})
		}
		fmt.Fprintln(c.Out, path)
		return 0
	case "logs":
		tail := 100
		if o.tail != "" {
			var err error
			tail, err = strconv.Atoi(o.tail)
			if err != nil {
				return fail(fmt.Errorf("--tail 需要整数"))
			}
		}
		if tail < 1 || tail > 10000 {
			return fail(fmt.Errorf("日志行数必须为 1 到 10000"))
		}
		if o.dry {
			return c.output(o, map[string]string{"component": "gds", "operation": action, "detail": "未读取日志内容或写入"})
		}
		log, err := m.GDS.Logs(tail)
		if err != nil {
			return fail(err)
		}
		if o.json {
			return c.output(o, map[string]string{"log": log})
		}
		fmt.Fprint(c.Out, log)
		return 0
	case "configure":
		if o.gpu == "" && o.file == "" {
			path, err := m.GDS.ConfigPath()
			if err != nil {
				return fail(err)
			}
			if _, err = m.GDS.ReadConfig(); err != nil {
				return fail(fmt.Errorf("请显式提供 --gpu 完整UUID 或 --file 配置：%w", err))
			}
			return c.output(o, map[string]string{"detail": "已有配置：" + path + "；通过 --file 显式替换，restart 生效。"})
		}
		if o.gpu != "" {
			if err := gdsapi.DefaultConfig(o.gpu).Validate(); err != nil {
				return fail(err)
			}
			path, err := m.GDS.ConfigPath()
			if err != nil {
				return fail(err)
			}
			if _, err = os.Lstat(path); err == nil {
				return fail(fmt.Errorf("已有 GDS 配置；请通过 --file 显式替换"))
			} else if !os.IsNotExist(err) {
				return fail(err)
			}
		}
	case "probe", "validate", "install", "update", "start", "stop", "restart", "remove":
	default:
		return fail(fmt.Errorf("未知 GDS 操作: %s", action))
	}
	source := ""
	if action == "install" || action == "update" {
		var err error
		source, err = gdsReleaseSource(args, o.artifact)
		if err != nil {
			return fail(err)
		}
	}
	if action == "configure" {
		source = o.file
	}
	if o.dry {
		previewAction := action
		if o.check {
			previewAction = "check-update"
		}
		if err := m.GDS.Preflight(previewAction, source, o.checksum); err != nil {
			return fail(err)
		}
		return c.output(o, map[string]string{"component": "gds", "operation": previewAction, "detail": "本地静态预检通过；未联网、下载、执行程序或写入，远端版本及实时 GPU 能力未检查。"})
	}
	if action == "update" && o.check {
		info, err := m.GDS.CheckUpdate()
		if err != nil {
			return fail(err)
		}
		return c.output(o, info)
	}
	if action == "probe" {
		sample, err := m.GDS.Probe()
		if err != nil {
			return fail(err)
		}
		return c.output(o, sample)
	}
	if action == "validate" {
		if err := m.GDS.Validate(); err != nil {
			return fail(err)
		}
		return c.output(o, map[string]string{"component": "gds", "operation": action, "detail": "配置、程序协议及设备观察能力检查通过；未分配显存。"})
	}
	if m.configBytes == nil {
		return fail(fmt.Errorf("尚未初始化，请先运行 nido init"))
	}
	// Invalid local inputs must fail before prompting, even for normal execution.
	if err := m.GDS.Preflight(action, source, o.checksum); err != nil {
		return fail(err)
	}
	if err := c.confirm(o, action+" 内置 GDS"); err != nil {
		return fail(err)
	}
	err := m.mutate(false, func() error {
		switch action {
		case "configure":
			return m.GDS.Configure(o.gpu, o.file)
		case "install":
			return m.GDS.Install(source, o.checksum)
		case "update":
			return m.GDS.Update(source, o.checksum)
		case "start":
			return m.GDS.Start()
		case "stop":
			return m.GDS.Stop()
		case "restart":
			return m.GDS.Restart()
		case "remove":
			return m.GDS.Remove()
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	return c.output(o, map[string]string{"component": "gds", "operation": action, "detail": "操作完成；安装和配置不自动启动，卸载保留配置与数据。"})
}
