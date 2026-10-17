package proxy

import "testing"

func TestEndpointSelectionIsExplicit(t *testing.T) {
	config, err := ParseConfig([]byte("port: 7890\nsocks-port: 7891\n"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.Endpoint(""); err == nil {
		t.Fatal("ambiguous endpoints selected implicitly")
	}
	endpoint, err := config.Endpoint("socks")
	if err != nil || endpoint.URL() != "socks5h://127.0.0.1:7891" {
		t.Fatalf("explicit endpoint = %+v, err = %v", endpoint, err)
	}
	if _, err := config.Endpoint("private-input"); err == nil || err.Error() == "private-input" {
		t.Fatal("unknown endpoint accepted")
	}
}
