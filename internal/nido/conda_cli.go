package nido

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
)

func (c *CLI) conda(m *Manager, o cliOptions, args []string) int {
	if len(args) == 0 {
		args = []string{"status"}
	}
	action, rest := args[0], args[1:]
	switch action {
	case "status", "show":
		if len(rest) != 0 {
			return c.fail(fmt.Errorf("conda %s 不接受位置参数", action))
		}
		return c.output(o, m.CondaStatus())
	case "mirrors":
		if len(rest) != 0 {
			return c.fail(fmt.Errorf("conda mirrors 不接受位置参数"))
		}
		return c.output(o, []struct {
			Tool   string `json:"tool"`
			Name   string `json:"name"`
			Detail string `json:"detail"`
		}{
			{"conda", "default", defaultMirrorDetail(m.Config.Conda.Defaults.CondaCaptured)},
			{"conda", "official", "官方仓库；conda 为同义名称"},
			{"conda", "conda", "官方仓库；与 official 相同"},
			{"conda", "tuna", "清华 defaults、conda-forge 和 bioconda 镜像"},
			{"pip", "default", defaultMirrorDetail(m.Config.Conda.Defaults.PipCaptured)},
			{"pip", "official", "https://pypi.org/simple；pypi 为同义名称"},
			{"pip", "pypi", "官方 PyPI；与 official 相同"},
			{"pip", "tuna", "https://pypi.tuna.tsinghua.edu.cn/simple"},
			{"pip", "ustc", "https://mirrors.ustc.edu.cn/pypi/simple"},
			{"pip", "HTTPS URL", "自定义单个 /simple 索引，无认证信息"},
			{"all", "off", "释放镜像管理；default 快照与原生路径保留"},
		})
	case "validate":
		if len(rest) != 0 {
			return c.fail(fmt.Errorf("conda validate 不接受位置参数"))
		}
		if !m.CondaStatus().Configured {
			return c.fail(fmt.Errorf("尚未配置 conda/pip 管理项"))
		}
		if o.dry {
			return c.output(o, map[string]string{"operation": "conda validate", "detail": "将执行当前环境中的本地 conda/Python 只读配置查询，不联网"})
		}
		if err := m.checkCondaMirror(m.Config.Conda, true); err != nil {
			return c.fail(err)
		}
		checks, err := m.Conda.Validate(m.Config.Conda)
		if err != nil {
			return c.fail(err)
		}
		return c.output(o, struct {
			Checks []string `json:"checks"`
			Detail string   `json:"detail"`
		}{checks, "已验证当前本地配置；未测试镜像联网、包可用性或写入权限"})
	}
	next := m.Config.Conda
	captureTool := ""
	scope := conda.NativeScope{}
	switch action {
	case "setup":
		if len(rest) > 1 {
			return c.fail(fmt.Errorf("用法: nido conda setup [conda|pip|all]"))
		}
		captureTool = "all"
		if len(rest) == 1 {
			captureTool = rest[0]
		}
	case "mirror":
		if len(rest) != 2 {
			return c.fail(fmt.Errorf("用法: nido conda mirror conda|pip VALUE；预设见 conda mirrors"))
		}
		value := rest[1]
		if value == "off" {
			value = ""
		}
		switch rest[0] {
		case "conda":
			scope.CondaMirror = true
			if value == "conda" {
				value = "official"
			}
			if value != "" && value != "default" && value != "official" && value != "tuna" {
				return c.fail(fmt.Errorf("Conda 镜像仅支持 default、official（conda）、tuna、off"))
			}
			next.CondaMirror = value
			if value != "" {
				captureTool = "conda"
			}
		case "pip":
			if value == "pypi" {
				value = "official"
			}
			next.PipMirror = value
			next.PipIndexURL = ""
			if value != "" && value != "default" {
				index, err := conda.PipMirror(value)
				if err != nil {
					return c.fail(err)
				}
				next.PipIndexURL = index
				if value != "official" && value != "tuna" && value != "ustc" {
					next.PipMirror = "custom"
				}
			}
			if value != "" {
				captureTool = "pip"
			}
		default:
			return c.fail(fmt.Errorf("镜像工具必须为 conda 或 pip"))
		}
	case "cache":
		if len(rest) != 2 || rest[0] != "conda" && rest[0] != "pip" {
			return c.fail(fmt.Errorf("用法: nido conda cache conda|pip DIR|off"))
		}
		value, err := condaDirectory(m, rest[1])
		if err != nil {
			return c.fail(err)
		}
		if rest[0] == "conda" {
			scope.PkgsDir = true
			next.PkgsDir = value
		} else {
			scope.PipCacheDir = true
			next.PipCacheDir = value
		}
	case "envs":
		if len(rest) != 1 {
			return c.fail(fmt.Errorf("用法: nido conda envs DIR|off"))
		}
		value, err := condaDirectory(m, rest[0])
		if err != nil {
			return c.fail(err)
		}
		next.EnvsDir = value
		scope.EnvsDir = true
	case "reset":
		if len(rest) != 0 {
			return c.fail(fmt.Errorf("conda reset 不接受位置参数"))
		}
		next = conda.Config{Defaults: m.Config.Conda.Defaults}
	default:
		return c.fail(fmt.Errorf("未知 conda 操作: %s", action))
	}
	if m.configBytes == nil {
		return c.fail(fmt.Errorf("尚未初始化，请先运行 nido init"))
	}
	var err error
	next, err = m.prepareCondaSource(next, scope)
	if err != nil {
		return c.fail(err)
	}
	if action == "setup" && captureTool != "pip" && m.Config.Conda.CondaMirror == "" {
		next.SourceCONDARC, err = m.Conda.ResolveSource(m.Config.Conda, next)
		if err != nil {
			return c.fail(err)
		}
	}
	// Capture before publishing a profile or editing native storage settings.
	// Repeated setup, switches and resets must preserve the first snapshot.
	if captureTool != "" {
		captured, err := m.Conda.CaptureAtSource(next, next.Defaults, captureTool)
		if err != nil {
			return c.fail(fmt.Errorf("记录本机 default 失败；未切换镜像: %w", err))
		}
		next.Defaults = captured
	}
	if action == "setup" {
		var source []string
		if captureTool != "pip" && m.Config.Conda.CondaMirror == "" {
			source = []string{next.SourceCONDARC}
		}
		if err := m.CheckCondaDefaults(next.Defaults, source...); err != nil {
			return c.fail(err)
		}
		if o.dry {
			return c.output(o, struct {
				Operation string               `json:"operation"`
				Defaults  conda.MirrorDefaults `json:"defaults"`
				Detail    string               `json:"detail"`
			}{"conda setup", next.Defaults, "只读预览；首次 default 将记录到 Nido，原生配置保持原样"})
		}
		if err := c.confirm(o, "记录首次本机镜像为 default，已有记录保留"); err != nil {
			return c.fail(err)
		}
		// Confirmation can take arbitrarily long. Record the first effective
		// configuration at application time, preserving already-saved snapshots.
		captured, err := m.Conda.CaptureAtSource(next, m.Config.Conda.Defaults, captureTool)
		if err != nil {
			return c.fail(err)
		}
		next.Defaults = captured
		if err := m.RecordCondaDefaults(next.Defaults, source...); err != nil {
			return c.fail(err)
		}
		return c.output(o, map[string]string{"operation": "conda setup", "detail": "已记录本机原始镜像为 default；已有记录、原生配置及数据保留。"})
	}
	next, err = m.prepareCondaPreset(next, scope)
	if err != nil {
		return c.fail(err)
	}
	if err := m.CheckConda(next, scope); err != nil {
		return c.fail(err)
	}
	files, err := m.Conda.NativePathFiles(m.Config.Conda, next, scope)
	if err != nil {
		return c.fail(err)
	}
	note := ""
	if next.CondaMirror != "" && next.SourceCONDARC != "" {
		note = "原 CONDARC 路径保留，仅原地修改镜像字段；证书、代理等其他设置继续由原文件实时读取。"
	}
	if o.dry {
		return c.output(o, struct {
			Operation   string            `json:"operation"`
			Config      conda.Config      `json:"config"`
			Environment map[string]string `json:"environment"`
			Files       []string          `json:"files"`
			Detail      string            `json:"detail"`
		}{"conda " + action, next, publicCondaVariables(next, m.Conda.Variables(next)), files, "只读预览；路径直接写入原生配置，不恢复旧值；default 快照保留。" + note})
	}
	if note != "" {
		fmt.Fprintln(c.Err, note)
	}
	if err := c.confirm(o, "保存 conda/pip 配置；路径直接修改原生配置，镜像 default 永久保留"); err != nil {
		return c.fail(err)
	}
	if captureTool != "" {
		captured, err := m.Conda.CaptureAtSource(next, m.Config.Conda.Defaults, captureTool)
		if err != nil {
			return c.fail(err)
		}
		next.Defaults = captured
	}
	if err := m.ConfigureConda(next, scope); err != nil {
		return c.fail(err)
	}
	detail := "已保存镜像与 default 快照；当前终端执行 . " + shellQuote(filepath.Join(m.Root, "env.sh")) + " 后更新镜像。"
	if action == "cache" || action == "envs" {
		detail = "已更新原生目录配置；off 只停止跟踪，不恢复旧路径，不搬迁或删除数据。"
	}
	if action == "reset" {
		detail = "已释放管理；default 快照与原生目录设置保留。重新加载 env.sh 释放镜像环境。"
	}
	return c.output(o, map[string]string{"operation": "conda " + action, "environment": filepath.Join(m.Root, "env.sh"), "saved": strings.Join(files, ", "), "detail": detail})
}

func condaDirectory(m *Manager, value string) (string, error) {
	if value == "off" {
		return "", nil
	}
	return m.Conda.NormalizePath(value)
}
func defaultMirrorDetail(captured bool) string {
	if captured {
		return "已保存的本机原始镜像；重复 setup 和 reset 不覆盖"
	}
	return "尚未记录；setup 或首次切换前读取本机有效配置"
}
