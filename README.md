# CDEToolkit

Cloud Development Environment Toolkit

轻量开发机管理 CLI：管理 Proxy、Codex、已有组件与持久盘路径变化；GDS 为可选模块。
Go 是新功能的唯一实现，核心运行时只需一个 `cdet` 文件。原 Bash 入口仍保留，当前没有切换实际入口。

## 安装管理工具

命令简称为 **`cdet`**，对应 Cloud Development Environment Toolkit 的四个首字母。
支持 Linux/macOS 的 amd64、arm64，不需要 Go、Python、Node、Conda 或 sudo：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
cdet version
cdet status
```

该地址使用已发布的 GitHub Release，首次正式发布前不可用。安装器只下载并校验管理程序，
不安装 Proxy/Codex/GDS，不接管已有组件，不注册或启动服务，不改 Shell 配置、认证、会话和数据。
默认管理目录为 `~/.local/cdetoolkit`，命令链接为 `~/.local/bin/cdet`；若该目录尚未加入 PATH，
安装器只打印当前 Shell 可执行的 `export PATH=...`，不会自动修改启动文件。
依赖仅为常见的 curl、tar、SHA-256 工具及 POSIX 系统命令。

持久盘可显式指定管理目录与命令目录；环境变量应传给管道右侧的 `sh`：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | \
  CDET_HOME=/persistent/.local/cdetoolkit CDET_BIN_DIR=/persistent/.local/bin sh
```

可用 `sh -s -- --version X.Y.Z` 固定稳定版本，或 `sh -s -- --dry-run` 只预览目标与下载地址。
安装器只接受 HTTPS，校验包的 SHA-256、文件清单与程序报告的版本；不会执行包中的其他文件。
重复运行会更新同一实例；无归属的同名文件、外部链接或损坏状态会停止安装，不会静默换装。
命令链接由 Go 核心登记，和管理程序共享文件事务，支持挂载移动后的 repair 及安全卸载。

Go 版旧安装的 `STK_HOME`、`.local/stk`、已归属的 `stk` 程序及 schema-1 状态仍可读取；
内部归属标记保留以便安全接管。不会为了命名变化搬动旧配置、认证、会话或运行数据。
旧 Bash 源码安装器 `./install.sh` 仍保留；新的单文件安装入口是 `scripts/install.sh` 和对应 Release 资产。

## 构建与隔离验证

构建需要 Go >= 1.23。管理核心无需 Python、Node、Conda 或 CDEToolkit 后台进程。
CI 同时检查最低 Go 1.23 和固定的当前工具链；正式发行使用 `.go-version` 指定的工具链。

```sh
make check
make smoke
make dist TARGET_OS=linux TARGET_ARCH=amd64
make release
```

`check` 包含 vet、race 测试和临时目录内的 CLI 测试；网络测试使用本地 TLS 模拟服务。
`dist` 生成仅包含 `cdet` 的 tar.gz 和 SHA-256，不打包或调用旧 Bash 安装器。没有系统 install 目标。
程序位于 `build/GOOS-GOARCH/cdet`；`VERSION` 是发行版本的唯一来源，直接 `go build` 的版本显示为 `dev`。
`smoke` 只在临时 HOME 内执行包中程序的 version/status，不安装或启动组件。
`release` 交叉构建 Linux/macOS 的 amd64/arm64 四个发行包并静态校验架构与内容，同时生成独立的 `dist/install.sh`。

## 安装与接管

以下是未来实际使用方式；本次开发仅在隔离目录验证，没有执行真实安装。

```sh
cdet --home /persistent/.local/cdetoolkit install proxy codex \
  --root local=/persistent/.local --root projects=/persistent/proj \
  --proxy-config /private/proxy.yaml --dry-run
```

`install` 优先接管已有组件，只有没有候选时才下载官方稳定版。已有安装失效或候选有歧义时停止，
要求先修复路径或用 `--binary NAME=/absolute/path` 明确选择。`setup` 保持只接管，除非明确提供安装源。
Proxy 需要已有配置；CDEToolkit 不生成订阅或凭据，也不启动新下载的代理。
Codex 全新安装只创建必要的空主目录，已有认证、配置、会话和数据库原位保留。

```sh
cdet --home /persistent/.local/cdetoolkit setup codex \
  --binary codex=/existing/codex --codex-home /persistent/.local/.codex --dry-run

cdet --home /persistent/.local/cdetoolkit install codex --install codex=latest --dry-run
cdet --home /persistent/.local/cdetoolkit install proxy --install proxy=X.Y.Z \
  --proxy-config /private/proxy.yaml --dry-run
```

`X.Y.Z` 替换为具体稳定版本。内置源为 `openai/codex` 与 `MetaCubeX/mihomo` 的官方 GitHub Releases，
支持 Linux/macOS 的 amd64/arm64。Linux Codex 使用 musl CLI 包；Mihomo amd64 优先选 v1，
必要时兼容旧的 compatible 命名，不选择可能需要 v3 指令集的通用 amd64 包。
不安装 Codex 桌面版、可选 sidecar 或额外语言环境。

官方源自动读取并验证 SHA-256；不执行上游安装脚本。也可提供离线文件或自定义 HTTPS 包：

```sh
cdet --home /persistent/.local/cdetoolkit install codex --install codex=/cache/codex.tar.gz --dry-run
cdet --home /persistent/.local/cdetoolkit install codex \
  --install codex=https://example.org/codex.tar.gz --sha256 codex=64位十六进制摘要 --dry-run
```

