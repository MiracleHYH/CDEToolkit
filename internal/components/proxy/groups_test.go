package proxy

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func groupResult(t *testing.T, response ProxiesResponse, name string) GroupStatus {
	t.Helper()
	for _, status := range ResolveGroups(response) {
		if status.Name == name {
			return status
		}
	}
	t.Fatal("group result missing", name)
	return GroupStatus{}
}

func TestResolveGroupsUsesCurrentSelectionAndFinalNodeHistoryOnly(t *testing.T) {
	data := []byte(`{"proxies":{
	  "proxy":{"type":"Selector","now":"auto","all":["wrong","auto"],"history":[{"time":"2026-10-19T10:00:00Z","delay":500}]},
	  "auto":{"type":"URLTest","now":"leaf","all":["leaf"],"history":[{"time":"2026-10-19T11:00:00Z","delay":200}]},
	  "leaf":{"name":"leaf","type":"Shadowsocks","dialer-proxy":"relay","history":[{"time":"2026-10-19T09:00:00Z","delay":42},{"time":"2026-10-19T08:00:00Z","delay":81}]},
	  "relay":{"type":"Selector","now":"wrong","all":["wrong"]},
	  "wrong":{"type":"Http"}
	}}`)
	response, err := ParseProxiesResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(response)
	status := groupResult(t, response, "proxy")
	if status.State != "resolved" || status.Node != "leaf" || !reflect.DeepEqual(status.Chain, []string{"proxy", "auto", "leaf"}) || status.DelayState != "measured" || status.DelayMS == nil || *status.DelayMS != 42 || status.MeasuredAt == nil || status.MeasuredAt.Format(time.RFC3339) != "2026-10-19T09:00:00Z" {
		t.Fatalf("incorrect selected chain/cache: %+v", status)
	}
	line := status.String()
	for _, expected := range []string{"proxy -> auto -> leaf", "42 ms", "2026-10-19T09:00:00Z", "已有节点测量"} {
		if !strings.Contains(line, expected) {
			t.Fatalf("overview omitted %q: %s", expected, line)
		}
	}
	if strings.Contains(line, "relay") || strings.Contains(line, "742") {
		t.Fatal("overview inferred a dialer chain or summed group and node delays")
	}
	statuses := ResolveGroups(response)
	if len(statuses) != 3 || statuses[0].Name != "auto" || statuses[1].Name != "proxy" || statuses[2].Name != "relay" {
		t.Fatalf("group order not stable: %+v", statuses)
	}
	after, _ := json.Marshal(response)
	if string(before) != string(after) {
		t.Fatal("resolution mutated its native input")
	}
}

func TestResolveGroupsReportsAmbiguousMissingAndUnmeasuredState(t *testing.T) {
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, kind, selection, expectedState, expectedDelay string
		leaf                                                ProxyState
	}{
		{"load balance", "LoadBalance", "leaf", "load_balance", "unmeasured", ProxyState{Type: "Http"}},
		{"unknown group", "FutureGroup", "leaf", "unknown", "unmeasured", ProxyState{Type: "Http"}},
		{"relay group", "Relay", "leaf", "unknown", "unmeasured", ProxyState{Type: "Http"}},
		{"no current selection", "Selector", "", "unselected", "unmeasured", ProxyState{Type: "Http"}},
		{"missing selection", "Selector", "missing", "missing", "unmeasured", ProxyState{Type: "Http"}},
		{"unknown node", "Selector", "leaf", "unknown", "unmeasured", ProxyState{Type: "FutureNode"}},
		{"routing control", "Selector", "leaf", "unknown", "unmeasured", ProxyState{Type: "PassRule"}},
		{"unmeasured node", "Selector", "leaf", "resolved", "unmeasured", ProxyState{Type: "Http"}},
		{"missing measurement time", "Selector", "leaf", "resolved", "unmeasured", ProxyState{Type: "Http", History: []ProxyHistory{{Delay: 50}}}},
		{"zero delay", "Selector", "leaf", "resolved", "unavailable", ProxyState{Type: "Http", History: []ProxyHistory{{Time: now, Delay: 0}}}},
		{"negative delay", "Selector", "leaf", "resolved", "unavailable", ProxyState{Type: "Http", History: []ProxyHistory{{Time: now, Delay: -1}}}},
		{"overflow delay", "Selector", "leaf", "resolved", "unavailable", ProxyState{Type: "Http", History: []ProxyHistory{{Time: now, Delay: 65536}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := ProxiesResponse{Proxies: map[string]ProxyState{
				"group": {Type: test.kind, Now: test.selection, All: []string{"leaf"}},
				"leaf":  test.leaf,
			}}
			status := groupResult(t, response, "group")
			if status.State != test.expectedState || status.DelayState != test.expectedDelay || status.DelayMS != nil {
				t.Fatalf("ambiguous state guessed a current node or latency: %+v", status)
			}
			if test.expectedState != "resolved" && (status.Node != "" || status.Reason == "") {
				t.Fatalf("unknown selection lacks an honest reason: %+v", status)
			}
			if strings.Contains(status.String(), "0 ms") || status.String() == "" {
				t.Fatal("unmeasured/failed cache was presented as zero latency")
			}
		})
	}
}

func TestResolveGroupsBoundsCyclesAndSelectionDepth(t *testing.T) {
	response := ProxiesResponse{Proxies: map[string]ProxyState{
		"a": {Type: "Selector", Now: "b"}, "b": {Type: "Fallback", Now: "a"},
	}}
	status := groupResult(t, response, "a")
	if status.State != "cycle" || !reflect.DeepEqual(status.Chain, []string{"a", "b", "a"}) || !strings.Contains(status.String(), "循环") {
		t.Fatalf("cycle did not terminate honestly: %+v", status)
	}
	response = ProxiesResponse{Proxies: make(map[string]ProxyState)}
	for index := 0; index < 70; index++ {
		response.Proxies[fmt.Sprint(index)] = ProxyState{Type: "Selector", Now: fmt.Sprint(index + 1)}
	}
	status = groupResult(t, response, "0")
	if status.State != "unknown" || len(status.Chain) > 65 || !strings.Contains(status.Reason, "过深") {
		t.Fatal("selection depth was not bounded")
	}
}

func TestGroupStatusStringQuotesControlCharacters(t *testing.T) {
	status := GroupStatus{Chain: []string{"日常", "node\n\x1b[31m"}, State: "resolved", DelayState: "unmeasured"}
	line := status.String()
	if strings.ContainsAny(line, "\n\x1b") || !strings.Contains(line, "日常") || !strings.Contains(line, `\n\x1b`) || !strings.Contains(line, "未测量") {
		t.Fatalf("unsafe or misleading group display: %q", line)
	}
}

func TestParseProxiesResponseRejectsInvalidDataWithoutLeakingPayload(t *testing.T) {
	for _, data := range []string{"", "null", "[]", "{}", `{"proxies":null}`, `{"proxies":{}} {}`, `{"proxies":{"g":{"history":[{"time":"secret-malformed-time"}]}}}`, strings.Repeat("x", maxConfig+1)} {
		if _, err := ParseProxiesResponse([]byte(data)); err == nil || strings.Contains(err.Error(), "secret-malformed-time") {
			t.Fatalf("invalid or secret-bearing response error: %v", err)
		}
	}
	response, err := ParseProxiesResponse([]byte(`{"proxies":{}}`))
	if err != nil || len(ResolveGroups(response)) != 0 {
		t.Fatalf("empty native group list rejected: %v", err)
	}
}
