# CDEToolkit 使用指南

命令名为 `cdet`。首次使用从 [README 快速开始](../README.md#快速开始) 安装 CLI、加载 PATH，再选择要管理的组件。本文中的 `/path/to/...` 和 `/new/location/...` 都需要替换为自己的实际路径。

## 安装选项与更新 cdet

首次安装只初始化 CLI 和空清单，重复安装复用已有清单。安装器不安装组件、不修改 Shell 配置、不注册或启动服务；安装完成后无需再次运行 `cdet init`。

非交互安装时，明确指定两个互不包含的目录：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh \
  | sh -s -- --prefix /path/to/cdet-cli --store /path/to/components
```

也可通过 `CDET_PREFIX`、`CDET_STORE` 指定。需要固定版本时：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh \
  | sh -s -- --version 0.2.1 --prefix /path/to/cdet-cli --store /path/to/components
```

更新 `cdet` 本身时，重新运行第一条安装命令，使用原来的两个目录。不要只因为 `cdet` 已加入 PATH 就省略自定义目录：下载器不会从 PATH 推断当前实例。工具没有 `cdet update self` 命令。

下载器核对发行包的 SHA-256 后，将初始化交给 Go CLI。发行资产及其 Sigstore 签名可从 [Releases](https://github.com/MiracleHYH/CDEToolkit/releases) 下载。

使用自定义安装目录时，加载当前终端的 PATH：

```sh
eval "$("/path/to/cdet-cli/bin/cdet" env bash)"
cdet version
```

这条命令适用于 Bash / Zsh；可自行加入对应的 `~/.bashrc` 或 `~/.zshrc`。挂载变化后，应先修复再更新该行中的 CLI 路径。

## 两个目录怎样分工

```text
<安装目录>/
  bin/cdet                     # 管理命令
  bin/codex                    # 安装 Codex 后生成的启动入口
  bin/mihomo                   # 安装 Proxy 后生成的启动入口
  config.toml                  # 两个根目录的关系与身份
<组件存储目录>/
  .cdet/                       # 管理清单与受管程序
  codex/                       # 完整 CODEX_HOME
  proxy/                       # Mihomo 配置、规则、缓存等
  gds/                         # 显式安装 GDS 后才有
```

两个根互不包含。组件数据直接位于 `<store>/<组件名>`，例如 `CODEX_HOME=<store>/codex`。请为工具选择独立目录，不要把项目或实验结果目录整体作为管理根。

## 完整迁入已有组件

默认安装会发现标准 HOME / XDG 目录和 PATH 中的已有组件，不搜索整盘。没有发现时才全新安装；自动发现的完整迁入需要确认。

```sh
cdet scan
cdet install codex proxy --dry-run
cdet install codex proxy
```

自定义来源用 `--from NAME=DIR` 指定完整数据根。显式指定表示选择该来源，不再二次确认，因此建议先查看预览：

```sh
cdet install codex --from codex=/path/to/existing/.codex --dry-run
cdet install codex --from codex=/path/to/existing/.codex
cdet install proxy --from proxy=/path/to/existing/mihomo
```

迁入复制并校验整个目录，来源原件保留。它不会自动下钻名为 `data/` 的子目录；Codex 只支持完整 CODEX_HOME 迁入，不提供“只迁对话”。Codex 程序由官方安装脚本部署到受管位置，数据按文件复制，不解析或修改数据库结构。

迁入前退出访问来源目录的 Codex CLI、daemon / app-server、Mihomo 等进程。工具不会替你停止进程；检测到占用或无法可靠检查时会拒绝操作。

普通文件的权限和 mtime 会保留。合法内部链接可在副本中重定位，外部或循环链接、特殊文件及已识别的外置资源会阻止不完整迁入。系统 keyring 认证、外部项目、附件、MCP 和插件依赖不会随 HOME 自动搬迁。外置 `CODEX_SQLITE_HOME` 或显式 `sqlite_home` 也需先检查；不要把完整 HOME 复制理解为所有外部依赖都会自动适配。

来源原件的最终清理由用户验证后的独立操作完成。

## 明确全新安装

不想迁入外部安装时，使用 `--fresh`：

```sh
cdet install codex --fresh
cdet install proxy --fresh
```

`--fresh` 表示忽略外部安装，不会清空已经受管的组件数据。已有受管组件需要更新时，使用 `cdet update`。

Codex 的安装和更新统一使用官方脚本，注入受管目录、代理环境和隔离的临时 HOME，不接受 `--artifact codex=...` 或 `--sha256 codex=...`。

## Proxy 程序与配置

默认从 Mihomo 官方发布包安装；也可提供本地程序包或有摘要的自定义 HTTPS 包：

```sh
cdet install proxy --fresh --artifact proxy=latest
cdet install proxy --fresh --artifact proxy=/path/to/mihomo.gz
cdet install proxy --fresh --artifact proxy=https://example.org/mihomo.gz \
  --sha256 proxy=填写实际的64位十六进制摘要
```

这些是互相替代的安装方式。只有 `--fresh` 明确跳过外部安装；`--artifact proxy=latest` 本身不会阻止迁入已有组件。

新装后由你提供 `<store>/proxy/config.yaml`，工具不生成订阅、节点或凭据。配置就绪后：

```sh
cdet exec proxy --
```

该命令在前台运行 Mihomo，传入受管的 `-d` 数据目录和 `-f` 配置位置；也可使用 `mihomo` 启动入口。CDEToolkit 不提供 systemd / Supervisor 服务管理命令。

下载、安装和受管组件继承当前终端的代理环境。已有代理需先可用，工具不会为下载自动启动代理。

## 查看版本与更新组件

```sh
cdet status
cdet versions codex proxy
cdet update codex --dry-run
cdet update codex
cdet update proxy
```

`status` 查询本机程序版本；`versions` 联网查询上游最新版本。更新先展示版本和计划，再要求确认；`--yes` 可用于已确认计划的非交互执行。`--dry-run` 不运行安装器、不下载程序、不调用更新 POST。

Codex 使用官方安装脚本。Mihomo 在本地官方控制 API 适用时优先使用官方更新，**可能重启 Mihomo**；不适用时才下载、校验并替换本地程序。官方调用失败后不会静默切换另一条更新路线。工具不维护历史版本仓库或回滚系统。

## 挂载路径变化后的修复

先停止使用受管组件的进程。两根一起移动、且相对关系保持不变时，从新 CLI 位置预览并执行：

```sh
/new/location/cdet-cli/bin/cdet repair --dry-run
/new/location/cdet-cli/bin/cdet repair
```

只有存储根移动时，明确选择新位置：

```sh
cdet repair --store /new/location/components --dry-run
cdet repair --store /new/location/components
```

工具核对安装与存储身份，寻找原存储，而不是创建一个空目录替代。存在同 ID 的多份副本时，需要明确选择 `--store`。如果终端仍导出旧的 `CDET_PREFIX` / `CDET_STORE`，请先清除这些旧值，或用 `--prefix` / `--store` 指定两个新位置。

修复会重建引导、环境、受管入口，并安全重绑已知的官方 Codex `current` 和可见命令链接；不会改写配置、认证、会话、数据库或任意内部绝对路径。外部项目和插件依赖仍需单独核对。安装根变化后，更新 Shell 中的 PATH 加载行。

## 卸载与数据保留

```sh
cdet remove codex --dry-run
cdet remove codex
cdet remove proxy
cdet remove self
```

组件卸载会移除受管程序和启动入口，保留完整 `<store>/<组件名>` 及未知文件。`remove self` 移除 CLI 和其入口，保留组件存储与引导身份，以便后续重新初始化。工具没有删除组件数据的 purge 选项。

操作按组件顺序执行；多组件操作不是全部成功或全部撤销。失败时保留已发布的数据目录和必要的诊断标记，不自动删除完整 HOME。迁入源的原件也不会被卸载删除。

## 可选 GDS

GDS 不在默认安装或更新范围内，必须显式提供本地独立程序包：

```sh
cdet install gds --fresh --artifact gds=/path/to/gds
```

安装后仍由你显式运行。GDS 没有默认在线下载源。

## 旧版兼容

v0.1.0 使用旧管理布局，不能原位升级到当前双目录协议。请选择独立的安装目录与组件存储目录，再按本指南处理组件迁入。根目录的旧 `install.sh` 是冻结的 Bash 入口；请使用 README 的 GitHub Release 安装地址。
