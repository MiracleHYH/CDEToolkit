package conda

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const maxOutput = 1 << 20

func (s Service) run(name string, args ...string) ([]byte, error) {
	run := s.Runner
	if run == nil {
		run = runCommand
	}
	b, err := run(name, args, s.environment())
	if len(b) > maxOutput {
		return nil, fmt.Errorf("%s 输出超过 1 MiB", name)
	}
	if err != nil {
		return nil, fmt.Errorf("%s 只读配置检查失败: %w", name, err)
	}
	return b, nil
}

// Validate checks the current shell's actual upstream configuration. It never
// injects the desired variables, so an unactivated configuration cannot pass.
func (s Service) Validate(c Config) ([]string, error) {
	if err := s.ValidateConfig(c); err != nil {
		return nil, err
	}
	if issues := s.Issues(c); len(issues) != 0 {
		return nil, fmt.Errorf("%s", strings.Join(issues, "；"))
	}
	var checked []string
	if c.CondaMirror != "" || c.EnvsDir != "" || c.PkgsDir != "" {
		args := []string{"config", "--show"}
		if c.CondaMirror != "" {
			args = append(args, defaultMirrorKeys...)
		}
		// Conda 24 evaluates root writability while resolving envs_dirs. Do not
		// trigger that unrelated upstream probe during mirror/cache checks.
		// A selected directory still uses Conda's actual effective value.
		if c.EnvsDir != "" {
			args = append(args, "envs_dirs")
		}
		if c.PkgsDir != "" {
			args = append(args, "pkgs_dirs")
		}
		args = append(args, "--json")
		b, err := s.run("conda", args...)
		if err != nil {
			return checked, err
		}
		var actual struct {
			Channels            []string                     `json:"channels"`
			DefaultChannels     []json.RawMessage            `json:"default_channels"`
			CustomChannels      map[string]json.RawMessage   `json:"custom_channels"`
			CustomMultichannels map[string][]json.RawMessage `json:"custom_multichannels"`
			EnvsDirs            []string                     `json:"envs_dirs"`
			PkgsDirs            []string                     `json:"pkgs_dirs"`
		}
		if err := json.Unmarshal(b, &actual); err != nil {
			return checked, fmt.Errorf("Conda 没有返回有效配置 JSON")
		}
		if c.CondaMirror == "default" {
			actualYAML, err := captureCondaYAMLForSnapshot(b, c.Defaults.CondaYAML)
			if err != nil || actualYAML != c.Defaults.CondaYAML {
				return checked, fmt.Errorf("Conda 实际镜像与记录的本机 default 不一致")
			}
			checked = append(checked, "Conda 已恢复为首次记录的本机 default 镜像")
		} else if c.CondaMirror != "" {
			if (c.Defaults.CondaCaptured || c.PresetChannelsSet) && (actual.Channels == nil || !slices.Equal(actual.Channels, DesiredChannels(c))) {
				return checked, fmt.Errorf("Conda 实际频道顺序或完整地址与所选镜像不一致；原配置的 #!final 可能阻止切换")
			}
			defaults, custom := mirrorChannels(c.CondaMirror)
			if !sameChannels(actual.DefaultChannels, defaults) || !sameChannels(actual.CustomMultichannels["defaults"], defaults) {
				return checked, fmt.Errorf("Conda defaults 实际来源与镜像配置不一致；其他 condarc 的 #!final 或环境变量可能覆盖了映射")
			}
			for _, name := range []string{"conda-forge", "bioconda"} {
				channel, err := channelURL(actual.CustomChannels[name])
				if err != nil || channel != custom[name]+"/"+name && channel != custom[name] {
					return checked, fmt.Errorf("Conda %s 实际来源与镜像配置不一致", name)
				}
			}
			if err := validatePresetMirrorContext(c, b); err != nil {
				return checked, err
			}
			checked = append(checked, "Conda defaults、conda-forge 和 bioconda 镜像映射已生效；原有其他频道映射已保留")
		}
		for _, row := range []struct {
			name, expected string
			actual         []string
		}{{"envs_dirs", c.EnvsDir, actual.EnvsDirs}, {"pkgs_dirs", c.PkgsDir, actual.PkgsDirs}} {
			if row.expected == "" {
				continue
			}
			if len(row.actual) == 0 || filepath.Clean(row.actual[0]) != row.expected {
				return checked, fmt.Errorf("Conda %s 的实际首选目录与配置不一致", row.name)
			}
			checked = append(checked, "Conda "+row.name+" 首选目录已生效")
		}
	}
	if EffectivePipIndex(c) != "" || c.PipCacheDir != "" {
		b, err := s.run("python", "-m", "pip", "config", "list")
		if err != nil {
			return checked, err
		}
		options, err := pipOptions(b)
		if err != nil {
			return checked, err
		}
		if EffectivePipIndex(c) != "" {
			if options[":env:.index-url"] != EffectivePipIndex(c) {
				return checked, fmt.Errorf("当前 Python 的 pip 未读取已选择的 PIP_INDEX_URL")
			}
			if pipConflict(options, "extra-index-url", func(v string) bool { return v != "" }) {
				return checked, fmt.Errorf("pip 原生配置启用了 extra-index-url；额外索引与所选镜像并用，请自行明确处理")
			}
			if pipConflict(options, "no-index", truthy) {
				return checked, fmt.Errorf("pip 原生配置启用了 no-index，索引被禁用")
			}
			checked = append(checked, "当前 Python 的 pip 主索引已生效，未发现额外索引或禁用索引配置")
		}
		if c.PipCacheDir != "" {
			if pipCacheOverride(options, c.PipCacheDir) {
				return checked, fmt.Errorf("pip 原生配置存在与目标目录冲突的 cache-dir；命令专用目录需要自行明确处理")
			}
			if pipConflict(options, "no-cache-dir", func(v string) bool { return v != "" }) {
				return checked, fmt.Errorf("pip 原生配置提供了 no-cache-dir，缓存被禁用")
			}
			b, err := s.run("python", "-m", "pip", "cache", "dir")
			if err != nil {
				return checked, err
			}
			// A filesystem name may end in spaces. Remove only the command's
			// single line ending and reject additional lines instead of trimming
			// meaningful path bytes or accepting a contaminated response.
			actual := strings.TrimSuffix(string(b), "\n")
			actual = strings.TrimSuffix(actual, "\r")
			if strings.ContainsAny(actual, "\r\n") || actual != c.PipCacheDir {
				return checked, fmt.Errorf("当前 Python 的 pip 实际缓存目录与配置不一致")
			}
			checked = append(checked, "当前 Python 的 pip 缓存目录已生效")
		}
	}
	if len(checked) == 0 {
		checked = append(checked, "尚未配置 Conda 或 pip 管理项")
	}
	return checked, nil
}

