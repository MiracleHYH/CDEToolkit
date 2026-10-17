package cdet

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func validateCodexSQLiteLocation(data string) error {
	config := filepath.Join(data, "config.toml")
	if absent(config) {
		return nil
	}
	b, e := readRegular(config)
	if e != nil {
		return e
	}
	var c struct {
		SQLiteHome *string `toml:"sqlite_home"`
	}
	if e = toml.NewDecoder(bytes.NewReader(b)).Decode(&c); e != nil {
		return fmt.Errorf("Codex 配置不是有效 TOML，未运行组件；配置内容未输出")
	}
	if c.SQLiteHome != nil && *c.SQLiteHome != data {
		return fmt.Errorf("Codex sqlite_home 超出当前受管数据目录；请检查独立副本，原配置未改动")
	}
	return nil
}

func (m *Manager) Status() []Status {
	var out []Status
	for _, name := range []string{"codex", "proxy", "gds"} {
		row := Status{Module: name, State: "未安装"}
		if c, ok := m.State.Components[name]; ok {
			row.Managed = c.Installed
			if !c.Installed {
				row.State = "未安装（数据保留）"
			} else if m.Relocated {
				row.State = "路径待重建"
			} else if e := m.componentOK(name, c); e != nil {
				row.State, row.Detail = "需要修复", e.Error()
			} else {
				row.State = "已安装"
				entry := filepath.Join("bin", moduleCommand(name))
				f, e := fingerprint(filepath.Join(m.Prefix, entry))
				if e != nil || f.Kind == "absent" || m.State.Generated[entry] == "" {
					row.State, row.Detail = "路径待重建", "受管启动入口缺失"
				} else if f != (Fingerprint{Kind: "file", Digest: m.State.Generated[entry], Mode: 0755}) {
					row.State, row.Detail = "需要修复", "受管启动入口已被外部修改"
				}
				if name != "gds" {
					v, e := m.softwareVersion(name)
					if e == nil {
						row.Version = v
					} else if row.Detail == "" {
						row.Detail = "软件版本查询失败，请检查组件程序；未自动替换或回退"
					}
				}
				if name == "proxy" && absent(filepath.Join(m.Store, name, "config.yaml")) {
					row.State = "已安装，待配置"
				}
				if name == "codex" {
					if e := validateCodexSQLiteLocation(filepath.Join(m.Store, name)); e != nil {
						row.State, row.Detail = "需要修复", e.Error()
					}
				}
			}
		}
		out = append(out, row)
	}
	return out
}
func (m *Manager) Command(name string, args []string) (string, []string, []string, error) {
	if e := m.requireReady(); e != nil {
		return "", nil, nil, e
	}
	if !knownModule(name) {
		return "", nil, nil, fmt.Errorf("未知组件: %s", name)
	}
	if e := m.componentOK(name, m.State.Components[name]); e != nil {
		return "", nil, nil, e
	}
	env, e := m.networkEnv()
	if e != nil {
		return "", nil, nil, e
	}
	set := func(key, val string) {
		var next []string
		for _, v := range env {
			if !strings.HasPrefix(v, key+"=") {
				next = append(next, v)
			}
		}
		env = append(next, key+"="+val)
	}
	set("PATH", filepath.Join(m.Prefix, "bin")+string(os.PathListSeparator)+m.Path)
	set("CDET_PREFIX", m.Prefix)
	set("CDET_STORE", m.Store)
	data := filepath.Join(m.Store, name)
	if name == "codex" {
		if e := validateCodexSQLiteLocation(data); e != nil {
			return "", nil, nil, e
		}
		env = codexRuntimeEnv(env, data, filepath.Join(m.Store, ".cdet", "bin"))
	}
	if name == "proxy" {
		config := filepath.Join(data, "config.yaml")
		if !regular(config) {
			return "", nil, nil, fmt.Errorf("Proxy 尚未配置，请将配置放入受管 proxy/config.yaml")
		}
		args = append([]string{"-d", data, "-f", config}, args...)
	}
	return filepath.Join(m.Store, programPath(name)), args, env, nil
}
