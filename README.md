# Nido: Lightweight Development Environment Manager

**在开发容器中输入 `nido`，交互配置 Proxy、Codex 和 Conda/pip。**

Nido 是用户级的 Go 命令行工具：管理内置 Mihomo Proxy；为外部 Codex 提供目录关联、PATH、代理和官方安装更新入口；快捷修改 Conda/pip 镜像、缓存与环境目录。它不管理云平台、镜像、CUDA/驱动或 Python/Conda 环境内部，也不搬迁外部数据。

> v0.10.1 修复 SS2022 分享链接兼容、Codex 命令发现与菜单判断。下面的两条安装命令均使用最新正式版本，详见 [v0.10.1 发行说明](docs/nido-v0.10.1-release.md)。

## 安装

面向 Linux amd64，需要 curl、base64、gzip 和 SHA-256 工具，无需 Go。

官方入口：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

GitHub 访问困难时，使用自己信任的加速入口：

```sh
curl -fsSL https://gh-proxy.org/https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

两条命令均无需额外参数。安装脚本自带压缩的 Go 程序，不再二次下载；解包校验后，在统一界面选择安装目录、GitHub Accelerate、Proxy 与可选 Bash/Zsh 自动加载，最终确认。默认软件目录是 `~/.nido`，也可选择自定义绝对路径；安装器不配置或启动组件。首次脚本下载的网络路径由上面所选 URL 决定。

安装完成后，执行输出中对应**实际软件目录**的环境加载命令，然后打开菜单：

```sh
. '/实际安装目录/env.sh'
nido
```

安装子进程无法改变父终端环境。Shell 注册只影响后续读取对应启动文件的新终端，当前终端仍需加载 `env.sh`。

第三方加速服务可以接触并替换其转发的脚本、包和摘要。客户端检查 SHA-256，不验证 Sigstore；摘要一致不能独立证明官方来源。仅使用自己信任的服务。

## 日常使用

```text
nido
  ── 组件 ──
  Proxy
  Codex
  Conda / pip 配置
  ── Nido ──
  GitHub Accelerate：None / 加速前缀 / Proxy
  Proxy：None / URL
  检查更新
