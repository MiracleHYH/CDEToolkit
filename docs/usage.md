# Nido 使用指南

GDS 保持可选。Nido v0.5.0 起支持独立 Linux amd64 组件的官方下载安装、空闲观察和有限许可显存保留。首版 GDS 为 gds-v0.1.0；真实 GPU 验收尚未完成。旧 Shell/CUDA worker 不作为新 CLI 后端。

从 v0.3.0 起，Nido 使用单目录设计和 `nido` 命令。旧 `cdet` 版本不会被自动迁移；本指南描述 Nido 的安装和使用。

独立 Proxy 组件自 v0.4.0 提供；本指南中的 quick start、节点链接解析和类别/单工具绑定自 **v0.6.0** 提供。安装 Nido 不会自动安装或启动 Mihomo，也不会切换已有组件入口。

本文的 **Nido 内部 GitHub API listener 绑定及未绑定时禁用 API**已随 v0.8.2 发布，仅面向 Linux amd64，v0.8.1 尚不具备；其他已发行功能沿用各自版本说明。源码审计、原生 CI、发布工作流、独立验签、正式包 API 隔离，以及公网直链和 gh-proxy.org 加速安装自更新验收均已通过；首次加速配额失败和复验记录见 [v0.8.2 记录](nido-v0.8.2-release.md)。

v0.9.0 增加交互安装、组件配置菜单和 `nido shell setup`。显式参数与既有数据保护保持有效，完整约定见[交互设计](interactive-design.md)。v0.9.1 发布准备中，增加安装器下载方式编号菜单；功能提交已通过原生 CI，发布与公开安装验证状态见 [v0.9.1 发行记录](nido-v0.9.1-release.md)。[v0.9.0 历史记录](nido-v0.9.0-release.md)单独保留。

## 初始化与 Shell 环境

自 v0.7.0 起，安装器支持 GitHub 下载策略，CLI 支持 `nido download` 与 `nido update`。以下命令安装当前公开最新版，不会直接改变当前终端的 PATH。

选择一个独立软件目录，默认为 `~/.nido`，v0.9.0 可在安装引导中输入自定义绝对路径。首次安装选择官方脚本入口或第三方加速链，不能指望脚本内的回退修复外层脚本下载失败。

官方脚本入口：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

脚本运行后，在安装引导中选择后续包下载方式；只接受官方来源时选择 `direct`。`auto` 在官方文件请求失败后可能使用内置第三方前缀。

GitHub 直连困难时，通过自己信任的第三方入口获取脚本：

```sh
curl -fsSL https://gh-proxy.org/https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

外层 `curl` 只获取脚本；脚本运行后再交互选择包下载方式，无需额外传参，也不需要推断外层 URL。v0.9.1 菜单依次为 `1` 直连、`2` `gh-proxy.org`、`3` `ghfast.top`、`4` 自定义 HTTPS 前缀、`5` `auto`；选择自定义后再输入前缀。发布完成前，当前稳定版 v0.9.0 仍接受 `direct`、HTTPS 前缀或 `auto` 的文字输入。请只使用自己信任的入口，加速脚本具有直接执行第三方内容的信任边界。

安装后，执行安装输出中指向**实际软件根**的 `. '.../env.sh'` 命令，再检查版本：

```sh
nido version
```

自定义目录可在安装时选择，也可预先执行 `export NIDO_HOME=/absolute/path/to/nido` 作为引导默认值。固定版本可使用 `sh -s -- --version X.Y.Z`；脚本也可从对应 `releases/download/vX.Y.Z/install.sh` 获取。源码构建方式见 [README](../README.md#开发)。

自动化或无终端环境可继续使用显式参数，例如 `sh -s -- --root /absolute/path/to/nido --github-proxy https://gh-proxy.org --yes`。这些参数不属于日常交互安装的必需步骤。

安装器也接受 `--root /absolute/path/to/nido`，优先于 `NIDO_HOME`；之后必须加载该实际目录中的 `env.sh`。首次初始化只接受不存在或空目录，不把普通数据目录或旧 toolkit 目录变成 Nido。重复初始化保留配置，并刷新自己的程序和环境文件；用户修改过的 `env.sh` 不被静默覆盖。

终端安装引导依次选择软件目录、文件下载方式、是否注册 Bash / Zsh，最后确认。注册默认不启用；`--root`、`--github-proxy` 等显式值仍可直接提供。`--yes` 或无交互终端时使用参数模式，`--dry-run` 不进入引导。下载安装期间从 `/dev/tty` 读取回答，避免把管道中的脚本字节当作选项。

初始化不安装组件、不自动关联 Codex、不启动服务。CLI 无法直接改变父 Shell；当前终端仍需加载实际根中的 `env.sh`。

安装时可以选择注册 Shell，也可以稍后单独运行：

```sh
nido shell setup
nido shell setup bash
nido shell setup zsh
```

该入口在明确同意后向相应用户 Shell 配置添加 Nido 受管加载块，保存实际软件根；不执行或加载原配置、不启动新 Shell，已有配置和用户其他内容保留。它用于后续读取该启动文件的新 Shell；Bash 登录配置若未加载 `.bashrc`，仍需自行接入。不声称已刷新当前终端。非交互自动化需要明确 Shell 和确认；预览使用 `--dry-run`。无需注册时，按安装输出手动加载即可。

`nido env bash` / `nido env zsh` 输出同样的环境片段。未配置外部集成时，只激活 Nido 自身路径；关联 Codex 后增加 `CODEX_HOME` 和已识别的 Codex 命令目录，不生成 Codex 包装入口。自 v0.8.0 起还按所选 Conda / pip 镜像输出变量；缓存和环境目录直接保存到上游配置，不通过环境片段激活。重复加载不会不断增加 PATH。

## 配置交互与自动化

v0.9.0 提供以下配置菜单。组件裸命令仍查看状态；只有明确进入配置动作才提问：

