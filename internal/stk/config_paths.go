package stk

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

func validEndpoint(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return fmt.Errorf("代理 endpoint 必须是本机 HTTP 地址")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("代理 endpoint 必须绑定 loopback")
	}
	p, e := strconv.Atoi(u.Port())
	if e != nil || p < 1 || p > 65535 {
		return fmt.Errorf("代理 endpoint 端口无效")
	}
	return nil
}

func proxyEndpoint(path, explicit string) (string, error) {
	if explicit != "" {
		return explicit, validEndpoint(explicit)
	}
	b, e := readRegular(path)
	if e != nil {
		return "", e
	}
	var cfg struct {
		MixedPort int    `yaml:"mixed-port"`
		Bind      string `yaml:"bind-address"`
		Listeners []struct {
			Name   string `yaml:"name"`
			Type   string `yaml:"type"`
			Listen string `yaml:"listen"`
			Port   int    `yaml:"port"`
		} `yaml:"listeners"`
	}
	if e = yaml.Unmarshal(b, &cfg); e != nil {
		return "", fmt.Errorf("代理 YAML 无效（内容未输出）")
	}
	for _, listener := range cfg.Listeners {
		if listener.Name == "common" && listener.Type == "mixed" {
			endpoint := "http://" + net.JoinHostPort(listener.Listen, strconv.Itoa(listener.Port))
			return endpoint, validEndpoint(endpoint)
		}
	}
	if cfg.MixedPort > 0 {
		host := cfg.Bind
		if host == "" {
			host = "127.0.0.1"
		}
		endpoint := "http://" + net.JoinHostPort(host, strconv.Itoa(cfg.MixedPort))
		return endpoint, validEndpoint(endpoint)
	}
	return "", fmt.Errorf("未识别 common mixed listener；请显式提供 --endpoint（不会修改 YAML）")
}

func (m *Manager) rewriteConfig(name string, before []byte) ([]byte, error) {
	changed := false
	if name == "codex" {
		var doc map[string]any
		if err := toml.Unmarshal(before, &doc); err != nil {
			return nil, fmt.Errorf("Codex TOML 无效（内容未输出）")
		}
		rewrite := func(table map[string]any, keys ...string) {
			for _, key := range keys {
				if value, ok := table[key].(string); ok {
					if after := m.rebase(value); after != value {
						table[key] = after
						changed = true
					}
				}
			}
		}
		// Only known schema locations are eligible. In particular, env maps
		// and arbitrary nested tables may contain credentials named path/cwd.
		rewrite(doc, "log_dir")
		if projects, ok := doc["projects"].(map[string]any); ok {
			rebuilt := map[string]any{}
			for path, entry := range projects {
				after := m.rebase(path)
				if _, exists := rebuilt[after]; exists {
					return nil, fmt.Errorf("项目路径迁移发生冲突")
				}
				rebuilt[after] = entry
				changed = changed || after != path
			}
			doc["projects"] = rebuilt
		}
		if servers, ok := doc["mcp_servers"].(map[string]any); ok {
			for _, value := range servers {
				server, ok := value.(map[string]any)
				if !ok {
					continue
				}
				rewrite(server, "command", "cwd", "working_directory")
				if args, ok := server["args"].([]any); ok {
					for i, value := range args {
						if path, ok := value.(string); ok {
							if after := m.rebase(path); after != path {
								args[i] = after
								changed = true
							}
						}
					}
				}
			}
		}
		if !changed {
			return before, nil
		}
		return toml.Marshal(doc)
	}
	if name == "proxy" {
		var node yaml.Node
		if err := yaml.Unmarshal(before, &node); err != nil {
			return nil, fmt.Errorf("代理 YAML 无效（内容未输出）")
		}
		// Decode only the known path fields to detect inherited/aliased values
		// that the direct AST edits below must not silently miss or expand.
		type providerPath struct {
			Path string `yaml:"path"`
		}
		var known struct {
			ExternalUI string                  `yaml:"external-ui"`
			Proxy      map[string]providerPath `yaml:"proxy-providers"`
			Rule       map[string]providerPath `yaml:"rule-providers"`
		}
		if err := node.Decode(&known); err != nil {
			return nil, fmt.Errorf("代理 YAML 路径结构无效（内容未输出）")
		}
		expected, rewritten := 0, 0
		if m.rebase(known.ExternalUI) != known.ExternalUI {
			expected++
		}
		for _, providers := range []map[string]providerPath{known.Proxy, known.Rule} {
			for _, provider := range providers {
				if m.rebase(provider.Path) != provider.Path {
					expected++
				}
			}
		}
		var rewriteErr error
		sharedPath := func() {
			rewriteErr = fmt.Errorf("共享 YAML 路径锚点或合并表需手动迁移，避免影响其他配置值")
		}
		rewrite := func(table *yaml.Node, key string, shared bool) {
			shared = shared || table.Anchor != ""
			if table.Kind != yaml.MappingNode {
				return
			}
			for i := 0; i+1 < len(table.Content); i += 2 {
				k, v := table.Content[i], table.Content[i+1]
				if k.Value != key || v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
					continue
				}
				if after := m.rebase(v.Value); after != v.Value {
					if shared || v.Anchor != "" {
						sharedPath()
						return
					}
					v.Value, changed = after, true
					rewritten++
				}
			}
		}
		if len(node.Content) > 0 {
			root := node.Content[0]
			rewrite(root, "external-ui", false)
			if root.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(root.Content); i += 2 {
					k, providers := root.Content[i], root.Content[i+1]
					if k.Value != "proxy-providers" && k.Value != "rule-providers" {
						continue
					}
					if providers.Kind != yaml.MappingNode {
						continue
					}
					for j := 1; j < len(providers.Content); j += 2 {
						if providers.Content[j-1].Tag == "!!merge" {
							continue
						}
						rewrite(providers.Content[j], "path", providers.Anchor != "" || root.Anchor != "")
					}
				}
			}
		}
		if rewriteErr != nil {
			return nil, rewriteErr
		}
		if rewritten != expected {
			return nil, fmt.Errorf("共享 YAML 路径含别名或合并表，需手动迁移")
		}
		if !changed {
			return before, nil
		}
		var out bytes.Buffer
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(2)
		err := enc.Encode(&node)
		enc.Close()
		return out.Bytes(), err
	}
	return before, nil
}

func scopedEnvironment(env []string, home, endpoint string) []string {
	var result []string
	noProxy := "localhost,127.0.0.1,::1"
	for _, item := range env {
		key, value, _ := strings.Cut(item, "=")
		if home != "" && key == "CODEX_HOME" {
			continue
		}
		if endpoint != "" {
			switch key {
			case "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy":
				continue
			case "NO_PROXY", "no_proxy":
				if value != "" {
					noProxy = value
				}
				continue
			}
		}
		result = append(result, item)
	}
	if home != "" {
		result = append(result, "CODEX_HOME="+home)
	}
	if endpoint != "" {
		for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			result = append(result, key+"="+endpoint)
		}
		result = append(result, "NO_PROXY="+noProxy, "no_proxy="+noProxy)
	}
	return result
}
