package cdet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Release is provenance, not a package manager database. Source URLs are never
// persisted: custom URLs can carry credentials or temporary query tokens.
type Release struct {
	Repository string `json:"repository,omitempty"`
	Version    string `json:"version,omitempty"`
	Asset      string `json:"asset,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	Platform   string `json:"platform,omitempty"`
}

type Artifact struct {
	Release
	Source string `json:"-"`
}

// The only transport seam is per Manager, so isolated tests never change a
// process-wide client or contact the real upstream.
type ReleaseClient struct {
	HTTP *http.Client
	API  string
	OS   string
	Arch string
	// Test-only ownership seam; production verifies the listening socket's
	// process and executable before sending controller credentials.
	MihomoOwner func(endpoint, program string) (bool, error)
}

func newReleaseClient() *ReleaseClient {
	return &ReleaseClient{API: "https://api.github.com", OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func (m *Manager) releases() *ReleaseClient {
	if m.Releases == nil {
		m.Releases = newReleaseClient()
	}
	return m.Releases
}

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32
}

func secureURL(source string) (*url.URL, error) {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("下载地址必须是无用户信息和片段的 HTTPS URL")
	}
	return u, nil
}

func (c *ReleaseClient) get(source string, limit int64) ([]byte, error) {
	if _, err := secureURL(source); err != nil {
		return nil, err
	}
	base := c.HTTP
	if base == nil {
		base = &http.Client{Timeout: 2 * time.Minute}
	}
	client := *base
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if _, err := secureURL(req.URL.String()); err != nil || len(via) > 5 {
			return fmt.Errorf("拒绝不安全重定向")
		}
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, source, nil)
	if err != nil {
		return nil, fmt.Errorf("无效下载地址")
	}
	req.Header.Set("User-Agent", "CDEToolkit")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTPS 请求失败（地址未输出）")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("下载大小越界")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("下载不完整或大小越界")
	}
	return b, nil
}

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func releaseTag(name, version string) (string, error) {
	v := strings.TrimPrefix(version, "v")
	if name == "codex" {
		v = strings.TrimPrefix(version, "rust-v")
		v = strings.TrimPrefix(v, "v")
	}
	if !versionPattern.MatchString(v) {
		return "", fmt.Errorf("版本必须是 latest 或稳定版 X.Y.Z")
	}
	if name == "codex" {
		return "rust-v" + v, nil
	}
	return "v" + v, nil
}

func mihomoAssetNames(tag, goos, arch string) ([]string, error) {
	if goos != "linux" && goos != "darwin" {
		return nil, fmt.Errorf("在线安装仅支持 Linux/macOS")
	}
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("在线安装仅支持 amd64/arm64；其他平台可使用显式 artifact")
	}
	arches := []string{arch}
	if arch == "amd64" {
		// Unsuffixed amd64 can require v3. Never silently select that build.
		arches = []string{"amd64-v1", "amd64-compatible"}
	}
	var names []string
	for _, a := range arches {
		names = append(names, "mihomo-"+goos+"-"+a+"-"+tag+".gz")
	}
	return names, nil
}

func (c *ReleaseClient) latestVersion(name string) (string, error) {
	repo := map[string]string{"proxy": "MetaCubeX/mihomo", "codex": "openai/codex"}[name]
	if repo == "" {
		return "", fmt.Errorf("%s 没有内置版本查询源", name)
	}
	b, e := c.get(strings.TrimRight(c.API, "/")+"/repos/"+repo+"/releases/latest", maxMetadata)
	if e != nil {
		return "", e
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if json.Unmarshal(b, &release) != nil {
		return "", fmt.Errorf("发布元数据无效")
	}
	tag, e := releaseTag(name, release.Tag)
	if e != nil || tag != release.Tag || release.Draft || release.Prerelease {
		return "", fmt.Errorf("最新发布不是有效稳定版")
	}
	if name == "codex" {
		return strings.TrimPrefix(tag, "rust-v"), nil
	}
	return tag, nil
}

func (c *ReleaseClient) resolve(name, version, supplied string) (Artifact, error) {
	if name != "proxy" {
		return Artifact{}, fmt.Errorf("%s 没有内置在线发布源", name)
	}
	repo := "MetaCubeX/mihomo"
	if _, err := mihomoAssetNames("v0.0.0", c.OS, c.Arch); err != nil {
		return Artifact{}, err
	}
	tag := ""
	endpoint := c.API + "/repos/" + repo + "/releases/latest"
	if version != "latest" {
		var err error
		tag, err = releaseTag(name, version)
		if err != nil {
			return Artifact{}, err
		}
		endpoint = c.API + "/repos/" + repo + "/releases/tags/" + tag
	}
	b, err := c.get(endpoint, maxMetadata)
	if err != nil {
		return Artifact{}, err
	}
	var r struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return Artifact{}, fmt.Errorf("发布元数据无效")
	}
	normal, err := releaseTag(name, r.Tag)
	if err != nil || normal != r.Tag || r.Draft || r.Prerelease || tag != "" && r.Tag != tag {
		return Artifact{}, fmt.Errorf("发布版本无效或不是指定稳定版")
	}
	names, _ := mihomoAssetNames(r.Tag, c.OS, c.Arch)
	for _, wanted := range names {
		var found []Artifact
		for _, a := range r.Assets {
			if a.Name != wanted {
				continue
			}
			u, e := secureURL(a.URL)
			if e != nil || u.Host != "github.com" || u.RawQuery != "" || u.Path != "/"+repo+"/releases/download/"+r.Tag+"/"+a.Name {
				return Artifact{}, fmt.Errorf("官方发布包地址与仓库、版本或文件名不符")
			}
			hash := strings.TrimPrefix(a.Digest, "sha256:")
			if !strings.HasPrefix(a.Digest, "sha256:") || !validDigest(hash) || a.Size <= 0 || a.Size > maxDownload {
				return Artifact{}, fmt.Errorf("官方包缺少有效 SHA-256 或大小越界")
			}
			if supplied != "" && !strings.EqualFold(supplied, hash) {
				return Artifact{}, fmt.Errorf("指定 SHA-256 与官方发布摘要不符")
			}
			found = append(found, Artifact{Release: Release{Repository: repo, Version: r.Tag, Asset: a.Name, SHA256: strings.ToLower(hash), Platform: c.OS + "/" + c.Arch}, Source: a.URL})
		}
		if len(found) > 1 {
			return Artifact{}, fmt.Errorf("发布包名称重复")
		}
		if len(found) == 1 {
			return found[0], nil
		}
	}
	return Artifact{}, fmt.Errorf("发布 %s 缺少兼容的 %s/%s 包", r.Tag, c.OS, c.Arch)
}

func (m *Manager) artifact(name, source string) (Artifact, error) {
	if name == "codex" {
		return Artifact{}, fmt.Errorf("Codex 程序由其上游安装工具管理，不使用 cdet 原生发布包")
	}
	hash := m.Options.Digests[name]
	if hash != "" && !validDigest(hash) {
		return Artifact{}, fmt.Errorf("SHA-256 必须是 64 位十六进制摘要")
	}
	if source == "latest" || !strings.Contains(source, "/") {
		client, e := m.networkReleases()
		if e != nil {
			return Artifact{}, e
		}
		return client.resolve(name, source, hash)
	}
	if strings.HasPrefix(source, "https://") {
		if _, err := secureURL(source); err != nil {
			return Artifact{}, err
		}
		if !validDigest(hash) {
			return Artifact{}, fmt.Errorf("自定义 HTTPS 安装必须提供 --sha256 NAME=摘要")
		}
	} else if err := absolute(source); err != nil {
		return Artifact{}, err
	}
	return Artifact{Release: Release{SHA256: strings.ToLower(hash)}, Source: source}, nil
}
