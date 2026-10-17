package cdet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type VersionInfo struct {
	Module          string `json:"module"`
	Current         string `json:"current,omitempty"`
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Error           string `json:"error,omitempty"`
}

var codexSoftwareVersion = regexp.MustCompile(`(?m)^codex(?:-cli)?\s+v?([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)\s*$`)
var mihomoSoftwareVersion = regexp.MustCompile(`(?mi)^mihomo(?:\s+meta)?\s+(\S+)`)
var semanticSoftwareVersion = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

func parseSoftwareVersion(name string, out []byte) (string, error) {
	pattern := codexSoftwareVersion
	if name == "proxy" {
		pattern = mihomoSoftwareVersion
	} else if name != "codex" {
		return "", fmt.Errorf("组件没有标准版本查询接口")
	}
	match := pattern.FindSubmatch(out)
	if len(match) != 2 {
		return "", fmt.Errorf("无法识别 %s 软件版本", name)
	}
	return strings.TrimPrefix(string(match[1]), "v"), nil
}

func (m *Manager) softwareVersion(name string) (string, error) {
	flag := "--version"
	if name == "proxy" {
		flag = "-v"
	} else if name != "codex" {
		return "", fmt.Errorf("组件没有标准版本查询接口")
	}
	// A local --version probe needs no network. Broken or unavailable proxy
	// settings must not hide an otherwise usable installed software version.
	env := append([]string(nil), os.Environ()...)
	data := filepath.Join(m.Store, dataPath(name))
	env = setEnvValue(env, "HOME", data)
	if name == "codex" {
		env = codexRuntimeEnv(env, data, filepath.Join(m.Store, ".cdet", "bin"))
	}
	out, err := m.run(CommandSpec{Path: filepath.Join(m.Store, programPath(name)), Args: []string{flag}, Env: env})
	if err != nil {
		return "", err
	}
	return parseSoftwareVersion(name, out)
}

func compareSoftwareVersions(a, b string) (int, error) {
	left := semanticSoftwareVersion.FindStringSubmatch(strings.TrimPrefix(a, "v"))
	right := semanticSoftwareVersion.FindStringSubmatch(strings.TrimPrefix(b, "v"))
	if left == nil || right == nil {
		return 0, fmt.Errorf("软件版本格式不可比较")
	}
	for i := 1; i <= 3; i++ {
		l, errA := strconv.ParseUint(left[i], 10, 64)
		r, errB := strconv.ParseUint(right[i], 10, 64)
		if errA != nil || errB != nil {
			return 0, fmt.Errorf("软件版本数字越界")
		}
		if l < r {
			return -1, nil
		}
		if l > r {
			return 1, nil
		}
	}
	if left[4] == right[4] {
		return 0, nil
	}
	if left[4] == "" {
		return 1, nil
	}
	if right[4] == "" {
		return -1, nil
	}
	lp, rp := strings.Split(left[4], "."), strings.Split(right[4], ".")
	for i := 0; i < len(lp) && i < len(rp); i++ {
		if lp[i] == rp[i] {
			continue
		}
		ln, le := strconv.ParseUint(lp[i], 10, 64)
		rn, re := strconv.ParseUint(rp[i], 10, 64)
		if le == nil && re == nil {
			if ln < rn {
				return -1, nil
			}
			return 1, nil
		}
		if le == nil {
			return -1, nil
		}
		if re == nil {
			return 1, nil
		}
		if lp[i] < rp[i] {
			return -1, nil
		}
		return 1, nil
	}
	if len(lp) < len(rp) {
		return -1, nil
	}
	if len(lp) == len(rp) {
		return 0, nil
	}
	return 1, nil
}

func (m *Manager) Versions(names []string) ([]VersionInfo, error) {
	if len(names) == 0 {
		names = []string{"codex", "proxy"}
	}
	if err := componentNames(names); err != nil {
		return nil, err
	}
	client, networkErr := m.networkReleases()
	var rows []VersionInfo
	for _, name := range names {
		row := VersionInfo{Module: name}
		if component := m.State.Components[name]; component.Installed {
			if m.Relocated || m.componentOK(name, component) != nil {
				row.Error = "当前软件路径需要 repair，未运行版本查询"
			} else if current, err := m.softwareVersion(name); err != nil {
				row.Error = "当前软件版本查询失败"
			} else {
				row.Current = current
			}
		}
		if networkErr != nil {
			row.Error = strings.TrimSpace(row.Error + " 最新版本查询失败")
		} else if latest, err := client.latestVersion(name); err != nil {
			row.Error = strings.TrimSpace(row.Error + " 最新版本查询失败")
		} else {
			row.Latest = strings.TrimPrefix(latest, "v")
		}
		if row.Current != "" && row.Latest != "" {
			comparison, err := compareSoftwareVersions(row.Current, row.Latest)
			if err != nil {
				row.Error = strings.TrimSpace(row.Error + " 当前版本与稳定发布不可比较")
			} else {
				row.UpdateAvailable = comparison < 0
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
