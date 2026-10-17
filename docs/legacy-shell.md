# Server Toolkit — 旧 Bash 入口兼容说明

本文记录仍保留的旧入口行为，不代表新版 Go 功能范围。当前开发、验证和发行以 [Go README](../README.md) 与 [CI/发布说明](go-ci-release.md) 为准。旧入口和运行目录尚未切换。

面向持久化开发容器的一体化工具包。`stk` 提供统一配置、模块安装、状态、日志、诊断和生命周期管理；当前运行模块由 supervisord 直接监管，不依赖 Kubernetes 层配置。

## 新平台布局与功能范围

当前保留代理、Codex、Conda、Git、基础工具、Shell 便捷配置和可选 GDS。
VolcML 账号、任务查询、提交、审阅、火山 Skill 及相应菜单已经移除；历史账号文件、
实验数据和恢复快照仍归用户保管。

仓库只提供通用路径模板 `platforms/zenith-dev.example.conf`。使用旧入口前，
可将其复制为 `platforms/zenith-dev.conf` 并填写自己的挂载路径；实际平台配置已加入
`.gitignore`。当源码位于该配置的 `local_root/server-toolkit` 时，自动采用其中的布局。
以下使用模板中的通用示例路径：

| 用途 | 目录 |
|---|---|
| 配置 | `/persistent/.local/config/server-toolkit` |
| 状态 | `/persistent/.local/state/server-toolkit` |
| 命令 | `/persistent/.local/bin` |
| Codex | `/persistent/.local/.codex` |
| Conda 环境 | `/persistent/.local/conda_envs` |
| 代码 | `/persistent/proj` |
| 大型数据和实验结果 | `/data` |

`SERVER_TOOLKIT_PLATFORM=generic` 可关闭自动识别；也可用
`SERVER_TOOLKIT_PLATFORM_FILE=/absolute/platform.conf` 提供相同格式的路径文件。
已有 Release 安装元数据及显式的配置/状态环境变量优先。

新机恢复时，可运行 `./install.sh` 后选择 `proxy` 和 `codex`，或执行：

```bash
./install.sh --module proxy --module codex
```

GDS、基础工具和 Shell 均可单独选择。安装器优先从本地私有事务快照恢复代理配置，
并接管已有 Codex 发布包、修复相对软链接、迁移配置中的旧路径和移除受管火山说明。
Codex 认证文件、历史会话和数据库不在改写范围内。
没有可复用的 Codex 时才调用官方安装脚本；直连失败且代理尚未启动时会明确停止。
首次安装不会启动服务。需要运行代理时再执行 `pm start`，需要 Supervisor 托管时再执行
`stk service install`。已有 Supervisor 托管的模块重装仍会同步其托管配置。
在新 shell 中使用命令入口前，将自己配置的 `local_root/bin` 加入 PATH；
选择 Codex 模块会自动写入受管 PATH 和 CODEX_HOME 配置。

## 安装

### GitHub Release 一行安装（推荐）

仓库发布第一个 `v*` tag 后，可直接安装，不需要 clone：

```bash
curl -fsSL https://github.com/OWNER/server-toolkit/releases/latest/download/install.sh | sh
```

Release workflow 会自动把实际的 `OWNER/server-toolkit` 注入安装脚本，因此源码中不需要保存个人 GitHub 用户名。安装命令会先像 Conda 安装器一样依次显示并允许修改路径，然后再打开模块选择器：

```text
STK_HOME [~/.stk]:
命令入口目录 [/usr/local/bin]:
```

普通用户的命令入口默认是 `~/.local/bin`。直接按 Enter 接受默认值；`config/`、`state/`、core 和 workdir 都从 STK_HOME 自动派生，完整保存在同一目录树中。路径确认后使用上下键移动、空格选择、Enter 安装模块，全部模块默认不选中。选择结果只通过安装界面处理，远程脚本不接受模块参数；没有真实终端时会明确失败。

安装前必须已有可用的 Python 3.10 或更高版本，并提供标准库 `venv` 与 `ensurepip`。解释器可以来自操作系统，也可以来自 Conda；安装 STK 本身不要求 Conda。安装器使用标准库 `venv` 在 `$STK_HOME/runtime/python` 创建专属环境，所有内部 Python 脚本均通过该环境的固定解释器运行，不修改 `.bashrc`、不激活环境，也不污染系统 Python 或用户的项目环境。可通过 `STK_BOOTSTRAP_PYTHON=/absolute/path/to/python3` 显式选择用于创建环境的解释器。

