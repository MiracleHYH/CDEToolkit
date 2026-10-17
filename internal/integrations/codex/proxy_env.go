package codex

import (
	"fmt"
	"strings"
)

func proxyKey(key string) bool {
	switch key {
	case "http_proxy", "https_proxy", "all_proxy", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
		return true
	}
	return false
}

// dotenvProxy reads literal proxy assignments only. It does not expand dollar
// variables, run commands, or interpret any unrelated dotenv values.
func dotenvProxy(content []byte) (map[string]string, error) {
	out := map[string]string{}
	var otherQuote byte
	for _, line := range strings.Split(string(content), "\n") {
		if otherQuote != 0 {
			if quotedEnd(line, otherQuote) >= 0 {
				otherQuote = 0
			}
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[6:])
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if !proxyKey(key) {
			// Unrelated multiline dotenv strings can contain text resembling a
			// proxy assignment. Skip their contents without interpreting them.
			if value != "" && (value[0] == '\'' || value[0] == '"') && quotedEnd(value[1:], value[0]) < 0 {
				otherQuote = value[0]
			}
			continue
		}
		literal := false
		if value != "" && (value[0] == '\'' || value[0] == '"') {
			quote := value[0]
			literal = quote == '\''
			end := strings.IndexByte(value[1:], quote)
			if end < 0 {
				return nil, fmt.Errorf(".env 的代理变量引号不完整")
			}
			end++
			tail := strings.TrimSpace(value[end+1:])
			if tail != "" && !strings.HasPrefix(tail, "#") {
				return nil, fmt.Errorf(".env 的代理变量包含不支持的表达式")
			}
			value = value[1:end]
		} else {
			for i := range value {
				if value[i] == '#' && (i == 0 || value[i-1] == ' ' || value[i-1] == '\t') {
					value = strings.TrimSpace(value[:i])
					break
				}
			}
		}
		if strings.ContainsAny(value, "\x00\r\n") || !literal && strings.ContainsAny(value, "$\\") {
			return nil, fmt.Errorf(".env 的代理变量必须为单行字面值，不支持转义或变量展开")
		}
		key = strings.ToLower(key)
		if old, exists := out[key]; exists && old != value {
			return nil, fmt.Errorf(".env 存在冲突的代理变量，请先明确处理")
		}
		out[key] = value
	}
	if otherQuote != 0 {
		return nil, fmt.Errorf(".env 存在未闭合的多行值，无法安全识别代理配置")
	}
	return out, nil
}

func quotedEnd(value string, quote byte) int {
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			i++
			continue
		}
		if value[i] == quote {
			return i
		}
	}
	return -1
}

// ProxyEnv combines literal .env proxy values with process proxy variables.
// Explicit process values take precedence; callers may then apply CLI overrides.
func (s Service) ProxyEnv(c Config) (map[string]string, error) {
	out := map[string]string{}
	if c.Home != "" {
		home, err := s.home(c.Home, false)
		if err != nil {
			return nil, err
		}
		file, err := readEnv(home)
		if err != nil {
			return nil, err
		}
		out, err = dotenvProxy(file.content)
		if err != nil {
			return nil, err
		}
	}
	env := s.environment()
	for _, key := range []string{"http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		value, exists := proxyEnvValue(env, key)
		if !exists {
			value, exists = proxyEnvValue(env, strings.ToUpper(key))
		}
		if exists {
			out[key] = value
		}
	}
	for key, value := range out {
		if key == "no_proxy" {
			if strings.ContainsAny(value, "\x00\r\n") {
				return nil, fmt.Errorf("no_proxy 配置无效")
			}
		} else if value != "" {
			if err := validProxy(value); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func proxyEnvValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], key+"="); ok {
			return value, true
		}
	}
	return "", false
}