```sh
nido codex configure
nido conda configure
nido proxy configure
nido gds configure
nido download configure
```

终端中缺少参数时给出候选或询问输入，已有配置默认保持；镜像、目录、代理、UUID 和导入配置各自选择，不因进入菜单就写入。新增向导摘要只列目标与操作，Proxy quick start 不展开订阅、节点凭据或完整 YAML，首次镜像快照也不在普通状态中展开。输入仍可能正常显示，显式代理命令也可能显示地址，请避免共享含凭据的终端与日志。保存前仍执行相应归属、冲突与停止状态检查；取消或输入结束不继续提交。

显式参数用于自动化，`--yes` 只接受已确定的操作，不代替选择；`--json` 不弹出菜单。无交互终端时，缺少必要值应明确报错；安装器自身的无终端参数模式可使用其文档默认值。`--dry-run` 不提问，也不保存配置、下载包或启动服务；无法确定的选择应保持未检查状态。Conda 的显式参数预览仍可能查询本地上游配置，不代表为上游提供了防写沙箱。

安装、配置、注册 Shell 和代理绑定不隐式启动或重启组件；`start`、`stop`、`restart` 仍是单独、明确的命令。菜单不放宽 API 禁用规则：首次 Proxy/GDS 在线安装依赖未就绪时，可交互提供可信本地 artifact 和摘要，不会绕过绑定门控。

## GitHub 下载与 Nido 自身更新

```sh
nido download show
nido download set auto
nido download set direct
nido download set https://gh-proxy.org
nido update --check
nido update
nido update --version X.Y.Z --github-proxy direct
```

将 `X.Y.Z` 替换为要更新到的稳定新版。自身更新只允许当前运行的受管 `<Nido>/bin/nido`，相同版本不下载，不支持降级；`dev` 源码构建请通过源码更新。

`direct` 使用官方 GitHub URL；`auto` 先短时尝试官方地址，API 请求失败后只尝试 `https://gh-proxy.org`，发行包、摘要等资产请求才会继续尝试 `https://ghfast.top`，不会无限重试。自定义 HTTPS 前缀只通过该服务转发完整的公开 GitHub URL，例如 `https://gh-proxy.org/https://github.com/...`。前缀可以含路径，但不能带账号、密码、查询参数或片段，也不接受 HTTP/SOCKS 代理地址。下述 API listener 绑定生效时，API 固定使用官方 URL 经 listener 访问，不套用前缀；资产仍采用此下载策略。

优先级为本次 `--github-proxy`、`NIDO_GITHUB_PROXY` 环境变量、已保存配置、默认 `auto`。显式设置空的 `NIDO_GITHUB_PROXY` 表示直连。`nido download set VALUE` 保存后续下载策略；更新命令上的选项只影响本次请求。安装时明确选择的新值会传给 `init` 保存；下载问题直接回车或重跑时未明确选择策略，本次使用 `auto` 下载并保留既有策略。显式 flag 或环境变量也会传给 `init` 保存。安装脚本本身不会解析 TOML。自动回退只在已经执行的安装脚本内部生效，外层 `curl` 需由用户选择能访问的来源。

`download show` 同时显示保存值和本次实际值；v0.8.2 另显示 `api_state`、`api_listener`、`api_proxy`。没有 sidecar 或 listener 为空时为 `disabled`，名称和地址为空；已配置非空绑定时为 `configured`。这是静态配置状态，不证明 listener 正在运行或网络可达。普通 status/doctor 不联网，未绑定是正常禁用状态，不计作 doctor 故障。错误的下载偏好只阻止使用它的联网操作，不阻止 Codex 或离线状态查看；可通过 `download set` 修正，`doctor` 会报告无效的实际策略。Go 下载不会在收到响应头后仅因传输超过 8 秒而切换到第三方；连接和响应头有短期限，整个请求仍有大小与时间上限。加速服务返回的跳转也会检查认证参数和目标域名，自定义入口不能将下载跳转到任意外部站点。

在 v0.8.2 中，先运行 `nido proxy bind download`，从 listeners 选择一次入口，启用 Nido 自身、Proxy、GDS 以及 Nido 主动查询 Codex 版本的内部 GitHub API；后续正常运行 `nido update --check`、`nido update` 即可，无需重复指定地址。checksum、发行包等资产继续使用保存的文件加速策略。升级保留旧配置，但不会猜测 listener 或自动生成绑定；未绑定时 API 功能禁用，依赖 API 的操作在网络请求、候选执行、确认或写入前明确提示未启用。`--proxy`、`--github-proxy`、HTTP(S)_PROXY 等代理变量和文件前缀均不能在无绑定时启用 API；已有有效非空绑定记录后，`--proxy URL` 才可临时覆盖本次请求，且不会保存或移除绑定，也不核验所保存 listener 是否运行。

如果文件加速入口只支持发行资产，按 [Nido → Proxy → API 的自举方案](nido-proxy-bootstrap.md)先安装 Nido，再用可信本地 Mihomo 包安装、配置并显式启动 Proxy，最后完成 API listener 绑定。`install.sh` 按文件路径自举，不依赖 API，未绑定不影响它。绑定只影响 Nido 发起的上述 API 请求，不修改全局代理环境、Codex 官方安装/更新脚本的独立代理、Git 配置、Conda/pip 配置，也不代理 Codex 程序运行或用户自行运行的 curl、Git、Conda、pip。用户显式 `proxy probe --url` 仍是独立诊断，不受 API 开关限制。不会因联网操作自动启动服务。

Nido 自身更新只替换自己的程序，保留管理配置、生成环境、内置组件和外部数据。安装脚本与自身更新均核对下载包和摘要；当前客户端**不验证 Sigstore 签名**。通过第三方前缀时，该服务可接触并替换所转发的内容；尤其加速首装会直接执行第三方返回的脚本。HTTPS 和 SHA-256 检查不消除这项信任边界，请仅使用自己信任的服务。发布工作流的签名不等于安装客户端已经验签。

