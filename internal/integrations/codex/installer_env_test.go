package codex

import (
	"reflect"
	"strings"
	"testing"
)

func TestInstallerEnvironmentUsesOnlyExplicitProxyAndPreservesCaller(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{true: "direct", false: "explicit-proxy"}[direct], func(t *testing.T) {
			s, home := fixture(t)
			s.Env = append(s.Env,
				"http_proxy=http://stale.invalid:8080", "HTTPS_PROXY=http://stale.invalid:8081",
				"ALL_PROXY=socks5://stale.invalid:8082", "No_Proxy=*", "NO_PROXY=*",
				"CODEX_CUSTOM=preserve")
			before := append([]string(nil), s.Env...)
			var temporary map[string]string
			if !direct {
				temporary = map[string]string{
					"http_proxy": "http://127.0.0.1:17890", "https_proxy": "http://127.0.0.1:17890",
					"all_proxy": "http://127.0.0.1:17890", "no_proxy": "localhost,127.0.0.1,::1",
				}
			}
			env, err := s.installerEnv(Config{Home: home}, temporary, "/isolated-temporary-home", "/selected-bin")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range env {
				if strings.Contains(entry, "stale.invalid") || entry == "No_Proxy=*" || entry == "NO_PROXY=*" {
					t.Fatalf("inherited proxy escaped the chosen route: %q", entry)
				}
			}
			for _, key := range []string{"http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
				if got := envValue(env, key); got != temporary[key] || envValue(env, strings.ToUpper(key)) != temporary[key] {
					t.Fatalf("unexpected temporary proxy value for %s", key)
				}
			}
			if !reflect.DeepEqual(s.Env, before) || envValue(env, "CODEX_CUSTOM") != "preserve" {
				t.Fatal("changed the caller environment or unrelated Codex settings")
			}
		})
	}
}
