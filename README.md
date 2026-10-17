# CDEToolkit

**用 `cdet` 统一管理开发机上的 Codex 和代理。**

CDEToolkit（Cloud Development Environment Toolkit）是一个轻量的命令行工具：新开发机上直接安装组件，已有环境中完整迁入组件数据，持久化存储换了挂载位置后重新定位并修复入口。

支持 **Linux / macOS · amd64 / arm64**。运行时只需一个 Go 编译的可执行文件，无需安装 Go、Python 或 Node。

[快速开始](#快速开始) · [已有组件迁入](#已有组件迁入) · [常用命令](#常用命令) · [使用指南](docs/usage.md) · [下载版本](https://github.com/MiracleHYH/CDEToolkit/releases/latest)

## 快速开始

### 1. 安装 CDEToolkit

在需要管理的开发机终端执行：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

安装器下载最新正式版，并询问两个目录。直接回车即可使用默认值：

| 目录 | 默认位置 | 用途 |
| --- | --- | --- |
| 安装目录 | `~/.cdet` | `cdet` 和组件的命令入口 |
| 组件存储目录 | `~/.cdet-store` | Codex、Proxy 等组件的程序和完整数据 |

两个目录应选择在持久化存储上，且互不包含。安装器需要 `curl`、`tar` 和 SHA-256 工具（`sha256sum` 或 `shasum`）。这一步只安装管理工具，组件在下一步单独安装。

### 2. 让终端找到 cdet

使用默认安装目录时，在当前 Bash / Zsh 终端执行：

```sh
eval "$("$HOME/.cdet/bin/cdet" env bash)"
cdet version
```

若选择了自定义安装目录，将第一行中的 `$HOME/.cdet/bin/cdet` 替换为实际的 `<安装目录>/bin/cdet`。命令中的 `env bash` 输出也适用于 Zsh。

安装器不自动修改 Shell 配置。希望每次打开终端都能使用时，将上面的 `eval` 行加入自己的 `~/.bashrc` 或 `~/.zshrc`。

### 3. 安装并使用 Codex

```sh
cdet install codex
codex
```

没有已有安装时，`cdet` 调用 Codex 官方脚本安装程序，并使用受管的 `CODEX_HOME`。进入你的项目目录运行 `codex`，按 Codex 的提示完成首次登录即可。Codex 本身的使用见[官方文档](https://learn.chatgpt.com/docs/codex/cli#getting-started)。

如果发现已有 Codex，命令会提示将**完整 CODEX_HOME** 迁入组件存储目录，并保留来源副本。迁入前请先退出使用该目录的 Codex、daemon / app-server 等进程。可先运行 `cdet install codex --dry-run` 查看计划。

## CDEToolkit 能帮你做什么

| 场景 | 使用方式 |
| --- | --- |
| 准备一台新开发机 | 安装 Codex、Mihomo 代理；目录统一管理 |
| 整理已有开发环境 | 将组件的完整数据迁入存储目录，保留来源副本 |
| 持久化存储挂载路径变化 | 核对原存储身份，重新定位并重建受管命令入口 |
| 日常维护 | 查看组件状态、比较软件版本、更新程序、卸载并保留数据 |

程序安装、迁入和路径修复不会自动注册或启动服务。组件需要时由你显式运行。

## 安装与使用 Proxy

Proxy 使用 **Mihomo** 内核：

```sh
cdet install proxy
cdet status
```

已有安装会提示完整迁入；没有安装时下载官方最新程序。新装后，将自己的 Mihomo 配置放到 `<组件存储目录>/proxy/config.yaml`，然后在前台运行：

```sh
cdet exec proxy --
```

也可使用生成的 `mihomo` 命令入口。工具会传入受管的数据目录和配置位置；订阅、节点及凭据由你自行提供。

下载和受管组件继承当前终端的代理环境。网络受限时，先确保已有代理可用；`cdet` 不会为了下载自动启动代理。

## 已有组件迁入

先查看标准 HOME / XDG 目录及 PATH 中能发现的组件：

```sh
cdet scan
cdet install codex proxy --dry-run
```

确认计划后运行 `cdet install codex proxy`。自定义位置可以明确指定来源：

```sh
cdet install codex --from codex=/path/to/existing/.codex --dry-run
cdet install codex --from codex=/path/to/existing/.codex
cdet install proxy --from proxy=/path/to/existing/mihomo
```

`--from` 指向完整数据根，表示明确选择该来源，实际执行时不再二次询问。迁入前退出相关进程；工具复制并校验完整目录，不改写原件或解析 Codex 内部数据库。外置数据库、外部链接及系统 keyring 等边界见[迁入说明](docs/usage.md#完整迁入已有组件)。

## 常用命令

| 目的 | 命令 |
| --- | --- |
| 查看组件状态和本机版本 | `cdet status` |
| 检查是否需要修复 | `cdet doctor` |
| 查看当前与上游最新版本 | `cdet versions` |
| 预览组件更新 | `cdet update codex --dry-run` |
| 更新 Codex / Proxy | `cdet update codex` / `cdet update proxy` |
| 显式运行受管组件 | `cdet exec codex --` / `cdet exec proxy --` |
| 卸载程序并保留数据 | `cdet remove codex` / `cdet remove proxy` |
| 查看完整选项 | `cdet --help` |

更新会展示计划并请求确认。Codex 使用官方安装脚本；Mihomo 优先使用官方更新接口，该操作**可能重启 Mihomo**。更新 `cdet` 本身时，重新运行一行安装并选择原来的两个目录；自定义目录可[显式指定](docs/usage.md#安装选项与更新-cdet)。

## 挂载路径变化后修复

先退出使用受管组件的进程。安装目录和组件存储目录一起移动、且相对关系保持不变时，从新位置运行：

```sh
/new/location/cdet-cli/bin/cdet repair --dry-run
/new/location/cdet-cli/bin/cdet repair
```

仅组件存储目录移动时：

```sh
cdet repair --store /new/location/components --dry-run
cdet repair --store /new/location/components
```

修复会核对原存储身份，重建入口和已知的官方程序链接。安装目录变化后，重新加载新位置的 PATH。组件配置中的任意绝对路径、外部项目和插件依赖仍需单独处理，详见[路径修复](docs/usage.md#挂载路径变化后的修复)。

## 更多用法

- [使用指南](docs/usage.md)：自定义目录、固定版本、明确全新安装、Proxy 本地包、可选 GDS、迁入边界和卸载。
- [设计说明](docs/go-design.md)：双目录与上游安装更新职责。
- [安全与验证范围](docs/two-root-audit.md)：数据保留、进程检查和隔离测试。
- [开发与发行](docs/go-ci-release.md)：本地构建、CI、四平台包和签名。

**旧版提示：** v0.1.0 的管理状态不能原位升级到当前双目录版本。请选择独立的安装目录和组件存储目录，再按完整迁入流程处理已有组件。

## 参与开发

源码最低要求 Go 1.23，固定发行工具链见 [.go-version](.go-version)。

```sh
make check
make smoke
make dist TARGET_OS=linux TARGET_ARCH=amd64
```

检查使用临时目录、模拟安装器和组件夹具。旧 Bash 源码冻结保留，不作为 Go 生命周期后端或进入发行包，详见[旧实现边界](docs/legacy-boundary.md)。
