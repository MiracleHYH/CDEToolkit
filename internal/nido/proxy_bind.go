package nido

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/gitproxy"
)

type proxyCategory struct {
	name, label string
	tools       []string
}

var proxyCategories = []proxyCategory{
	{"ai", "AI 工具", []string{"codex"}},
	{"daily", "日常工具", []string{"git"}},
	{"download", "Nido API 查询", []string{"nido"}},
}

type proxyChoice struct {
	address  string
	endpoint *proxy.Endpoint
	keep     bool
}

type proxyBindingResult struct {
	Category string   `json:"category,omitempty"`
	Tools    []string `json:"tools"`
	Proxy    string   `json:"proxy,omitempty"`
	State    string   `json:"state"`
}

func (c *CLI) proxyBind(m *Manager, o cliOptions, args []string) int {
	fail := func(err error) int { return c.proxyError(o, err) }
	if o.home != "" || o.file != "" || o.url != "" || o.tail != "" || o.artifact != "" || o.checksum != "" || o.check || len(args) > 1 || o.tool != "" && len(args) != 0 || o.proxy != "" && o.endpoint != "" {
		return fail(fmt.Errorf("proxy bind 需要类别或 --tool；--endpoint 与 --proxy 不能同时使用"))
	}
	var categories []proxyCategory
	if o.tool != "" {
		if o.tool != "codex" && o.tool != "git" && o.tool != "nido" {
			return fail(fmt.Errorf("--tool 仅支持 codex、git 或 nido"))
		}
		categories = []proxyCategory{{label: o.tool, tools: []string{o.tool}}}
	} else if len(args) == 1 {
		for _, category := range proxyCategories {
			if category.name == args[0] {
				categories = append(categories, category)
			}
		}
		if len(categories) == 0 {
			return fail(fmt.Errorf("类别仅支持 ai、daily、download"))
		}
	} else {
		if o.endpoint != "" || o.proxy != "" {
			return fail(fmt.Errorf("显式代理需要同时指定类别或 --tool，避免将各类别隐式绑定到同一入口"))
		}
		categories = proxyCategories
	}
	if o.dry && o.proxy == "" && o.endpoint == "" {
		endpoints, _ := m.Proxy.ListenerEndpoints()
		return c.output(o, struct {
			Listeners []proxy.Endpoint `json:"listeners"`
			Detail    string           `json:"detail"`
		}{endpoints, "仅列出 listener 候选；每类或单工具独立选择，也可 --proxy 自定义。未写入配置。"})
	}
	results := []proxyBindingResult{}
	for _, category := range categories {
		result := proxyBindingResult{Category: category.name, Tools: append([]string{}, category.tools...)}
		if len(category.tools) == 0 {
			result.State = "no_targets"
			fmt.Fprintln(c.Err, category.label+"：暂无可配置工具。")
			results = append(results, result)
			continue
		}
		choice, err := c.chooseProxy(m, o, category.label, len(category.tools) == 1 && category.tools[0] == "nido")
		if err != nil {
			return fail(err)
		}
		if choice.keep {
			result.State = "unchanged"
			results = append(results, result)
			continue
		}
		var apiSnapshot apiProxySnapshot
		for _, tool := range category.tools {
			if err := m.checkToolProxy(tool, choice.address); err != nil {
				return fail(fmt.Errorf("%s：%w", tool, err))
			}
			if tool == "nido" {
				_, apiSnapshot, err = m.readAPIProxy()
				if err != nil {
					return fail(err)
				}
			}
		}
		result.Proxy = choice.address
		if o.dry {
			result.State = "preview"
		} else {
			fmt.Fprintf(c.Err, "%s：将 %s 的代理配置为 %s\n", category.label, strings.Join(category.tools, ", "), choice.address)
			if err := c.confirm(o, "应用代理配置"); err != nil {
				return fail(err)
			}
			for _, tool := range category.tools {
				var err error
				if tool == "nido" {
					err = m.configureAPIProxySnapshot(choice.address, choice.endpoint, apiSnapshot)
				} else {
					err = m.configureToolProxy(tool, choice.address, choice.endpoint)
				}
				if err != nil {
					return fail(fmt.Errorf("%s 未完成（已完成的类别保持各自结果）：%w", tool, err))
				}
			}
			result.State = "applied"
			fmt.Fprintln(c.Err, category.label+"：配置已应用。")
		}
		results = append(results, result)
	}
	return c.output(o, results)
}