更新期间若入口、程序目录或候选文件被替换，操作停止；不会覆盖该替换，也不会在失败清理时删除未知文件。版本查询和确认使用同一次入口身份快照；下载固定为已确认的版本，不在最后一步重新选择另一个 latest。

## 目录与所有权

```text
<Nido 软件目录>/
  bin/nido
  config.toml
  download-proxy.toml # v0.8.2：显式绑定的内部 GitHub API listener，私有独立文件
  env.sh
  conda/              # v0.8.0 起，显式选择 Conda 镜像后创建
    mirror-<hash>.yaml # 内容确定的镜像覆盖文件
  proxy/              # 显式安装后创建
    mihomo
    config.yaml       # configure 保存的唯一主配置
    process.json      # 最近一次受管启动及配置摘要
    output.log
    providers/        # 默认模板使用的 provider 缓存
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

`nido codex configure` 用于关联目录、配置代理或选择安装入口；已有选择默认保留。`nido codex install` 尚无关联且未提供 `--home` 时，可在终端输入外部 HOME；`nido codex proxy` 缺少地址时可选择可用 listener、自定义或保持。非交互调用继续明确提供参数。以下发现与数据保护规则不变。

```sh
nido codex setup
```

首次自动发现按优先级检查：当前环境变量 `CODEX_HOME` → 默认 `~/.codex` → 询问自定义路径。找到首个有效目录就停止；环境变量为空、失效或不是可用目录时再检查默认目录，两者都不可用才询问自定义路径，也可选择安装或跳过。非交互模式下未发现目录会提示使用 `--home`，不会猜测、扫描整盘或创建空替代目录。关联前仍需确认，可用 `--yes` 显式确认。

显式 `--home` 直接使用指定目录，不参与自动发现；指定路径无效时直接报错，不静默回退。已经登记目录时，重复 `setup` 默认沿用该目录，不跟随新终端的环境变量切换关联。

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

在 v0.8.2 中，`codex update --check` 的 GitHub API 查询同样要求先绑定 Nido API listener；未绑定时明确失败，启用后经所选 listener 查询官方元数据。普通 `codex update` 的可选版本预查询也使用该开关；未绑定时跳过预查询，不调用 API，仍可继续委托官方脚本。官方安装/更新脚本及其内部网络请求保留原有独立代理配置和确认流程，不自动继承 API listener；Codex 程序运行网络也不变。`--dry-run` 不查询 API、不下载或执行安装器。实际更新需要确认，已确认的非交互调用可以加 `--yes`。更新不维护自己的 Codex 版本仓库或回滚副本。

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

这只从原生配置的 listeners 读取候选，不证明可以联网，也不会启动服务；停止时仍可绑定。多个 listener 可交互选择，始终提供自定义地址；缺少核心、配置或 listener 时同样可以自定义。带认证的 listener 会询问用户名和密码，输入正常显示；非交互调用须提供完整自定义 URL。唯一无认证 listener 可用 `nido codex proxy builtin --yes` 直接绑定。端口变更后需重新绑定，已写入 Codex 的地址不会自动跟随。

仅清理 Nido 的代理块：

```sh
nido codex proxy off
```

官方安装/更新脚本的代理优先级为：本次 `--proxy`、当前终端代理变量、Codex `.env` 中的字面代理变量；Nido 主动查询版本的 GitHub API 另受上述绑定开关约束。例如：

```sh
nido codex update --proxy http://127.0.0.1:7890
```

本次覆盖不写入 `.env`。Nido 的环境片段不全局导出代理，以免影响其他程序。

进程代理变量的小写形式优先于大写形式，显式空值也会覆盖 `.env`，并同步到安装子进程的两种大小写变量。本次 `--proxy` 只覆盖代理端点，保留 `NO_PROXY`。对于变量展开、转义或冲突的 `.env` 代理表达式，Nido 明确报错，不尝试执行或完整解释 dotenv。

## Conda / pip

`nido conda configure` 分别选择镜像、缓存和环境目录；进入菜单不自动执行全部配置。也可直接执行缺值的 `nido conda mirror`、`nido conda cache` 或 `nido conda envs`，在终端补齐选择。已有选择默认保留，目录默认不变，不因接受镜像预设而顺带移动或重设环境。下面的显式参数形式仍适用于自动化。

本节命令自 v0.8.0 提供；v0.8.1 已发布，包含隐式用户配置的镜像合并修复与按项查询优化。[本次发行记录](conda-v0.8.1-release.md)以 Linux amd64 为唯一执行验收目标，分别列出正式包、旧上游矩阵及各公网端点的实际状态。Nido 把 Conda 与 pip 作为外部工具配置，不要求先安装内置 Proxy，也不安装 Conda 或执行 `conda init`。

先查看保存值和可选镜像：

```sh
nido conda
nido conda status --json
nido conda show
nido conda mirrors
```

`conda`、`status`、`show` 和 `mirrors` 都只读取本地状态，不执行 Conda / Python 或访问网络。首次启用先记录本机原有镜像：

```sh
nido conda setup             # 缺省为 all
nido conda setup conda       # 也可分别记录
nido conda setup pip
```

原有有效镜像被持久保存为 `default`，以后重复 setup 或切换镜像不会覆盖它。首次直接执行某个工具的 mirror 切换时，也会先自动捕获该工具的 default；捕获失败不切换。default 保存在 Nido 配置中，可跨 Shell、终端重启和机器重启继续选择。状态和镜像列表只显示是否已记录，默认不展开可能包含凭据的原 URL。

镜像、包缓存和环境目录分别配置：

```sh
nido conda mirror conda tuna
nido conda mirror pip tuna
nido conda cache conda "$HOME/.cache/conda/pkgs"
nido conda cache pip "$HOME/.cache/pip"
nido conda envs "$HOME/.conda/envs"
. "$NIDO_HOME/env.sh"
```

镜像的 `default` 指首次保存的本机原配置，`official` 指官方地址，两者并列。`off` 停止镜像管理，重新加载后释放受管变量；它不选择任何预设，也不删除 default 快照。

| 项目 | 接受值 | 作用 |
| --- | --- | --- |
| Conda 镜像 | `default`、`official` / `conda`、`tuna`、`off` | 原镜像、官方或清华；通过 `CONDARC` 加载 |
| pip 镜像 | `default`、`official` / `pypi`、`tuna`、`ustc`、HTTPS simple index、`off` | 通过 `PIP_INDEX_URL` 选择一个 index |
| Conda 缓存 | 目录或 `off` | 原生 `.condarc` 的 `pkgs_dirs`；off 只停止跟踪 |
| pip 缓存 | 目录或 `off` | 原生 pip 配置的 `global.cache-dir`；off 只停止跟踪 |
| Conda 环境目录 | 目录或 `off` | 原生 `.condarc` 的 `envs_dirs`；off 只停止跟踪 |

例如 `nido conda mirror pip https://packages.example.com/simple` 可使用自己的 HTTPS index；新输入 URL 不能包含认证信息、查询参数或片段。目录必须为外部绝对路径，支持作为参数传入的字面 `~/`；包含空格时加引号。已有目标必须是目录，不能与 Nido 软件目录相同或相互包含。Nido 不创建指定的数据目录；路径尚不存在时由上游工具在首次需要时创建，也不搬动已有包或环境。

