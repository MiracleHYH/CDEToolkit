package proxy

import (
	"context"
	"fmt"
	"strings"
)

// First installation must not depend on an already installed Proxy or a GitHub
// API route. These compressed-asset sizes and SHA-256 digests were checked on
// 2026-10-19 against the official release API and its public asset listing:
// https://api.github.com/repos/MetaCubeX/mihomo/releases/tags/v1.19.32
// https://github.com/MetaCubeX/mihomo/releases/expanded_assets/v1.19.32
// Changing this version requires reviewing and replacing every platform entry.
// Native upgrades remain the running core's responsibility.
const pinnedInstallVersion = "v1.19.32"

type pinnedAsset struct {
	name   string
	size   int64
	sha256 string
}

func pinnedInstallAsset(platform string) (pinnedAsset, error) {
	switch platform {
	case "linux/amd64":
		return pinnedAsset{"mihomo-linux-amd64-compatible-v1.19.32.gz", 22877876, "ba3ce607747a07f948fc35780e108a4a7c7f552a38b9bd4d115f313ebcb89c20"}, nil
	case "linux/arm64":
		return pinnedAsset{"mihomo-linux-arm64-v1.19.32.gz", 20809592, "9dd862e28b46ff7d775f169cceebc28deccaa0a9e804237d421cd2571e0caba0"}, nil
	case "darwin/amd64":
		return pinnedAsset{"mihomo-darwin-amd64-compatible-v1.19.32.gz", 22302953, "18b382df77bded2ad0fb3db27db5636cb15b20729d5ba995a357eeb9b46bf507"}, nil
	case "darwin/arm64":
		return pinnedAsset{"mihomo-darwin-arm64-v1.19.32.gz", 20756622, "3312a6780652c622890fd4357c6a853bbf865464fd047ac7b7f52dab8de18652"}, nil
	default:
		return pinnedAsset{}, fmt.Errorf("在线 Proxy 安装仅支持 Linux/macOS 的 amd64/arm64")
	}
}

func (a pinnedAsset) source() string {
	return "https://github.com/MetaCubeX/mihomo/releases/download/" + pinnedInstallVersion + "/" + a.name
}

// The descriptor is private and selected by value from the fixed platform
// table. Tests can exercise this fetch boundary with inert bytes without
// weakening Service's production asset identity.
func (m *Service) fetchPinnedArtifact(ctx context.Context, asset pinnedAsset, supplied string) (preparedArtifact, error) {
	if err := ctx.Err(); err != nil {
		return preparedArtifact{}, err
	}
	if !strings.HasPrefix(asset.name, "mihomo-") || !strings.HasSuffix(asset.name, "-"+pinnedInstallVersion+".gz") || strings.ContainsAny(asset.name, "\\/?#\r\n\t ") || !validChecksum(asset.sha256) || asset.size <= 0 || asset.size > maxProgram {
		return preparedArtifact{}, fmt.Errorf("内置 Mihomo 安装包信息无效")
	}
	if supplied != "" && !strings.EqualFold(supplied, asset.sha256) {
		return preparedArtifact{}, fmt.Errorf("指定 SHA-256 与内置 Mihomo 安装包不一致")
	}
	data, err := m.getContext(ctx, asset.source(), asset.size)
	if err != nil {
		return preparedArtifact{}, err
	}
	if err := ctx.Err(); err != nil {
		return preparedArtifact{}, err
	}
	if int64(len(data)) != asset.size || !strings.EqualFold(hash(data), asset.sha256) {
		return preparedArtifact{}, fmt.Errorf("官方包大小或 SHA-256 校验失败")
	}
	return preparedArtifact{data: data, expectedVersion: pinnedInstallVersion}, nil
}
