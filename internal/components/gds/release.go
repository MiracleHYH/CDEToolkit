package gds

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsutil"
	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

const releaseRepository = "MiracleHYH/CDEToolkit"
const releaseAsset = "gds-linux-amd64"

var stableReleaseTag = regexp.MustCompile(`^gds-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func officialSource(source string) bool {
	return source == "" || source == "latest" || stableReleaseTag.MatchString(source)
}

// ValidateSource is static: it never opens a file, writes, executes or connects.
// Empty/latest selects the newest stable GDS release, independently of Nido.
func ValidateSource(source, checksum string) error {
	if checksum != "" && !validHash(checksum) {
		return fmt.Errorf("SHA-256 摘要无效")
	}
	if !officialSource(source) && !filepath.IsAbs(source) {
		return fmt.Errorf("GDS 来源必须是 latest、gds-vX.Y.Z 或本地 artifact 绝对路径")
	}
	return nil
}

type releaseAssetInfo struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type releaseInfo struct {
	Tag        string             `json:"tag_name"`
	Draft      bool               `json:"draft"`
	Prerelease bool               `json:"prerelease"`
	Assets     []releaseAssetInfo `json:"assets"`
}

func (s *Service) release(source string) (releaseInfo, error) {
	api := strings.TrimRight(s.API, "/")
	if api == "" {
		api = "https://api.github.com"
	}
	base := api + "/repos/" + releaseRepository + "/releases"
	if source != "" && source != "latest" {
		if !stableReleaseTag.MatchString(source) {
			return releaseInfo{}, fmt.Errorf("无效 GDS 发布标签")
		}
		b, err := s.get(base+"/tags/"+source, 4<<20)
		if err != nil {
			return releaseInfo{}, err
		}
		var release releaseInfo
		if json.Unmarshal(b, &release) != nil || release.Tag != source || release.Draft || release.Prerelease {
			return releaseInfo{}, fmt.Errorf("指定 GDS 发布不是匹配的稳定版本")
		}
		return release, nil
	}
	// /releases/latest belongs to Nido. Scan a bounded complete release list and
	// compare arbitrary-length decimal components, not upload time or lexically.
	var latest releaseInfo
	seen := map[string]bool{}
	for page := 1; page <= 10; page++ {
		b, err := s.get(fmt.Sprintf("%s?per_page=100&page=%d", base, page), 4<<20)
		if err != nil {
			return releaseInfo{}, err
		}
		var releases []releaseInfo
		if string(b) == "null" || json.Unmarshal(b, &releases) != nil || len(releases) > 100 {
			return releaseInfo{}, fmt.Errorf("GDS 发布列表无效")
		}
		for _, release := range releases {
			if !stableReleaseTag.MatchString(release.Tag) || release.Draft || release.Prerelease {
				continue
			}
			if seen[release.Tag] {
				return releaseInfo{}, fmt.Errorf("GDS 发布列表包含重复标签；请指定版本后重试")
			}
			seen[release.Tag] = true
			if latest.Tag == "" || stableVersionLess(latest.Tag, release.Tag) {
				latest = release
			}
		}
		if len(releases) < 100 {
			if latest.Tag == "" {
				return releaseInfo{}, fmt.Errorf("官方仓库没有稳定 GDS 发布")
			}
			return latest, nil
		}
	}
	return releaseInfo{}, fmt.Errorf("GDS 发布列表超出完整检查上限；请指定 gds-vX.Y.Z")
}

func (s *Service) Latest() (string, error) {
	release, err := s.release("latest")
	return release.Tag, err
}

type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
}

// CheckUpdate reads the installed identity and checks online metadata. It never
// executes the installed binary or changes configuration, receipts or runtime.
func (s *Service) CheckUpdate() (UpdateInfo, error) {
	var result UpdateInfo
	program, err := s.programSnapshot()
	if err != nil {
		return result, err
	}
	receiptSnapshot, receipt, err := s.receiptSnapshot(false)
	if err != nil {
		return result, err
	}
	if receipt.SHA256 != gdsutil.Hash(program.Data) {
		return result, fmt.Errorf("GDS 程序与安装凭证不匹配")
	}
	result.Current = receipt.Version
	currentTag := "gds-v" + receipt.Version
	if !stableReleaseTag.MatchString(currentTag) {
		return result, fmt.Errorf("本地 GDS 版本不是可比较的稳定版本；请自行核对发布版本")
	}
	result.Latest, err = s.Latest()
	if err != nil {
		return result, err
	}
	if err = program.Check(); err != nil {
		return result, err
	}
	if err = receiptSnapshot.Check(); err != nil {
		return result, err
	}
	result.Available = stableVersionLess(currentTag, result.Latest)
	return result, nil
}

func stableVersionLess(a, b string) bool {
	x := strings.Split(strings.TrimPrefix(a, "gds-v"), ".")
	y := strings.Split(strings.TrimPrefix(b, "gds-v"), ".")
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

func officialAsset(release releaseInfo, name string, maximum int64) (releaseAssetInfo, error) {
	var selected releaseAssetInfo
	count := 0
	for _, asset := range release.Assets {
		if asset.Name == name {
			selected = asset
			count++
		}
	}
	expected := "https://github.com/" + releaseRepository + "/releases/download/" + release.Tag + "/" + name
	if count != 1 || selected.URL != expected || !strings.HasPrefix(selected.Digest, "sha256:") || !validHash(strings.TrimPrefix(selected.Digest, "sha256:")) || selected.Size <= 0 || selected.Size > maximum {
		return selected, fmt.Errorf("GDS 官方资产缺失、重复，或地址、摘要、大小无效")
	}
	return selected, nil
}

func (s *Service) downloadRelease(source, supplied string) (preparedArtifact, error) {
	release, err := s.release(source)
	if err != nil {
		return preparedArtifact{}, err
	}
	asset, err := officialAsset(release, releaseAsset, maxProgram)
	if err != nil {
		return preparedArtifact{}, err
	}
	digest := strings.TrimPrefix(asset.Digest, "sha256:")
	if supplied != "" && !strings.EqualFold(supplied, digest) {
		return preparedArtifact{}, fmt.Errorf("指定 SHA-256 与 GDS 官方发布摘要不一致")
	}
	checksumAsset, err := officialAsset(release, releaseAsset+".sha256", 256)
	if err != nil {
		return preparedArtifact{}, err
	}
	checksum, err := s.get(checksumAsset.URL, 256)
	if err != nil {
		return preparedArtifact{}, err
	}
	if int64(len(checksum)) != checksumAsset.Size || !strings.EqualFold(gdsutil.Hash(checksum), strings.TrimPrefix(checksumAsset.Digest, "sha256:")) || string(checksum) != strings.ToLower(digest)+"  "+releaseAsset+"\n" {
		return preparedArtifact{}, fmt.Errorf("GDS 官方摘要文件的内容、文件名或 SHA-256 校验失败")
	}
	data, err := s.get(asset.URL, maxProgram)
	if err != nil {
		return preparedArtifact{}, err
	}
	if int64(len(data)) != asset.Size || !strings.EqualFold(gdsutil.Hash(data), digest) {
		return preparedArtifact{}, fmt.Errorf("GDS 官方程序大小或 SHA-256 校验失败")
	}
	if err = nativeProgram(data, s.platform()); err != nil {
		return preparedArtifact{}, err
	}
	return preparedArtifact{data: data, expectedVersion: strings.TrimPrefix(release.Tag, "gds-v"), releaseTag: release.Tag}, nil
}

func (s *Service) get(source string, maximum int64) ([]byte, error) {
	return githubdownload.Get(s.HTTP, source, maximum, s.GitHubProxy)
}