Conda 镜像定义保存在 `$NIDO_HOME/conda/mirror-<hash>.yaml`，由内容确定文件名。没有显式外部 CONDARC 时通过该文件加载；已有外部 CONDARC 时保留原路径，镜像字段直接修改原文件。官方和清华预设保留本次选择时的频道顺序与范围，包括后来新增的私有频道，并保留私有频道映射和别名；只将已知 defaults/main/r/msys2、conda-forge/bioconda 地址映射到相应预设。v0.8.1 的镜像操作将完整七字段同步到标准用户文件及受管显式源，避免旧列表与后置文件合并；v0.8.0 的问题发现和修复源码结果见 [P0 历史验收](conda-p0-acceptance.md)。缓存、环境目录和 pip 命令不会顺便重写镜像。default 会把受管标准用户文件和显式源文件的已捕获镜像字段一起恢复，清除后来新增的映射；如果需要新增带凭据的地址，两个目标都必须保持足够私密的权限。中科大目前仅提供社区频道的动态缓存，因此不提供完整 Conda 预设。

`CONDARC` 是上游配置合并链的一部分，系统、root、用户和目标环境文件保持原顺序，管理员 `#!final` 或环境变量仍可能影响结果。存在自定义 CONDARC 时，Nido 记录并保留同一个规范绝对文件路径，只原地修改七个镜像字段；证书、客户端证书及私钥、代理、超时等其他设置及优先级不变，也不复制进 Nido。后续编辑或原子替换原文件，下次 Conda 调用实时读取；标准 `.condarc` 不重复加载，`$CONDARC` 引用和目标环境的原有读取规则保留。Conda 24.1.2 的 `config` 没有 `--prefix`，隔离测试使用仅对子进程设置的 `CONDA_PREFIX` 验证另一环境，不表示新增了该 CLI 参数。原文件必须是可安全修改的普通文件；缺失、不安全或目录型输入会明确拒绝切源。

pip 不设置或更改 `PIP_CONFIG_FILE`，保留原有配置文件读取方式；`PIP_INDEX_URL` 优先于文件中的全局与命令专属 index，但已有 `extra-index-url`、`no-index`、禁用缓存等其他设置仍可能影响结果。Nido 不把附加 index 当作备用镜像，也不关闭 TLS 校验。

Conda 镜像操作会立即写入相关原生镜像字段，选定 CONDARC 与受管环境覆盖仍需重新 source；CLI 本身不能更改父 Shell。缓存和环境位置直接修改原生配置，不设置 `CONDA_ENVS_PATH`、`CONDA_PKGS_DIRS` 或 `PIP_CACHE_DIR`，无需 source。如果这些变量已覆盖相应路径，变更会报冲突，需用户先明确处理；Nido 不以保存成功掩盖环境覆盖。pip INI 无法精确保留路径首尾空白，setter 会拒绝这类输入；Conda 路径同样拒绝首尾空白。

原生配置优先使用当前显式的外部 `CONDARC` / `PIP_CONFIG_FILE`，否则选择用户标准配置文件；已选择 Nido Conda 镜像时，频道和目录写入标准用户 `.condarc`，并同步持久关联的原显式外部 `CONDARC`（若存在），新 Shell 无原值 marker 时也能使用。这样避免释放镜像后旧文件重新覆盖设置，不修改不可变 overlay。任一目标文件不安全则预检失败，不作部分更新。其他键的语义、注释、权限和所属组保留，Conda YAML 重新序列化时格式可能调整；pip 尽量保留非目标行。`--dry-run` 的 `files` 和成功结果列出实际修改的文件。若本次需要把带凭据的新镜像地址写入他人可读的文件，会拒绝操作。

用下面命令核对当前环境和上游实际读取值：

```sh
nido conda validate
nido conda validate --json
```

`validate` 显式调用本地 Conda（已有 `CONDA_EXE` 时使用它，否则从 PATH 查找），以及当前 Python 的 `python -m pip`；没有 `python` 时查找 `python3`。只读检查配置和缓存位置，不联网、不安装包、不执行 `clean` 或 `conda init`。检查以当前进程环境为准，不在子进程偷偷注入保存值；尚未加载 `env.sh`、工具缺失、实际值不符，以及附加源等覆盖因素都会报告。因此保存成功和验证成功是两个不同状态。

