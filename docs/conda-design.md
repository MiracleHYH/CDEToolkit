# Conda / pip 外部配置集成

本组件自 Nido v0.8.0 提供。日常命令见 [使用指南](usage.md#conda--pip)，发行功能与验证入口见 [v0.8.0 发行说明](conda-release.md)。镜像提供可持久选择的原配置、官方和清华预设；缓存与环境目录直接修改上游原生配置，不依靠 Shell 环境切换。

## 配置范围

| 操作 | 命令 | 意义 |
| --- | --- | --- |
| 静态状态 | `nido conda status` / `show`；非交互时无子命令同 status | 查看记录与当前选择，不执行上游程序 |
| 交互配置（组件源码收尾完成，尚未发布） | 交互终端中的 `nido conda` / `nido conda configure` | 接入统一 `componentPage` 平铺页面 |
| 首次记录 | `nido conda setup [conda\|pip\|all]` | 缺省 all；记录首次启用时的本机有效原镜像 |
| 列出镜像 | `nido conda mirrors` | 展示 default 是否已记录、官方与镜像选项 |
| Conda 镜像 | `nido conda mirror conda default\|official\|conda\|tuna\|off` | 原镜像、官方、清华，或释放管理 |
| pip 镜像 | `nido conda mirror pip default\|official\|pypi\|tuna\|ustc\|HTTPS_URL\|off` | 原镜像、单个预设或 HTTPS simple index，或释放管理 |
| 缓存目录 | `nido conda cache conda\|pip DIR\|off` | 原生配置持久修改；off 只停止跟踪 |
| 环境目录 | `nido conda envs DIR\|off` | 原生配置持久修改；off 只停止跟踪 |
| 实际核对 | `nido conda validate` | 显式调用当前本地 Conda 与 Python/pip，只读且不联网 |
| 停止管理 | `nido conda reset` | 清空选择，保留 default 快照与已写入的原生路径 |

`conda` 是 Conda 官方镜像的别名，`pypi` 是 pip 官方镜像的别名。default、official 和 off 含义不同：default 主动恢复首次保存的镜像；official 主动选择官方地址；off 释放 Nido 对该项的管理，不选择预设。

## 统一交互页面（组件源码收尾完成，尚未发布）

Conda 已接入公共 `componentPage`，本轮组件源码收尾和下文限定范围的 Linux amd64 隔离验证已完成，尚未发布。公共 UI 的真实键盘与完整 PTY 验收仍由 Dev 统一完成。下文编号菜单的历史验收不作为新 UI 已通过的证据。组件仍只快捷修改外部 Conda / pip 的配置，不安装或管理 Python、Conda，不创建、搬迁或清理环境与缓存数据。

在交互终端中，`nido conda` 和 `nido conda configure` 统一进入公共 `componentPage`，每项完成后刷新同一页面。非交互时，`nido conda` 无子命令仍查看静态状态；`configure` 需要交互终端，脚本使用明确子命令。

页面直接显示已保存的 Conda / pip 镜像选择、环境目录（`envs`）、Conda 缓存目录（`pkgs`）、pip 缓存目录，以及 Conda / pip 各自是否已有首次镜像记录，无需进入“查看”动作。状态明确标为 Nido 保存的选择，不能冒充上游此刻的实际生效值，也不展开首次记录中的原始地址或凭据。进入和刷新页面不执行 Conda 或 Python；“核对当前生效设置”是显式动作，执行本地只读配置查询。变更的预检和首次镜像捕获也可能读取本地上游配置，不联网或安装包。

页面保留 13 个一级动作，视觉分组只帮助定位，不增加“查看”“配置”或“停止管理”的导航子菜单：

| 视觉分组 | 一级动作 |
| --- | --- |
| 镜像 | 设置 Conda 镜像、设置 pip 镜像 |
| 目录 | 设置环境目录、设置 Conda 缓存目录、设置 pip 缓存目录 |
| 首次镜像记录 | 保存首次镜像（选择 Conda 和 pip、仅 Conda 或仅 pip） |
| 核对 | 核对当前生效配置（本地只读查询） |
| 停止管理 | 停止 Conda 镜像、pip 镜像、环境目录跟踪、Conda 缓存跟踪、pip 缓存跟踪，或停止全部管理 |

镜像选择在首次切换时自动记录对应工具的原镜像；已有 default 不覆盖。页面保留一个“保存首次镜像”动作，通过必要的范围参数选择“Conda 和 pip”“仅 Conda”或“仅 pip”，不切换镜像；缺少另一个工具时仍可单独记录已有工具。该选择属于动作参数，不增加导航层，也不增加一级动作数量。脚本仍可使用 `nido conda setup all`、`nido conda setup conda` 或 `nido conda setup pip`。停止目录跟踪保留原生路径值，停止全部管理沿用 reset 语义并保留首次镜像记录。

修改镜像时一次选择参数：默认“保持当前选择”，其余为“首次保存的镜像设置”、官方源、清华镜像；pip 另有中科大镜像和自定义 HTTPS simple index，后者仅追加必需的 URL 输入。此处不提供返回或停止管理选项；Esc 取消参数选择，停止管理使用页面上的对应一级动作。修改目录仅输入对应路径，保留当前路径即不提交修改。镜像值选择和路径、URL 字段是动作参数，不作为新的导航层。既有 `official`、`conda`、`pypi`、`default`、`off` 命令值与别名保持不变；交互目录字段不把 `off` 作为停止指令，脚本仍可显式使用该命令值。

组件的 `choose`、`ask`、`confirm` 只作为公共 UI 的薄调用层。选择使用 ↑ / ↓ 和 Enter；Esc 在参数或确认中取消当前操作，在页面中返回。写入确认默认取消。Ctrl+C 请求安全退出，等待正在进行的操作结束并恢复终端，不承诺强制中断写入或撤销已确认的操作。生产交互不再以编号、`y/N` 或 `q` 退出指令解析输入。Conda 组件不自行处理 ANSI、键盘事件，也不引入独立 TUI 依赖；输入结束（EOF）结束交互，不反复提示。

只读上游查询使用可选的 `Service.Ctx`。CLI 在一次 Conda 操作入口临时注入当前 `Session.Context`，并在返回时恢复原 context，避免把已取消的会话状态带给之后的调用。默认执行器的每个 Conda / Python 只读子进程继承该 context，仍保留最长 10 秒、进程组清理、输出大小限制与错误输出脱敏。取消后不启动下一查询，也不使用取消期间返回的结果捕获首次镜像；测试注入的 Runner 即使不能中途停止，也必须在返回后检查取消状态。

每个写操作复用原有明确子命令的预检和写入流程。预检通过后、确认写入前，显示 Nido 保存的当前选择到目标选择的变化，并列出用于核对的 `config.toml`、`env.sh`、适用的镜像配置文件，以及本次需改写的原生文件；没有原生文件变更时明确说明。预览不展开首次镜像快照中的原始地址或凭据。目录原生生效、镜像环境仍需加载 `env.sh` 的边界继续提示。

取消确认或保留现有设置是正常操作，不视为写入失败；只按类型识别本次操作的取消错误，并发退出请求不会掩盖无关的原生文件写入或终端错误。Manager 在首次文件提交前检查 context，取消后不进入尚未开始的提交；本次提交一旦开始，仍沿既有原子替换和失败补偿路径安全结束，不在文件替换中途强杀。每项单独预览、确认并提交：取消、离开页面或后续某项失败，不撤销此前已完成的独立操作；本次尚未提交的选择不会保存。统一页面不增加覆盖全部操作的回滚，单次写入的失败补偿及并发编辑保护仍遵循下文原有边界。

## 持久 default

Conda 和 pip 分别拥有一次性捕获的 default 快照。`setup` 显式查询本机有效配置，将原镜像信息保存到 Nido 状态；首次直接切换某工具镜像时也先捕获该工具，捕获失败不切换。已有快照不会被重复 setup、后续切换或 reset 覆盖。

快照可以跨 Shell、终端重启和机器重启继续选择。它与 source 前的环境变量记录不同：后者仅用于本 Shell 停止管理时撤销受管变量，不能代替持久 default。快照只描述镜像，不备份整个 Conda / pip 配置或包数据。

首次记录会在原生频道写入前保存，并同步 Nido 目录；后续写入失败时可以保留有效的 default，但不会提交失败的镜像选择。用户取消确认或预检失败均不保存新快照。确认等待期间原配置可能改变，因此首次记录在确认后重新读取。Conda 快照包括旧频道别名和自定义频道的迁移映射；这些规则会改变同一 URL 的实际解析目标。旧五字段快照继续可读，但无法补回当时没有记录的迁移规则。

原镜像可能来自私有仓库，URL 中可能有凭据。默认文本与 JSON 状态只显示是否已记录，不展开快照内容；`mirrors` 必须让用户看到 default 可选及其记录状态。快照与 overlay 留在 Nido 私有文件中，不输出到普通日志；若恢复频道需要把带凭据的新 URL 写入他人可读的原生文件，则预检拒绝，不扩散私有快照中的秘密。

`nido init` 仍只初始化 Nido，不执行 Conda 或 Python。上游工具缺失时静态查看正常工作，但 setup 或首次捕获会明确失败，不猜测或替换成本机“原配置”。

## 所有权与文件

实现位于 `internal/integrations/conda`，由 `internal/nido` 组合到 CLI、主配置与环境输出。保持静态组合，不增加后台进程、插件框架或通用事务引擎。

```text
<Nido>/config.toml                       # 选择、原 CONDARC 关联与持久 default 状态
<Nido>/env.sh                           # 仅激活镜像相关变量
<Nido>/conda/mirror-<hash>.yaml           # 不可变镜像 overlay
<选定原生 condarc>                      # 频道 / 原生路径；default 恢复完整已捕获镜像字段
<原显式 CONDARC 文件>                   # 存在时原地更新镜像字段，其他键实时读取
<选定原生 pip 配置>                     # global.cache-dir
<外部 Conda 环境、Conda/pip 包缓存>/     # 数据由上游管理
```

镜像 overlay 的内容决定文件名；切换不将同一路径改成另一个镜像，其他终端仍可引用旧文件。重置不递归删除历史 overlay、环境或缓存。

原生修改只改变对应键的语义，保留其他键、注释与文件权限。Conda 使用 YAML node 重新序列化，格式可能调整，不承诺其他内容逐字节不变；pip 按行修改目标，尽量保留非目标行。外部目录接受绝对路径及字面 `~/`；已有目标必须是目录，不能与 Nido 软件目录相同或相互包含，路径首尾空白会被拒绝。只做路径预检，不创建目标数据目录、不搬迁旧环境、不复制缓存、不执行 clean 或 purge。

原生文件的所属组也保留，无法保留时安全失败。已有 overlay 必须是当前用户拥有、没有额外硬链接的私有普通文件。各命令只应用显式指定的项：改 pip 缓存不会重写 Conda 频道；重复指定同一路径或镜像会重新应用该项，保留其他项的后续手工修改。

## 原生路径配置

| 项目 | 写入位置 | Shell 行为 |
| --- | --- | --- |
| Conda 环境目录 | 原生 Conda 配置的 `envs_dirs` | 不设置 `CONDA_ENVS_PATH` |
| Conda 包缓存 | 原生 Conda 配置的 `pkgs_dirs` | 不设置 `CONDA_PKGS_DIRS` |
| pip 缓存 | 原生 pip 配置的 `[global] cache-dir` | 不设置 `PIP_CACHE_DIR` |

优先选择当前显式外部 `CONDARC` / `PIP_CONFIG_FILE`，否则选用户标准文件。已经选择 Nido Conda 镜像时，频道和目录写入标准用户 `.condarc`，并同步已保存的原显式外部 `CONDARC`（若存在），防止旧值覆盖新设置。原文件路径保存为关联，新 Shell 无环境 marker 时仍能使用；不将证书或代理等内容复制进 Nido。不可变 overlay 不接受路径修改。两文件任一不安全则预检失败，无部分更新；`--dry-run` 的 `files` 与成功结果列出实际修改目标。

执行 off/reset 后、尚未重新 source 的过渡状态也同步已知原文件。原生 Conda 目标限定为上游会读取的 `.condarc`、`condarc`、`.yaml` 或 `.yml` 文件名；例如 `custom.condarc` 会被当前 Conda 忽略，因此拒绝声称已写入生效。

原生路径写入后立即持久生效，无需 source。若现有 `CONDA_ENVS_PATH`、`CONDA_PKGS_DIRS`、`PIP_CACHE_DIR` 等覆盖该目标，操作报告冲突，要求使用者先明确处理；组件不会静默清空用户变量或声称写入一定等于实际生效。其他系统或环境级配置也按上游合并规则处理，显式 validate 核对最终值。

路径的 off 和 reset 只停止跟踪，保留已写入的原生值，不恢复旧目录、不删除键、不清理数据。pip INI 不能精确表示路径首尾空白，setter 明确拒绝，不通过 trim 静默改成另一个目录。需要改回其他位置时，再显式设置所需目录。

## 镜像与频道

官方与清华 Conda 预设映射 defaults/main/r/msys2 与 conda-forge/bioconda，保留本次选择时的有效频道顺序和范围，包括后来新增的私有频道。与当前镜像上下文中这些已知源匹配的完整 URL 同样映射；其他私有频道、别名、自定义频道和频道集合映射保留。未在映射中声明的 URL 只对明确的公共镜像域名和标准路径做识别，不能仅凭 `/pkgs/main` 等路径片段猜测私有仓库。切往公共镜像时不会携带原源的认证、查询参数或转义 token。default 始终使用首次捕获的完整原镜像信息。不增加频道或扩大依赖解析范围。

镜像切换还原生更新标准用户文件的 `channels`，避免较早频道序列被合并进新镜像。存在原显式 `CONDARC` 时，保持其路径，只在该文件原地更新七个镜像字段：`channels`、`default_channels`、`custom_channels`、`custom_multichannels`、`channel_alias`、`migrated_channel_aliases`、`migrated_custom_channels`。其他键及其合并注释保留。没有显式文件关联时，镜像映射继续通过 Nido overlay 提供。显式选择 default 时，标准用户文件也恢复快照中完整的五个或七个镜像字段，清除后来新增的映射；仅靠 overlay 的 `#!final` 不能删除较早配置的映射键。新复制的镜像凭据不得写入他人可读的原生文件。off/reset 不回写旧镜像字段，快照永不覆盖。

| 项目 | `official` | `tuna` | `ustc` |
| --- | --- | --- | --- |
| defaults/main、r、msys2 | `https://repo.anaconda.com/pkgs/...` | `https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/...` | 不提供 |
| conda-forge、bioconda | `https://conda.anaconda.org/<频道>` | `https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud/<频道>` | 不提供完整 Conda 预设 |
| pip index | `https://pypi.org/simple` | `https://pypi.tuna.tsinghua.edu.cn/simple` | `https://mirrors.ustc.edu.cn/pypi/simple` |

`custom_channels` 的值是频道名之前的父路径，例如 `conda-forge: https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud`。只映射地址不会自动启用该频道。中科大当前仅提供社区频道动态缓存，因此不作为完整 Conda 预设。

pip 使用单个 `PIP_INDEX_URL`，不自动添加 `extra-index-url`。多个 index 没有“主源失败再用备用源”的顺序保证，与私有包混用还可能发生 dependency confusion；已有附加源由用户配置，验证时报告。公共自定义 index 必须是 HTTPS simple endpoint，不添加 trusted-host，不禁用证书验证。

pip 的配置查询输出按 Python 字符串表示精确解码，避免把转义字符记录成不同的原地址。缓存验证也核对命令专用的 `cache-dir`，不因 `pip cache dir` 正确就忽略 install/download 等命令的覆盖。INI 中缩进的 section 外观内容仍可能是上一选项的多行值；存在歧义时拒绝修改。

## 镜像激活与释放

Conda 镜像操作立即更新原生镜像字段；之后仍需显式 source `$NIDO_HOME/env.sh` 激活选定 `CONDARC` 及镜像环境覆盖。pip 使用 `PIP_INDEX_URL`；不设置或更改 `PIP_CONFIG_FILE`，原有 pip 全局与命令专属配置仍读取，用户显式 CLI 参数仍可覆盖。

`CONDARC` 是上游搜索链的一部分，系统、root、用户和目标环境的标准配置仍按 Conda 原有顺序读取。原变量指向外部文件时，Nido 保留同一条规范绝对路径，仅原地更新镜像字段，不额外加载或遮蔽该文件。因此证书、客户端证书及私钥路径、代理、超时、目录等非镜像设置保持原优先级、变量展开和实时读取行为；即使 `CONDARC` 恰为标准 `.condarc`，也不重复加载而提升其优先级。`--prefix` / `--name` 指向其他环境时仍由 Conda 自行决定环境配置优先级。

Nido 只持久保存原文件的路径关联及镜像快照，不备份证书、代理凭据或任意非镜像配置。原文件后续手工编辑或原子替换无需重新生成 overlay；下一次 Conda 调用立即读取。首次 setup conda/all 可记录未激活的来源；显式 Conda 镜像操作可选择当前外部文件，旧受管 overlay 则从原值 marker 或持久关联寻找来源。pip、路径、off/reset 和活动状态的 setup 不偷偷改变已有来源关联。off/reset 保留该关联及原生镜像值，重新启用时仍可使用。

新 Shell 尚未导出已保存的 CONDARC 时，首次捕获与预设频道查询显式读取正在配置的来源；validate 仍只检查调用者的真实环境。查询前后记录并核对将要修改的原生文件，保存前再次核对，避免慢查询覆盖期间新增的私有频道。首次 default 或缺失工具的 default 可以先保存，但中间记录不改变已激活的来源；本次原生差异始终与操作开始时的配置比较。

原文件需是 Nido 目录之外、名称受上游支持的有界普通文件。缺失、目录型、符号链接、硬链接或不安全父路径会明确拒绝切源，不创建一个只有镜像键的新文件来替代丢失的证书和代理设置。文件必须可安全原子修改；新复制的镜像凭据仍受到原生文件权限检查。YAML 重新序列化可能改变格式，但不会更改非镜像节点的语义、注释、权限或所属组。更早系统配置的 `#!final` 及显式环境变量仍可能限制镜像结果，应使用 validate 核对。

预览与写入均拒绝序列化后超过 1 MiB 的原生文件。多文件预检在任何写入前完成；主配置未提交时进行安全补偿，遇到并发编辑不覆盖用户修改。新文件发布后即使临时链接清理失败也尝试撤销本次发布；若操作系统持续拒绝撤销，会明确报告已有文件无法安全补偿。主意图已提交而环境文件刷新失败时保留已应用的原生配置，可重新 init 刷新环境。

镜像环境片段还管理有限的 Conda 频道覆盖变量，例如 `CONDA_CHANNELS`、`CONDA_DEFAULT_CHANNELS` 和 `CONDA_CHANNEL_ALIAS`。default 重放首次捕获值，其他镜像临时清除这些覆盖，防止旧环境配置悄然覆盖所选镜像。

环境片段记住本 Shell 加载前的值和是否存在。off 或 reset 后再次 source，若变量仍为 Nido 上次设置的值或 unset 状态，则恢复原值或 unset；用户中途手改的值保留。重复 source 不误记受管值为原值。这个机制只用于镜像，不为 cache/envs 生成环境恢复状态。

持久 default 则由配置文件提供，可以在新 Shell 中选择原镜像，不依赖前一个终端的内存。CLI 无法直接改变父 Shell，也不会自动更新其他已打开终端。

## 状态、预览与验证

`status`、`show`、`mirrors` 和 init 不执行上游程序。setup、首次镜像切换的自动捕获，以及 validate 显式查询本地 Conda / Python，不联网、不安装包。变更沿用 Nido 的 `--dry-run` 与确认；dry-run 不保存快照、不发布 overlay、不修改原生文件或数据目录。

本地 Conda 优先使用已有 `CONDA_EXE`，否则从 PATH 查找；pip 使用当前 Python 的 `python -m pip`，没有 python 时查找 python3。validate 保持当前环境，不向子进程偷偷注入尚未 source 的镜像选择；它核对镜像、原生目录和额外源等覆盖因素。检查只代表当前上游版本与进程环境，不证明镜像下载可用、包完整或其他环境相同。

## 隔离测试与证据

2026-10-19，本轮统一页面与取消保护的最终源码快照 `verified-inputs/` 完成 Linux amd64 仿真容器隔离验证。普通与 `-race` 两种模式各通过 66 组 Nido Conda 回归和 90 组假上游 Conda 库测试，容器退出码均为 0；`tests/conda-cli-isolation.sh` 也通过。66 组中包含 21 组交互专项和 5 组 Context 专项，均为子集，不重复累计。一个既有 Darwin 专用用例 `TestCondaP0CommittedIntentSurvivesEnvironmentRefreshFailure` 在 Linux 跳过；两个真实 Conda 运行时 opt-in 入口被精确排除、未执行，不计入通过项。

组件交互测试采用类型化的测试 Driver，按稳定动作 ID 选择 13 个一级动作并核对状态、参数、预览、确认和保护边界，不与页面位置或数字输入协议耦合。5 组 Nido Context 专项覆盖捕获取消不启下一查询、确认期间取消不提交、Manager 拒绝已取消的提交、Session context 注入后恢复，以及并发取消不吞无关写入错误。库中新增的 6 组 Context 用例还覆盖默认只读执行器对慢速 `sh` / `sleep` 子进程组的取消，测试要求在 3 秒内完成清理。Go vet、gofmt、Bash 语法与 diff 检查通过。

本轮使用普通 UID `10001`、组 `10002`、只读根、临时 HOME / cache 和 `--network none`；宿主未执行 Nido 测试，未运行真实 Conda / Python，也未联网、发布或操作真实机器配置。证据根为 `build/conda-ui-v2-20261019/`，最终日志为 `verified-unit.log` 与 `verified-race.log`，源码摘要见 `verified-inputs.sha256`。同一证据根保留开发过程的失败日志（含 `attempt-1/`）；最终结论只基于上述最终源码的重新验证。

公共 UI 的真实键盘、Esc / Ctrl+C、默认取消与完整 PTY 验收由 Dev 统一完成，不以类型化 Driver 的组件测试代替。公共 `ScriptDriver` 仅供 Go 测试显式注入，其编号、`y/N` 与 `q` 兼容输入不是生产 UI 协议，也不能证明生产箭头交互通过；生产不会因环境变量、CLI 参数或终端识别结果自动启用该测试驱动。

以下为此前实现的历史证据，保留原有测试对象、输入协议与边界，不计入本轮通过数。

2026-10-19，上轮尚未发布的编号循环子菜单通过 Linux amd64 仿真容器中的组件隔离验证：59 组 Nido Conda 回归（其中 19 组为旧编号交互专项，不重复累计）及 84 组假上游 Conda 库测试通过。覆盖连续操作与重新进入、返回与 EOF、预览和确认拒绝、首次记录、原始镜像遮蔽、并发编辑拒绝，以及停止管理保留原生值。一个既有 Darwin 专用不可变文件故障注入用例在 Linux 跳过，两个真实 Conda 运行时 opt-in 入口未执行，不计入通过项。

该轮使用普通 UID `10001`、只读根、临时 HOME 和 `--network none`，只运行假 Conda/Python。测试父目录保持私有；既有文件权限夹具使用 `umask 022`，产品私有文件仍显式使用 `0600`。首次扩展检查因 `umask 077` 与两项既有 `0640` 夹具断言不一致而失败，日志保留，修正测试环境后复验通过，未放宽产品权限规则。组件证据位于 `build/conda-menu-20261019/verified-entrypoints/`，不替代 Dev 汇总后的完整主菜单、网络及其他组件验收；该轮未提交、发布或操作真实环境。

2026-10-18，修订后的实现通过本机 Go 1.26 的 `make check smoke`（全项目 vet、race、CLI 隔离与本机包冒烟），新增组件另通过最低 Go 1.23 的定向 race 回归。CLI 完成 Linux/macOS amd64、arm64 四平台编译；交叉编译不代表在其他平台运行过真实 Conda/pip。

`tests/conda-cli-isolation.sh /absolute/path/to/nido` 使用临时 HOME、Nido 根与原生配置，仅运行本源码构建的 CLI 和可控的只读假上游。测试清除宿主 Conda/pip 变量，避免使用真实 Conda/Python、镜像网络或用户文件；静态命令确认不会触发假上游。

`conda run -n agent --no-capture-output python tests/conda-source-upstream-isolation.py /absolute/path/to/nido /absolute/path/to/conda` 可显式运行真实 Conda 的离线隔离验收。它自行建立临时 HOME、Conda root、两个 prefix、证书与数据夹具，核对非标准及标准 CONDARC 的 TLS、代理、`$CONDARC` 引用、动态 prefix 优先级、实时编辑与 off/reset/default 行为，不联网或安装包。实际通过版本和日志见 [审计记录](conda-audit.md)。

测试覆盖首次捕获、已有 default 不覆盖、捕获失败不切换、跨新 Shell 选择 default、官方别名、完整 URL 频道的原生切换与 default 恢复、多原生文件同步、off/reset 保留快照和原生值、其他键与注释权限保留、环境覆盖拒绝、dry-run 无写入，以及镜像 source 的恢复和手改保留。环境片段不得激活旧的 cache/envs 变量。脚本进入 `make check`，不会失败后回退到宿主工具。

验收记录以最终代码的检查结果为准。本地假上游与文件行为测试不代表完成实际镜像下载、其他平台或全部上游版本验收。

2026-10-18，另在临时 HOME 中使用已有 Conda 26.1.1 与 `agent` 环境的 pip 26.2.1 / Python 3.12 完成实际上游只读核对：首次 setup 捕获后切换清华、官方、reset，原生目录内容在 reset 时保持不变；之后手动改变原生镜像地址、重复 setup，default 快照仍未被覆盖。显式选择 default 并 validate 后，首次的完整 main URL、私有频道和原镜像恢复，新目录仍保留，strict 与 `ssl_verify` 语义不变。过程不联网、不安装包、不创建数据、环境或缓存目录，只证明这组本地版本的配置合并、持久恢复与原生写入行为，不代表镜像下载可用或其他平台通过。

## 上游依据

- [Conda 配置搜索、合并与优先级](https://docs.conda.io/projects/conda/en/stable/user-guide/configuration/use-condarc.html)。
- [Conda 配置项与目录语义](https://docs.conda.io/projects/conda/en/stable/configuration.html)。
- [Conda 发布说明](https://docs.conda.io/projects/conda/en/stable/release-notes.html)：25.9 调整隐式 defaults，26.1 支持 `custom_multichannels.defaults` 优先覆盖。
- [pip 配置优先级](https://pip.pypa.io/en/stable/topics/configuration/)、[cache-dir](https://pip.pypa.io/en/stable/cli/pip/#cmdoption-cache-dir)、[附加 index 搜索语义](https://pip.pypa.io/en/stable/cli/pip_install/#finding-packages)。
- [清华 Conda 帮助](https://mirrors.tuna.tsinghua.edu.cn/help/anaconda/)、[清华 PyPI 帮助](https://mirrors.tuna.tsinghua.edu.cn/help/pypi/)。
- [中科大 Conda 帮助](https://mirrors.ustc.edu.cn/help/anaconda.html)、[中科大 PyPI 帮助](https://mirrors.ustc.edu.cn/help/pypi.html)。

镜像范围按 2026-10-18 的站点说明核实；预设不构成站点持续可用或任意包版本完整的保证。
