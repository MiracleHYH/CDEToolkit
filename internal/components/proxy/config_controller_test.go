package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseConfigControllerPrivateAndCanonical(t *testing.T) {
	for _, test := range []struct{ address, canonical string }{
		{"127.0.0.1:9090", "127.0.0.1:9090"},
		{"[::1]:9090", "[::1]:9090"},
		{"[::ffff:127.0.0.1]:09090", "127.0.0.1:9090"},
	} {
		t.Run(test.address, func(t *testing.T) {
			data := []byte("mixed-port: 7890\nexternal-controller: '" + test.address + "'\nsecret: private-controller-token\n")
			config, err := ParseConfig(data, t.TempDir())
			if err != nil || config.Controller == nil {
				t.Fatalf("controller missing: %v", err)
			}
			if config.Controller.Address != test.canonical || config.Controller.Secret != "private-controller-token" || config.Digest != hash(data) || !validChecksum(config.Controller.Digest()) {
				t.Fatal("controller authority, secret or digest changed")
			}
			encoded, err := json.Marshal(config)
			if err != nil || strings.Contains(string(encoded), "private-controller-token") || !strings.Contains(string(encoded), test.canonical) {
				t.Fatalf("controller JSON did not retain only public metadata: %s %v", encoded, err)
			}
		})
	}
	config, err := ParseConfig([]byte("mixed-port: 7890\n"), t.TempDir())
	if err != nil || config.Controller != nil {
		t.Fatalf("advanced config without controller rejected: %v", err)
	}
}

func TestParseConfigControllerRejectsInvalidOrConflictingAuthority(t *testing.T) {
	for name, controller := range map[string]string{
		"public":          "external-controller: 192.0.2.1:9090\nsecret: token\n",
		"wildcard":        "external-controller: 0.0.0.0:9090\nsecret: token\n",
		"hostname":        "external-controller: localhost:9090\nsecret: token\n",
		"url":             "external-controller: http://127.0.0.1:9090\nsecret: token\n",
		"zero port":       "external-controller: 127.0.0.1:0\nsecret: token\n",
		"signed port":     "external-controller: '127.0.0.1:+9090'\nsecret: token\n",
		"empty secret":    "external-controller: 127.0.0.1:9090\n",
		"secret type":     "external-controller: 127.0.0.1:9090\nsecret: false\n",
		"secret blank":    "external-controller: 127.0.0.1:9090\nsecret: ' '\n",
		"secret newline":  "external-controller: 127.0.0.1:9090\nsecret: \"bad\\nsecret\"\n",
		"secret too long": "external-controller: 127.0.0.1:9090\nsecret: '" + strings.Repeat("x", 4097) + "'\n",
		"proxy conflict":  "external-controller: 127.0.0.1:7890\nsecret: token\n",
		"mapped conflict": "external-controller: '[::ffff:127.0.0.1]:7890'\nsecret: token\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig([]byte("mixed-port: 7890\n"+controller), t.TempDir()); err == nil {
				t.Fatal("invalid controller was accepted")
			}
		})
	}
	if _, err := ParseConfig([]byte("mixed-port: 7890\nexternal-controller: 127.0.0.2:7890\nsecret: token\n"), t.TempDir()); err != nil {
		t.Fatalf("distinct loopback authorities were conflated: %v", err)
	}
}

func TestControllerDigestBindsSecretAndAuthorityOnly(t *testing.T) {
	base := ControllerConfig{Address: DefaultControllerAddress, Secret: "first-token"}
	if base.Digest() == (ControllerConfig{Address: base.Address, Secret: "second-token"}).Digest() || base.Digest() == (ControllerConfig{Address: "127.0.0.1:9091", Secret: base.Secret}).Digest() {
		t.Fatal("controller authorization changes did not alter its digest")
	}
}