这里的只读指 Nido 请求配置查询，不是对子进程施加系统调用沙箱。Conda 24.1.2 解析 `envs_dirs` 时会以 `a+` 打开 base 的 `conda-meta/history` 探测可写性；目录只读时会改变默认候选顺序。v0.8.1 只查询实际管理的项目，镜像单项仍完整检查七个镜像字段，但不再触发无关目录探测；验证 envs 仍保留上游真实行为。normal 与 strict 隔离结果分别与其原生基线比较，不保证普通 CLI 从不产生上游写入意图。

切回首次记录的镜像，或停止管理：

```sh
nido conda mirror conda default
nido conda mirror pip default
. "$NIDO_HOME/env.sh"
nido conda mirror conda off
nido conda cache pip off
nido conda envs off
nido conda reset
. "$NIDO_HOME/env.sh"
```

镜像环境片段记住本 Shell 加载前的 `CONDARC` / `PIP_INDEX_URL`，并管理有限的 Conda 频道覆盖变量，防止旧环境镜像覆盖所选预设。default 重放首次捕获的覆盖变量；其他预设临时清除这些覆盖。off 或 reset 后重新加载，仍是 Nido 上次设置状态的变量恢复原值或 unset；用户在期间手动改过的值保留。这个短期 Shell 撤销状态与持久的 default 快照分开，切换到 default 不依赖之前那个终端仍然打开。

变更命令支持 `--dry-run` 只读预检，以及 `--yes` 明确确认。off 和 reset 只停止管理，不回写旧镜像或目录；显式选择 default 才恢复首次镜像。reset 清空当前选择但保留 default 快照、原 CONDARC 路径关联、原生配置及历史镜像定义，不删除、迁移或清空用户环境和缓存。已有来源时，pip 操作和重复 setup 不会因当前临时的 CONDARC 覆盖而偷偷更换关联。配置合并、来源与测试边界见 [Conda 设计](conda-design.md)。

## 类别与单工具快捷代理配置

从 listeners 选择一次地址，再应用到一个类别中的全部工具；每个类别独立选择，不隐式复用前一类别的选择：

```sh
nido proxy bind                 # 逐类选择
nido proxy bind ai              # AI：目前为 Codex
nido proxy bind daily           # 日常：目前为 Git
nido proxy bind download        # v0.8.2：选择 Nido 内部 GitHub API listener
```

交互菜单按显示序号选择，也可输入 `listener:名称` 精确选择任意 listener（包括 `off`、`custom`、`keep` 或纯数字名称）。默认回车保持现状。

也可独立配置单个工具：

```sh
nido proxy bind --tool codex
nido proxy bind --tool git
```

AI、日常及 Codex/Git 单工具菜单始终包含自定义、保持现状和移除 Nido 受管配置。即使 Proxy 未安装、配置不可读或 listeners 为空，这些工具仍可输入 `protocol://[username:password@]host:port`；认证可省略，支持 HTTP、HTTPS、SOCKS5、SOCKS5H，IPv6 用方括号，认证中的保留字符使用百分号编码。输入和结果正常显示，不隐藏或脱敏。

`download` 或 `--tool nido` 只选择当前配置中具名且无认证的 listener，也支持保持现状和移除绑定；此项不保存自定义 URL 或凭据。确认时保存 listener 名称，后续每次内部 API 请求重新解析它的端口并检查受管进程和配置已就绪。listener 消失、要求认证、服务停止或配置修改后尚未重启时明确失败，不直连、不切文件前缀，也不自动启动或重启。只有显式高级临时覆盖才可替代本次绑定。

明确目标后可非交互配置或只读预检：

```sh
nido proxy endpoints --json
nido proxy bind ai --endpoint endpoint-isp --yes
nido proxy bind daily --endpoint endpoint-proxy --dry-run
nido proxy bind download --endpoint endpoint-download --yes
nido proxy bind --tool nido --endpoint endpoint-proxy --yes
nido proxy bind download --proxy off --yes
nido proxy bind --tool git --proxy 'socks5h://127.0.0.1:7892' --yes
nido proxy bind --tool codex --proxy 'http://user:p%40ss@127.0.0.1:7890' --yes
nido proxy bind --tool git --proxy off --yes
```

`--endpoint` 使用当前 YAML 中的 listener 名称；Codex/Git 的 `--proxy` 使用自定义 URL 或 `off`，Nido API 绑定只接受 `off`。两项互斥。`--yes` / JSON 非交互模式要求明确选择；不带显式地址的 `--dry-run` 只列出候选，带地址时执行对应工具的只读路径与冲突预检，不写文件或启动服务。本地 artifact、dry-run、普通只读和无需 API 的同版本操作不受 API 禁用或坏绑定阻断。

API 绑定单独保存在软件根的 `download-proxy.toml`，权限为 `0600`，包含 `schema = 1`、`product = "nido-api-proxy"` 和 `listener = "名称"`。不增加 `config.toml` 字段，旧版可以忽略并保留该文件；旧版不会自动使用绑定，也不具备未绑定禁用规则。移除绑定将 listener 清空并禁用 API，不删除来历不明的文件，冲突或不安全文件明确拒绝覆盖。

类别绑定是一次批量应用，单工具绑定只影响该工具；之后的显式操作生效，没有后台继承或端口同步。Codex 仍只写已关联 HOME 的 `.env` 受管块。Git 在用户级配置（默认 `~/.gitconfig`，可由 `GIT_CONFIG_GLOBAL` 指定）末尾维护自己的 `[http] proxy` 块，保留其他字节；它适用于 HTTP/HTTPS 远端，不配置 SSH。Nido 不解析仓库级、系统级配置或 include；用户级 include、已有外部代理、受管块被改动及路径安全冲突会停止写入，不强制覆盖。Git 自身的仓库/系统/环境优先级仍由 Git 决定。

