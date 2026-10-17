# CDEToolkit

Cloud Development Environment Toolkit，命令名 **`cdet`**。轻量管理开发机上的 Codex、Proxy 和可选 GDS：统一目录、完整迁入、重建入口，并调用软件自身的安装与更新能力。

当前源码采用未发布的 schema-4 协议；已发布的 v0.1.0 使用旧布局。以下新命令与目录规则不适用于旧版，当前开发不发布或切换真实入口。

## 两个目录

首次只选择 **cdet 安装目录** 和 **组件存储目录**，默认分别为 `~/.cdet`、`~/.cdet-store`，也可通过 `CDET_PREFIX`、`CDET_STORE` 指定。

```text
<安装目录>/
  .instance
  config.toml
  bin/cdet
  bin/codex
  bin/mihomo
<组件存储目录>/
  .cdet/
    state.json
    bin/codex                 # 由官方安装器管理
    bin/mihomo
    bin/gds                   # 显式安装后才有
  codex/                      # 完整 CODEX_HOME
  proxy/                      # config.yaml、规则、缓存等
  gds/                        # 显式安装后才有
```

两根互不包含。组件数据直接位于 `<store>/<组件名>`，程序使用固定入口。清单记录当前安装状态、来源及必要的归属信息，不维护历史版本仓库。

## 初始化、安装与迁入

```sh
cdet init
# 只询问：cdet 安装目录、组件存储目录

cdet init --prefix /persistent/cdet-cli --store /persistent/components
cdet scan
cdet install codex proxy --dry-run
cdet install codex proxy --yes

cdet install codex --from codex=/existing/.codex --dry-run
cdet install codex --from codex=/existing/.codex
cdet install proxy --from proxy=/existing/mihomo
```

`init` 只部署 CLI 和空清单，不安装组件或修改 Shell。`cdet env bash` 输出需要加入当前 Shell 的 PATH。项目的 `scripts/install.sh` 负责下载、校验 CLI 并转交 Go `init`，支持一行安装；新协议发布前应使用本地构建验证。

`install` 在没有组件时新装；发现已有组件时，将完整数据目录复制到存储根，校验后生成入口。`--from NAME=DIR` 指定确切的数据根，不自动下钻其 `data/` 子目录；`--fresh` 明确跳过外部安装。默认发现使用标准 HOME/XDG 目录及 PATH，不搜索整盘。自动发现的迁入需要确认或 `--yes`，显式 `--from` 表示选择该来源。

**Codex 的程序安装和更新统一使用官方安装脚本**，不接受 `--artifact codex=...` 或 `--sha256 codex=...`。安装器使用受管 `CODEX_HOME`、程序目标和隔离的临时 HOME，避免修改用户的 Shell 文件。下载、安装和组件执行继承现有代理环境；CDEToolkit 不为了下载自动启动代理。

Proxy 可迁入已有独立程序，或从 Mihomo 官方发布包安装。自定义 HTTPS 包必须提供摘要；GDS 始终显式可选，必须提供本地独立程序包。

```sh
cdet install proxy --fresh --artifact proxy=latest --dry-run
cdet install proxy --fresh --artifact proxy=/cache/mihomo.gz
cdet install proxy --fresh --artifact proxy=https://example.org/mihomo.gz \
  --sha256 proxy=64位十六进制摘要
cdet install gds --fresh --artifact gds=/cache/gds
```

新装 Proxy 可以处于待配置状态，配置放在 `<store>/proxy/config.yaml`。工具不生成订阅、凭据，也不注册或启动服务。

## 完整目录的安全边界

Codex 迁入整个 `CODEX_HOME`，包括配置、目录内认证、会话、插件、技能和数据库。所有内容按文件复制，不解析、筛选或改写数据库，不提供“只迁对话”。迁入后运行环境为 `CODEX_HOME=<store>/codex`。

来源原件始终保留。复制在同一存储中的临时目录完成，校验后通过 rename 发布；保留权限和普通文件 mtime。合法内部链接可重定位，内部规范绝对链接仅在副本中转为相对链接。外部或循环链接、特殊文件和已识别的外置资源会阻止不完整迁入。