固定版本或更改代码安装目录：

```bash
curl -fsSL https://github.com/OWNER/server-toolkit/releases/latest/download/install.sh \
  | STK_VERSION=v0.1.0 STK_HOME=/opt/server-toolkit \
    STK_BIN_DIR=/usr/local/bin sh
```

引导器只接受 HTTPS Release 下载，校验随 Release 发布的 SHA-256，并限制下载大小、归档成员数、单文件大小与总展开大小；路径穿越、非规范化/重复路径、符号链接、特殊文件和 Bash 语法错误都会被拒绝。检测到 `cosign` 时还会自动下载 bundle，并固定 GitHub Actions workflow 与 tag 身份验证归档签名。设置 `STK_VERIFY_SIGNATURE=required` 可在缺少 cosign、bundle 或签名无效时拒绝安装；`disabled` 可显式关闭，默认 `auto`。Release 安装与升级使用 STK_HOME 相邻的互斥锁，多个 `curl | sh` 不会同时改写同一安装。升级失败会一并恢复旧版本、外部 managed block、命令入口、supervisord 配置和升级前的模块运行/停止状态；core、workdir、GDS 配置和原安装 prefix 会被保留。重新执行同一条命令即可升级。

每个 Release 产物还会通过 GitHub Actions OIDC 生成 Cosign keyless 签名，并将证书、签名和透明日志证明保存在 `.sigstore.json` bundle 中。一行 `curl | sh` 为便捷入口；如果需要在执行安装脚本前完成发布者身份验证，使用以下流程：

```bash
REPOSITORY=OWNER/server-toolkit
TAG=v0.1.0
BASE="https://github.com/${REPOSITORY}/releases/download/${TAG}"
IDENTITY="https://github.com/${REPOSITORY}/.github/workflows/release.yml@refs/tags/${TAG}"

for asset in install.sh server-toolkit.tar.gz server-toolkit.tar.gz.sha256; do
  curl -fsSLO "${BASE}/${asset}"
  curl -fsSLO "${BASE}/${asset}.sigstore.json"
  cosign verify-blob "${asset}" \
    --bundle "${asset}.sigstore.json" \
    --certificate-identity "${IDENTITY}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
done
sh install.sh
```

### 本地源码安装

开发仓库或离线解压后的目录仍可直接运行：

```bash
./install.sh
```

交互选择器使用 `↑` / `↓` 移动、空格切换、Enter 安装。所有模块默认均不选中，`stk` 入口始终安装。

```bash
./install.sh --list
./install.sh --module proxy --module gds
./install.sh --all
./install.sh --no-modules
```

`--prefix /absolute/bin` 可指定入口目录，已有非本项目入口只有在传入 `--force` 时才会被替换。

首次选择 `proxy` 时，安装器会先询问是否有 Shadowsocks ISP 节点，再要求输入 HTTPS 订阅 URL：

- 无 ISP：生成仅含 `common` listener 的基础配置，Codex 与 Git 共用 `127.0.0.1:7890`。
- 有 ISP：额外询问 ISP server、port、cipher 和 password，生成 `isp-relay` 与 `isp` listener，Codex 使用 `127.0.0.1:7891`。

公开 YAML 模板只包含拓扑和占位符，不包含真实订阅或 ISP 节点信息。

安装后可随时重新运行向导或校验当前配置：

```bash
stk proxy configure
stk proxy validate
```

重配会先生成候选文件；本机已有 mihomo core 时必须通过其完整校验才会原子替换，旧配置保留为 `proxy.yaml.previous`。自动化场景可用 `proxy/install.sh --input-file FILE`，其中 FILE 权限必须禁止 group/other 访问，并依次保存五个 NUL 分隔字段：订阅 URL、ISP server、port、cipher、password。敏感值不会出现在渲染子进程的命令行或环境变量中。

## 模块

| 模块 | 类型 | 功能 |
|---|---|---|
| `proxy` | 服务 | mihomo 本地代理与 Dashboard，独立入口 `pm` |
| `codex` | 可选配置 | 优先恢复现有 Codex 发布包、入口、路径与代理环境 |
| `gds` | 服务 | NVIDIA GPU 空闲监控和显存保留，独立入口 `gds` |
| `base-tools` | 可选配置 | `rg`、`fd`、`fzf`、`jq`、`htop`、网络与归档工具 |
| `shell` | 可选配置 | Bash 历史、fzf 键位与少量快捷命令 |

