# Go 检查与发行

当前文档描述未发布的双目录源码。命令为 `cdet`，主程序位于 `cmd/cdet`，发行辅助工具位于 `cmd/cdet-dist`。Go 生命周期不调用旧 Bash 或 Python 安装后端。

## 本地入口

| 命令 | 范围 |
|---|---|
| `make check` | 格式、Shell 语法、vet、race、隔离安装器和 CLI 测试 |
| `make smoke` | 构建本机包、校验包与摘要，只读运行版本和空环境状态 |
| `make dist TARGET_OS=linux TARGET_ARCH=amd64` | 构建并校验一个平台包 |
| `make release` | 本地构建四平台包、摘要和 POSIX 下载器 |
| `make verify-release` | 本地复核四包及下载器源码一致性 |
| `make check-gds` | 显式选择 Linux 的模拟 GPU/CUDA 隔离测试 |

这些构建目标不上传产物、不安装真实组件，也不启动服务。`make check` 会在临时目录部署测试 CLI，并运行合成组件；不会使用真实 HOME、认证、会话或配置。

`VERSION` 是版本来源。正式发布检查使用 `STK_RELEASE_TAG=vX.Y.Z make release-check`，要求标签存在、等于 VERSION、指向当前 HEAD 且工作树干净。此目标仍只检查和构建，不替用户提交、创建标签或推送。

源码最低 Go 1.23；固定发行工具链为 `.go-version` 中的 Go 1.27.2。运行时是单个 CGO=0 程序，无 SQLite 驱动、Python、Node 或宿主 sqlite3 依赖。配置解析只使用 TOML/YAML 基础依赖。

## GitHub Actions

`check.yml` 在 Linux 最低 Go、Linux 固定发行 Go 和 macOS 固定发行 Go 上执行检查与只读冒烟。源码与历史另做 gitleaks 审查。GDS job 仅在手动触发并选择时运行。

`release.yml` 只响应 `v*` 标签。它复核标签、源码、测试、包与本机冒烟，再逐项签名、自验证并发布白名单资产。普通检查只有 `contents: read`；发布 job 才具有上传和 OIDC 权限。Action 固定完整提交 SHA，checkout 不保留凭据。

发布白名单为：

- Linux/macOS × amd64/arm64 的四份 `cdet-GOOS-GOARCH.tar.gz`。
- 四份对应 `.sha256`。
- 来自 `scripts/install.sh` 的单份 POSIX 下载器。
- 每项资产对应的 `.sigstore.json`。

每个压缩包只含一个 `cdet` 程序，不含组件、配置、认证、会话、缓存、旧 Bash 或整份源码。签名或验证失败时阻止发布；额外 `dist/` 文件不会被通配上传。

包校验使用 Go 标准库，检查摘要、归档结构、固定权限、程序身份和目标平台；拒绝越界、链接、额外成员、损坏压缩流和异常尾随数据。跨平台产物只做静态校验，本机产物才执行只读冒烟。

## 安装器与隔离验证

POSIX 下载器依赖系统 curl、tar 和 SHA-256 工具，获取并校验 CLI 后交给 Go `init`。它只接受两个目标根；已有安装由 Go 的归属和身份规则处理，不代替组件安装。

隔离测试使用临时 HOME、下载夹具、合成原生组件和禁止服务调用的哨兵。Codex 安装更新只执行假 curl 返回的本地模拟脚本；Mihomo 控制 API 使用本地 HTTP 夹具，不运行真实官方安装器或真实服务。发布脚本测试使用假 cosign/gh，不进行真实签名或上传。服务器验证只在独立临时目录运行并清理，不操作既有安装。

Go 最低版本、完整测试、四平台构建和 Linux 临时环境的结果应以本轮实际输出记录到 [验证范围](two-root-audit.md)，不能沿用已删除数据库实现的历史结果。

## 历史清理记录（原文保留）

以下为 CI 初次迁移时的清理和验证记录，不作为当前布局或测试通过的声明。

## 已删除的源码

共 28 个文件。删除前已制作源码备份并记录逐文件摘要；解除工作流及 check.sh 的调用后复查了保留源码的引用。GDS 独立测试仍使用的三份夹具（nvcc、gds-nvidia-smi、gds-worker.c）保留。

