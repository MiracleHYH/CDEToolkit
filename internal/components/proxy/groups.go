package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ProxiesResponse is the read-only subset of Mihomo's GET /proxies response.
// Unknown fields remain core-owned; no node credentials or mutation operations
// are represented here.
type ProxiesResponse struct {
	Proxies map[string]ProxyState `json:"proxies"`
}

type ProxyState struct {
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Now     string         `json:"now"`
	All     []string       `json:"all"`
	History []ProxyHistory `json:"history"`
}

type ProxyHistory struct {
	Time  time.Time `json:"time"`
	Delay int       `json:"delay"`
}

type GroupStatus struct {
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	Chain      []string   `json:"chain"`
	Node       string     `json:"node,omitempty"`
	State      string     `json:"state"`
	Reason     string     `json:"reason,omitempty"`
	DelayState string     `json:"delay_state"`
	DelayMS    *int       `json:"delay_ms,omitempty"`
	MeasuredAt *time.Time `json:"measured_at,omitempty"`
}

func ParseProxiesResponse(data []byte) (ProxiesResponse, error) {
	var response ProxiesResponse
	if len(data) == 0 || len(data) > maxConfig {
		return response, fmt.Errorf("Proxy 组状态响应为空或超过大小限制")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&response); err != nil || response.Proxies == nil {
		return ProxiesResponse{}, fmt.Errorf("Proxy 组状态响应无效")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ProxiesResponse{}, fmt.Errorf("Proxy 组状态响应必须是单个 JSON 对象")
	}
	return response, nil
}

func selectedGroup(kind string) bool {
	return kind == "Selector" || kind == "URLTest" || kind == "Fallback"
}

func groupEntry(state ProxyState) bool {
	return selectedGroup(state.Type) || state.Type == "LoadBalance" || state.Type == "Relay" || state.All != nil || state.Now != ""
}

// These are concrete outbound types returned by the native API. An unknown
// type is reported as unknown rather than assumed to be a terminal node.
func terminalProxy(kind string) bool {
	switch kind {
	case "Direct", "Reject", "RejectDrop", "Compatible", "Dns", "Shadowsocks", "ShadowsocksR", "Snell", "Socks5", "Http", "Vmess", "Vless", "Trojan", "Hysteria", "Hysteria2", "WireGuard", "Tuic", "Ssh", "Mieru", "AnyTLS", "Sudoku", "Masque", "TrustTunnel", "ShadowQuic", "OpenVPN", "Tailscale", "ZeroTier", "EasyTier", "GostRelay":
		return true
	}
	return false
}

// ResolveGroups follows only the currently reported now selections. It does
// not select the first candidate, infer dialer-proxy chains, sum delays, fetch
// providers, or initiate delay measurements. Returned measurements belong only
// to the final node's existing history, not the whole selection chain.
func ResolveGroups(response ProxiesResponse) []GroupStatus {
	names := make([]string, 0, len(response.Proxies))
	for name, state := range response.Proxies {
		if groupEntry(state) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := make([]GroupStatus, 0, len(names))
	for _, name := range names {
		status := GroupStatus{Name: name, Type: response.Proxies[name].Type, DelayState: "unmeasured"}
		seen := make(map[string]bool)
		current := name
		for {
			status.Chain = append(status.Chain, current)
			if seen[current] {
				status.State, status.Reason = "cycle", "当前选择存在循环"
				break
			}
			if len(seen) >= 64 {
				status.State, status.Reason = "unknown", "当前选择链过深"
				break
			}
			seen[current] = true
			state, found := response.Proxies[current]
			if !found {
				status.State, status.Reason = "missing", "当前选择引用的节点不存在"
				break
			}
			if state.Type == "LoadBalance" {
				status.State, status.Reason = "load_balance", "负载均衡没有唯一当前节点"
				break
			}
			if selectedGroup(state.Type) {
				if state.Now == "" {
					status.State, status.Reason = "unselected", "核心未报告当前选择"
					break
				}
				current = state.Now
				continue
			}
			if groupEntry(state) || !terminalProxy(state.Type) {
				status.State, status.Reason = "unknown", "该类型没有可确认的单一当前节点"
				break
			}
			status.State, status.Node = "resolved", current
			var latest *ProxyHistory
			for index := range state.History {
				entry := &state.History[index]
				if !entry.Time.IsZero() && (latest == nil || !entry.Time.Before(latest.Time)) {
					latest = entry
				}
			}
			if latest != nil {
				measuredAt := latest.Time
				status.MeasuredAt = &measuredAt
				if latest.Delay > 0 && latest.Delay <= 65535 {
					delay := latest.Delay
					status.DelayState, status.DelayMS = "measured", &delay
				} else {
					status.DelayState = "unavailable"
				}
			}
			break
		}
		result = append(result, status)
	}
	return result
}

// String is a compact read-only overview. Control characters in native names
// are quoted so a node label cannot create terminal commands or extra rows.
func (s GroupStatus) String() string {
	chain := make([]string, len(s.Chain))
	for index, name := range s.Chain {
		if strings.ContainsFunc(name, unicode.IsControl) {
			name = strconv.Quote(name)
		}
		chain[index] = name
	}
	line := strings.Join(chain, " -> ")
	if line == "" {
		line = strconv.Quote(s.Name)
	}
	if s.State != "resolved" {
		return line + "：" + s.Reason
	}
	if s.DelayMS != nil && s.MeasuredAt != nil {
		return fmt.Sprintf("%s：%d ms（%s，已有节点测量）", line, *s.DelayMS, s.MeasuredAt.Format(time.RFC3339))
	}
	if s.DelayState == "unavailable" {
		return line + "：最近缓存测量无有效延迟"
	}
	return line + "：未测量"
}
