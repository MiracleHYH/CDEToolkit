# Nido v0.8.1：Conda 镜像合并修复

[v0.8.1](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.8.1) 已于 2026-10-18 发布。**本次使用与执行验收目标仅为 Linux amd64**，不在 macOS 安装。正式资产与签名、固定旧上游 normal/strict 及 gh-proxy.org 固定版本全套验收均通过。发行当轮官方直链首装通过，重复安装和真实自更新发生传输阶段错误，**公网双链整体验收未通过**。没有部署到 Zenith-Dev 或其他真实机器，没有切换真实镜像、调整真实路径或迁移数据。

后续 [Linux amd64 网络复查](nido-v0.8.1-network-recheck.md) 已补齐当前时段的直链重复安装，真实旧包自更新仍因 archive 响应不完整失败，且核实了此次失败后的文件保护；没有重写以下历史失败记录。诊断保护、内部 API 代理及分路由修复现已纳入 [v0.8.2](nido-v0.8.2-release.md)，该版正式包验签和公网验收另行记录，不覆盖本页的 v0.8.1 结果。

## 修复内容

v0.8.0 在无显式 CONDARC、用户 `.condarc` 已有清华 `default_channels` 的目标形态下，切换 official 会把原三项与官方三项合并。后置 overlay 的 `#!final` 阻止更后来源，却不清除之前的列表。v0.8.1 镜像操作将完整选中上下文同步到标准用户文件及受管显式 CONDARC；official/tuna 保留本次私有频道、别名和迁移映射，default 继续恢复首次持久快照。七字段为 `channels`、`default_channels`、`custom_channels`、`custom_multichannels`、`channel_alias`、`migrated_channel_aliases`、`migrated_custom_channels`。

validate 改为按当前管理项查询，镜像单项仍完整验证七字段，只在实际管理 envs/pkgs 时查询对应目录。这样避免镜像检查多余触发旧 Conda 的 base 可写探测；实际 envs 查询仍使用原生解析，不模拟上游合并语义。

