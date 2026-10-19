package gds

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsutil"
)

// A filename and ELF header do not establish that a file belongs to GDS. The
// receipt is written only after candidate metadata was authorized and validated.
// Interrupted two-file commits deliberately leave a mismatch that fails closed.
type installReceipt struct {
	Schema     int    `json:"schema"`
	Product    string `json:"product"`
	Protocol   int    `json:"protocol"`
	Platform   string `json:"platform"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	ReleaseTag string `json:"release_tag,omitempty"`
}

func (s *Service) receiptSnapshot(optional bool) (gdsutil.Snapshot, installReceipt, error) {
	var receipt installReceipt
	dir, _, err := s.paths()
	if err != nil {
		return gdsutil.Snapshot{}, receipt, err
	}
	before, err := gdsutil.Capture(filepath.Join(dir, "installed.json"), 8<<10, optional)
	if err != nil {
		// A missing receipt is an invalid installed program, not an absent one.
		return before, receipt, fmt.Errorf("GDS 安装凭证不可读；保留现有程序: %v", err)
	}
	if before.Info == nil {
		return before, receipt, nil
	}
	if before.Info.Mode().Perm() != 0600 || json.Unmarshal(before.Data, &receipt) != nil || receipt.Schema != 1 || receipt.Product != "gds" || receipt.Protocol != gdsapi.ProtocolVersion || receipt.Platform != "linux/amd64" || receipt.Version == "" || !validHash(receipt.SHA256) {
		return before, receipt, fmt.Errorf("GDS 安装凭证无效；保留现有文件")
	}
	if receipt.ReleaseTag != "" && (!stableReleaseTag.MatchString(receipt.ReleaseTag) || receipt.ReleaseTag != "gds-v"+receipt.Version) {
		return before, receipt, fmt.Errorf("GDS 官方安装凭证的发布标签无效；保留现有文件")
	}
	return before, receipt, nil
}

func (s *Service) ownedProgram(core gdsutil.Snapshot) (gdsutil.Snapshot, error) {
	before, receipt, err := s.receiptSnapshot(false)
	if err != nil {
		return before, err
	}
	if receipt.SHA256 != gdsutil.Hash(core.Data) {
		return before, fmt.Errorf("GDS 程序与安装凭证不匹配；禁止执行、替换或删除未知程序")
	}
	return before, nil
}
