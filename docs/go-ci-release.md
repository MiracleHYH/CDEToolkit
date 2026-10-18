# Nido 构建、检查与发行

Nido: Lightweight Development Environment Manager。开发中的命令为 `nido`，主程序位于 `cmd/nido`；源码发行工具位于 `cmd/nido-dist`，不会进入安装包。Go module 和 GitHub 仓库仍使用 `github.com/MiracleHYH/CDEToolkit`。

从 v0.3.0 起发布 Nido Go 版。历史 cdet Release 保留原状，新版使用下述包名和安装行为，不自动迁移旧版安装。

## 本地检查

| 命令 | 范围 |
|---|---|
| `make check` | 格式、Shell 语法、vet、race、下载器与 CLI 隔离验证 |
| `make smoke` | 构建并校验本机发行包，运行只读版本与空环境状态检查 |
| `make dist TARGET_OS=linux TARGET_ARCH=amd64` | 构建、打包并校验指定平台 |
| `make release` | 仅在本地构建四个平台包、摘要与 POSIX 下载器 |
| `make verify-release` | 复核四个平台包与下载器源码一致性 |
| `make check-gds` | 显式运行假 GPU、假 CUDA 和 CPU worker 的 GDS 隔离测试 |
| `make check-gds-linux-native` | 普通用户 Linux amd64 的离线 namespace 验证，需要已有 SDK、GCC、bwrap 和 vendor |
| `make gds-release` | 固定 Go SDK 与 Bullseye 编译环境，两次离线构建独立 GDS 并比较字节 |
| `make gds-verify-release` | GDS ELF、ABI 基线、程序身份、摘要和只读元数据校验 |

这些目标不会上传、安装或启动真实组件。安装器测试使用临时 HOME、合成 Release、本地 curl 夹具及禁止执行的组件命令；验证外部 Codex 配置、认证、会话、数据库、代理文件和 Shell 配置保持原样。

`VERSION` 是软件版本来源，`.go-version` 固定发行工具链，`go.mod` 声明最低 Go 版本。`NIDO_RELEASE_TAG=vX.Y.Z make release-check` 还会核验 Git 工作树干净、标签等于 VERSION 且指向当前 HEAD；它不会替用户创建标签或推送。

Go 检查直接使用 runner 普通权限。Codex 外部辅助集成不扫描全机进程，不再为迁移检查额外创建特权 PID 或挂载命名空间。

## 安装边界

`scripts/install.sh` 是 POSIX 下载器，只依赖 curl、tar 和 SHA-256 工具。它接受 `--version`、`--root`、`--yes` 与 `--dry-run`，软件目录默认为 `~/.nido`，可通过 `NIDO_HOME` 指定。旧的 `--prefix`、`--store` 和迁移参数不再支持。

下载器通过 HTTPS 上的 HTTP/1.1 获取当前平台 Nido 包与摘要，核对摘要、单成员归档和程序报告的版本，再由临时 Nido 执行：

```sh
nido --root /absolute/software/path init --yes
```

初始化只把 Nido 放入 `<root>/bin/nido`，写入自己的配置与 `env.sh` 并输出 Shell 接入说明。它不会下载内置组件、发现或关联 Codex、启动服务、覆盖其他入口或编辑用户 Shell 文件。已有无归属的文件与链接必须拒绝覆盖。

安装器可用本地夹具验证，不能为了测试而下载并执行真实 Codex 安装脚本。外部 Codex 的关联、代理和官方安装委托由独立集成逻辑与对应隔离测试负责。

根目录 `install.sh` 和其他历史 Bash 入口暂时保留为冻结源码；它们不是 Nido 下载器，也不会打入 Go 包。

## GitHub Actions

`check.yml` 在 Linux 最低 Go、Linux 固定 Go 和 macOS 固定 Go 上执行 `make check smoke`。单独检查源码与历史中的敏感内容。GDS 的假驱动、CPU worker 测试和普通用户原生隔离验证自动运行；历史 Shell GDS 测试另保留显式入口。

