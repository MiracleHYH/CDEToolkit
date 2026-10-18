# GDS 发布与安装

GDS 独立版本为 `gds-v0.1.0`，Nido `v0.5.0` 提供官方组件下载与更新入口。GDS 是显式可选组件，安装不会启动它。当前发行范围为普通用户 Linux amd64、glibc 2.31 或更新版本；Nido 本身仍发布 Linux/macOS × amd64/arm64 四个平台的纯 Go 包。

## 官方安装和更新

Nido 官方安装器：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

指定安装根或版本时可先保存脚本、检查后执行。已有 Nido 软件目录可用同一个安装器升级，不需要切换当前 Shell 入口：

```sh
sh install.sh --version 0.5.0 --root /absolute/software/nido --yes
/absolute/software/nido/bin/nido --root /absolute/software/nido gds install --yes
/absolute/software/nido/bin/nido --root /absolute/software/nido gds update --check
/absolute/software/nido/bin/nido --root /absolute/software/nido gds update --yes
```

`gds install 0.1.0` 或 `gds update gds-v0.1.0` 选择指定官方版本；不提供版本时选择最新稳定 GDS。`--artifact /absolute/gds` 仍支持已有本地 ELF，可附加 `--sha256`。版本与本地 artifact 互斥。预览不联网、不下载或执行组件；`update --check` 读取安装凭证并查询官方版本，不执行已有程序、不写组件文件。

安装器只替换可确认归属的程序。当前有效配置和 `env.sh` 保留原始字节，包括 TOML 注释。GDS 更新与移除保留配置和未知文件；没有安装凭证、摘要不匹配、并发改动或运行归属不明时拒绝覆盖。不会迁移旧 Bash 部署、编辑用户 Shell、改变认证或会话、安装系统服务或自动启动 GPU 工作。

## 发行与校验协议

独立资产位于同一仓库的 `gds-vX.Y.Z` Release：

| 资产 | 内容 |
| --- | --- |
| `gds-linux-amd64` | 独立原生 cgo ELF，不包含配置或用户数据 |
| `gds-linux-amd64.sha256` | 严格的 SHA-256 与固定文件名记录 |
| 上述两个文件的 `.sigstore.json` | GitHub Actions OIDC 签名与透明日志证明 |

客户端经 HTTPS 查询 GitHub Release API，排除草稿、预发布及 Nido 标签，按数值版本选择 GDS。下载前核对固定仓库、标签、资产名与 URL，限制响应大小；下载后同时核对 API 的 SHA-256、大小与 sidecar 内容。最后核验 ELF 和程序报告的产品、协议、版本，再提交程序与 `installed.json` 凭证。可选的用户摘要是额外约束。默认更新不隐式降级。

CI 使用固定 Go SDK、固定 Bullseye 编译镜像和离线 vendor，两次全新构建必须字节相同。静态核验要求 Linux amd64、GOAMD64 v1、正确 loader、允许的动态依赖、无 RPATH，以及不高于 GLIBC 2.31 的所需符号。仅运行 `--version --json` 验证加载与身份，不加载真实驱动。发布工作流先签名再逐项验签，只上传上述白名单。

GitHub 的两个 GDS job 使用一次性 `ubuntu-22.04` runner，先安装 `bubblewrap` 测试工具，再以普通 runner 用户执行完整 user、network、PID、mount namespace 自检及隔离测试。Ubuntu 24.04 的默认策略会阻止当前 bubblewrap 初始化私有 loopback，因此此处选择兼容 runner；不修改 AppArmor 策略、不关闭全局限制，也不回退到 root 或未隔离的宿主测试。其他 Nido 测试仍覆盖 Ubuntu 24.04。该准备步骤仅属于 CI，不在用户机器上安装工具或修改配置。

`ubuntu-22.04` 目前仍可用，GitHub 公告的退役日期为 2027-04-17；届时需改用经相同 namespace 自检验证的 runner。[GitHub runner 退役公告](https://github.com/actions/runner-images/issues/14254)

客户端自动检查 HTTPS、API 摘要与 sidecar；当前不自动验证 Sigstore bundle。需要签名验证时，应单独使用 cosign，并将证书身份绑定到本仓库 `gds-release.yml@refs/tags/gds-vX.Y.Z`、issuer 绑定到 `https://token.actions.githubusercontent.com`。

GDS Release 明确不占用 GitHub `latest`；Nido Release 明确更新 `latest`。源码与发行工具分别从 `GDS_VERSION`、`VERSION` 读取版本，已发布标签不移动。

## 验证范围

离线原生验收入口为 `tests/gds-linux-native-isolation.sh`；公开程序验收入口为 `tests/gds-public-release-isolation.sh`。两者都拒绝 root 和非原生 Linux amd64，隔离 HOME、PID、设备和临时数据，不挂载 GPU 或服务 socket。公开程序验收显式共享公网，覆盖 Nido v0.4.0 → v0.5.0、重复安装、GDS 官方下载校验及更新；较旧本地测试版本只用于验证升级路径，不冒充历史官方 GDS 发行。

真实 GPU、CUDA 分配、驱动组合、功耗和真实负载让出仍未验收。CPU 协议及假 NVML/CUDA 测试不能替代这些硬件结果。运行边界与前期安全审计见 [设计](gds-design.md) 和 [审计记录](gds-audit.md)。
