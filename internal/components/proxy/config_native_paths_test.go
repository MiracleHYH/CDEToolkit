package proxy

import (
	"fmt"
	"testing"
)

func TestParseConfigResourcesCannotAliasNativeUpdatePaths(t *testing.T) {
	for _, path := range []string{"meta-update", "meta-update/provider.yaml", "META-UPDATE/provider.yaml", "meta-backup", "meta-backup/provider.yaml", "providers/../meta-backup"} {
		for _, section := range []string{"proxy-providers", "rule-providers", "tls"} {
			t.Run(section+"/"+path, func(t *testing.T) {
				data := fmt.Sprintf("mixed-port: 7890\n%s: {p: {type: file, path: %q}}\n", section, path)
				if section == "tls" {
					data = fmt.Sprintf("mixed-port: 7890\ntls: {certificate: %q}\n", path)
				}
				if _, err := ParseConfig([]byte(data), t.TempDir()); err == nil {
					t.Fatal("resource alias into native updater state was accepted")
				}
			})
		}
	}
	if _, err := ParseConfig([]byte("mixed-port: 7890\nproxy-providers: {p: {type: file, path: providers/subscription.yaml}}\n"), t.TempDir()); err != nil {
		t.Fatalf("ordinary provider path was rejected: %v", err)
	}
}