// Listener enumeration is optional: a custom external proxy works even when
// the component is missing or its native configuration has no listeners.
func (c *CLI) chooseProxy(m *Manager, o cliOptions, label string, listenerOnly bool) (proxyChoice, error) {
	if o.proxy != "" {
		if o.proxy == "off" {
			return proxyChoice{}, nil
		}
		if listenerOnly {
			return proxyChoice{}, fmt.Errorf("Nido API 绑定请选择无认证的命名 listener，不保存自定义代理地址")
		}
		if err := validateBindingProxy(o.proxy); err != nil {
			return proxyChoice{}, err
		}
		return proxyChoice{address: o.proxy}, nil
	}
	endpoints, readErr := m.Proxy.ListenerEndpoints()
	selectListener := func(endpoint proxy.Endpoint) (proxyChoice, error) {
		if listenerOnly && endpoint.Authentication {
			return proxyChoice{}, fmt.Errorf("Nido API 绑定请选择无认证的 listener")
		}
		return c.listenerChoice(endpoint, o.yes || o.json || o.dry)
	}
	if o.endpoint != "" {
		if readErr != nil {
			if listenerOnly {
				return proxyChoice{}, fmt.Errorf("没有可用的 listener；请先配置 Proxy：%w", readErr)
			}
			return proxyChoice{}, fmt.Errorf("没有可用的 listener 端点；请用 --proxy 自定义代理：%w", readErr)
		}
		for _, endpoint := range endpoints {
			if endpoint.Name == o.endpoint {
				return selectListener(endpoint)
			}
		}
		if listenerOnly {
			return proxyChoice{}, fmt.Errorf("未找到指定 listener；请运行 proxy endpoints 后重新选择")
		}
		return proxyChoice{}, fmt.Errorf("未找到指定 listener；请重新选择或用 --proxy 自定义")
	}
	if o.yes || o.json || o.dry {
		if listenerOnly {
			return proxyChoice{}, fmt.Errorf("非交互 Nido API 绑定需要 --endpoint NAME；解除绑定使用 --proxy off")
		}
		return proxyChoice{}, fmt.Errorf("非交互配置需要 --endpoint NAME 或 --proxy URL|off")
	}
	fmt.Fprintln(c.Err, label+"：请选择代理")
	if readErr != nil {
		fmt.Fprintln(c.Err, "没有可用的 listener 端点：", readErr)
		endpoints = nil
	} else if len(endpoints) == 0 {
		fmt.Fprintln(c.Err, "没有可用的 listener 端点。")
	}
	for i, endpoint := range endpoints {
		auth := ""
		if endpoint.Authentication {
			auth = "（需要认证）"
		}
		fmt.Fprintf(c.Err, "%d. %s  %s %s\n", i+1, endpoint.Name, endpoint.URL(), auth)
	}
	custom, keep, off := len(endpoints)+1, len(endpoints)+2, len(endpoints)+3
	if listenerOnly {
		custom, keep, off = 0, len(endpoints)+1, len(endpoints)+2
	} else {
		fmt.Fprintf(c.Err, "%d. 自定义代理地址\n", custom)
	}
	fmt.Fprintf(c.Err, "%d. 保持现状\n%d. 移除 Nido 管理的代理配置\n", keep, off)
	value, err := c.ask("选择序号，或 listener:名称", strconv.Itoa(keep))
	if err != nil {
		return proxyChoice{}, err
	}
	// Explicit names cannot collide with menu commands or numeric positions.
	// Keep bare names and command aliases for compatibility, while the visible
	// prompt documents the unambiguous spelling for every valid listener name.
	if name, explicit := strings.CutPrefix(value, "listener:"); explicit {
		for _, endpoint := range endpoints {
			if endpoint.Name == name {
				return selectListener(endpoint)
			}
		}
		return proxyChoice{}, fmt.Errorf("未找到指定 listener")
	}
	if index, err := strconv.Atoi(value); err == nil {
		if index > 0 && index <= len(endpoints) {
			return selectListener(endpoints[index-1])
		}
		switch index {
		case custom:
			if listenerOnly {
				return proxyChoice{}, fmt.Errorf("无效的 listener 选择")
			}
			value = "custom"
		case keep:
			value = "keep"
		case off:
			value = "off"
		default:
			return proxyChoice{}, fmt.Errorf("无效的代理选择；按名称选择请使用 listener:名称")
		}
	}
	if value == "keep" {
		return proxyChoice{keep: true}, nil
	}
	if value == "off" {
		return proxyChoice{}, nil
	}
	if value == "custom" {
		if listenerOnly {
			return proxyChoice{}, fmt.Errorf("Nido API 绑定请选择无认证的命名 listener")
		}
		address, err := c.ask("代理地址 protocol://[username:password@]host:port", "")
		if err != nil {
			return proxyChoice{}, err
		}
		if err = validateBindingProxy(address); err != nil {
			return proxyChoice{}, err
		}
		return proxyChoice{address: address}, nil
	}
	for _, endpoint := range endpoints {
		if value == endpoint.Name {
			return selectListener(endpoint)
		}
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
		password, err := c.ask("代理密码", "")
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