Proxy 的真实核心与跨版本测试需要显式提供已核验程序，普通 CI 不会下载这些输入，不能把 `make check smoke` 通过解释为完成全部平台的真实核心验证。Linux 的两个隔离入口会拒绝 TestOS 或显式跨版本测试跳过、缺少 PASS 及子命令失败；纯 mock 回归只验证这些入口判定和清理边界。预构建输入的架构、CLI 元数据和 race 符号检查不能证明其来自当前提交；需另记录冻结源码与产物摘要。当前各平台的实际证据、缺口及发布阻塞项见 [Proxy 最终审计](proxy-audit.md#跨版本验证后的最终发布审计2026-10-18)。

本次 Proxy 的服务器部署目标已限定为 Linux amd64，其他平台的验证缺口不阻塞该目标；Nido 的四平台包构建与静态校验仍按原流程保留。

`release.yml` 只响应 `v*` 标签。在上传前，它检查精确标签、源码、测试、包与本机只读冒烟，再使用 Sigstore 签名并逐项验证发布白名单。普通检查仅有 `contents: read`；只有发布 job 才拥有上传和 OIDC 权限。Action 固定完整 SHA，checkout 不保留凭据。

Nido 发行资产仅包含：

- Linux/macOS × amd64/arm64 的四个 `nido-GOOS-GOARCH.tar.gz`，每包只含一个普通可执行文件 `nido`。
- 四个对应 `.sha256` 文件。
- 来自 `scripts/install.sh` 的单个 `install.sh`。
- 上述九个文件各自的 `.sigstore.json` bundle。

发行流程不会上传用户数据、仓库快照、旧 Bash 安装器或 dist 中其他文件。源码 URL 和仓库名保持不变：

```text
https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh
```

`gds-release.yml` 独立响应 `gds-v*`，使用 `GDS_VERSION` 核对精确标签。GDS 首个正式版本为 `gds-v0.1.0`，仅发布 Linux amd64 的 `gds-linux-amd64`、`.sha256` 和各自的 Sigstore bundle。固定 Go 工具链与 glibc 2.31 编译环境执行两次离线构建，随后静态核验和只读元数据冒烟。普通用户 native job 使用私有网络、PID 和最小设备 namespace，执行假驱动与 CPU 协议测试。

GDS 发布明确使用 `--latest=false`，Nido 发布明确使用 `--latest`。Nido 的安装入口继续解析 Nido 最新版；GDS 客户端只枚举独立的稳定 `gds-vX.Y.Z` 标签。官方下载与验证协议见 [GDS 发布与安装](gds-release.md)。

## 发行校验

包校验检查 Go 主程序身份、GOOS/GOARCH、CGO=0、唯一普通成员、权限、大小、归档尾部和 SHA-256 文件名，拒绝链接、越界路径、额外成员、损坏 CRC 和尾随数据。归档时间和所有者固定，避免泄露构建机器信息。

原生冒烟只运行 `version` 与 `status --json`；验证 Codex 分类为外部集成，Proxy、GDS 分类为内置组件，且空环境不配置任何组件、不创建软件目录。跨平台包只做静态校验，不尝试执行。

发布测试只调用假的 cosign 与 gh，覆盖签名或验签失败后停止上传，以及发布白名单。测试本身不产生标签、发布、推送或真实服务操作。

`tests/gds-public-release-isolation.sh` 是显式的公开发行验收入口，独立于离线 CI。它在普通用户的临时 HOME、软件根、PID 和设备 namespace 中共享公网，只下载并安装官方程序到临时根，覆盖 Nido 旧版升级、重复安装及 GDS 安装、检查更新、更新和移除。只执行 GDS 元数据查询，不运行探针、monitor、worker 或真实服务；配置、认证、会话和数据哨兵必须逐字保留。
