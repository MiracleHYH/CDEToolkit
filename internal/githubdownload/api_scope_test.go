package githubdownload

import (
	"net/http"
	"net/url"
	"testing"
)

func TestAPIScopeRetainsOfficialAuthorityAndDoesNotAcceptAssetHosts(t *testing.T) {
	for _, source := range []string{
		"https://api.github.com/repos/a/b/releases/latest",
		"https://API.GITHUB.COM:443/repos/a/b/releases/latest",
	} {
		request, _ := http.NewRequest(http.MethodGet, source, nil)
		if err := CheckAPIRequest(request); err != nil {
			t.Fatal("official API rejected", source, err)
		}
	}
	for _, source := range []string{
		"http://api.github.com/", "https://api.github.com:444/", "https://api.github.com:/",
		"https://api.github.com.invalid/", "https://api.github.com./",
		"https://user:secret@api.github.com/", "https://api.github.com/#fragment",
		"https://github.com/asset", "https://raw.githubusercontent.com/asset",
		"https://release-assets.githubusercontent.com/asset", "https://objects.githubusercontent.com/asset",
		"https://github-releases.githubusercontent.com/asset",
	} {
		target, err := url.Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		request := &http.Request{Method: http.MethodGet, URL: target, Host: target.Host, Header: make(http.Header)}
		if err := CheckAPIRequest(request); err == nil {
			t.Fatal("non-API authority accepted", source)
		}
	}
	for _, request := range []*http.Request{nil, {URL: nil}, {URL: &url.URL{Scheme: "https", Host: "[api.github.com]"}}, {URL: &url.URL{Scheme: "https", Host: "api.github.com", Opaque: "//api.github.com/"}}} {
		if err := CheckAPIRequest(request); err == nil {
			t.Fatal("invalid raw request accepted")
		}
	}
	request, _ := http.NewRequest(http.MethodGet, "https://api.github.com/", nil)
	request.Host = "another.invalid"
	if err := CheckAPIRequest(request); err == nil {
		t.Fatal("custom Host accepted")
	}
}