迁入前须退出访问该目录的 Codex CLI、daemon/app-server 或其他进程。工具检查进程与源文件状态，不自动停止进程；无法可靠检查时停止操作。

通过 `cdet exec` 或生成入口启动的组件持有共享运行锁，本地程序替换、迁入和路径修复取得同组件的独占写锁。这只覆盖经 cdet 启动的进程；外部直接启动的程序仍须退出，并接受进程检查。Mihomo 官方在线更新 POST 由上游处理，不用独占写锁阻断正在运行的 Mihomo。

外置 `CODEX_SQLITE_HOME`、显式 `sqlite_home`、外部附件、项目目录、MCP/插件依赖不随 HOME 自动搬迁。系统 keyring 认证也不在目录中，需要时重新登录。挂载修复重建工具定位、环境和已知官方程序链接，不能据此假定所有内部绝对路径都已适配。

## 状态、版本与更新

```sh
cdet status
cdet versions codex proxy
cdet update codex --dry-run
cdet update codex
cdet update proxy --yes
```

`status` 只查询本机程序版本，不联网检查最新版本。`versions` 查询当前与上游最新版本；网络失败会明确显示。`update` 先展示版本和计划，再确认；`--yes` 跳过确认，`--dry-run` 不运行安装器、不下载程序、不调用更新 POST。

Codex 交由官方脚本更新。Mihomo 在本地官方控制 API 可用时优先使用官方更新；**该操作可能重启 Mihomo**，确认前会提示。不适用时才下载、校验并替换本地程序。官方更新调用失败后不会暗中切换到另一种更新方式。

工具只保留当前安装信息，不提供版本仓库、`rollback`、`self rollback` 或通用事务回放。更新 cdet 本身使用项目官方安装器。组件自身的数据兼容与恢复遵循其上游规则。

## 挂载变化与卸载

```sh
/new/location/cdet-cli/bin/cdet repair --dry-run
/new/location/cdet-cli/bin/cdet repair
cdet repair --store /new/location/components

cdet remove codex
cdet remove self
```

两根一起移动时，使用新 CLI 位置和相对关系定位存储；只移动存储时显式指定新位置。核对安装与存储身份，遇到同 ID 副本要求明确选择，缺失存储不会变成空实例。`repair` 重建引导和入口，并将已知官方 Codex `current` 与可见命令链接安全重绑为相对链接；只处理这些链接，不改写组件配置、认证、会话或数据库，也不重建旧挂载。安装根变化后需要重新载入 PATH。

操作按组件顺序执行，不要求多组件全部成功或全部撤销。未完成操作只保留必要的短期标记；已经发布的数据目录不会被自动回滚删除。失败时保留现场并报告需处理的位置，不提供可重放任意文件变更的日志。

`remove` 移除受管程序和入口，完整保留 `<store>/<组件名>` 及未知文件；没有数据 purge。`remove self` 保留组件存储和引导身份，后续可重新 `init`。原来源的清理由用户验证后的独立操作完成。

## 开发与验证

```sh
make check
make smoke
make dist TARGET_OS=linux TARGET_ARCH=amd64
```

源码最低 Go 1.23，固定发布工具链见 `.go-version`。运行时为单个 CLI，不依赖 Go、Python、Node、sqlite3 或常驻管理进程。Codex 官方安装路径需要系统 curl 和 POSIX sh；Proxy 包处理使用 Go。

隔离测试只用临时 HOME、本地假 curl/假安装器、合成程序和服务调用哨兵，不执行真实上游安装器或真实服务。`make smoke` 只读检查本机发行包，`make release` 和 `make verify-release` 只本地构建与核验。

旧 Bash 源码冻结保留，不作为 Go 生命周期后端或进入发行包；旧 schema 实验状态不原位兼容。详见 [设计](docs/go-design.md)、[验证边界](docs/two-root-audit.md)、[CI 与发行](docs/go-ci-release.md) 和 [旧 Bash 边界](docs/legacy-boundary.md)。