所有模块只在用户明确选中时配置。GDS 保留为可选模块。`base-tools` 当前支持 apt 系发行版，安装系统包需要 root。Python 私有环境属于 STK 核心运行时，不是可选模块。

```bash
stk module list
stk module install proxy
stk module start proxy
stk module restart proxy
stk module stop proxy
stk module remove proxy       # 保留模块配置与运行数据
```

安装或更新 `gds` 时，STK 还会向 Codex 当前生效的全局 `AGENTS.md` 或
`AGENTS.override.md` 合并受管规则。Codex 使用本地 GPU 时会忽略经 PID 和可执行文件路径
双重确认的 GPUDynamicScheduler worker，不会等待其保留显存先释放，也不会为了获得 GPU
停止 GDS；实际 GPU 进程出现后，GDS monitor 会在下一次成功探测时自动退出 worker。碰巧
同名的 `scheduler` 和所有其他 GPU 进程仍按普通占用处理。

## 常用命令

```bash
stk status
stk doctor
stk runtime ensure
stk codex restore
stk logs proxy --lines 100
stk logs service -f

stk conda status
stk conda link
stk conda migrate --dry-run
stk conda migrate --yes

stk config show
stk config get no_proxy
stk config set service_backend supervisord
stk config set service_user root        # 留空则使用执行 service install 的当前用户
stk config edit
stk config validate
stk config repair

stk service install
stk service status
stk service remove

stk version
stk config set self_update_signers 'SHA256:RELEASE_SIGNING_KEY_FINGERPRINT'
stk config set self_update_policy signed-tag
stk self-update check
stk self-update run
stk self-update rollback
stk uninstall                 # 保留配置和数据
stk uninstall --dry-run       # 仅显示计划
stk uninstall --purge-config --purge-state --yes
stk uninstall --purge-data --yes
stk uninstall --purge-all --yes         # 仅 Release 安装；同时删除整个 STK_HOME
```

自更新默认关闭。推荐的 `signed-tag` 策略要求目标提交正好对应一个能被本机 Git/GPG/SSH 验证的签名 tag，并要求实际 GPG/SSH 签名指纹位于 `self_update_signers` 逗号分隔允许列表；`git-ff` 仅要求快进，适合已经通过其他方式信任 origin 的环境。更新前会在隔离 worktree 中由当前可信版本执行静态预检；快进后会由新版本重新验证配置并确保 Python 私有运行时可用，失败时恢复原提交。仓库没有初始提交、`origin`、上游分支或存在已跟踪修改时会安全拒绝。GitHub Release 安装不包含 `.git`，应通过重新执行 curl 安装命令升级，而不是使用 `stk self-update`。

## 统一配置与状态

GitHub Release 安装的全部数据位于 STK_HOME：统一配置为 `~/.stk/config/config.conf`，状态为 `~/.stk/state/`，Python 私有环境为 `~/.stk/runtime/python`，安装元数据为 `~/.stk/config/install.conf`。配置文件权限为 `600`，配置和状态目录权限为 `700`。本地源码开发模式仍默认使用 `~/.config/server-toolkit` 与 `~/.local/state/server-toolkit`，防止私人信息进入准备发布的工作树。

Python 运行时需要 Python 3.10 或更高版本，采用标准库 `venv + pip`，依赖由 [requirements/runtime.lock](./requirements/runtime.lock) 统一维护。锁定文件、引导解释器内容/版本变化、环境目录迁移或环境损坏时，重新运行安装器会重建专属环境；依赖必须固定版本并提供哈希。STK 永远通过 `$STK_HOME/runtime/python/bin/python` 调用内部脚本，无需也不支持手动激活该环境。

配置与状态都有显式 `schema_version`。首个公开版本不携带开发阶段 schema 的历史迁移代码，只接受当前版本；不匹配时会安全停止，由 `stk doctor` 在只读降级模式中报告。配置使用严格的允许键列表解析，不会被 shell `source`。未知键、重复键、非法值或不安全权限会让普通命令立即停止，不再静默回退默认值；`stk config edit` 可原地修正，`stk config repair` 会先备份再规范化。

