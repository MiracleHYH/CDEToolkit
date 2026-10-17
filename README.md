# Nido: Lightweight Development Environment Manager

**用 `nido` 配置外部开发工具，维护少量内置组件。**

Nido 为 Codex 提供目录关联、环境与代理配置，以及官方安装更新入口；Proxy（Mihomo）和可选 GDS 则由 Nido 安装和管理。Codex 数据留在原处，你继续直接使用官方 `codex` 命令。

支持 Linux / macOS 的 amd64、arm64。Nido 本身是一个 Go 可执行文件。

> **当前 main 是尚未发布的 Nido 重构。** GitHub 已有 latest Release 仍是旧 `cdet`，其一行安装地址目前不会安装 Nido。以下先从源码构建体验；仓库和 Go module 暂时保留 CDEToolkit 名称。

[快速开始](#快速开始) · [Codex](#关联或安装-codex) · [Proxy](#使用内置-proxy) · [使用指南](docs/usage.md) · [开发与发行](docs/go-ci-release.md)

## 快速开始

需要 Go 1.23 或以上版本和 Make；固定发行工具链见 [.go-version](.go-version)。以下命令只初始化临时 Nido 目录，不安装组件，也不启动服务：

```sh
git clone https://github.com/MiracleHYH/CDEToolkit.git
cd CDEToolkit
make build

nido_demo_root="$(mktemp -d "${TMPDIR:-/tmp}/nido-demo.XXXXXX")"
"./build/$(go env GOOS)-$(go env GOARCH)/nido" --root "$nido_demo_root" init --yes
. "$nido_demo_root/env.sh"
nido status
```

正式使用时，将 `--root` 改为自己选择的独立目录，例如 `"$HOME/.nido"`，再加载对应的 `env.sh`。Nido 只使用这一个软件目录，内置组件位于其中；外部 Codex HOME 必须在其之外。

Nido 不自动修改 Shell 配置。需要新终端自动加载时，自行将正式目录的 `. "/实际软件目录/env.sh"` 加入 `~/.bashrc` 或 `~/.zshrc`；不要保存上述临时目录。

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

```sh
nido proxy install
```

将自己的 Mihomo 配置放到 **`$NIDO_HOME/proxy/config.yaml`**，再显式启动：

```sh
nido proxy start
nido status
nido codex proxy builtin
```

最后一条只把内置 Proxy 的本地地址配置给已关联 Codex，不会启动或重启任何组件。网络受限时，下载可使用当前终端已有的代理；Nido 不为了下载自动启动代理。

## 常用命令

| 目的 | 命令 |
| --- | --- |
| 本地状态 / 环境检查 | `nido status` / `nido doctor` |
| 输出 Shell 环境 | `nido env bash` / `nido env zsh` |
| 仅为本次 Codex 安装更新指定代理 | `nido codex update --proxy http://127.0.0.1:7890` |
| 清理 Nido 写入的 Codex 代理块 | `nido codex proxy off` |
| 解除 Codex 关联，保留程序与数据 | `nido codex detach` |
| 更新内置 Proxy | `nido proxy update` |
| 停止 / 卸载内置 Proxy 程序 | `nido proxy stop` / `nido proxy remove` |
| 显式安装可选 GDS | `nido gds install --artifact /absolute/path/to/gds` |
| 查看命令说明 | `nido --help` |

内置组件卸载保留配置、日志和数据；安装、更新不会自动启动组件。Nido 不提供目录迁移、挂载路径恢复、版本仓库、通用回滚或外部 Codex 卸载功能。

GDS 的 Go 调度启动尚未适配，当前启动操作会明确拒绝。仓库内 `scheduler.cu` 只是 CUDA worker，不能直接替代负责空闲检测的旧 monitor；本轮保留可选源码及显式程序管理，不会直接启动 worker。

## 开发

```sh
make check
make smoke
make dist TARGET_OS=linux TARGET_ARCH=amd64
```

测试使用临时目录、模拟安装器和组件夹具，不需要真实 Codex、代理服务或 GPU。GDS 的额外隔离测试通过 `make check-gds` 显式选择。

- [使用指南](docs/usage.md)：目录、代理、官方安装更新与数据边界。
- [设计说明](docs/go-design.md)：外部集成与内置组件的职责及扩展方式。
- [构建与发行](docs/go-ci-release.md)：隔离验证、四平台包与签名。
- [旧实现边界](docs/legacy-boundary.md)：冻结 Bash 源码与历史文档。