支持原始 ELF/Mach-O 程序、带 shebang 的脚本、gzip、受限 tar.gz。在线压缩包上限 256 MiB，
程序或总展开内容上限 512 MiB，tar 最多 128 项；禁止链接、特殊文件、越界路径和不安全重定向。
官方包还会检查二进制平台。自定义包的兼容性由提供者负责。
下载使用标准 `HTTPS_PROXY` 等环境设置；不会为了下载而启动代理。

## 更新与卸载

```sh
cdet --home /persistent/.local/cdetoolkit update codex --dry-run
cdet --home /persistent/.local/cdetoolkit update proxy --install proxy=X.Y.Z --dry-run
cdet --home /persistent/.local/cdetoolkit update codex --takeover --dry-run
cdet --home /persistent/.local/cdetoolkit uninstall codex --dry-run
cdet --home /persistent/.local/cdetoolkit uninstall cdet --dry-run
```

不指定更新模块时，只更新 CDEToolkit 所有的 Proxy/Codex。相同官方版本、包摘要与本地程序摘要一致时不重复下载或改文件。
外部安装默认交给原安装器更新；`--takeover` 明确安装 CDEToolkit 副本并切换 Go 管理关系，保留原程序和用户数据。
GDS 没有内置在线源，安装或更新需要显式提供本地程序/artifact，默认不启用。

`uninstall NAME` 解除对应管理关系，删除未被用户改过的 CDEToolkit 程序、启动器与模板；
外部程序、组件内部链接、配置、认证、会话、数据库、缓存及未知文件均保留。
`uninstall cdet` 额外移除 Go 管理程序、环境文件与已登记的 Shell 块；
保留 `config.toml`、空模块的 `state.json`、事务备份和数据目录，支持审计与重新接管。
不递归删除目录；遇到归属或内容冲突会停止。卸载不自动停止或解除已注册服务，实际卸载前需显式处理运行态。

CDEToolkit 自身复用同一 artifact/事务流程，不再 git pull 后重新跑 Bash 安装器：

```sh
cdet --home /persistent/.local/cdetoolkit update cdet \
  --install cdet=https://example.org/cdet-linux-amd64.tar.gz --sha256 cdet=64位十六进制摘要 --dry-run
```

重新运行一行安装命令会从 CDEToolkit 的 GitHub Release 更新管理程序，并复用其归属校验与回滚事务。直接使用 `update cdet` 时仍要求明确提供文件或 HTTPS 地址及摘要。
`repair` 会保留已更新的管理程序。

## 路径与服务

挂载移动后可从新管理目录运行 `repair --dry-run`；独立数据盘用 `--root data=/new/location` 显式映射。
位置变化尚未 repair 时，status/doctor 报告“路径待重建”；exec、env、start/restart 停止，防止新程序路径与旧配置混用。
`env bash` 输出环境；`exec codex -- ARGS` 使用已选程序与应用范围内的代理。
Codex 仅自动继承已启用的 Proxy；其自己显式配置的 endpoint 保持优先。
只有显式 `--shell-file` 才登记 Shell 块，只有 `--manage-config` 才允许迁移外部配置内部路径。
任何安装、更新、修复、卸载都不自动注册、加载、停止或启动服务；运行态操作是独立的显式命令。

在线 `--dry-run` 仅请求小型发布元数据，不下载程序、不写文件；计划标记为 deferred，不能直接 Apply。
本地 artifact 预览可计算完整文件计划。移除 `--dry-run` 才下载、校验、重新生成并执行计划。
文件锁、内容指纹、元数据读取基线、备份和中断恢复共用一套实现，失败不会静默覆盖后续用户修改。

详细设计见 `docs/go-design.md`，旧 Bash 的过渡边界见 `docs/legacy-boundary.md`。

## 审计后的恢复边界

```sh
cdet --home /persistent/.local/cdetoolkit rollback --dry-run
```

`rollback` 回滚最近一批已完成更新；`repair --recover` 撤销中断的文件事务。
二者先验证实例、物理位置、内容指纹与备份摘要，后续用户修改不会被覆盖。
备份在实际读取时重新校验；当前更新仅保留最近一批回滚所需备份，未知文件继续保留。
挂载变化后先 repair；旧位置的回滚记录不重放，但不妨碍后续更新建立新记录。
更新只处理选中的模块，latest 不自动降级；显式版本参数可以选择旧版本。

已有的 CODEX_HOME 环境设置会参与首次接管；失效的环境路径不会自动变成空目录。
健康的组件入口链接可以登记保留，挂载变化后再修复；共享父目录链接不会被接管。
未接管或禁用的配置保留历史路径映射，后续明确接管时仍可重建。
Proxy 配置中需要迁移的路径若使用共享 YAML 表、锚点、别名或合并表，会停止自动改写并要求手动迁移。

完整审计与旧 Bash 清理条件见 [docs/go-safety-audit.md](docs/go-safety-audit.md)。
GitHub 工作流已迁移到 Go 检查、四平台单文件包、SHA-256 和 Sigstore 签名；独立发布只安装 Go CLI 的 POSIX sh 下载器，旧 Bash 安装器不进入发行资产。
已删除不再被引用的旧发布/测试源码；实际旧入口及 GDS 依赖继续原位保留。
迁移范围、保留原因及验证命令见 [docs/go-ci-release.md](docs/go-ci-release.md)。
