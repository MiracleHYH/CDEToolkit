package nido

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/gitproxy"
)

type proxyChoice struct {
	address  string
	endpoint *proxy.Endpoint
	keep     bool
}

// Listener enumeration is optional: a custom external proxy works even when
// the component is missing or its native configuration has no listeners.
func (c *CLI) chooseProxy(m *Manager, o cliOptions, label string) (proxyChoice, error) {
	if o.proxy != "" {
		if o.proxy == "off" {
			return proxyChoice{}, nil
		}
		if err := validateBindingProxy(o.proxy); err != nil {
			return proxyChoice{}, err
		}
		return proxyChoice{address: o.proxy}, nil
	}
	endpoints, readErr := m.Proxy.ListenerEndpoints()
	selectListener := func(endpoint proxy.Endpoint) (proxyChoice, error) {
		return c.listenerChoice(endpoint, o.yes || o.json || o.dry)
	}
	if o.endpoint != "" {
		if readErr != nil {
			return proxyChoice{}, fmt.Errorf("没有可用的 listener 端点；请用 --proxy 自定义代理：%w", readErr)
		}
		for _, endpoint := range endpoints {
			if endpoint.Name == o.endpoint {
				return selectListener(endpoint)
			}
		}
		return proxyChoice{}, fmt.Errorf("未找到指定 listener；请重新选择或用 --proxy 自定义")
	}
	if o.yes || o.json || o.dry {
		return proxyChoice{}, fmt.Errorf("非交互配置需要 --endpoint NAME 或 --proxy URL|off")
	}
	fmt.Fprintln(c.Err, label+"：请选择代理")
	if readErr != nil {
		fmt.Fprintln(c.Err, "没有可用的 listener 端点：", readErr)
		endpoints = nil
	} else if len(endpoints) == 0 {
		fmt.Fprintln(c.Err, "没有可用的 listener 端点。")
	}
	options := make([]string, 0, len(endpoints)+3)
	for _, endpoint := range endpoints {
		auth := ""
		if endpoint.Authentication {
			auth = "（需要认证）"
		}
		options = append(options, endpoint.Name+"  "+endpoint.URL()+" "+auth)
	}
	custom, keep := len(endpoints), len(endpoints)+1
	options = append(options, "自定义代理地址", "保持现状", "移除该工具的 Nido 代理配置")
	choice, err := c.choose(label+"：选择 listener 或自定义代理", options, keep)
	if err != nil {
		return proxyChoice{}, err
	}
	if choice < len(endpoints) {
		return selectListener(endpoints[choice])
	}
	if choice == keep {
		return proxyChoice{keep: true}, nil
	}
	if choice == len(endpoints)+2 {
		return proxyChoice{}, nil
	}
	if choice == custom {
		address, err := c.secretPrompt("代理地址 protocol://[username:password@]host:port", "")
		if err != nil {
			return proxyChoice{}, err
		}
		if err = validateBindingProxy(address); err != nil {
			return proxyChoice{}, err
		}
		return proxyChoice{address: address}, nil
	}
	return proxyChoice{}, fmt.Errorf("无效的代理选择")
}

func (c *CLI) listenerChoice(endpoint proxy.Endpoint, noninteractive bool) (proxyChoice, error) {
	address := endpoint.URL()
	if endpoint.Authentication {
		if noninteractive {
			return proxyChoice{}, fmt.Errorf("listener 要求认证；请用 --proxy 提供完整代理地址")
		}
		username, err := c.ask("代理用户名", "")
		if err != nil {
			return proxyChoice{}, err
		}
		password, err := c.secretPrompt("代理密码", "")
		if err != nil {
			return proxyChoice{}, err
		}
		u, _ := url.Parse(address)
		u.User = url.UserPassword(username, password)
		address = u.String()
	}
	if err := validateBindingProxy(address); err != nil {
		return proxyChoice{}, err
	}
	return proxyChoice{address: address, endpoint: &endpoint}, nil
}

func validateBindingProxy(address string) error {
	if !utf8.ValidString(address) {
		return fmt.Errorf("代理地址必须为有效 UTF-8")
	}
	if err := gitproxy.ValidateProxyURL(address); err != nil {
		return err
	}
	u, err := url.Parse(address)
	if err != nil || u.Port() == "" {
		return fmt.Errorf("代理地址需要 protocol://[username:password@]host:port")
	}
	if u.User != nil {
		password, _ := u.User.Password()
		if strings.ContainsFunc(u.User.Username()+password, unicode.IsControl) {
			return fmt.Errorf("代理认证信息不能包含控制字符")
		}
	}
	return nil
}
