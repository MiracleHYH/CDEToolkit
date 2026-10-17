# Nido 使用指南

GDS 保持可选。当前 Go 版保留显式程序安装、更新和删除，调度启动尚未接入，`nido gds start` 会明确拒绝。仓库的 CUDA worker 不能单独替代旧脚本的空闲检测与 monitor。

当前 main 为未发布的 Nido 单目录重构，命令为 `nido`。已有 GitHub latest Release 仍使用旧 `cdet`，不要用其下载结果验证本文行为。从 [README](../README.md#快速开始) 构建当前源码后再使用本指南。

## 初始化与 Shell 环境

选择一个独立软件目录，默认 `~/.nido`。从仓库构建后执行：

```sh
make build
"./build/$(go env GOOS)-$(go env GOARCH)/nido" --root "$HOME/.nido" init --yes
. "$HOME/.nido/env.sh"
nido version
```

也可以通过 `NIDO_HOME` 指定目录；显式 `--root` 优先。首次初始化只接受不存在或空目录，不把普通数据目录或旧 toolkit 目录变成 Nido。重复初始化保留配置，并刷新自己的程序和环境文件；用户修改过的 `env.sh` 不被静默覆盖。

初始化不安装组件、不自动关联 Codex、不启动服务、不编辑 Shell 文件。需要新 Bash / Zsh 自动生效时，自行将下面一行加入对应的 `~/.bashrc` 或 `~/.zshrc`：

```sh
. "$HOME/.nido/env.sh"
```

自定义目录请替换路径。配置 Codex 后重新加载同一文件，才能更新当前终端的环境；CLI 无法直接修改父 Shell。

`nido env bash` / `nido env zsh` 输出同样的环境片段。未关联 Codex 时，只配置 Nido 自身路径；关联后增加 `CODEX_HOME` 和已识别的 Codex 命令目录，不生成 Codex 包装入口。重复加载不会不断增加 PATH。

## 目录与所有权

```text
<Nido 软件目录>/
  bin/nido
  config.toml
  env.sh
  proxy/              # 显式安装后创建
    mihomo
    config.yaml       # 用户提供
  gds/                # 显式安装后创建
    gds

<用户选择的外部目录>/  # 不得与 Nido 软件目录相互包含
  .env                 # 仅 Nido 代理配置块属于本工具
  config.toml
  auth.json
  sessions/
  ...                  # Codex 自己管理
```

Nido 不移动、复制或重新组织外部 Codex 数据。软件目录自身可以放在持久化存储上，但当前版本不提供目录搬运或自动适配历史挂载路径的功能。

## 关联已有 Codex

```sh
nido codex setup
```

首次关联检查当前 `CODEX_HOME` 和默认 `~/.codex`，等价路径去重，不扫描整盘，不采用不可用候选或创建空替代目录。发现唯一候选后仍需确认；没有唯一候选时，可以输入自定义目录、选择安装或跳过。已经登记目录时，重复 `setup` 默认沿用该目录，不跟随新终端的环境变量切换关联。

明确指定已有目录：

```sh
nido codex setup --home /absolute/path/to/.codex
. "$NIDO_HOME/env.sh"
```

`setup` 只关联已存在目录，不安装或执行 Codex。PATH 中的 Codex 命令单独识别，命令存在不代表 HOME 已确定；没有找到程序时仍可完成目录关联，状态会显示命令缺失。

已有其他关联时先执行 `nido codex detach`，再关联新目录，避免留下旧目录中的代理配置。Nido 不自动移动数据、修复内部软链接或改写私有配置字段。

## 安装与更新 Codex

新安装必须明确选择外部 HOME：

```sh
nido codex install --home "$HOME/.codex"
. "$NIDO_HOME/env.sh"
codex
```

Nido 获取并执行 Codex 官方脚本，提供 `CODEX_HOME`、`CODEX_INSTALL_DIR` 与非交互环境。新装程序默认位于用户的 `~/.local/bin`，已有已关联 standalone 程序则使用原命令目录。程序和 HOME 都在 Nido 软件目录之外。

安装器使用临时 HOME、CURL_HOME 和 XDG_CONFIG_HOME，避免其 Shell 配置写入用户真实配置；下载与安装脚本接收代理环境。Nido 不替用户登录、启动 Codex、管理 daemon 或检查其内部数据库。

查看当前和最新版本，然后决定更新：

```sh
nido codex update --check
nido codex update --dry-run
nido codex update
```

`--check` 显式联网查询最新版本，`--dry-run` 不下载或执行安装器。实际更新需要确认，已确认的非交互调用可以加 `--yes`。更新继续委托官方脚本，不维护自己的 Codex 版本仓库或回滚副本。

npm、Homebrew 和无法确认来源的安装不会被静默转换为 standalone；仍可使用目录与代理辅助配置，程序更新交给原渠道。

更新前重新检查实际入口仍属于所关联 HOME 的 standalone 安装，不仅依赖保存的渠道记录。不能把上游 `packages` 内部的程序目录作为安装入口；入口或渠道变化时，停止并提示重新确认，不自动修补。

## Codex 代理配置

保持现有配置是默认行为。需要 Nido 配置代理时显式执行：

```sh
nido codex proxy http://127.0.0.1:7890
```

它只修改关联 HOME 的 `.env` 中带有 Nido 标记的代理块，支持 HTTP、HTTPS、SOCKS5、SOCKS5H 地址；不执行 `.env` 内容，不改其他配置。文件中已存在非 Nido 的同名代理变量、标记归属不明、受管内容后来被修改或 `.env` 为链接时，停止并保留原文件。

使用内置 Proxy 配置中的本地端口：

```sh
nido codex proxy builtin
```

这只读取 `<Nido 软件目录>/proxy/config.yaml`，不证明代理正在运行或可以联网，也不会启动服务。配置变化供后续启动使用，不影响已经运行的 Codex。

仅清理 Nido 的代理块：

```sh
nido codex proxy off
```

官方安装更新的代理优先级为：本次 `--proxy`、当前终端代理变量、Codex `.env` 中的字面代理变量。例如：

```sh
nido codex update --proxy http://127.0.0.1:7890
```

本次覆盖不写入 `.env`。Nido 的环境片段不全局导出代理，以免影响其他程序。

进程代理变量的小写形式优先于大写形式，显式空值也会覆盖 `.env`，并同步到安装子进程的两种大小写变量。本次 `--proxy` 只覆盖代理端点，保留 `NO_PROXY`。对于变量展开、转义或冲突的 `.env` 代理表达式，Nido 明确报错，不尝试执行或完整解释 dotenv。

## 内置 Proxy

从 Mihomo 官方稳定版下载当前平台内核，不导入外部 Proxy：

```sh
nido proxy install
```

也可使用本地独立程序或 gzip 程序包：

```sh
nido proxy install --artifact /absolute/path/to/mihomo.gz
```

可加 `--sha256` 校验本地输入。该选项接受 64 位十六进制摘要；程序还会检查平台和格式。当前不接受任意自定义下载 URL。

安装后自行提供固定位置的配置文件：

```text
<Nido 软件目录>/proxy/config.yaml
```

Nido 不生成订阅、节点或认证。准备好配置后：

```sh
nido proxy start
nido proxy status
nido proxy stop
```

`start` 直接启动本实例内核并记录进程归属，不注册 systemd、Supervisor 或自启动。停止和更新不会只凭进程名称发信号。

```sh
nido proxy update
```

内置 Proxy 的官方本地更新接口适用且能确认控制器归属时优先使用，可能重启内核；否则本地替换要求组件已经停止。已提交官方更新后结果不明，不再静默下载覆盖。安装、文件替换和卸载不会代替用户停止或启动组件。

下载继承当前终端已有的代理环境，不会为了下载自动启动 Proxy。

## 状态与预览

```sh
nido status
nido status --json
nido codex status --json
nido doctor
nido doctor --json
```

状态按外部 Codex 集成、内置 Proxy 和 GDS 分类，不联网或顺便修复配置。关联 Codex 的版本通过其 `--version` 查询，不宣称认证、会话或联网可用。`doctor` 另外检查生成环境及当前终端的 `CODEX_HOME` 是否一致。

`doctor --json` 输出 `components` 与 `issues`，发现问题时返回非零退出码。交互提示和查询警告写入 stderr，成功的 JSON 结果单独写入 stdout；`env` 输出可加载的 Shell 代码，不接受 `--json`。即使安装配置损坏，仍可运行 `nido version`。

变更命令可加 `--dry-run` 查看目标；Codex 安装、代理和解除关联还进行相应的只读路径、渠道或归属预检，不写配置、不下载、不运行安装器或启动组件。预检不保证网络和上游安装器一定成功。`--yes` 只确认明确操作，不猜测目录或自动迁移数据。

## 解除关联与卸载

```sh
nido codex detach
```

有代理归属记录时，解除关联先清理仍与记录一致的 Nido 代理块，再移除自己的关联信息与环境配置；Codex 程序、HOME 和全部数据保留。受管块被用户改动或原目录无法安全访问时停止，不能通过解除关联强行覆盖原文件。没有代理归属记录时，只清理 Nido 自己的关联，不读取或修改外部 `.env`。当前终端可能还保留原 `CODEX_HOME`；按需执行 `unset CODEX_HOME` 并重新加载环境。

```sh
nido proxy stop
nido proxy remove
```

内置组件卸载只删除其程序及已停止的进程记录，保留配置、日志、数据和未知文件。GDS 同样使用 `nido gds remove`。

当前没有 Nido 自身卸载、递归清理数据或 Codex 卸载命令。准备移除软件目录时，先按需解除外部关联、停止内置组件，并自行保留所需配置数据；不要把程序更新理解为数据备份。

## 可选 GDS

GDS 不参与默认初始化或在线安装，必须显式提供本地独立程序：

```sh
nido gds install --artifact /absolute/path/to/gds
nido gds status
nido gds remove
```

当前 Go 版尚未接入 GDS 调度启动；`nido gds start` 会明确拒绝，且不会创建运行目录或启动 worker。没有默认在线下载源。它与 Codex 集成、Proxy 相互独立。

## 路径变化与旧版

当前没有 `repair`、`--store`、`--from`、完整 HOME 迁入或挂载恢复命令。路径变化时不猜测旧路径、不扫描磁盘或改写外部链接。普通外部目录更换通过明确解除关联再关联处理；原目录已不可用且无法清理代理块时，需要用户先明确处理原配置。

旧 cdet 的双目录配置不会原位升级。请在独立 Nido 软件目录初始化，按需关联仍在原位的 Codex；旧配置和运行数据保持原样。冻结 Bash 行为见 [旧实现边界](legacy-boundary.md)，不属于当前 CLI。
