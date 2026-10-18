# Nido: Lightweight Development Environment Manager

**用 `nido` 配置外部开发工具，维护少量内置组件。**

Nido 为 Codex 提供目录关联、环境与代理配置，以及官方安装更新入口；Proxy（Mihomo）和可选 GDS 则由 Nido 安装和管理。Codex 数据留在原处，你继续直接使用官方 `codex` 命令。

支持 Linux / macOS 的 amd64、arm64。Nido 本身是一个 Go 可执行文件。

从 **v0.3.0** 起，项目使用 Nido 名称和 `nido` 命令；仓库和 Go module 暂时保留 CDEToolkit 名称。旧 `cdet` 安装不会被自动迁移或替换。

[快速开始](#快速开始) · [Codex](#关联或安装-codex) · [Proxy](#使用内置-proxy) · [使用指南](docs/usage.md) · [开发与发行](docs/go-ci-release.md)

## 快速开始

需要 curl、tar 和 SHA-256 工具（Linux 通常自带 `sha256sum`，macOS 自带 `shasum`），无需 Go。下面一行安装最新 Go 版到 `~/.nido`，并在当前终端加载 `nido` 命令：

```sh
curl --http1.1 -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh && . "${NIDO_HOME:-$HOME/.nido}/env.sh"
nido version
nido status
```

安装器只安装 Nido，不安装组件、不关联已有 Codex，也不启动服务。再次运行同一命令会更新 Nido，保留已有管理配置。可提前设置 `NIDO_HOME` 选择其他独立软件目录；外部 Codex HOME 必须在其之外。

安装脚本运行在子 Shell 中，不能直接更改当前终端的 PATH，因此命令末尾会显式加载 `env.sh`。Nido 不自动编辑 Shell 配置。需要新终端自动加载时，将 `. "$HOME/.nido/env.sh"` 加入 `~/.bashrc` 或 `~/.zshrc`；自定义目录请替换路径。

## 关联或安装 Codex

已有 Codex 时，发现并关联现有目录：

```sh
nido codex setup
```

自定义目录可直接使用 `nido codex setup --home /path/to/.codex`。没有安装时，明确选择外部 HOME，由官方脚本安装：

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

## 使用内置 Proxy

以下独立 Proxy 组件及命令自 v0.4.0 提供；早期发行版以其 `--help` 为准。Nido 安装包只包含 CLI，Mihomo 仍需显式安装。

Proxy 的服务器验证目标为 Linux amd64，已完成原生 race 和指定版本对的真实停机更新；各平台的历史证据与验证边界见 [Proxy 发布前审计](docs/proxy-audit.md#跨版本验证后的最终发布审计2026-10-18)。其他平台的验证缺口不作为该部署目标的发布阻塞项。

```sh
nido proxy install
nido proxy configure
nido proxy validate
nido proxy start
nido proxy status
nido proxy endpoint
```

`configure` 首次交互隐藏订阅 URL，生成原生 Mihomo provider 配置；已有配置时只显示路径。也可用 `nido proxy configure --file /absolute/config.yaml` 导入私有 YAML 文件，文件须禁止 group/other 访问。安装和配置均不自动启动；主配置固定为 **`$NIDO_HOME/proxy/config.yaml`**。

默认只接受本地回环 HTTP/SOCKS 代理。端点优先选择名为 `common` 的入口，否则要求唯一；多个入口用 `nido proxy endpoint NAME` 显式选择。`status` 只做本地文件与进程观察，`validate` 执行核心检查，显式 `probe` 才测试指定网络目标：

```sh
nido proxy logs --tail 100
nido proxy probe --url https://example.com
nido proxy restart
nido codex proxy builtin
```

`restart` 先验证新配置，失败时保留旧进程。最后一条只把默认无认证端点写入已关联 Codex 的代理块，不会启动组件。Proxy 不依赖 Codex，也不配置全局代理。

更新先显式停止，替换完成后仍保持停止：

```sh
nido proxy update --check
nido proxy stop
nido proxy update
nido proxy start
```

更新不调用 controller 热更新，不清理已有未知数据。下载可使用当前终端代理或本次 `--proxy URL`；需要借用旧代理下载时，先另行准备本地包，再停止并通过 `--artifact /absolute/path/to/mihomo.gz` 更新。详见 [Proxy 使用说明](docs/usage.md#内置-proxy)及[组件设计](docs/proxy-design.md)。

## 常用命令

| 目的 | 命令 |
| --- | --- |
| 本地状态 / 环境检查 | `nido status` / `nido doctor` |
| 输出 Shell 环境 | `nido env bash` / `nido env zsh` |
| 仅为本次 Codex 安装更新指定代理 | `nido codex update --proxy http://127.0.0.1:7890` |
| 清理 Nido 写入的 Codex 代理块 | `nido codex proxy off` |
| 解除 Codex 关联，保留程序与数据 | `nido codex detach` |
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

常规测试使用临时目录、模拟安装器和组件夹具，不需要真实 Codex、代理服务或 GPU。GDS 的 CPU 状态机、协议与假驱动测试进入常规测试，也可用 `make check-gds` 单独执行；普通用户 Linux amd64 原生 namespace 验证见[测试入口](docs/usage.md#可选-gds)。历史 Shell 测试通过 `make check-gds-legacy` 选择。

真实 Mihomo 的回环兼容测试通过 `NIDO_TEST_MIHOMO=/absolute/path/to/verified/mihomo make check-proxy-real` 显式执行；测试不下载程序，仅使用临时 HOME、临时软件根和本地 provider/HTTP/SOCKS 夹具。核心来源、结果和验证范围见 [Proxy 审计记录](docs/proxy-audit.md)。

已有 Linux Docker engine 时，可用已核验的 Linux 核心执行 `NIDO_TEST_MIHOMO=/absolute/path/to/linux-mihomo make check-proxy-linux`。它在无外部网络、无宿主挂载的临时同架构容器中原生运行，不安装系统服务。

原生 Linux 主机已有 bubblewrap 时，可用 `make check-proxy-linux-native` 运行同样的四产物隔离检查；race 模式要求 CLI 和两份测试程序全部启用检测。预构建输入、静态链接和两种入口的用法见 [Proxy 验证说明](docs/proxy-design.md#linux-race-与原生主机入口)。

- [使用指南](docs/usage.md)：目录、代理、官方安装更新与数据边界。
- [设计说明](docs/go-design.md)：外部集成与内置组件的职责及扩展方式。
- [构建与发行](docs/go-ci-release.md)：隔离验证、四平台包与签名。
- [旧实现边界](docs/legacy-boundary.md)：冻结 Bash 源码与历史文档。
