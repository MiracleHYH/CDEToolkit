package codex

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

// LatestHTTP queries only public version metadata through a caller-selected API
// client. It does not run curl, invoke the installer, or change Codex settings.
// A nil client is rejected so missing routing intent cannot become a direct
// request through the default HTTP transport.
func (s Service) LatestHTTP(client *http.Client) (string, error) {
	if client == nil {
		return "", fmt.Errorf("Codex 版本查询需要显式 API 客户端")
	}
	data, err := githubdownload.Get(client, LatestURL, 4<<20, "direct")
	if err != nil {
		return "", fmt.Errorf("查询 Codex 最新版本失败: %w", err)
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if json.Unmarshal(data, &release) != nil {
		return "", fmt.Errorf("Codex 最新版本响应无效")
	}
	version := strings.TrimPrefix(release.Tag, "rust-v")
	if !releaseVersion.MatchString(version) {
		return "", fmt.Errorf("Codex 最新版本格式无效")
	}
	return version, nil
}