仓库中的 [config.example.conf](./config.example.conf) 只包含通用占位值；交互安装结束时可以选择运行个人配置向导，默认跳过。

主要配置项包括 Git 身份、`CODEX_HOME`、Conda 环境目录、代理 listener、`NO_PROXY`、GitHub 私钥源/目标路径、Proxy 私有 YAML 路径、可选 release 加速地址、服务后端和服务运行用户。姓名、邮箱、内部域名和密钥都没有仓库默认值；可以运行 `stk config setup` 交互填写。Release 安装的代理订阅默认保存为 `$STK_HOME/config/proxy.yaml`；源码开发模式则保存到仓库外的 `~/.config/server-toolkit/proxy.yaml`。

所有跨模块写操作使用同一个进程锁；锁位于 STK_HOME 相邻位置，不会被 `--purge-state` 或 `--purge-all` 在操作尚未结束时删除。异常锁会同时校验 owner PID 和进程启动标记，避免 PID 复用造成误判。配置、状态和 managed block 写入会拒绝危险的符号链接，并尽量采用同目录原子替换。卸载会先保存相关外部文件快照，恢复 Git/Codex/SSH 等受管修改；任何阶段失败都会尝试回滚快照。个人配置、状态和 core 数据只在分别显式指定 purge 选项时删除。

统一安装器和单模块安装/移除会先预检并对 STK 文件变更建立事务快照；中途失败会恢复入口、配置、状态、私有 Python 环境和模块运行文件。`base-tools` 通过 apt 安装的系统软件包由系统包管理器维护，不在 STK 文件事务的回滚范围内。

## Conda 环境目录与迁移

Conda 功能是按需使用的快捷配置，不是安装依赖，也不会执行 `conda init`、修改 `.bashrc`、自动激活 base 或管理 STK 的 Python 私有环境。默认目标由 `conda_envs_dir` 控制。普通布局采用 `~/.local/conda_envs`，新平台采用：

```text
/persistent/.local/conda_envs
```

直接运行 `stk` 也可以从主面板进入“Conda 环境管理”。二级面板会直接显示 Conda、Base、配置文件、`envs_dirs` 首位、目标目录和链接状态；可在面板中修改目标目录、预览迁移或确认迁移本机环境。进入面板及查看预览不会修改任何配置。

配置链接后，STK 会通过用户 `.condarc` 将目标放在 `envs_dirs` 首位，并保留 Conda 原有的默认环境目录作为后备。此后 `conda create -n NAME ...` 会优先创建到配置的 Conda 环境目录，`conda activate NAME` 和旧式 `source activate NAME` 均可按名称找到该环境：

```bash
stk conda link
stk conda link --path /absolute/vepfs/conda_envs
stk conda status
```

迁移会扫描当前 Conda base 的 `envs/` 与 `~/.conda/envs/`，跳过 base，只迁移包含 `conda-meta/history` 的真实环境。先查看计划，再确认执行：

```bash
stk conda migrate --dry-run
stk conda migrate --yes
```

也可以通过重复的 `--source /absolute/envs-dir` 指定额外源目录，通过 `--target PATH` 临时覆盖目标。当前激活的环境、符号链接环境、同名冲突和已有目标都会被拒绝。迁移统一使用 Conda 自身的 `create --clone` 创建目标，不使用不同 Conda 版本行为不一致的 `rename`，也不会直接移动包含绝对 prefix 的文件；只有克隆成功并校验目标后 Conda 才删除源环境。克隆失败会清理本次新建的不完整目标并保留源环境；若目标已完成但源删除失败，则同时保留两者并明确提示人工确认。批量迁移中某个环境失败时会立即停止，已经完成的环境保持可用，未处理的源环境仍能通过保留的后备 `envs_dirs` 激活。非交互迁移必须显式添加 `--yes`。

如果设置了 `CONDA_ENVS_PATH`，它会覆盖 `.condarc` 的 `envs_dirs`。STK 检测到目标未实际生效时会回滚配置并提示先移除该覆盖，或把目标目录放到该变量首位。

## Proxy、Codex 与 Git

默认无 ISP 配置让 Codex 复用 `common` listener：

```text
HTTP_PROXY=http://127.0.0.1:7890
HTTPS_PROXY=http://127.0.0.1:7890
http_proxy=http://127.0.0.1:7890
https_proxy=http://127.0.0.1:7890
NO_PROXY=localhost,127.0.0.1,::1
no_proxy=localhost,127.0.0.1,::1
```