## 内置 Proxy

Proxy 独立管理 `<Nido 软件目录>/proxy`，不依赖 Codex，不接管外部 Mihomo 或旧 Bash 环境。从 Mihomo 官方稳定版下载当前平台程序：

```sh
nido proxy install
nido proxy validate
nido proxy start
nido proxy status
```

默认在线 `proxy install` 需要先查询 GitHub API。如果首次联网依赖尚未安装的 Proxy，使用 [自举方案](nido-proxy-bootstrap.md)中的可信本地程序包与 SHA-256，避免这项依赖；随后再显式配置和启动。未绑定时的终端引导可输入本地程序包的绝对路径与摘要，无需先手写 `--artifact`；不在无 API 绑定时偷偷转为在线安装。`--artifact latest` 仍是在线来源，不能用于这个步骤。

交互 `install` 安装成功且没有配置时可进入 quick start，已有配置不被重建；`install --yes` 只安装核心，之后可运行 `nido proxy configure` 补齐首次配置。已有配置菜单默认保持，可明确导入新的完整私有 YAML，不自动合并或替换；确认摘要不展开订阅、节点凭据或完整 YAML。输入仍可能正常显示；JSON 结果单独写 stdout。

引导依次询问：

1. Provider URL、日常节点 filter 和端口（默认 7890）。filter 必填，`.*` 表示全部；订阅须提供原生 Mihomo provider 格式，不设置转换服务。
2. 是否启用独立下载入口；启用后填写独立 filter 和端口（默认 7891）。
3. 是否启用 ISP；启用后粘贴一行节点链接，填写端口（默认 7892）及测试地址，默认 `https://www.gstatic.com/generate_204`。
4. ISP 是否需要前置代理；启用后填写 provider 的前置节点 filter。

生成一个共享 HTTP provider，`proxy: DIRECT` 下载，缓存为 `providers/subscription.yaml`，启用 300 秒常规地址健康检查。`proxy`、可选 `download`、可选 `isp-relay` 分别使用自己的 filter 和 `url-test`，常规测试地址相同；空候选拒绝转发。启用 ISP 时生成 `isp-node` 和引用它的 `isp` url-test 组，后者使用用户选择的测试地址，测量 ISP 完整出站；有前置时节点通过 `dialer-proxy: isp-relay` 建链，方向是服务器 → 前置节点 → ISP → 目标。

默认只生成 mixed listeners：`endpoint-proxy`、可选 `endpoint-download`、可选 `endpoint-isp`，均绑定 `127.0.0.1` 且显式指定各自出站组；端口必须有效且不同。不生成传统 `port` / `mixed-port` / `socks-port`，规则只有 `MATCH,DIRECT`，不启用 controller。用户可修改所有 listener 名称或自行调整完整 YAML，名称没有保留契约；快捷配置只读取 listeners，不从传统端口猜测候选。

节点链接解析支持 SS（SIP002 与旧式 Base64，已支持的 obfs/v2ray-plugin）、VMess Base64 JSON、VLESS、Trojan、Hysteria2/Hy2、HTTP(S)、SOCKS5/SOCKS5H。VLESS 支持已实现的 TLS/Reality 与传输参数；不代表覆盖各协议所有分享链接变体。字段只解码一次，例如 `%2D` 恢复为 `-`、`%252D` 恢复为 `%2D`，字面 `+` 保留；未知、重复、冲突或当前协议不支持的参数报错，不静默丢弃。解析不联网，最终由核心检查配置；UDP/QUIC ISP 节点还要求前置链具有相应能力，不能假定 HTTP 或仅 TCP 前置可以承载它。

也可显式导入完整原生 YAML：

```sh
chmod 600 /absolute/path/to/config.yaml
nido proxy configure --file /absolute/path/to/config.yaml
nido proxy config-path
```

`--file` 要求绝对路径、普通文件及私有权限。配置在停止状态下经过静态和核心验证后原子保存为 `proxy/config.yaml`；不复制外部缓存或资源。首次 quick start 只允许原子新建，交互期间若出现其他配置则保留它并报错。缺少程序、空配置、重复键、多文档、非法端口或核心验证失败时不提交配置。安装、配置和更新均不自动启动。

配置策略限定为回环显式代理：

- 支持手工配置的传统 `mixed-port`、`port`、`socks-port` 和 `listeners` 的 mixed/http/socks；运行至少需要一个有效入口。传统端口仍参与进程和监听归属检查，但不作为快捷绑定候选。
- 独立 `listeners` 必须显式设置字面回环 IP，不能依赖 `allow-lan: false` 限制它；传统端口在 `allow-lan: false` 时使用 `127.0.0.1`。传统 IPv6 在启用 `allow-lan` 后使用带方括号的 `bind-address: '[::1]'`。
- HTTP/file provider 必须显式指定组件根内的相对 `path`。provider、规则及已支持的顶层 TLS 文件资源不能覆盖受管文件或核心固定缓存、数据库；链接与共享硬链接会被拒绝。`tls.custom-certifactes` 使用内联证书字符串列表。完整节点与规则语义继续交给核心，这些检查不能隔离任意 outbound 扩展或不可信配置。
- 策略字段使用规范的小写连字符名称。传统端口使用 YAML 整数，端口整数不能带前导零；listeners 另支持引号包围的十进制端口。
- 当前不支持 TUN、透明代理、非回环监听、其他入站类型、TLS/Reality 代理入站、脚本、外部 UI 或 YAML 别名与合并键。可选 HTTP controller 必须使用字面回环地址和非空 secret；Nido 不调用它。

配置检查与运行确认含义不同：`status` 只观察本地程序、静态配置与受管进程，`config=readable` 不代表核心验证通过；`validate` 执行 `mihomo -t`，不证明端口能绑定、provider 内容已加载或远端可达。核心检查可能按上游行为访问缓存，因此不属于只读状态查询。

