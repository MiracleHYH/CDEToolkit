# GDS 历史发行记录（当前停用）

自 2026 年 10 月 19 日起，GDS 仅在 [standalone/gds](../standalone/gds/README.md) 保留独立源码。当前 Nido 不注册 GDS，不提供 `nido gds`；根构建、CI 和发行流程均不接入 GDS，旧 Make 目标与 `scripts/gds-release-build.sh` 已移除。本轮不下载安装程序，不迁移或清理既有部署，不建立自动发行入口。

本页保留 2026 年 10 月 18 日的公开发行事实、资产协议和验收出处。已发布的 [GDS gds-v0.1.0](https://github.com/MiracleHYH/CDEToolkit/releases/tag/gds-v0.1.0) 与 [Nido v0.5.0](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.5.0) 不因源码隔离而被改写；以下历史命令和流程不适用于当前源码。

## 历史安装和更新流程

Nido v0.5.0 当时提供 GDS 官方下载与更新，组件安装后仍停止。GDS 的历史发行范围为普通用户 Linux amd64、glibc 2.31 或更新版本；当时 Nido 发布 Linux/macOS × amd64/arm64 四个平台的纯 Go 包。这些范围不是当前 GDS 的硬件验收声明。

以下为当时文档中的安装示例，仅作历史文本保留。`releases/latest` 是浮动地址，不能用来固定复现当时的发行：

```text
# 历史示例，当前 Nido 源码没有 gds 子命令
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
sh install.sh --version 0.5.0 --root /absolute/software/nido --yes
/absolute/software/nido/bin/nido --root /absolute/software/nido gds install --yes
/absolute/software/nido/bin/nido --root /absolute/software/nido gds update --check
/absolute/software/nido/bin/nido --root /absolute/software/nido gds update --yes
```

历史接口中，`gds install 0.1.0` 或 `gds update gds-v0.1.0` 选择指定官方版本；不提供版本时选择最新稳定 GDS。`--artifact /absolute/gds` 接收已有本地 ELF，可附加 `--sha256`。版本与本地 artifact 互斥。预览不联网、不下载或执行组件；`update --check` 读取安装凭证并查询官方版本，不执行已有程序、不写组件文件。

当时的安装器只替换可确认归属的程序；有效配置和 `env.sh` 保留原始字节，包括 TOML 注释。GDS 更新与移除保留配置和未知文件；没有安装凭证、摘要不匹配、并发改动或运行归属不明时拒绝覆盖。该设计不迁移旧 Bash 部署、编辑用户 Shell、改变认证或会话、安装系统服务或自动启动 GPU 工作。相关管理实现现仅留在独立模块内部。

## 历史发行与校验协议

当时独立资产位于同一仓库的 `gds-vX.Y.Z` Release：

| 资产 | 内容 |
| --- | --- |
| `gds-linux-amd64` | 独立原生 cgo ELF，不包含配置或用户数据 |
| `gds-linux-amd64.sha256` | 严格的 SHA-256 与固定文件名记录 |
| 上述两个文件的 `.sigstore.json` | GitHub Actions OIDC 签名与透明日志证明 |

历史客户端经 HTTPS 查询 GitHub Release API，排除草稿、预发布及 Nido 标签，按数值版本选择 GDS。下载前核对固定仓库、标签、资产名与 URL，限制响应大小；下载后同时核对 API 的 SHA-256、大小与 sidecar 内容。最后核验 ELF 和程序报告的产品、协议、版本，再提交程序与 `installed.json` 凭证。可选的用户摘要是额外约束，默认更新不隐式降级。当前 Nido 已移除这条 GDS 执行链。

历史 CI 使用固定 Go SDK、固定 Bullseye 编译镜像和离线 vendor，要求两次全新构建字节相同。静态核验要求 Linux amd64、GOAMD64 v1、正确 loader、允许的动态依赖、无 RPATH，以及不高于 GLIBC 2.31 的所需符号。当时仅运行生产程序的 `--version --json` 验证加载与身份，不加载真实驱动；发布工作流先签名再逐项验签，只上传上述白名单。此元数据执行和发布流程均未在本轮重跑，也不是当前隔离测试的内容。

当时 GitHub 的两个 GDS job 使用一次性 `ubuntu-22.04` runner，先安装 `bubblewrap` 测试工具，再以普通 runner 用户执行完整 user、network、PID、mount namespace 自检及隔离测试。原记录中 Ubuntu 24.04 的策略阻止该 bubblewrap 环境初始化私有 loopback，因此选择兼容 runner；没有修改 AppArmor 策略、关闭全局限制，或回退到 root 与未隔离的宿主测试。其他 Nido 测试当时仍覆盖 Ubuntu 24.04。该准备步骤只属于历史 CI，不是用户机器的安装操作。

原记录引用的 [GitHub runner 退役公告](https://github.com/actions/runner-images/issues/14254) 标注 `ubuntu-22.04` 的退役日期为 2027-04-17。本轮未联网复核 runner 状态，也不再为 GDS 配置 CI runner。

历史客户端自动检查 HTTPS、API 摘要与 sidecar，但不自动验证 Sigstore bundle。当时独立验签使用 cosign，证书身份绑定本仓库 `gds-release.yml@refs/tags/gds-vX.Y.Z`，issuer 绑定 `https://token.actions.githubusercontent.com`。工作流文件现已移除；该历史身份仍是核对旧资产出处的依据，不表示恢复发布。

当时 GDS Release 明确不占用 GitHub `latest`，Nido Release 明确更新 `latest`；GDS 与 Nido 的版本分别从根 `GDS_VERSION`、`VERSION` 读取。隔离后 GDS 版本文件为 `standalone/gds/VERSION`，根 `VERSION` 继续属于 Nido。版本文件保留不触发发行，已发布标签不移动。

## 当前源码检查与历史验收入口

当前只保留独立模块的编译检查与显式 CPU 假驱动测试。普通用户原生隔离入口迁至 [standalone/gds/tests/linux-native-isolation.sh](../standalone/gds/tests/linux-native-isolation.sh)，需已有 Go、编译工具、离线依赖和 namespace 能力；只运行内部 CPU/假驱动测试。`cmd/gds` 测试仅编译，生产程序只构建和静态检查，不执行生产 GDS 或其元数据，不构建 Nido。它不安装工具或组件，也不回退到宿主测试。

历史入口 `tests/gds-linux-native-isolation.sh` 曾同时构建 Nido 并执行 GDS 元数据；这些步骤已退出当前测试。旧 `tests/gds-public-release-isolation.sh` 曾显式共享公网，验证 Nido v0.4.0 → v0.5.0、重复安装、GDS 官方下载校验及更新，现归档为不可执行的 [standalone/gds/archive/gds-public-release-isolation.sh.txt](../standalone/gds/archive/gds-public-release-isolation.sh.txt)，不再提供公开发行验收命令。两者当时均拒绝 root 和非原生 Linux amd64，隔离 HOME、PID、设备和临时数据，不挂载 GPU 或服务 socket；较旧本地测试版本只用于验证升级路径，不代表历史官方 GDS 发行。

真实 GPU、CUDA 分配、驱动组合、功耗和真实负载让出仍未验收。CPU 协议及假 NVML/CUDA 测试不能替代这些硬件结果。运行边界与前期安全审计见 [设计](gds-design.md) 和 [审计记录](gds-audit.md)。

## 首次公开发行验收（2026-10-18 历史记录）

2026 年 10 月 18 日已发布 [GDS gds-v0.1.0](https://github.com/MiracleHYH/CDEToolkit/releases/tag/gds-v0.1.0) 和 [Nido v0.5.0](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.5.0)，均来自冻结提交 `60a71a9`。GDS 的 4 项和 Nido 的 18 项公开资产已逐项核对 API 摘要、大小及固定 URL；程序包与本地构建相同，Nido `latest` 最终指向 v0.5.0。

普通 UID 1001 的 Linux amd64 原生验证通过 253 项 GDS 与 28 项 Nido GDS CLI 测试及子测试，零失败和跳过。公开程序另通过官方旧版升级、默认 latest 重复安装、GDS 下载校验安装、更新查询、指定/默认更新和移除；配置、认证、会话、数据及 Shell 哨兵均逐字保持。具体环境、修复、边界与证据见 [公开发行补验](gds-audit.md#公开发行补验2026-10-18)。
