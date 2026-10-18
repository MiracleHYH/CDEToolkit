# Nido 构建、检查与发行

Nido: Lightweight Development Environment Manager。开发中的命令为 `nido`，主程序位于 `cmd/nido`；源码发行工具位于 `cmd/nido-dist`，不会进入安装包。Go module 和 GitHub 仓库仍使用 `github.com/MiracleHYH/CDEToolkit`。

从 v0.3.0 起发布 Nido Go 版。历史 cdet Release 保留原状，新版使用下述包名和安装行为，不自动迁移旧版安装。

v0.8.0 增加 Conda / pip 外部配置集成，随同四平台 Nido 包提供，不增加 Conda 安装包或独立组件资产。v0.8.1 已纳入完整镜像同步及按项查询修复；[新版本发行记录](conda-v0.8.1-release.md)与 [v0.8.0 历史记录](conda-release.md)分别保留。

## 本地检查

| 命令 | 范围 |
|---|---|
| `make check` | 格式、Shell 语法、vet、race、下载器与 CLI 隔离验证 |
| `make smoke` | 构建并校验本机发行包，运行只读版本与空环境状态检查 |
| `make dist TARGET_OS=linux TARGET_ARCH=amd64` | 构建、打包并校验指定平台 |
| `make release` | 仅在本地构建四个平台包、摘要与 POSIX 下载器 |
| `make verify-release` | 复核四个平台包与下载器源码一致性 |
| `make check-nido-public-release` | 显式使用已验签新旧 Nido 包，验证公网直链、GitHub 加速安装及真实旧包自更新 |
| `make check-conda-p0` | 显式使用固定完整旧上游镜像，在 normal/strict 两种模式离线验收可信 Linux 程序；镜像与程序须另行验真 |
| `make check-proxy-public-release` | 显式在 Linux amd64 临时 HOME 中验证已验签的公开 Nido 包、安装器和 Proxy quick start；需提供核验输入及已有 bwrap |
| `make check-gds` | 显式运行假 GPU、假 CUDA 和 CPU worker 的 GDS 隔离测试 |
| `make check-gds-linux-native` | 普通用户 Linux amd64 的离线 namespace 验证，需要已有 SDK、GCC、bwrap 和 vendor |
| `make gds-release` | 固定 Go SDK 与 Bullseye 编译环境，两次离线构建独立 GDS 并比较字节 |
| `make gds-verify-release` | GDS ELF、ABI 基线、程序身份、摘要和只读元数据校验 |

普通离线检查目标不会上传、安装或启动真实组件。安装器测试使用临时 HOME、合成 Release、本地 curl 夹具及禁止执行的组件命令；验证外部 Codex 配置、认证、会话、数据库、代理文件和 Shell 配置保持原样。显式公网验收目标则真实下载官方 Nido 包到临时根，Proxy 核心始终使用拒绝启动的夹具，详情见下文。

`VERSION` 是软件版本来源，`.go-version` 固定发行工具链，`go.mod` 声明最低 Go 版本。`NIDO_RELEASE_TAG=vX.Y.Z make release-check` 还会核验 Git 工作树干净、标签等于 VERSION 且指向当前 HEAD；它不会替用户创建标签或推送。

Go 检查直接使用 runner 普通权限。Codex 外部辅助集成不扫描全机进程，不再为迁移检查额外创建特权 PID 或挂载命名空间。

## 安装边界

自 v0.7.0 起，安装器、Nido 自身更新和内置组件下载支持下述 GitHub 下载策略；v0.6.0 及更早版本不包含这些新增参数与命令。

`scripts/install.sh` 是 POSIX 下载器，依赖 curl、tar、SHA-256 及系统自带文本工具。它接受 `--version`、`--root`、`--github-proxy`、`--yes` 与 `--dry-run`，软件目录默认为 `~/.nido`，可通过 `NIDO_HOME` 指定。旧的 `--prefix`、`--store` 和迁移参数不再支持。

下载器通过 HTTPS 上的 HTTP/1.1 获取当前平台 Nido 包与摘要。`--github-proxy` 或 `NIDO_GITHUB_PROXY` 支持 `direct`、`auto` 和无认证信息的 HTTPS 前缀；没有显式配置时默认 `auto`。安装脚本的 `auto` 对官方地址的连接/总超时为 4/180 秒，之后依次尝试 `gh-proxy.org` 和 `ghfast.top`，每项为 5/90 秒，不额外 retry；直连或明确前缀为 10/180 秒。可达的慢速官方包不会被原来的 15 秒总期限提前中断。curl 禁止 URL globbing，前缀中的括号和花括号保持字面含义。只重写 GitHub 包与摘要地址，不注入通用网络代理，也不影响 Codex 官方脚本。