`start` 在有限期限内核验进程和全部导出代理监听的归属；其他进程占用端口不能算启动成功。它不注册 systemd、launchd、Supervisor 或自动重启。`stop` 只向本实例的精确进程发送终止信号，超时或无法确认身份时保留记录并报错，不隐式强杀。

编辑固定配置后，用 `restart` 应用：

```sh
nido proxy restart
nido proxy endpoints --json
nido proxy endpoint endpoint-proxy
nido proxy logs --tail 100
nido proxy probe --endpoint endpoint-proxy --url https://example.com
```

`restart` 先验证新配置，失败时保持旧进程；停止后重新启动失败会明确报错。当前不提供热 reload。运行期间手工改动主配置后，状态通过 `config_changed` 提醒磁盘与受管启动快照不同。

`endpoints` 只列出 listeners；`endpoint [NAME]` 查询配置中的单个端点，不指定名称时必须唯一，不再优先 `common`。传统入口名仍为 `mixed`、`http`、`socks`，可用于明确查询和运行检查。混合入口输出 HTTP URL，SOCKS 输出 `socks5h` URL，均不含凭据。配置认证时，单个端点导出与无凭据探测会拒绝，绑定可交互提供认证或使用完整自定义 URL；按来源地址豁免认证时仍保守报告已配置认证。

`logs` 有界读取普通日志文件，不执行日志内容。日志保留核心原文。`probe` 用 `--endpoint NAME` 选择已就绪端点，无名称时要求唯一，测试指定 HTTP/HTTPS 目标且不跟随重定向；请求前后复核同一受管实例，记录、配置、程序或监听变化时拒绝成功。检查和连接并非原子操作，结果只说明该目标在当时的响应。状态、端点和 dry-run 均不联网或启动核心。

查看当前和最新版本，再在停止状态更新：

```sh
nido proxy update --check
nido proxy stop
nido proxy update
nido proxy start
```

`--check` 显式联网查询发布信息，并执行本地核心版本查询；不修改组件。实际更新不调用 controller 或 `/upgrade`，只在停止状态下准备私有临时程序，检查格式、版本与现有配置，再复核快照并原子替换。失败不覆盖现有程序，成功后仍保持停止。`meta-update`、`meta-backup` 及其他未知数据不被清理。

本地独立程序或 gzip 包可用于安装及更新：

```sh
nido proxy install --artifact /absolute/path/to/mihomo.gz --sha256 SHA256_HEX --yes
nido proxy update --artifact /absolute/path/to/mihomo.gz --sha256 SHA256_HEX
```

`--sha256` 接受输入文件的 64 位十六进制摘要；gzip 输入使用压缩包摘要。本地来源不查询发布 API，也不绑定官方 tag 或自动验签；安装仍会执行候选版本查询，已有配置时还会运行核心验证。自举时应固定可信版本、兼容架构和独立核对的摘要；在线下载另检查官方资产摘要、平台与展开大小。摘要验证不等于签名验证。v0.8.2 的内部 GitHub API 必须先绑定 listener，未绑定时在线安装和版本查询等 API 功能禁用；本地 artifact 不受此限制。已有有效非空绑定后可使用本次 `--proxy URL` 临时覆盖，资产保持 GitHub 文件策略。不为了下载自动启动 Proxy。若更新依赖当前 Proxy，先另行获取并核验本地包，再停止并使用 `--artifact` 更新，成功后仍需显式启动。

常规隔离测试使用临时根、假核心及回环辅助程序。真实核心兼容测试可通过 `NIDO_TEST_MIHOMO=/absolute/path/to/verified/mihomo make check-proxy-real` 显式执行，不自动下载核心，仅使用临时 HOME、软件根及回环 provider、HTTP/SOCKS/HTTPS 夹具。已执行的版本、结果和范围见[审计记录](proxy-audit.md)；本地夹具不能替代真实订阅、全部节点协议及其他平台的验证。

本次新流程的普通用户 Linux amd64 隔离入口只使用已有 SDK/GCC/bubblewrap 和预先 vendor 的源码，不使用真实 Mihomo 或外网：

```sh
NIDO_TEST_QUICKSTART_SOURCE=/absolute/offline-vendored-source \
NIDO_TEST_QUICKSTART_GO=/absolute/go/bin/go \
NIDO_TEST_QUICKSTART_OUTPUT=/absolute/new-evidence-directory \
make check-proxy-quickstart-linux-native
```

该入口使用临时 HOME、软件根和构建缓存，私有文件系统/PID，网络与宿主共享但测试只用临时回环端口；清空真实核心环境和代理环境、关闭模块下载。Proxy/Git 包、Nido 定向 race、新 CLI 与拒绝启动的假核心夹具须实际通过。仅允许 `TestLinuxKernelIPv6AndWildcardOwnership/IPv6_loopback` 与 `/IPv6_wildcard` 两项有明确地址族、回环地址或路由不可用证据的环境跳过；输出、环境元数据和 `environment-skips.txt` 保留理由及完整跳过记录，不计为 IPv6 通过，也不修改宿主网络。其他跳过、缺少环境理由、任何失败、缺工具或 namespace 失败均直接失败，不回退宿主执行。假核心与真实核心的证据分别记录。

