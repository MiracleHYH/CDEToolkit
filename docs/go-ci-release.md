# Go CI、发布与旧源码清理

本文记录 CI 初次迁移阶段的清理与验证。当前命令已更名为 `cdet`；最新安装与发行流程见 README，历史记录中的 `stk` 和旧资产清单不作为当前使用说明。

日期：2026-10-17。仅修改源码、构建和隔离测试；不推送代码、创建标签或发布 Release，不切换实际入口，不注册/启动真实服务，不修改用户配置、认证、会话和数据。

## 构建与检查

- `make check`：gofmt、vet、race 和已有三组 CLI 隔离测试。
- `make smoke`：构建当前平台包、验摘要/内容/Go 主程序身份/架构，并在临时 HOME 运行包中程序的 `version` 与 `status --json`；不会执行 install/setup。
- `make dist TARGET_OS=linux TARGET_ARCH=amd64`：生成指定平台 tar.gz 和 `.sha256`。
- `make release` / `make verify-release`：构建/校验 Linux/macOS × amd64/arm64 四个平台。各平台使用独立 build 子目录，避免相互覆盖。
- `STK_RELEASE_TAG=vX.Y.Z make release-check`：先要求标签与 VERSION 完全一致、工作树干净且标签指向当前 HEAD，再依次完整检查、构建四包、验包和本机只读冒烟。这里只生成文件，不发布。标签经环境传入，不插入 shell 源码；不会替调用者提交、打标签或拉取代码。
- `make check-gds`：Linux 可选检查，只复制 GDS/common/CUDA 源码到临时目录，使用假 nvcc、假 nvidia-smi 和 CPU fixture worker。默认 CI、Go 安装与 Go 发行包均不包含此步骤。

`VERSION` 是发行版本来源；本次保留已有版本文件不变。Make 通过 Go 链接参数注入版本，直接 go build 显示 dev。
发布工具链固定于 `.go-version`；CI 另检查 go.mod 承诺的最低 Go 1.23。Go 依赖未增加。

## 工作流

check.yml 在 Linux（最低/当前 Go）及 macOS 执行 Go 检查和本机包冒烟，另保留完整历史的 gitleaks 审查。GDS job 仅在手动触发并勾选 gds 时运行，不需要 CUDA、GPU 或 Supervisor。

release.yml 仅响应 v* 标签。它读取 VERSION、检查标签、运行完整 Go 验证、构建四个平台、校验摘要/包结构、执行本机只读冒烟，再逐个签名、自验证并发布固定清单中的 Go 包和摘要。每份 tar.gz 严格只含 `stk`，不再生成 `install.sh` 或整个源码安装包。

Go 标准库负责 tar/gzip/摘要，包内时间、UID/GID、权限和文件名固定，不依赖 GNU/BSD tar 选项。校验拒绝额外安装器、链接、PAX 元数据、错误架构、非 STK 程序、摘要错误、损坏压缩流或非零尾随内容。交叉产物静态校验，本机产物执行版本检查；不会尝试执行其他架构程序。Go 在 trimpath 构建信息中不保留链接参数，运行版本由只读冒烟确认。

Actions 均固定为核对过的完整提交 SHA；checkout 不保留凭据。PR/普通检查仅有 contents:read，发布 job 才有 contents:write 与 id-token:write。Sigstore 按仓库、release.yml 和标签身份自验证，签名或验证失败不会调用 gh release create。

源码和历史审查继续使用现有 public-audit，不删除已有泄漏检查。gitleaks v8.30.0 的 go.mod 仍声明旧模块路径，安装命令改为 github.com/zricethezav/gitleaks/v8，避免沿用旧 CI 中不匹配的路径。工具仅用于 CI/隔离验证，不进入 STK 运行时。

参考配置已核对 [setup-go 官方说明](https://github.com/actions/setup-go/blob/main/docs/advanced-usage.md) 与各 Action 的官方 tag/提交；新工作流在本轮只进行静态校验和本地等价检查，未实际触发 GitHub 发布。
发布脚本还用假 cosign/gh 检查固定资产清单、证书身份参数以及签名/验证失败时阻断发布；没有调用真实 OIDC 签名或上传服务。真实 GitHub runner 的身份令牌与签名链需首次正式发布时验证。

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

## CDEToolkit 命名与一行安装

首次提交 5ecfb46 的 GitHub Actions 全部通过（Linux Go 1.23/1.27.2、macOS、public-audit）；GDS 按可选策略跳过，没有失败项。

Go module 使用 github.com/MiracleHYH/CDEToolkit，主程序入口为 cmd/cdet，发行辅助入口为 cmd/cdet-dist。
四个平台包改为 cdet-GOOS-GOARCH.tar.gz，包内只有 cdet；独立 install.sh 来自 scripts/install.sh，逐字节核对源码后才签名发布。
发布白名单包含四包、四摘要和一个 POSIX 安装器及各自的 Sigstore bundle；不会上传旧根目录 install.sh 或其他 dist 文件。

安装器不依赖 Bash、Go 或 Python，使用系统 curl/tar/SHA-256 工具。其首次初始化、自更新、入口登记仍由 Go 文件事务完成。
新增隔离验证覆盖初装/重复/版本固定/预览、错误摘要、无 Release、额外归档成员、无归属文件和链接、父目录链接、版本不匹配及不支持的架构。
另验证 schema-1 旧入口兼容、管理命令相对链接的挂载重定位和安全卸载，以及包含新命令入口的更新记录退役/回滚。
所有安装验证使用临时 HOME、TLS/下载夹具及假服务命令，不针对现有组件执行生命周期操作。