安装脚本独立检查归档列表命令的退出状态与大小，版本必须为单行稳定版本；临时目录创建后立即注册清理。重复安装在已有程序目录不可写或经过链接时停止，不先修改下载偏好。Go 下载层的官方连接与响应头期限为 8 秒，收到响应后可在两分钟总预算内继续传输；API 只使用支持 API 的预设。每次重定向重新检查 HTTPS、认证参数和目标域名，仅允许官方 GitHub/CDN、所选加速入口及已知预设地域域名。

核对摘要、单成员归档和程序报告的版本，再由临时 Nido 执行：

```sh
nido --root /absolute/software/path init --yes
```

压缩包下载上限为 256 MiB；解包输出另在子 Shell 中施加 `ulimit -f 524288` 文件大小限制，避免小压缩包无限展开到临时盘。按 Shell 的 512/1024 字节块单位，该输出上限为 256/512 MiB。超限或解包失败时不执行候选程序，限制不传给后续初始化。该局部边界不替代完整归档验证，也不限制归档列表检查的 CPU 时间。

只有用户显式提供下载 flag 或环境变量时，再传入 `--github-proxy VALUE` 保存；普通重复安装不重置已有策略。Shell 不读取主配置。更新已安装 Nido 推荐 `nido update --check` 和 `nido update`，可加本次 `--version X.Y.Z`、`--github-proxy VALUE` 与独立 `--proxy URL`；持久偏好由 `nido download show/set` 管理。自身更新只允许当前运行的受管 `bin/nido`，只更新到稳定新版，同版不下载，拒绝降级；`dev` 构建需要源码更新。Go 下载器的自动 API 回退仅使用 `gh-proxy.org`，`ghfast.top` 仅用于资产。

自身更新在版本查询前捕获程序目录和入口身份，并与命令启动时的入口身份核对；确认后再次复核，不接受同内容但不同文件对象的替换。候选执行前后复核目录、文件身份和字节，失败时保留旧程序与用户替换内容。清理仅删除本次创建且身份仍一致的临时文件。发布准备还检查实际 ELF/Mach-O 架构，归档尾部一旦出现额外字节即拒绝，避免为垃圾尾部额外分配大块内存。

首次脚本尚未运行时不能使用内部回退，README 因此明确提供两条命令：官方脚本配 `--github-proxy direct`，或经 `https://gh-proxy.org/完整GitHub脚本URL` 下载并配同一加速前缀。两条命令最后都在调用者 Shell 中加载生成的 `env.sh`，使 `nido` 立即可用；不编辑用户 Shell 文件。后者执行第三方返回的脚本，需要信任该服务。客户端当前不验证 Sigstore，第三方可同时更换包与摘要；不能将 HTTPS 或摘要校验描述为官方真实性验证。下面的 Sigstore 验签指发行工作流和显式验收，不代表普通安装客户端已经验签。

初始化只把 Nido 放入 `<root>/bin/nido`，写入自己的配置与 `env.sh` 并输出 Shell 接入说明。它不会下载内置组件、发现或关联 Codex、启动服务、覆盖其他入口或编辑用户 Shell 文件。已有无归属的文件与链接必须拒绝覆盖。

安装器可用本地夹具验证，不能为了测试而下载并执行真实 Codex 安装脚本。外部 Codex 的关联、代理和官方安装委托由独立集成逻辑与对应隔离测试负责。

根目录 `install.sh` 和其他历史 Bash 入口暂时保留为冻结源码；它们不是 Nido 下载器，也不会打入 Go 包。

## GitHub Actions

`check.yml` 在 Linux 最低 Go、Linux 固定 Go 和 macOS 固定 Go 上执行 `make check smoke`。单独检查源码与历史中的敏感内容。GDS 的假驱动、CPU worker 测试和普通用户原生隔离验证自动运行；历史 Shell GDS 测试另保留显式入口。