```

使用 **↑↓ 选择、Enter 执行、Esc 返回、Ctrl+C 安全退出**；路径和 URL 才需要文本输入，确认默认取消。状态直接显示，动作按分组平铺，执行后保留页面和焦点。状态不查询远端最新版、不自动测速；显式查询才访问相应服务。参数与 `--json` 保留给自动化，无终端时不会等待交互。

### Proxy

在 Proxy 页面选择“安装 Proxy”，Nido 自动下载并校验 Mihomo 内核。安装完成后选择：

- **Quick Start**：按 Nido 推荐规范配置 Provider、订阅筛选、节点 URI、listeners、独立下载入口与可选 ISP/前置链路。
- **Skip / 高级配置**：跳过生成器，自行编辑显示的 `config.yaml`。

已有配置也能重复 Quick Start。确认替换时，仅保留最近一份原配置 `config.yaml.back`；首次生成、无变化、取消或校验失败不会更新备份。配置和启动分别确认，不自动启动或热重载。高级配置仍需符合用户级运行边界。

运行状态和 proxy-group 的选择链直接显示，例如 `组 -> 下游组 -> 节点: 最近延迟（时间）`。延迟来自 core 已有测量，缺失、循环或无法确定最终节点时明确提示。更新内核、更新订阅、重载、重启、停止和启动均为一级动作；内核更新委托 Mihomo 自身的稳定通道，新 Quick Start 的 Provider 默认不自动更新。原生更新需要内核运行，不为更新自动启动服务。

Proxy 只管理自身，不替 Codex、Git 或 Nido 设置代理。删除 Proxy 会先停止受管内核并删除程序，保留配置及数据；再次安装可复用它们。配置路径直接显示，高级用户自行编辑文件或查看日志。

### Codex

在 Codex 页面选择关联、代理、官方安装更新或解除关联。

已有目录按 `CODEX_HOME` → `~/.codex` → 自定义路径发现，用户可跳过。Nido 不移动目录、不包装 Codex、不管理 daemon，也不解析认证、会话或数据库。解除关联保留程序和数据，只清理仍能证明归属的配置。

数据目录已关联不代表程序已安装。命令从当前 `PATH`、显式 `CODEX_INSTALL_DIR`、默认 `~/.local/bin` 发现；可选择“重新发现 Codex 命令”，确认后刷新关联。缺少命令时提供官方安装，确认 standalone 入口后才提供官方更新；不覆盖 npm、Homebrew 或未知来源的程序。

“配置 Codex 运行代理”只修改 Codex `.env` 中的受管块。由 Nido 发起的官方安装、更新和版本查询使用 Nido 的 Proxy 设置，不读取 Codex 运行代理，也不使用 GitHub 加速前缀。安装更新委托官方脚本；其他既有安装渠道不被静默转换。

### Conda / pip

菜单快捷设置 Conda/pip 镜像、Conda 环境目录、Conda 包缓存和 pip 缓存。修改前显示 Nido 已保存选择、目标值及实际写入文件；原生覆盖因素可通过显式验证检查。

目录修改只更新配置，**不移动、创建、清理现有环境或缓存**。Nido 不安装 Conda/Python，不创建环境、不解析依赖。首次镜像快照和私有频道保护继续保留；停止管理不等于恢复任意历史原生配置。

### Nido 网络设置

首页 Nido 分组直接提供两项设置。GitHub Accelerate 选择 GitHub 文件路线：

| 模式 | 行为 |
| --- | --- |
| `None` | 官方 GitHub 文件地址直连，忽略 Proxy |
| 加速前缀 | 拼接 `https://gh-proxy.org` 或自定义 HTTPS 前缀后直连加速站，忽略 Proxy |
| `Proxy` | 官方 GitHub 文件地址通过下方 Proxy 配置访问 |

三条文件路线互斥，不叠加、不自动回退。选择 `Proxy` 前必须配置通用代理；加速前缀只用于 GitHub 文件，不拼接到 GitHub API 或任意网站。

Proxy 为 `None` 或通用代理 URL，启用时预填 `http://127.0.0.1:7890`，允许修改。Nido 对外 API 请求（包括 GitHub API）使用它；Nido 启动的官方安装更新命令通过临时 `http_proxy`、`https_proxy`、`all_proxy` 环境变量使用它，不改变父终端或工具的持久运行配置。两项默认均为 None，不继承环境中的旧代理。

Nido 更新及 Mihomo 首次安装的 GitHub 文件遵循 GitHub Accelerate。Proxy 首装下载 Nido 固定并已核验大小和 SHA-256 的内核包，不依赖 GitHub API 或运行中的 Proxy。Mihomo 原生内核与订阅更新由 core 自身处理，使用其配置与网络规则；本地管理接口始终直连。Nido 不为此启动已有或新安装的 Proxy。

## 开发

需要 Go 1.26 或更新版本；发行构建固定使用仓库 `.go-version`：

```sh
make check
```

在 Linux amd64 上可额外运行 `make smoke`，验证本地发行包；其他开发平台可运行 `make dist TARGET_OS=linux TARGET_ARCH=amd64` 做交叉打包，运行验收仍需 Linux amd64。

公开源码与敏感信息检查：

```sh
go run ./cmd/nido-dist audit --repository .
```

核心和组件业务均为 Go。仅保留解包校验和调用程序的极薄 POSIX 引导、Go 生成的 Shell 环境片段及隔离测试脚手架。旧 Bash 业务入口已退役。统一界面共用一套固定版本 Go TUI，不增加后台轮询或常驻 UI 服务。

GDS 源码保留为独立开发模块，不注册到 Nido，不参与默认编译、CI 或发行。发布包只包含 Nido 程序，不包含用户配置和数据。

[使用指南](docs/usage.md) · [交互设计](docs/interactive-design.md) · [组件边界](docs/go-design.md) · [构建发行](docs/go-ci-release.md)