func sameChannels(actual []json.RawMessage, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i, raw := range actual {
		value, err := channelURL(raw)
		if err != nil || value != expected[i] {
			return false
		}
	}
	return true
}

// Conda releases serialize Channel values either as URLs or as public Channel
// objects. Normalize only these supported forms without guessing partial URLs.
func channelURL(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		u, err := url.Parse(text)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", fmt.Errorf("频道 URL 无效")
		}
		return strings.TrimRight(text, "/"), nil
	}
	var channel struct {
		Scheme          string          `json:"scheme"`
		Location        string          `json:"location"`
		Name            string          `json:"name"`
		Auth            json.RawMessage `json:"auth"`
		Token           json.RawMessage `json:"token"`
		Platform        json.RawMessage `json:"platform"`
		PackageFilename json.RawMessage `json:"package_filename"`
	}
	if json.Unmarshal(raw, &channel) != nil || channel.Scheme != "https" || channel.Location == "" || channel.Name == "" {
		return "", fmt.Errorf("频道对象无效")
	}
	for _, value := range []json.RawMessage{channel.Auth, channel.Token, channel.Platform, channel.PackageFilename} {
		if len(value) != 0 && !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte(`""`)) {
			return "", fmt.Errorf("频道包含额外认证或平台信息")
		}
	}
	return channelURL(json.RawMessage(strconv.Quote("https://" + strings.TrimRight(channel.Location, "/") + "/" + strings.Trim(channel.Name, "/"))))
}

func pipOptions(data []byte) (map[string]string, error) {
	options := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.TrimSpace(key) != key || !strings.Contains(key, ".") {
			return nil, fmt.Errorf("pip config list 返回无法识别的配置行")
		}
		if _, exists := options[key]; exists {
			return nil, fmt.Errorf("pip config list 返回重复配置键")
		}
		decoded, err := pipValue(value)
		if err != nil {
			return nil, err
		}
		options[key] = decoded
	}
	return options, nil
}

// pip config list uses Python repr strings. Decode both quote forms and all
// escapes produced by repr so an original private URL is saved byte-for-byte
// and escaped control characters are rejected by the snapshot validation.
func pipValue(value string) (string, error) {
	if len(value) < 2 || value[0] != '\'' && value[0] != '"' || value[len(value)-1] != value[0] {
		return "", fmt.Errorf("pip 配置值缺少引号")
	}
	var decoded strings.Builder
	quote := value[0]
	for rest := value[1 : len(value)-1]; rest != ""; {
		r, _, tail, err := strconv.UnquoteChar(rest, quote)
		if err != nil {
			return "", fmt.Errorf("pip 配置值不是受支持的 Python repr 字符串")
		}
		// Python's \xNN denotes a Unicode code point, unlike the byte escape
		// accepted by Go's full-string Unquote; WriteRune preserves that value.
		decoded.WriteRune(r)
		rest = tail
	}
	return decoded.String(), nil
}

func pipConflict(options map[string]string, name string, conflict func(string) bool) bool {
	// pip install resolves nonempty global, install and environment values in
	// that order; settings belonging to unrelated commands do not participate.
	var effective string
	for _, section := range []string{"global", "install", ":env:"} {
		if value := options[section+"."+name]; value != "" {
			effective = value
		}
	}
	return conflict(effective)
}

func pipCacheOverride(options map[string]string, expected string) bool {
	// A nonempty environment option has the final precedence for every pip
	// command. Otherwise, `pip cache dir` alone cannot detect an install,
	// download or other command's own cache-dir override.
	if value := options[":env:.cache-dir"]; value != "" {
		return filepath.Clean(value) != expected
	}
	for key, value := range options {
		if value != "" && strings.HasSuffix(key, ".cache-dir") && filepath.Clean(value) != expected {
			return true
		}
	}
	return false
}
