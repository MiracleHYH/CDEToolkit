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

func TestAPIBindingAuditConcurrentConfirmationEdits(t *testing.T) {
	for _, edit := range []string{"created", "edited", "replaced-identically", "removed", "permissions", "hardlink"} {
		for _, action := range []string{"switch", "off", "same"} {
			t.Run(edit+"/"+action, func(t *testing.T) {
				m, _, _ := proxyBindingFixture(t)
				writeBindingListeners(t, m, bindingListeners)
				path := filepath.Join(m.Root, apiProxyFile)
				data := []byte("schema=1\nproduct='nido-api-proxy'\nlistener='renamed-ai'\n")
				if edit != "created" {
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"--root", m.Root, "proxy", "bind", "download"}
				switch action {
				case "switch":
					args = append(args, "--endpoint", "arbitrary-daily")
				case "same":
					args = append(args, "--endpoint", "renamed-ai")
				case "off":
					args = append(args, "--proxy", "off")
				}
				var afterEdit map[string]bootstrapFile
				var out, stderr bytes.Buffer
				runner := &cliProxyRunner{}
				cli := CLI{In: &beforeRead{reader: strings.NewReader("y\n"), before: func() {
					var err error
					switch edit {
					case "created", "edited":
						err = os.WriteFile(path, []byte("schema=1\nproduct='nido-api-proxy'\nlistener='concurrent-user-choice'\n"), 0600)
					case "replaced-identically":
						temporary := path + ".user-edit"
						if err = os.WriteFile(temporary, data, 0600); err == nil {
							err = os.Rename(temporary, path)
						}
					case "removed":
						err = os.Remove(path)
					case "permissions":
						err = os.Chmod(path, 0644)
					case "hardlink":
						err = os.Link(path, filepath.Join(m.UserHome, "keep-linked-binding"))
					}
					if err != nil {
						t.Fatal(err)
					}
					afterEdit = proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
				}}, Out: &out, Err: &stderr, ProxyRunner: runner}
				if code := cli.Run(args); code == 0 || !strings.Contains(stderr.String(), "发生变化") {
					t.Fatalf("concurrent receipt edit accepted: %d %s %s", code, out.String(), stderr.String())
				}
				if afterEdit == nil || !reflect.DeepEqual(afterEdit, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
					t.Fatal("refusal changed the concurrent binding or unrelated configuration/data")
				}
				if runner.starts != 0 || len(runner.commands) != 0 {
					t.Fatal("refused binding invoked a component")
				}
			})
		}
	}
}

type apiBindingAuditBody struct {
	io.Reader
	close func()
}

func (b *apiBindingAuditBody) Close() error { b.close(); return nil }

func TestAPIBindingAuditCloseFailureCannotReturnMetadata(t *testing.T) {
	m, _, _ := proxyBindingFixture(t)
	path := filepath.Join(m.Root, apiProxyFile)
	if err := os.WriteFile(path, []byte("schema=1\nproduct='nido-api-proxy'\nlistener='renamed-ai'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := m.readAPIProxy()
	if err != nil {
		t.Fatal(err)
	}
	concurrent := []byte("schema=1\nproduct='nido-api-proxy'\nlistener=''\n")
	client := &http.Client{Transport: apiBindingTransport{
		check: snapshot.check,
		base: updateCLITransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: &apiBindingAuditBody{
				Reader: strings.NewReader(`{"tag_name":"v9.9.9"}`), close: func() {
					if err := os.WriteFile(path, concurrent, 0600); err != nil {
						t.Fatal(err)
					}
				},
			}}, nil
		}),
	}}
	data, err := githubdownload.Get(client, "https://api.github.com/repos/MiracleHYH/CDEToolkit/releases/latest", 4096, "direct")
	if err == nil || len(data) != 0 {
		t.Fatal("binding changed after EOF but Get returned usable metadata", string(data), err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, concurrent) {
		t.Fatal("failed request undid a user's concurrent off operation", err)
	}
}