新版 quick start 的真实 Mihomo 验证另用 `make check-proxy-quickstart-real-linux`，只接受已核验核心和预建静态 race CLI/测试程序，在独立网络 namespace 的临时 HOME、回环端口运行。所需输入与可重复命令见[设计中的真实核心隔离入口](proxy-design.md#quick-start-的-linux-amd64-真实核心隔离验证)，执行结果及协议边界见[审计记录](proxy-audit.md)。

## 状态与预览

```sh
nido status
nido status --json
nido codex status --json
nido doctor
nido doctor --json
```

状态按外部集成、内置 Proxy 和 GDS 分类，不联网或顺便修复配置。关联 Codex 的版本通过其 `--version` 查询，不宣称认证、会话或联网可用。`doctor` 另外检查生成环境及当前终端的 `CODEX_HOME` 是否一致。自 v0.8.0 提供的 Conda / pip 状态为静态观察，实际工具核对通过显式 `nido conda validate` 执行。

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

Proxy 卸载要求停止，仅移除程序，保留配置、日志、进程历史、缓存和未知文件。GDS 使用 `nido gds remove`，同样保留配置与数据。

当前没有 Nido 自身卸载、递归清理数据或 Codex 卸载命令。准备移除软件目录时，先按需解除外部关联、停止内置组件，并自行保留所需配置数据；不要把程序更新理解为数据备份。

## 可选 GDS

首次 `nido gds configure` 引导选择输入完整 UUID 或导入配置，已有配置默认保留；不自动探测后选用某张 GPU。未绑定 API 时，`nido gds install` 可引导选择可信本地 artifact 和摘要。自动化继续显式提供 `--gpu` / `--file`，首次无 API 安装使用 `--artifact`。安装和配置始终不启动工作进程。

以下命令自 Nido v0.5.0 提供。独立 GDS 首版为 gds-v0.1.0，官方资产支持 Linux amd64 v1、glibc 2.31 及以上；使用端需兼容的 NVIDIA 驱动，无需 Go、nvcc 或 CUDA Toolkit。自行构建可运行 `make build-gds`；正式构建基线与验签见 [GDS 发行说明](gds-release.md)。

```sh
nido gds install
# 或固定独立组件版本
nido gds install 0.1.0
nido gds configure --gpu GPU-完整设备UUID
nido gds validate
nido gds start
nido gds status --json
nido gds logs --tail 100
```

配置固定在 `$NIDO_HOME/gds/config.toml`，策略不写入 Nido 主配置。默认连续空闲 30 分钟后保留总显存 25%，不持续运行 kernel；发现其他计算或图形活动、探针失效或显存余量不足时停止 worker。GPU UUID 必须完整，首版不接受 GPU index。MIG、MPS、vGPU、容器 PID 视图和非 DEFAULT 计算模式不受支持；观测能力无法确认时拒绝保留。

安装和配置不启动组件，`status` 不加载驱动或执行程序。`probe` 显式进行有界 GPU 查询，`validate` 额外检查程序协议和设备能力，两者都不分配显存。配置替换用 `configure --file /absolute/config.toml`，要求停止；已有配置不被首次配置命令覆盖。

自动让出有观察窗口，不能保证直接启动的任务不先 OOM。需要确定顺序时，先 `nido gds stop`，仅在成功后运行实际任务，结束后再按需 start。stop 等待 monitor 和 worker 退出；无法核验身份或退出时返回错误。

更新与卸载均要求所有受管进程退出：

```sh
nido gds stop
nido gds update
nido gds start
# 或在停止后卸载程序，保留配置、日志和运行历史
nido gds remove
```

`nido gds update --check` 只读取安装凭证并联网查询 GDS 稳定版，不执行程序或改写配置；默认更新按数值版本选择最新 GDS，不自动降级。`update 0.1.0` 可显式重装或回退到指定版本。安装、更新也接受 `--artifact /absolute/path/to/gds [--sha256 HASH]`，不可与官方版本混用。

官方来源固定为本仓库 `gds-vX.Y.Z` Release，独立于 Nido 的 `latest`。下载同时核对 API SHA-256、大小、严格摘要文件、ELF 和候选版本；Sigstore bundle 供独立验签，自动摘要校验不等于客户端验签。v0.8.2 的内部 GitHub API 必须先绑定 listener，未绑定时在线安装和版本查询等 API 功能禁用；本地 artifact 不受此限制。已有有效非空绑定后可使用本次 `--proxy URL` 临时覆盖，资产保持 GitHub 文件策略，不会为下载启动任何服务。

`--dry-run` 只检查本地路径、来源语法、配置和运行记录，不联网、下载、执行程序、加载驱动或分配显存；远端版本及实时设备能力明确为未检查。

安装生成 `installed.json`，绑定程序摘要与协议。缺失或不匹配的凭证会阻止执行、更新和卸载，不自动接管同路径的旧程序。并发提交冲突会保留内容并报告恢复文件路径；需审查现场后再处理，不自动覆盖或回滚。

CPU 隔离测试通过 `make check-gds` 执行。普通用户原生 Linux amd64 验证使用已有 Go、GCC、bubblewrap 和离线 vendor，不下载或安装依赖：

```sh
GO=/absolute/path/to/go \
NIDO_TEST_GDS_VENDOR=/absolute/path/to/offline/vendor \
NIDO_TEST_GDS_OUTPUT=/absolute/path/to/new-evidence-directory \
make check-gds-linux-native
```

此入口拒绝 root、其他平台及已有证据目录，在私有文件系统、PID、设备和网络空间中执行 race、CPU 多进程、假驱动及 CLI 隔离测试；namespace 创建失败时直接失败。真实 HOME、服务 socket 和 GPU 设备不进入测试环境。需要真 GPU 的验证未自动启用。当前验证范围见 [GDS 审查记录](gds-audit.md)，协议与限制见 [组件设计](gds-design.md)。

## 路径变化与旧版

当前没有 `repair`、`--store`、`--from`、完整 HOME 迁入或挂载恢复命令。路径变化时不猜测旧路径、不扫描磁盘或改写外部链接。普通外部目录更换通过明确解除关联再关联处理；原目录已不可用且无法清理代理块时，需要用户先明确处理原配置。

旧 cdet 的双目录配置不会原位升级。请在独立 Nido 软件目录初始化，按需关联仍在原位的 Codex；旧配置和运行数据保持原样。冻结 Bash 行为见 [旧实现边界](legacy-boundary.md)，不属于当前 CLI。