原始 default 不重捕或覆盖；已有五字段快照仍只恢复其实际记录的内容，不补造迁移字段。缓存及 envs 仍是原生配置原地修改，不创建、搬迁或删除数据；off/reset 保留已写 native 值。证书、私钥、代理和未知键、注释、mode/Gid、并发检查与失败补偿边界沿用原设计。没有新增 Conda 安装、初始化、环境修复或迁移功能。使用命令见 [指南](usage.md#conda--pip)。

## 发行与验收状态

| 项目 | 当前记录 |
| --- | --- |
| 源码 P0 历史证据 | 修复源码在 Conda 24.1.2 / Python 3.12.1 / pip 23.3.1 的 normal、strict 模式各 8/8；原 v0.8.0 各 7/8，问题组失败。详情见 [P0 历史](conda-p0-acceptance.md) |
| 冻结提交、VERSION 与 v0.8.1 标签 | [`02ac76b`](https://github.com/MiracleHYH/CDEToolkit/commit/02ac76b453bfb9a185ca9dd4168487e94edbe32d)，精确标签为 v0.8.1，VERSION 为 0.8.1 |
| 本次源码、公开敏感信息检查及 CI | 本地 `make check smoke release verify-release`、源码与历史 gitleaks 均通过；[main 检查](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39132395565)、[tag 检查](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39132395610)、[发行工作流](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39132395795)全部成功 |
| 正式资产、摘要、安装器和 Sigstore | 保留四平台共 18 项资产：允许列表、API 大小/摘要与静态检查通过；9 份签名匹配精确 workflow、issuer、commit、ref；9 份原始载荷与相同冻结源码的本地 Go 1.27.2 构建逐字一致，latest 指向 v0.8.1。其他平台资产只记发行与静态核验事实，不作为本次使用或执行验收 |
| Linux amd64 直链公网执行 | latest 首装、激活及空组件状态检查通过；重复安装的第二次摘要请求为空响应。独立真实旧包检查更新通过，但实际自更新获取校验文件连接失败或超时，直链整体验收未通过 |
| Linux amd64 加速公网执行 | 最后一次串行 gh-proxy.org 固定版本全套通过：首装、重复安装、Bash/PATH、数据保护、真实签名 v0.8.0→v0.8.1 原生自更新、失败保留、同版离线跳过、拒绝降级及 Conda CLI。只代表该固定版本链路，不代表 latest 加速路径 |
| v0.8.1 公开 Linux 包旧上游 normal/strict | 同一已验签公开程序两模式各 8/8，版本与五份测试脚本绑定精确标签；每模式运行时清单记录的类型、mode、UID/GID、链接及内容摘要前后不变 |

[v0.8.0 发行时的通过项目](conda-release.md#v080-历史发行记录)保留为当时的平台、版本与配置范围，不替代本次新增目标形态或 Linux 新资产验收。

## 公网端点与重试记录

| 平台、链路及入口 | 已确认结果 |
| --- | --- |
| Linux amd64 仿真，早期 direct | 首脚本请求 curl 52 空响应，重试出现 curl 35 TLS 连接错误；这些早期轮次未执行网络脚本，不算安装通过 |
| Linux amd64 仿真，最后一次 direct latest | 2026-10-18 14:29:05–14:34:51 UTC，首装、激活及 fresh 状态检查通过；重复安装的第二份 archive 已与验签输入一致，但 checksum 请求 curl 52 空响应，未完成重复安装，未进入实际 update；外层 exit 1，没有整体通过结果 |
| Linux amd64 仿真，direct 自更新独立阶段 | 2026-10-18 14:41:21–14:41:40 UTC，真实已验签 v0.8.0 初始化及 127.0.0.1 失败保留检查通过；匿名原生 `--check` 返回 current 0.8.0、latest 0.8.1、available true。实际 update 获取校验文件时报告连接失败或超时，未进入新包替换，exit 1、无通过结果；不执行首装/重复安装，也不改变前轮失败结论 |
| Linux amd64 仿真，gh-proxy.org，首轮固定版本、仅加速 | 首装、重复安装、Shell 注册和保护检查、真实旧包初始化、失败保留及原生 `update --check` 通过；实际 update 返回不完整响应，未完成全链。保留为独立失败记录 |
| Linux amd64 仿真，最后一次串行加速验收 | 新临时目录、默认生产超时、同出口无其他下载；2026-10-18 14:11:37–14:18:45 UTC（7 分 08 秒）完整通过，包含真实旧包更新与 Conda CLI。结果仅含 accelerated，direct 字段为 null |

故障均保留各轮独立证据，没有通过无限重试、放宽生产超时、执行未核验包或本地响应替代来标为成功。后续当次官方匿名 core API 返回已用 60/60，响应给出的重置时间为 **2026-10-18 14:28:26 UTC（北京时间 22:28:26）**；这解释该次 403 的网络条件，不保证重置后所有链路可用。

加速最终通过发生在单流条件下，之前有并发下载时的不完整响应仍保留，不能将一次通过扩展为所有出口负载和端点均可用。latest 加速包路由没有由固定版本结果代替验证；直链首装通过不抵消重复安装和独立自更新的网络失败，不拼接为原轮整体通过。所有本轮网络验收已终结，不再重试。后续复验须有新的网络条件、全新隔离根与独立证据，不把计划复验列作本轮未完成的后台工作。

公网测试工具在标签发布后增加可选加速端点、latest/固定版本与单独 transport，并加固官方元数据来源与每链完成门禁，生产程序和发行标签不变；通过结论以外层退出码 0 及最终整体结果为准。默认行为不变，本次只执行 Linux amd64。宿主 `gh` 可只读核对官方输入元数据，但凭据不传入安装器、容器或 Go 客户端；实际自更新仍走匿名原生网络。固定版本成功不等同于 latest 成功，不同加速端点的状态也分别记录。

<details>
<summary>前期额外临时测试记录</summary>

范围明确为 Linux amd64 前，曾在 macOS 的临时 HOME 测试公开程序，没有部署或修改真实配置。相关原始记录保留，不计入本次通过依据，也不继续在 macOS 安装或验收。

</details>

## 正式包旧上游验收

公开 Linux 程序 SHA256 为 `e412dd4d37ffaa2baa7add444d1e1e398fb65877b6e7ca48156c79392c68c964`，执行前已独立验签。实际上游固定为 Conda **24.1.2**、Python **3.12.1**、pip **23.3.1**，normal/strict 各八组全部通过，包含原始 default、目标隐式配置、原生路径、双用户 pip 配置、自定义 CONDARC 证书代理和覆盖变量、失败重试及运行时完整性。五份 P0 脚本的哈希均匹配发行标签；公网测试入口的后续参数化不改变这些 P0 输入。

两模式原始 trace 分别为 3,443,031 与 3,461,897 行，每侧 57 次已知 base history 探测；normal 可打开但内容不变，strict 全部由内核返回 EROFS。完整运行时清单记录的类型、mode、UID/GID、链接及内容摘要在各模式前后保持一致。各侧 11 个配置区外写入意图均为失败的 `/dev/tty` 探测（ENXIO），没有互联网 connect 或发送事件。可写 open 是意图证据，不把这些计数解释为曾写入多少字节；还须结合路径归类、文件摘要与只读挂载判断。

该运行覆盖 Apple arm64 宿主上的 Linux amd64 仿真及原生 arm64 VM syscall 追踪，不等于原生 Linux amd64 或真实 GPFS。normal/strict 的清单按同模式比较所记录字段，种子文件模式差异仍按 [历史 P0 边界](conda-p0-acceptance.md#输入与隔离)解释，不要求两模式摘要相同。

## 验证入口

使用固定 [.go-version](../.go-version) 工具链先完成 `make check smoke release verify-release`，再由正式标签工作流执行发布检查、签名与上传。资产执行前独立核对发行 API 的大小/摘要、文件允许列表、对应标签的 workflow/issuer/commit/ref 身份和 Sigstore bundle，并与相同冻结源码的本地构建比较。普通安装客户端当前不验证 Sigstore；验收方的独立验签不改变加速服务的信任边界。

公开安装与自更新须提供已经独立验真的新、旧正式资产。下面复现实际通过的 Linux amd64 单流固定版本加速流程；暂停其他下载，并使用全新证据目录。测试失败不得改用本地文件冒充公网成功：

```sh
NIDO_ACCEPT_INPUT=/absolute/path/to/verified-v0.8.1/assets \
NIDO_ACCEPT_OLD_INPUT=/absolute/path/to/verified-v0.8.0/assets \
NIDO_ACCEPT_VERSION=0.8.1 \
NIDO_ACCEPT_OLD_VERSION=0.8.0 \
NIDO_ACCEPT_MODE=docker \
NIDO_ACCEPT_PLATFORM=linux/amd64 \
NIDO_ACCEPT_TRANSPORT=accelerated \
NIDO_ACCEPT_GITHUB_ACCELERATOR=https://gh-proxy.org \
NIDO_ACCEPT_INSTALL_VERSION_MODE=tagged \
NIDO_ACCEPT_METADATA_AUTH=gh \
NIDO_ACCEPT_OUTPUT=/absolute/path/to/new-accelerated-evidence-directory \
make check-nido-public-release
```

入口只在 Linux amd64 容器的临时 HOME 和软件根中验证重复安装、Bash 注册与 PATH 幂等、真实旧版本更新、失败保留、同版本离线跳过和 Conda CLI 夹具。tagged 模式仍核验公开 latest 安装脚本，使用其既有 `--version 0.8.1` 参数固定包与摘要，不将结果记作 latest 包路由通过。`NIDO_ACCEPT_METADATA_AUTH=gh` 只用于宿主元数据核对，客户端仍使用真实匿名 API。新旧程序均须来自其正式发行，公网安装结果与已验签程序逐字比较。arm64 宿主上的 Linux amd64 容器属于仿真执行，应在结果中明确保留该边界。

指定旧上游验收从同一已验签 Linux amd64 发行包提取 Nido，使用已有完整运行时镜像：

```sh
NIDO_P0_IMAGE=nido-conda-p0:24.1.2-amd64-final \
NIDO_P0_BINARY=/absolute/path/to/verified-v0.8.1/linux-amd64/nido \
NIDO_P0_OUTPUT=/absolute/path/to/new-runtime-evidence-directory \
NIDO_P0_LABEL=published-v0.8.1 \
NIDO_P0_VERSION=0.8.1 \
NIDO_P0_STRACE=1 \
NIDO_P0_STRACE_COMMAND=/usr/local/bin/nido-p0-strace \
make check-conda-p0
```

运行时与 trace 工具的来源、原生 x86_64 strace 参数、模式差异及重建方式见 [运行时夹具](../tests/fixtures/conda-p0-runtime/README.md)。normal 使用私有可写完整 prefix，strict 由内核保护安装目录；测试区均允许写入。没有真实 HOME、SSH、宿主 Conda 或服务 socket 挂载；运行时配置验收禁用外部网络。安装公网验收与离线上游矩阵必须分别记录，不能互相替代。

## 部署与恢复边界

发行成功，正式包已满足上述固定旧上游、固定配置形态的软件兼容矩阵；固定版本加速通路已完成全套验收。本轮出口网络未满足完整直链重复安装和自更新的条件，不能宣布公网双链验收全部完成，也不自动满足真实 Zenith-Dev 部署条件。现场仍须确认 GPFS 配额与写入、原子 rename/link、锁及 fsync 能力，软件目录的持久保留方式，平台改源 hook 的职责，以及激活已有环境后的覆盖变量和悬空链接。隔离结果不证明镜像联网、求解、包下载或真实证书可用，也不等同于原生 Linux 或 GPFS 验收。

未来首次启用须在尚未切源的目标 Shell 中核对 `CONDA_EXE`、PATH 中的 Python、配置来源及覆盖变量，再分别记录 Conda/pip default；指定 `CONDA_EXE` 不会同时固定 pip 的 Python。目标形态的原清华 defaults、SUSTech NVIDIA 映射及原 iVolc index 都属于本机 default，不应替换成官方地址后再捕获。新版本升级不重新捕获已有快照，也不替用户处理真实环境的旧缓存覆盖。

本地证据位于忽略目录 `build/release-v0.8.1/`：`verified/receipt.json` 记录资产与签名；`local-check.log`、`public-audit-final.log` 与三个 CI 记录源码检查；`conda-p0-provenance.json` 绑定公开 Linux 程序、版本、五份脚本和两模式结果，`conda-p0/` 与 `conda-p0-trace-audit/` 保留原始命令、运行时清单和 trace。`accept-linux-accelerated-serial/container-evidence/` 的 `result.json` 与 `accelerated-result.json` 记录完整固定版本加速通过；`accept-linux-direct-final/` 和 `accept-linux-direct-update-only/` 保留直链最后两项失败的参数、HTTP/原生客户端输出与退出码，后者的安装/重复安装不执行。其余 `accept-linux*` 目录保留早期轮次，不把失败证据覆盖成成功。

`default` 只恢复首次镜像语义，off/reset 只停止管理。目录修改不会自动还原历史键或移动数据。失败补偿拒绝覆盖用户并发编辑，系统拒绝撤销时报告未恢复部分；合法首次 default 可以保留。主配置已提交而 env 刷新失败时保留意图和 native 值，修复故障后可重新 init。完整人工撤销须事先保存相关原文件和 metadata，并独立处理数据；本次发行范围不包含部署或真实机器配置修改。
