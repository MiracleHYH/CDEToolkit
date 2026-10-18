# Nido: Lightweight Development Environment Manager

**用 `nido` 配置外部开发工具，维护少量内置组件。**

Nido 为 Codex 提供目录关联、环境与代理配置，以及官方安装更新入口；Proxy（Mihomo）和可选 GDS 则由 Nido 安装和管理。自 v0.8.0 起提供 Conda / pip 镜像、缓存与环境目录配置。外部工具保留原有数据和命令入口。

**v0.8.2 正在准备发布，仅面向 Linux amd64。**本版增加内部 API listener 绑定与未启用门控；源码审计和隔离回归已通过，发行、签名及公网验收状态另见 [v0.8.2 发行记录](docs/nido-v0.8.2-release.md)。准备状态不表示该版本已经可下载。

支持 Linux / macOS 的 amd64、arm64。Nido 本身是一个 Go 可执行文件。

从 **v0.3.0** 起，项目使用 Nido 名称和 `nido` 命令；仓库和 Go module 暂时保留 CDEToolkit 名称。旧 `cdet` 安装不会被自动迁移或替换。

[快速开始](#快速开始) · [Codex](#关联或安装-codex) · [Conda / pip](#配置-conda--pip) · [Proxy](#使用内置-proxy) · [使用指南](docs/usage.md) · [开发与发行](docs/go-ci-release.md)

## 快速开始

**v0.7.0 起支持 GitHub 加速下载和 Nido 自身更新。** 以下命令安装最新版，并在当前 Bash / Zsh 中启用 `nido`。

需要 curl、tar 和 SHA-256 工具（Linux 通常自带 `sha256sum`，macOS 自带 `shasum`），无需 Go。首次安装请选择一种下载方式，安装最新 Go 版到 `~/.nido`。

**能直接访问 GitHub：** 脚本和安装包都从 GitHub 下载。

```sh
curl --http1.1 -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh -s -- --github-proxy direct && . "${NIDO_HOME:-$HOME/.nido}/env.sh"
```

**GitHub 访问困难：** 脚本和安装包都通过第三方加速前缀下载。

```sh
curl --http1.1 -fsSL https://gh-proxy.org/https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh -s -- --github-proxy https://gh-proxy.org && . "${NIDO_HOME:-$HOME/.nido}/env.sh"
```

第二种方式会执行第三方转发的脚本，仅在信任该服务时使用。安装器核对包的 SHA-256，当前客户端不会验证 Sigstore 签名；第三方也能同时替换脚本、包和摘要，因此加速下载不能提供与官方直链相同的真实性保证。

安装后检查：

```sh
nido version
nido status
```

安装器只安装 Nido，不安装组件、不关联已有 Codex，也不启动服务。再次运行同一命令会更新 Nido，保留已有管理配置。可提前执行 `export NIDO_HOME=/absolute/path/to/nido` 选择其他独立软件目录；外部 Codex HOME 必须在其之外。

上面命令末尾的 `. .../env.sh` 把 `nido` 加入当前终端的 PATH；安装脚本自身无法修改父 Shell。Nido 不自动编辑 Shell 配置。需要新终端自动加载时，将 `. "$HOME/.nido/env.sh"` 加入 `~/.bashrc` 或 `~/.zshrc`；自定义目录请替换路径。

之后可直接检查和更新 Nido 本身：

```sh
nido update --check
nido update
nido download show
nido download set auto
```

自身更新只操作当前运行的受管 `bin/nido`。相同版本不会下载，不支持降级；源码 `dev` 构建请通过源码更新。需要选择明确的新版本时使用 `nido update --version X.Y.Z`。

GitHub 下载策略支持 `direct`、`auto` 或自定义 HTTPS 前缀。`auto` 先短时尝试官方地址，失败后依次尝试少量内置第三方前缀；仅用于支持的公开 GitHub 下载，不是全局 HTTP 代理，不改写 Codex 官方脚本地址。首次显式选择会保存，重复安装未指定时保留既有配置；一次更新可用 `--github-proxy` 覆盖。详细参数与信任边界见[使用指南](docs/usage.md#github-下载与-nido-自身更新)。

## 关联或安装 Codex

已有 Codex 时，发现并关联现有目录：

```sh
nido codex setup
```

自动发现顺序为 `CODEX_HOME` → 默认 `~/.codex` → 询问自定义路径，找到首个有效目录即停止。自定义目录也可直接使用 `nido codex setup --home /path/to/.codex`。没有安装时，明确选择外部 HOME，由官方脚本安装：

```sh
nido codex install --home "$HOME/.codex"
```

完成后重新加载环境，直接运行 Codex：

```sh
. "$NIDO_HOME/env.sh"
codex
```

关联会配置 `CODEX_HOME`，并将已识别的官方命令目录加入环境。Nido 不移动 HOME、不包装 Codex 执行、不管理 daemon，也不解析或修改认证、会话和数据库。未关联时，不介入外部 Codex。

配置代理和更新：

```sh
nido codex proxy http://127.0.0.1:7890
nido codex update --check
nido codex update
```

代理只写入 `$CODEX_HOME/.env` 中属于 Nido 的配置块，保留其他内容；已有代理变量冲突时停止。更新委托官方脚本，并注入目录和代理。npm、Homebrew 等已有安装继续使用原渠道更新。

## 配置 Conda / pip

本组件自 v0.8.0 提供。v0.8.1 已修复隐式用户 `.condarc` 切源时混入旧 defaults 的问题，并按实际管理项查询配置；[本次 Linux amd64 发行与验收状态](docs/conda-v0.8.1-release.md)单独记录，公网路径仍有网络限制。本次只以 Linux amd64 为执行验收目标，不在 macOS 安装。首次启用先保存本机原有镜像为持久的 `default`，之后可在原配置、官方和清华之间切换。缓存与环境位置直接写入上游原生配置。

```sh
nido conda setup
nido conda mirrors
nido conda mirror conda tuna
nido conda mirror pip tuna
nido conda cache conda "$HOME/.cache/conda/pkgs"
nido conda cache pip "$HOME/.cache/pip"
nido conda envs "$HOME/.conda/envs"
. "$NIDO_HOME/env.sh"
nido conda validate
```

Conda 镜像支持 `default`、`official`（别名 `conda`）、`tuna`、`off`；pip 官方别名为 `pypi`，另支持 `ustc` 和自定义 HTTPS simple index。首次切换也会自动捕获对应工具的 `default`，以后不会覆盖；`default` 跨 Shell 和重启保留。已有自定义 CONDARC 时保留原路径，只原地修改镜像字段，证书、代理等设置继续实时读取；没有显式文件时使用镜像 overlay。选定镜像与环境覆盖需重新加载 `env.sh`；缓存和环境目录写入原生配置后即持久保留，无需 source。

原生修改保留其他键的语义、注释、权限和所属组，Conda YAML 的格式可能调整；不迁移或清空数据。镜像操作将完整选中上下文同步到受管标准用户文件及原 CONDARC，显式 `default` 使用首次已捕获的镜像字段，保留证书、代理等非镜像设置。新复制的镜像凭据不得写入他人可读的文件。`off` 只停止管理单项，`reset` 清空选择但保留 `default`、原 CONDARC 关联及原生值。`status` / `show` / `mirrors` 为静态查看；setup、镜像切换和 `validate` 会显式查询本地工具，不联网或安装包。旧 Conda 的 envs 查询可能尝试打开 base history 测可写性，Nido 的配置查询不是上游进程沙箱。详见[使用指南](docs/usage.md#conda--pip)、[设计说明](docs/conda-design.md)及[目标版本 P0 验收](docs/conda-p0-acceptance.md)。

## 使用内置 Proxy

独立 Proxy 组件自 v0.4.0 提供；下述 quick start、节点链接解析和类别/单工具代理绑定自 **v0.6.0** 提供。Nido 安装包只包含 CLI，Mihomo 需显式安装。

Proxy 的服务器验证目标为 Linux amd64，已完成原生 race 和指定版本对的真实停机更新；各平台的历史证据与验证边界见 [Proxy 发布前审计](docs/proxy-audit.md#跨版本验证后的最终发布审计2026-10-18)。其他平台的验证缺口不作为该部署目标的发布阻塞项。

```sh
nido proxy install
nido proxy validate
nido proxy start
nido proxy status
nido proxy endpoints --json
```

交互 `install` 安装后首次直接进入 quick start：填写 provider URL、日常 filter/端口，再选择独立下载和 ISP；ISP 接受一行节点链接，可配置测试地址及 provider 前置节点 filter。输入正常显示，完整 YAML 预览后验证保存；各代理组使用 `url-test`。`install --yes` 只安装，不猜测配置；随后可用 `proxy configure` 补齐首次引导，已有配置时只显示路径。也可用 `nido proxy configure --file /absolute/config.yaml` 导入私有原生 YAML。安装和配置不启动；主配置固定为 **`$NIDO_HOME/proxy/config.yaml`**。

默认只生成回环 listeners，不启用传统通用端口，规则仅 `MATCH,DIRECT`，各 listener 明确指定出站。用户可以修改全部 listener 名称和原生配置；快捷绑定从 listeners 读取候选，Codex/Git 也允许自定义代理地址。按类别或单工具分别选择：

```sh
nido proxy bind ai               # 当前为 Codex
nido proxy bind daily            # 当前为 Git
nido proxy bind download         # v0.8.2：Nido 内部 GitHub API
nido proxy bind --tool codex
nido proxy bind --tool git --proxy http://127.0.0.1:7890 --yes
```

类别配置一次应用到类别内所有工具，单工具配置只影响自身。Codex 只写关联 HOME 的受管 `.env` 块，Git 只写用户级 `[http] proxy` 受管块；不处理 Git SSH 远端。这些工具未安装 Proxy 或没有 listener 时仍可选自定义地址。

v0.8.2 增加 `proxy bind download`（或 `--tool nido`）：一次选择具名、无认证 listener，启用 Nido 自身、Proxy、GDS 及 Nido 主动查询 Codex 版本的内部 GitHub API；后续自动经该入口访问官方 URL，发行包和摘要仍使用原 GitHub 文件加速策略。升级不会自动选择或创建绑定；未配置或移除绑定时 API 功能禁用，上述依赖 API 的操作先提示未启用。`--proxy`、`--github-proxy` 或终端代理变量不能替代启用步骤。本地 artifact、dry-run、无需 API 的同版本操作可用；普通 status/doctor 不联网，未绑定属于正常禁用状态。`download show --json` 的 `api_state` 为 `disabled` 或 `configured`，后者只说明已配置，不表示网络就绪。

绑定独立保存在私有 `download-proxy.toml`，不修改全局环境、Codex 官方安装/更新脚本和程序运行流量，或用户的 curl/Git/Conda/pip。`nido codex update --check` 未绑定时失败；普通 Codex 更新未绑定时跳过 Nido 的可选 API 预查询，继续按原有独立代理和确认流程委托官方脚本。持久路由的 listener 失效或受管服务未就绪时明确失败，不直连、不自动启动；已有有效非空绑定记录后，高级 `--proxy` 可临时覆盖 API 路由，不核验所保存 listener 是否运行。文件自举安装脚本不依赖 API，首次 Proxy 安装使用可信本地 artifact，随后配置、启动并绑定。v0.8.1 没有上述绑定和禁用规则，旧客户端的临时代理升级方式及新版本启用步骤见[自举方案](docs/nido-proxy-bootstrap.md)和[发行记录](docs/nido-v0.8.2-release.md)。

`status` 只做本地文件与进程观察，`validate` 执行核心检查，显式 `probe` 才测试指定网络目标；多入口需明确名称：

```sh
nido proxy logs --tail 100
nido proxy endpoint endpoint-proxy
nido proxy probe --endpoint endpoint-proxy --url https://example.com
nido proxy restart
nido codex proxy builtin
```

`restart` 先验证新配置，失败时保留旧进程。最后一条沿用 listener 选择与自定义地址流程；端口改变后需显式重新绑定，不会后台覆盖工具配置。Proxy 不依赖 Codex，也不配置全局代理。

更新先显式停止，替换完成后仍保持停止：

```sh
nido proxy update --check
nido proxy stop
nido proxy update
nido proxy start
```

更新不调用 controller 热更新，不清理已有未知数据。新源码的内部 API 必须先绑定 listener 才启用，资产继续原文件策略；已保存有效非空绑定后可用本次 `--proxy URL` 临时覆盖，未绑定时不会因该参数启用 API。需要借用旧代理下载时，先另行准备本地包，再停止并通过 `--artifact /absolute/path/to/mihomo.gz` 更新。详见 [Proxy 使用说明](docs/usage.md#内置-proxy)及[组件设计](docs/proxy-design.md)。

## 常用命令

| 目的 | 命令 |
| --- | --- |
| 本地状态 / 环境检查 | `nido status` / `nido doctor` |
| 输出 Shell 环境 | `nido env bash` / `nido env zsh` |
| 检查 / 更新 Nido 本身 | `nido update --check` / `nido update` |
| 查看 / 保存 GitHub 下载策略 | `nido download show` / `nido download set auto` |
| 仅为本次 Codex 安装更新指定代理 | `nido codex update --proxy http://127.0.0.1:7890` |
| 清理 Nido 写入的 Codex 代理块 | `nido codex proxy off` |
| 解除 Codex 关联，保留程序与数据 | `nido codex detach` |
| 查看 / 核对 Conda 与 pip 配置 | `nido conda status` / `nido conda validate` |
| 停止 Conda 与 pip 配置管理 | `nido conda reset` |
| 查询 / 停止状态下更新内置 Proxy | `nido proxy update --check` / `nido proxy update` |
| 停止 / 卸载内置 Proxy 程序 | `nido proxy stop` / `nido proxy remove` |
| 安装官方可选 GDS | `nido gds install` |
| 查询 GDS 更新 | `nido gds update --check` |
| 查看命令说明 | `nido --help` |

内置组件卸载保留配置、日志和数据；安装、更新不会自动启动组件。Nido 不提供目录迁移、挂载路径恢复、版本仓库、通用回滚或外部 Codex 卸载功能。

Nido v0.5.0 起支持独立 GDS 官方下载；首版 GDS 为 `gds-v0.1.0`，面向 Linux amd64、glibc 2.31 及以上的完整 NVIDIA GPU。安装不配置或启动，需另行显式选择 GPU UUID。组件在连续空闲后保留显存，发现外部任务或观察失效时释放，默认不运行计算 kernel。可靠启动自己的任务前先执行 `nido gds stop` 并确认成功。真实 GPU 行为尚未验收；详见 [组件设计](docs/gds-design.md)、[审查记录](docs/gds-audit.md) 和 [独立发行说明](docs/gds-release.md)。

## 开发

源码构建需要 Go 1.23 或以上版本和 Make；固定发行工具链见 [.go-version](.go-version)。

```sh
git clone https://github.com/MiracleHYH/CDEToolkit.git
cd CDEToolkit
make build
make check
make smoke
make dist TARGET_OS=linux TARGET_ARCH=amd64
```

常规测试使用临时目录、模拟安装器和组件夹具，不需要真实 Codex、Conda、代理服务或 GPU。Conda CLI 隔离测试进入 `make check`，仅使用临时 HOME 与假命令，覆盖配置、撤销及重复加载环境。GDS 的 CPU 状态机、协议与假驱动测试进入常规测试，也可用 `make check-gds` 单独执行；普通用户 Linux amd64 原生 namespace 验证见[测试入口](docs/usage.md#可选-gds)。历史 Shell 测试通过 `make check-gds-legacy` 选择。

真实 Mihomo 的回环兼容测试通过 `NIDO_TEST_MIHOMO=/absolute/path/to/verified/mihomo make check-proxy-real` 显式执行；测试不下载程序，仅使用临时 HOME、临时软件根和本地 provider/HTTP/SOCKS 夹具。核心来源、结果和验证范围见 [Proxy 审计记录](docs/proxy-audit.md)。

已有 Linux Docker engine 时，可用已核验的 Linux 核心执行 `NIDO_TEST_MIHOMO=/absolute/path/to/linux-mihomo make check-proxy-linux`。它在无外部网络、无宿主挂载的临时同架构容器中原生运行，不安装系统服务。

原生 Linux 主机已有 bubblewrap 时，可用 `make check-proxy-linux-native` 运行同样的四产物隔离检查；race 模式要求 CLI 和两份测试程序全部启用检测。预构建输入、静态链接和两种入口的用法见 [Proxy 验证说明](docs/proxy-design.md#linux-race-与原生主机入口)。

本次 quick start 改动另有不使用真实核心的 Linux amd64 原生入口 `make check-proxy-quickstart-linux-native`，使用已有 SDK/GCC/bubblewrap 与离线 vendor，覆盖节点解析、配置生成、类别/单工具绑定及 CLI race；准备方式见[说明](docs/proxy-design.md#quick-start-的-linux-amd64-离线隔离验证)。新流程的真实 Mihomo 专项使用 `make check-proxy-quickstart-real-linux`，在独立网络空间内验证 provider、url-test、SS ISP 前置链及类别/单工具配置；输入、已执行场景和协议边界分别见[入口说明](docs/proxy-design.md#quick-start-的-linux-amd64-真实核心隔离验证)与[审计记录](docs/proxy-audit.md)。

- [使用指南](docs/usage.md)：目录、代理、官方安装更新与数据边界。
- [设计说明](docs/go-design.md)：外部集成与内置组件的职责及扩展方式。
- [构建与发行](docs/go-ci-release.md)：Linux amd64 包、隔离验证与签名；历史版本记录分别保留。
- [旧实现边界](docs/legacy-boundary.md)：冻结 Bash 源码与历史文档。