Conda / pip 的单元、race 与 CLI 隔离测试进入 `make check`，使用临时 HOME、配置和假命令；普通 CI 不要求安装 Conda/Python，也不访问镜像。真实 Conda 配置合并与来源保护验收须显式提供已有程序，分别运行 [Conda 发行说明](conda-release.md#验证入口)中的离线入口。源码检查、实际上游本地配置核对和公开发行资产验收分别记录，不能互相替代。

Conda 24.1.2 / Python 3.12.1 / pip 23.3.1 的完整运行时入口为 `make check-conda-p0`，不下载或执行宿主 Conda。`NIDO_P0_VERSION=0.8.1` 显式断言被测发行版本，不能只凭输出目录或标签名认定输入身份。normal 将完整种子解到私有可写 tmpfs，strict 使用只读镜像中的同一完整 prefix，配置目标均位于测试区；内核拒写与文件/网络 trace 另行核实。两模式各与其原生基线及运行时摘要比较。首次 default、隐式用户文件、双 pip 配置、缺失目录键、显式 CONDARC 的非镜像保护和失败补偿的范围及边界见 [P0 验收](conda-p0-acceptance.md)。

Proxy 的真实核心与跨版本测试需要显式提供已核验程序，普通 CI 不会下载这些输入，不能把 `make check smoke` 通过解释为完成全部平台的真实核心验证。Linux 隔离入口会拒绝指定测试跳过、缺少 PASS 及子命令失败；新版 quick start 真实入口还要求 17 个节点配置子项逐项精确 PASS。纯 mock 回归只验证这些入口判定和清理边界。预构建输入的架构、CLI 元数据和 race 符号检查不能证明其来自当前提交；需另记录冻结源码与产物摘要。新版 quick start 的假核心/真实核心入口、实际结果和协议边界见 [Proxy 审计](proxy-audit.md)与[独立入口说明](proxy-design.md#quick-start-的-linux-amd64-真实核心隔离验证)。

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

原生冒烟只运行 `version` 与 `status --json`；验证 Codex、Conda 分类为外部集成，Proxy、GDS 分类为内置组件，且空环境不配置任何组件、不创建软件目录。跨平台包只做静态校验，不尝试执行。

发布测试只调用假的 cosign 与 gh，覆盖签名或验签失败后停止上传，以及发布白名单。测试本身不产生标签、发布、推送或真实服务操作。

`tests/nido-public-release-isolation.sh` 显式验证普通 Nido 的公开发行包。必须先独立核对新旧包的 API 元数据、摘要、平台及对应标签的 Sigstore 身份，再把冻结目录交给入口；公网下载响应在执行前与已验签输入逐字比较。安装器和客户端自身仍只做各自的摘要验证。通过 `NIDO_ACCEPT_VERSION`、`NIDO_ACCEPT_OLD_VERSION` 绑定新旧版本，不能把当前源码伪装成旧版来代替真实自更新。本次 v0.8.1 执行范围仅为 Linux amd64，使用 Docker 隔离入口分别检查 direct 和 `gh-proxy.org`，明确记录是否仿真；不在 macOS 安装。随后从同一已验签 Linux 包提取的程序再做 `check-conda-p0`，两类验收结果分开保存。四平台发行构建和静态核验继续保留，但不扩展本次执行验收范围。

发行后的公网测试入口另支持选择 `NIDO_ACCEPT_GITHUB_ACCELERATOR=https://gh-proxy.org|https://ghfast.top`、`NIDO_ACCEPT_INSTALL_VERSION_MODE=latest|tagged`、`NIDO_ACCEPT_TRANSPORT=both|direct|accelerated`，默认仍为 gh-proxy.org、latest、both。`NIDO_ACCEPT_METADATA_AUTH=gh` 仅让宿主 gh 只读核对新旧官方 API 元数据，不把 token 传入安装器、容器或 Go 更新客户端，不替换客户端响应；客户端更新仍使用真实匿名网络。固定版本或单链通过不能记录成 latest 或另一端点通过。公网故障与有界重试按端点保留原证据，不调整生产超时或关闭真实性检查来掩盖失败。

`tests/proxy-public-release-isolation.sh` 是 Linux amd64 的显式公网验收入口，独立于离线 CI。先在外部核验发布 API 元数据、资产摘要和 Sigstore 身份，再提供固定输入。它用已有 bubblewrap 隔离 HOME、文件系统和 PID，共享网络仅用于官方 HTTPS 下载；真实下载必须与核验输入逐字一致。覆盖 latest 首装和重复安装、固定版本和含空格的临时软件根，以及可选的旧版本升级；随后对公开包内 CLI 运行 quick start 和类别/单工具配置夹具。惰性核心只接受版本与配置检查，拒绝启动；这项包验收不冒称真实 Mihomo 转发复验。输入、输出和执行方式见 [Proxy 设计](proxy-design.md)。

`tests/gds-public-release-isolation.sh` 是显式的公开发行验收入口，独立于离线 CI。它在普通用户的临时 HOME、软件根、PID 和设备 namespace 中共享公网，只下载并安装官方程序到临时根，覆盖 Nido 旧版升级、重复安装及 GDS 安装、检查更新、更新和移除。只执行 GDS 元数据查询，不运行探针、monitor、worker 或真实服务；配置、认证、会话和数据哨兵必须逐字保留。
