package nido

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

func TestNetworkConcurrentConfigurationEditsArePreserved(t *testing.T) {
	for _, edit := range []string{"edited", "replaced-identically", "removed", "symlink", "hardlink"} {
		t.Run(edit, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.InitWithDownload("direct"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(m.Root, "config.toml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch edit {
			case "edited":
				err = os.WriteFile(path, append(data, []byte("# USER EDIT\n")...), 0600)
			case "replaced-identically":
				err = os.WriteFile(path+".user", data, 0600)
				if err == nil {
					err = os.Rename(path+".user", path)
				}
			case "removed":
				err = os.Remove(path)
			case "symlink":
				err = os.Rename(path, path+".original")
				if err == nil {
					err = os.Symlink(path+".original", path)
				}
			case "hardlink":
				err = os.Link(path, filepath.Join(home, "user-config-link"))
			}
			if err != nil {
				t.Fatal(err)
			}
			before := proxyBootstrapSnapshot(t, "", m.Root, home)
			// The ordinary managed configuration writer must retain its original
			// receipt; network settings do not create an independent transaction file.
			err = m.setNetwork(DownloadConfig{GitHubAccelerate: "https://gh-proxy.org"})
			if edit == "replaced-identically" || edit == "hardlink" {
				// Same bytes under the normal owned config path are accepted by the
				// existing manager contract; no network-specific inode policy is added.
				if err != nil {
					t.Fatal(err)
				}
				if edit == "hardlink" {
					linked, err := os.ReadFile(filepath.Join(home, "user-config-link"))
					if err != nil || !bytes.Equal(linked, data) {
						t.Fatal("atomic save overwrote linked user data", err)
					}
				}
			} else {
				if err == nil {
					t.Fatal("unsafe/stale configuration was accepted", edit)
				}
				if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
					t.Fatal("refusal overwrote user edit")
				}
			}
		})
	}
}

type apiBindingAuditBody struct {
	io.Reader
	close func()
}

func (b *apiBindingAuditBody) Close() error { b.close(); return nil }
func TestAPIBindingAuditCloseFailureCannotReturnMetadata(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Root, "config.toml")
	data, _ := os.ReadFile(path)
	concurrent := append(append([]byte(nil), data...), []byte("# concurrent user choice\n")...)
	protected := proxyBootstrapSnapshot(t, path, m.Root, home)
	client := &http.Client{Transport: apiBindingTransport{check: m.networkCheck(), base: updateCLITransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &apiBindingAuditBody{Reader: strings.NewReader(`{"tag_name":"v9.9.9"}`), close: func() {
			if err := os.WriteFile(path, concurrent, 0600); err != nil {
				t.Fatal(err)
			}
		}}}, nil
	})}}
	response, err := githubdownload.Get(client, "https://api.github.com/repos/MiracleHYH/CDEToolkit/releases/latest", 4096, "direct")
	if err == nil || len(response) != 0 {
		t.Fatal("changed configuration returned usable metadata", string(response), err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, concurrent) {
		t.Fatal("failed request undid concurrent choice")
	}
	if !reflect.DeepEqual(protected, proxyBootstrapSnapshot(t, path, m.Root, home)) {
		t.Fatal("failed request changed unrelated data")
	}
}