选择 ISP 模式后，安装器会把 `proxy_codex_listener` 改为 `isp`，对应端口为 `7891`。

`stk codex-proxy enable --force` 会移除 `.env` 中冲突的非托管代理行，再写入严格 managed block。存在有效托管块时，launcher 只读取允许的六个代理变量，并清除继承的 `ALL_PROXY`；没有托管块时则保留调用方已有的代理环境。Git 代理、Git 身份与默认分支都保存接管前状态，remove 时可以恢复。

`stk codex install` 从 Codex 官方 HTTPS 地址下载安装器，限制 TLS/重定向协议、下载时间和脚本大小，并在执行前进行 shell 语法检查。该入口的发布者信任边界仍是 `chatgpt.com` 的 HTTPS 证书与官方分发服务；下载、安装或验证失败时，STK 会恢复原 `.bashrc`、launcher 和 Codex 安装目录。

## GitHub SSH

```bash
stk github-ssh configure
stk github-ssh known-hosts
stk github-ssh agent
stk github-ssh test
stk github-ssh status
stk github-ssh remove
```

仓库不包含任何私钥。用户通过 `github_key_source` 指向仓库外、由当前用户拥有且禁止 group/other 访问的普通非符号链接私钥源，工具再将其安装到 `github_key_path`（默认 `~/.ssh/server-toolkit-github`，权限 `600`）。SSH 目录与 config 分别为 `700` / `600`。`known-hosts` 使用 [GitHub 官方公布的 Ed25519 主机公钥](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints)，避免首次连接时盲目信任。加密私钥需在交互终端运行一次 `stk github-ssh agent` 输入口令；后续 shell 仅在 PID 启动标记和 socket 所有权都匹配时复用受管 agent，状态只在目标密钥的 fingerprint 确实已加载时显示成功。

## supervisord

`stk service install` 会预检模块，再原子更新 supervisord 配置。若主配置没有外部 `[include]` 段，工具会把模块定义放到私有 drop-in（Release 安装默认为 `$STK_HOME/config/supervisord/server-toolkit.conf`），主配置仅保留受管 include；存在无法安全合并的外部 include 时才回退为主配置 managed block。supervisord 直接启动：

```text
supervisord
├── proxy/pm.sh __run_core → mihomo
├── gds/gds.sh __run_monitor → scheduler（按需）
└── scripts/log-maintenance.sh loop
```

不再通过 tmux 包装服务进程。`pm stop/restart/update-provider` 与 `gds stop/restart` 检测到直管进程时会转交 `stk module ...`，避免出现杀掉后立即被 supervisord 拉起的假停止。交互模式仍可在未启用系统托管时独立使用。直管模式的模块输出由 supervisord 按 `5 MiB × 3` 轮转；维护进程只压缩没有活跃写入者的历史内部日志（`10 MiB × 5`）。日志文件打开与维护轮转共用 stk 写锁，避免截断期间丢失新追加内容。systemd 后端的 logrotate 会先停止受管模块，轮转完成后仅恢复原本活跃的服务，以避免 `copytruncate` 竞态。

在持久化容器中，应确保 supervisord 主配置和 STK_HOME 都位于会被保留的根云盘。容器重建时 PID 1 会重新读取受管 include/drop-in。

若 PID 1 是 systemd 且当前没有可管理的 supervisord，`service_backend=auto` 才会选择 systemd。systemd unit、健康定时器和 logrotate 配置采用整体快照；渲染、启用、停止或移除任一步失败都会恢复原文件及启用/运行状态，不会删除仍无法停止的服务定义。

## 验证与发行已迁移

旧 PTY、平台、真实 Supervisor 与故障注入测试及旧发布器已退役，不再提供旧 Bash 安装包。
`scripts/check.sh` 现在仅转交 `make check`；源码/历史审查仍使用 `scripts/public-audit.sh`。
GDS 的模拟编译和 worker 清理检查通过 Linux 上的 `make check-gds` 单独选择。

当前构建、Go 隔离测试、包校验及发布流程见 [go-ci-release.md](go-ci-release.md)。
不要将本兼容说明中的旧入口安装行为当作新版 Go 发布包的安装协议。

项目以 [Apache License 2.0](../LICENSE) 发布。