- `scripts/bootstrap.sh`
- `scripts/release-check.sh`
- `scripts/release-smoke.sh`
- `tests/bootstrap-pty.py`
- `tests/fault-injection.sh`
- `tests/fixtures/bootstrap-bin/cosign`
- `tests/fixtures/bootstrap-bin/curl`
- `tests/fixtures/bootstrap-bin/mv`
- `tests/fixtures/codex`
- `tests/fixtures/codex-installer.sh`
- `tests/fixtures/conda`
- `tests/fixtures/curl`
- `tests/fixtures/git-fault`
- `tests/fixtures/gitleaks`
- `tests/fixtures/gzip-fault`
- `tests/fixtures/mihomo-core`
- `tests/fixtures/mihomo-curl`
- `tests/fixtures/mihomo-release.json`
- `tests/fixtures/pm`
- `tests/fixtures/sleep`
- `tests/fixtures/slow-editor.sh`
- `tests/fixtures/supervisorctl`
- `tests/fixtures/systemctl`
- `tests/installer-pty.py`
- `tests/platform.sh`
- `tests/run.sh`
- `tests/supervisor-integration.sh`
- `tests/toolkit-menu-pty.py`

## 保留及原因

| 范围 | 保留原因 |
| --- | --- |
| toolkit.sh、install.sh、codex-launcher.sh、lib、modules、platforms | 旧入口尚未切换；动态 module/source 链和安装器仍依赖它们，不能拆空当前入口 |
| proxy 的管理器、安装器、Python helper、公开模板与 runtime.lock | 旧 pm/install/codex 仍有引用；不为源码清理动实际代理配置或运行时 |
| preflight-update.sh、check.sh、public-audit.sh | lifecycle 仍调用预检，预检要求后两路径存在；check.sh 已收敛为 Go 检查入口，public-audit 仍服务 CI |
| service-start.sh、log-maintenance.sh、ssh-agent.sh、systemd 模板 | 可能被已注册服务或 Shell 引用；保留固定路径，不迁移或卸载实际注册内容 |
| gds 目录及其依赖的 common/config/codex/modules 等库 | 可选 GDS 的安装/直管模式仍需依赖；运行源码及现有配置原样保留 |

没有为了删除模块而改写用户 Skill、AGENTS、Shell 或服务配置。旧核心不会进入 Go 包，也不会被 Go 作为安装、下载、解包或配置后端调用。实际入口未来切换后，再按新的引用快照清理剩余兼容组。

## 验证范围

发布工具新增单元检查覆盖版本/tag、可复现包元数据、归档越界/链接/额外成员/CRC/尾随数据、摘要文件名、输出链接保护和错误程序身份。
本轮还验证 Go 最低版本与固定工具链、四个平台的包、macOS/Linux 原生只读冒烟、Go CLI 数据哨兵、可选 GDS 的假 worker 清理、actionlint，以及隔离 Git 仓库中的源码/历史审查。
真实组件保护基线和源码清单在同步前后独立核对；部署只允许明确修改/删除的源码路径，摘要冲突立即停止。测试二进制、工具链与产物只放在工作区或远端临时目录。

## 历史：CDEToolkit 命名与首次一行安装

本节对应 schema-1 的首次发布，保留当时验证记录；文件事务、自更新回滚及旧入口兼容均不是当前 schema-4 功能。

首次提交 5ecfb46 的 GitHub Actions 全部通过（Linux Go 1.23/1.27.2、macOS、public-audit）；GDS 按可选策略跳过，没有失败项。

Go module 使用 github.com/MiracleHYH/CDEToolkit，主程序入口为 cmd/cdet，发行辅助入口为 cmd/cdet-dist。
四个平台包改为 cdet-GOOS-GOARCH.tar.gz，包内只有 cdet；独立 install.sh 来自 scripts/install.sh，逐字节核对源码后才签名发布。
发布白名单包含四包、四摘要和一个 POSIX 安装器及各自的 Sigstore bundle；不会上传旧根目录 install.sh 或其他 dist 文件。

安装器不依赖 Bash、Go 或 Python，使用系统 curl/tar/SHA-256 工具。其首次初始化、自更新、入口登记仍由 Go 文件事务完成。
新增隔离验证覆盖初装/重复/版本固定/预览、错误摘要、无 Release、额外归档成员、无归属文件和链接、父目录链接、版本不匹配及不支持的架构。
另验证 schema-1 旧入口兼容、管理命令相对链接的挂载重定位和安全卸载，以及包含新命令入口的更新记录退役/回滚。
所有安装验证使用临时 HOME、TLS/下载夹具及假服务命令，不针对现有组件执行生命周期操作。
