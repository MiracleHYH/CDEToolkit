// Package selfupdate prepares a verified Nido release without executing or
// installing it. The CLI owns confirmation and its local installation lock.
package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

const (
	repository  = "MiracleHYH/CDEToolkit"
	releasesAPI = "https://api.github.com/repos/" + repository + "/releases"
	maxMetadata = 4 << 20
	maxArchive  = 128 << 20
	maxBinary   = 512 << 20
)

var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var assetDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var releaseID = regexp.MustCompile(`^[1-9][0-9]*$`)

type Service struct {
	HTTP *http.Client
	// APIHTTP, when set, is the explicit network route for release metadata.
	// Metadata keeps its official URL and never uses GitHub file accelerators.
	APIHTTP     *http.Client
	GitHubProxy string
	Platform    string
}

type Info struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
}

type Prepared struct {
	Data    []byte `json:"-"`
	Version string `json:"version"`
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type release struct {
	Tag        string  `json:"tag_name"`
	URL        string  `json:"url"`
	HTMLURL    string  `json:"html_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

func normalizeVersion(version string) (string, error) {
	if !ValidVersion(version) {
		return "", fmt.Errorf("Nido 自身更新只支持稳定版本 X.Y.Z；开发构建请使用原安装方式")
	}
	return version, nil
}

// ValidVersion accepts the bare stable version used by nido version and CLI
// arguments. The GitHub tag prefix is added only when querying a release.
func ValidVersion(version string) bool { return stableVersion.MatchString(version) }

// IsNewer compares only valid stable versions. Unknown/development versions
// never authorize replacing an installed program or downgrading its schema.
func IsNewer(current, candidate string) bool {
	return ValidVersion(current) && ValidVersion(candidate) && less(current, candidate)
}

// Check fetches only release metadata. It never downloads or executes a CLI.
func (s Service) Check(current string) (Info, error) {
	var result Info
	var err error
	result.Current, err = normalizeVersion(current)
	if err != nil {
		return result, err
	}
	if _, err := s.platform(); err != nil {
		return result, err
	}
	r, err := s.release("")
	if err != nil {
		return result, err
	}
	result.Latest = strings.TrimPrefix(r.Tag, "v")
	result.Available = IsNewer(result.Current, result.Latest)
	return result, nil
}

func less(a, b string) bool {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := range x {
		if len(x[i]) != len(y[i]) {
			return len(x[i]) < len(y[i])
		}
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

func (s Service) release(version string) (release, error) {
	source := releasesAPI + "/latest"
	expectedTag := ""
	if version != "" {
		normalized, err := normalizeVersion(version)
		if err != nil {
			return release{}, err
		}
		expectedTag = "v" + normalized
		source = releasesAPI + "/tags/" + expectedTag
	}
	client, policy := s.HTTP, s.GitHubProxy
	if s.APIHTTP != nil {
		client, policy = s.APIHTTP, "direct"
	}
	b, err := githubdownload.Get(client, source, maxMetadata, policy)
	if err != nil {
		return release{}, fmt.Errorf("获取 Nido 发布信息失败: %w", err)
	}
	var r release
	if json.Unmarshal(b, &r) != nil || !strings.HasPrefix(r.Tag, "v") || !stableVersion.MatchString(strings.TrimPrefix(r.Tag, "v")) || r.Draft || r.Prerelease || expectedTag != "" && r.Tag != expectedTag {
		return release{}, fmt.Errorf("Nido 发布信息不是匹配的稳定版本")
	}
	if r.HTMLURL != "" && r.HTMLURL != "https://github.com/"+repository+"/releases/tag/"+r.Tag {
		return release{}, fmt.Errorf("Nido 发布页面与官方仓库或版本不符")
	}
	if r.URL != "" && (!strings.HasPrefix(r.URL, releasesAPI+"/") || !releaseID.MatchString(strings.TrimPrefix(r.URL, releasesAPI+"/"))) {
		return release{}, fmt.Errorf("Nido 发布 API 地址与官方仓库不符")
	}
	return r, nil
}

func (s Service) platform() (string, error) {
	platform := s.Platform
	if platform == "" {
		platform = runtime.GOOS + "/" + runtime.GOARCH
	}
	switch platform {
	case "linux/amd64":
		return platform, nil
	default:
		return "", fmt.Errorf("Nido 自身更新仅支持 Linux amd64")
	}
}

func selectAsset(r release, name string, maximum int64) (asset, error) {
	var selected asset
	count := 0
	for _, a := range r.Assets {
		if a.Name == name {
			selected = a
			count++
		}
	}
	expectedURL := "https://github.com/" + repository + "/releases/download/" + r.Tag + "/" + name
	if count != 1 || selected.URL != expectedURL || !assetDigest.MatchString(selected.Digest) || selected.Size <= 0 || selected.Size > maximum {
		return asset{}, fmt.Errorf("Nido 发布资产 %s 缺失、重复，或地址、摘要、大小不符", name)
	}
	return selected, nil
}

func (s Service) download(a asset, maximum int64) ([]byte, error) {
	b, err := githubdownload.Get(s.HTTP, a.URL, maximum, s.GitHubProxy)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != a.Size || "sha256:"+hex.EncodeToString(sum[:]) != a.Digest {
		return nil, fmt.Errorf("Nido 资产内容与发布 API 的大小或摘要不符")
	}
	return b, nil
}

// Prepare checks GitHub metadata, both checksums, the archive and Go build
// identity. It returns bytes only; it does not alter or execute any program.
func (s Service) Prepare(version string) (Prepared, error) {
	platform, err := s.platform()
	if err != nil {
		return Prepared{}, err
	}
	r, err := s.release(version)
	if err != nil {
		return Prepared{}, err
	}
	name := "nido-" + strings.ReplaceAll(platform, "/", "-") + ".tar.gz"
	program, err := selectAsset(r, name, maxArchive)
	if err != nil {
		return Prepared{}, err
	}
	checksum, err := selectAsset(r, name+".sha256", 256)
	if err != nil {
		return Prepared{}, err
	}
	check, err := s.download(checksum, 256)
	if err != nil {
		return Prepared{}, fmt.Errorf("获取 Nido 校验文件失败: %w", err)
	}
	if string(check) != strings.TrimPrefix(program.Digest, "sha256:")+"  "+name+"\n" {
		return Prepared{}, fmt.Errorf("Nido 校验文件与发布 API 摘要不一致")
	}
	archive, err := s.download(program, maxArchive)
	if err != nil {
		return Prepared{}, fmt.Errorf("下载 Nido 发布包失败: %w", err)
	}
	binary, err := unpack(archive)
	if err != nil {
		return Prepared{}, err
	}
	if err := checkBinary(binary, platform); err != nil {
		return Prepared{}, err
	}
	return Prepared{Data: binary, Version: strings.TrimPrefix(r.Tag, "v")}, nil
}
