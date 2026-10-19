# Proxy 组件审查

审查基线为 `9dd3951`，日期为 2026 年 10 月 17 日。基线结论为 Proxy 只有基本生命周期骨架，不能据此宣称组件构建完成。后续已改用独立的 `internal/components/proxy` 并逐轮验证。本文按阶段保留发现、修复和证据；各历史章节中的“当前实现”“最终”“已通过”只对应其当时冻结的源码。当前以紧接的“体验后精简”记录为准；它取代同日较早页面与绑定设计，不借用旧真实核心、跨版本、make check 或发行验收结论。

## 体验后精简、固定首装与安全删除（2026-10-19，未发布）

本轮只修改源码、假核心隔离测试和文档，不提交、推送、发布、SSH、下载或执行真实 Mihomo，不安装系统服务，不切换入口，也不读取或修改宿主现有配置、认证、会话和数据。官方只读元数据核验与 HEAD 请求不属于核心下载或兼容性执行。当前项目要求 Go >= 1.26.0，下表局部测试使用 Go 1.27.2；整体 Linux amd64 与 CI 仍由 Dev 根会话统一验收。

最新契约如下，优先于下方同日精简前的历史记录：

- 确认未安装且运行已停止时，一级 Proxy 页面只有“安装 Proxy”；已安装页面只有 Quick Start、更新内核、更新订阅、重载配置、重启、停止、启动、删除。公共动态动作回调在每次操作后重新读取状态和动作，安装、删除后立即更新页面。core、组选择链与已有延迟只做状态展示；同页只读显示 HTTP/file provider 的名称、类型和核心报告的更新时间，缺失标为未报告，不据此推断节点健康或刷新实连成功。
- Proxy 页面及 CLI 移除工具代理绑定、`--tool`、YAML 导入与文件日志入口。`proxy bind`、`--file` 和 `proxy logs` 不再可用；高级用户直接编辑固定 `config.yaml`，查看状态所示 `output.log` 路径。端点只导出原生 listeners，`chooseProxy` 由 Codex／根菜单承担。历史类别和单工具绑定测试不能代替本轮已精简入口的验收。
- 空来源及 `latest` 首次安装均选择固定 v1.19.32 官方包，经文件 HTTP 与 GitHub 加速 policy 直接 GET，不调用 Release API、Latest 或 APIHTTP。保留本地 `--artifact` 首装与原有 gzip、原生格式和版本检查；显式 Latest／CheckUpdate 仍可联网查询，但不参与首装。固定元数据按值返回，不增加可变 Service 描述符或生产测试开关。
- 根网络为两个独立设置：GitHubAccelerate 取 `none`、`proxy` 或 HTTPS 前缀，另存通用 ProxyURL。`proxy` 引用必须有 ProxyURL；前缀请求直连前缀，不叠通用代理，API／其他 Nido HTTP 则独立使用 ProxyURL。因此前缀与 ProxyURL 可以并存，`--proxy none` 不改变已选前缀，固定核心首装只走文件路线且 API 请求为零。旧 sidecar 退役，仅保留路径以保护未知数据，不读取、迁移或采用旧 listener 路线；Nido 不持久化 listener 名称，受管 listener 只是 Codex 的候选 URL。旧三模式互斥和 API／资产共路线均属于下方历史契约。
- remove 在同一组件锁内停止原记录的出生实例，确认退出、程序及运行记录快照未变后，只删除固定核心文件。停止失败、身份不明、PID 复用或并发替换均不删程序。配置、`.back`、provider、缓存、日志、未知文件和原生升级数据保留；正常停止可更新受管运行记录。再次安装复用这些数据，不重置 YAML 或 provider，不自动启动。
- Quick Start 保留分享链接、provider/filter、url-test、listeners、ISP 和前置链能力；新模板为 `IN-TYPE=INNER`、目标端口 443、精确域名 `github.com` 或 `release-assets.githubusercontent.com` 的两条 AND 规则指定 `download`，无该组时指定 `proxy`，最后 `MATCH,DIRECT`。普通 listener 仍使用自己的显式出站组，provider 引导仍为 DIRECT。已有配置不自动增加规则。
- 两条规则只建议正常初始化后的核心内部 HTTPS 更新流量，不将 Nido 网络模式传给核心，也不保证所有未来官方重定向经过代理。v1.19.32 的 `HandleTcp` 在 inner 未初始化或地址设置失败时同步报错，HTTP helper 才进入直拨分支；成功返回 INNER 管道后的异步代理拨号失败不会触发直拨重试。重定向到未覆盖的新域名仍可落到 `MATCH,DIRECT`。[HTTP helper](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/http/http.go#L74-L81)、[INNER 管道](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/listener/inner/tcp.go#L21-L40)、[updater 内部 HTTP](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/updater/update_core.go#L164-L188)。

固定安装信息于本轮重新核验，来源是[官方 v1.19.32 Release API](https://api.github.com/repos/MetaCubeX/mihomo/releases/tags/v1.19.32)，发布时间 `2026-09-30T17:09:36Z`，各资产状态 `uploaded`。官方[资产 HTML](https://github.com/MetaCubeX/mihomo/releases/expanded_assets/v1.19.32) 独立显示相同 SHA-256；精确字节数来自本次 API，而非 HTML 的四舍五入 MB。Linux amd64 资产的纯 HEAD 最终响应为 200，Content-Length 另确认 22,877,876。历史 receipt 只用于定位候选，没有被当作本次新信任来源。

| 固定资产 | 压缩包字节数 | 压缩包 SHA-256 |
| --- | ---: | --- |
| `mihomo-linux-amd64-compatible-v1.19.32.gz` | 22877876 | `ba3ce607747a07f948fc35780e108a4a7c7f552a38b9bd4d115f313ebcb89c20` |
| `mihomo-linux-arm64-v1.19.32.gz` | 20809592 | `9dd862e28b46ff7d775f169cceebc28deccaa0a9e804237d421cd2571e0caba0` |
| `mihomo-darwin-amd64-compatible-v1.19.32.gz` | 22302953 | `18b382df77bded2ad0fb3db27db5636cb15b20729d5ba995a357eeb9b46bf507` |
| `mihomo-darwin-arm64-v1.19.32.gz` | 20756622 | `3312a6780652c622890fd4357c6a853bbf865464fd047ac7b7f52dab8de18652` |

下载来源固定为 `https://github.com/MetaCubeX/mihomo/releases/download/v1.19.32/<资产名>`。SHA-256 pin 是源码审查后的完整性边界，不等于上游签名；同名包发生变化时应拒绝安装，不能从最新 API 自动更新摘要绕过固定值。原生升级仍由运行核心负责，不从这张首装表推导其升级校验结论。

当前新增定向结果如下，仅为假核心／临时 HOME／回环或纯 mock；不是整包、全项目、原生 Linux 或真实核心通过：

| 本轮定向检查 | 结果与边界 |
| --- | --- |
| Remove 生命周期 race | 2.411 秒通过；同锁停机后删除、失败保程序、身份复核与重装数据复用 |
| 固定安装与取消 race | 1.867 秒通过；固定 GET、APIHTTP 零调用、同长度错误 SHA 拒绝执行、配置/备份/未知数据不变、私有合成描述符成功 fetch 后本地惰性 gzip 安装、取消边界 |
| Quick Start／整改专项 race | 4.763 秒通过；新 INNER HTTPS 规则的下载组／默认组与普通 listener、provider 引导边界 |
| Linux 验证入口 mock | 19.496 秒通过；只证明入口契约及证据拒绝，未原生运行 Linux 或真实 Mihomo |
| 页面／CLI 专项 race | 3.726 秒通过；精简页面及移除旧入口的定向回归 |
| 新规则专项 race | 2.000 秒通过；同步修正固定 Template 的旧单规则期望后，模板专项 race 1.465 秒通过，不代表核心实连 |
| 首装与独立网络专项 race | 2.347 秒通过；单次确认、API 零请求、`--proxy none` 及前缀与独立 ProxyURL 共存 |
| 最终同页状态与动作专项 race | 2.981 秒通过；HTTP/file provider 名称、类型、核心更新时间展示及动态动作、删除、固定首装回归 |
| 当前真实 QS 夹具迁移 | 改为纯生成＋Service.Configure，无旧 PTY 菜单或绑定业务；只完成编译、三个纯辅助用例 race 1.502 秒和 Linux amd64 测试程序交叉编译，没有执行真实核心 |

固定下载变更的 `gofmt -l` 和定向 `git diff --check` 无输出。Proxy 源码已冻结交由 Dev 统一整合；Dev shell 的最终真实 PTY、原生 Linux 和整体 CI 验收仍待完成。下方较早整包 race、完整 PTY 和 `make check` 对应精简前源码，不能补作本轮通过。旧公网发行与真实核心章节均为对应历史源码的事实，已删除的类别／单工具业务不再作为当前可用验收；目前没有新增真实 provider、业务转发、原生更新／reload 或各平台兼容性执行证据，也尚无本轮可发布结论。

## 原生 controller 与一级 Proxy 页面阶段记录（2026-10-19，精简前，未发布）

本节保留当时的页面、绑定与测试事实，已由上一节取代当前产品契约；尤其工具绑定入口、YAML 导入、日志 CLI 与此前测试结果不代表体验后精简实现。

本轮根据后续确认的轻量原生方案修改源码、假核心隔离测试和文档。本 session 不提交、推送或发布，统一提交与发行交由 Dev 根会话在整体重构完成验收后执行；本阶段不 SSH，不运行真实 Mihomo、系统服务、发行包或公网安装，不读取或修改宿主现有配置、认证、会话及数据。Proxy 自有实现及对接的本地验收已按下表收尾，与整体项目、原生 Linux 和发行验收分开判断。当前共享 v2 框架要求 Go >= 1.26.0，旧 Go 1.23 的通过结果只属于历史实现。

本轮变更的责任和契约：

- Proxy 使用公共 `componentPage` 的一级页面，动作直接呈现 Quick Start、工具代理配置、核心原生 release 升级、provider 手动更新、reload、显式启停和核心/分组选择链与已有延迟。四个绑定入口合并为单个“配置工具代理”，先选 Codex/Git/Nido，再选 listener 或自定义地址；类别及单工具 CLI 保留。不增加 listener 探测、节点编辑、selector、测速榜或连接面板。
- 新 Quick Start 模板保留 provider/filter/listener/分流、全部已支持分享链接、ISP 和前置代理能力。CLI 提供回环 controller 与随机 secret；重复 Quick Start 复用安全的既有 controller 设置。纯生成器的 nil controller 仅为调用兼容，高级用户没有 controller 的原生 YAML 仍被接受，原生操作明确不可用，不自动补配置。
- 新模板 HTTP provider 使用 `interval: 0`，只取消定时拉取，健康检查和 URLTest 仍启用 300 秒周期；有效缓存缺失或损坏时，核心初始化仍可能下载。provider 更新通过原生接口，不删除缓存，不将一次 204 当作节点实连证明。用户已有 YAML 不自动改 interval。
- 分组只沿核心 `GET /proxies` 的 `now` 读取选择链，展示已有 history 的最近延迟和时间。负载均衡、循环、缺失、未知与未测量明确区分；不主动测速、不相加延迟、不据此推断 ISP dialer-proxy 的完整链路。
- 常规更新统一为原生 `POST /upgrade?channel=release`，不加 force，不将 Nido 的网络 mode 传入核心，不保留重复的 PrepareUpdate/CommitUpdate 下载和停机替换机制。首次 Install 仍可导入本地核心；本地 artifact 更新明确拒绝。原生响应 200 后仍须确认受管出生身份、固定程序与运行映像、版本及 controller/代理监听稳定，才能刷新记录；未知或失败不启动另一个实例掩盖结果。实现仅对 Linux amd64/arm64 开放原生升级，macOS 缺少等价映像证明时在请求前拒绝，不以历史 macOS 测试替代该边界。
- 升级前 `meta-update` 必须不存在；`meta-backup` 允许可证明属于当前用户的固定原生核心备份结构，避免正常重复升级被自身备份阻断。链接、共享硬链接、特殊对象、未知文件或归属不明均拒绝；Nido 不主动清理这些目录。核心原生下载与替换由上游执行，不声称 Nido 另行校验了该次资产 SHA-256，也不承诺上游失败时没有触碰程序。
- reload 固定 `PUT /configs?force=true`，body 只引用当前根内 `config.yaml`；controller 控制字段必须与运行快照一致，漂移时要求本地 restart。204 不代表全部 provider 或监听成功，更没有完整 YAML 读回核验。reload 可能中断连接，后续复核失败时报告未确认，不自动回滚、重试或重启。
- 配置替换保留旧原文到单份 `config.yaml.back` 的边界不变：无旧配置、同内容、取消或校验失败不改备份，锁内复核并发与安全文件，先备份再原子替换主文件；两个文件不是跨掉电事务。写配置仍明确要求停止。运行中的 Validate 不执行 `-t`，Restart 先静态检查、显式停止，再由 Start 执行核心检查；失败保持停止。
- 当前真实核心夹具同步移除旧语义：本地 artifact 更新只验证拒绝与原程序/数据保留；restart 的核心语义错误在停止旧出生实例后失败，恢复原配置后须显式 start。`TestProxyRealMihomoCrossVersion` 与第二核心输入变量保留兼容，但改为 `stop → remove → install` 的两个版本首次重装与缓存复用，不称为原生 updater 验证。本轮未执行真实 Mihomo；原生 updater 成功路径只有 fake controller 与 fake image 测试，旧真实停机更新的历史事实保持原样。

官方能力核对固定为 v1.19.32：[provider 路由](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/provider.go)、[配置重载参数](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/configs.go#L385-L430)、[force 与监听应用](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/executor/executor.go#L177-L198)、[原生升级和异步 exec](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/upgrade.go#L24-L49)、[固定备份目录](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/updater/update_core.go#L117-L159)、[interval 与初始化](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/resource/fetcher.go#L53-L106)。没有 `/listeners` 路由；`GET /configs` 只返回 General，不能读回完整配置。此核对不等于实际运行该核心，也不自动推及其他版本。

本轮 Proxy 自有范围的最终本地检查如下。均使用 Go 1.27.2、假核心、临时 HOME 或本地 HTTP 夹具，没有执行真实 Mihomo：

| 本地检查 | 结果与边界 |
| --- | --- |
| Proxy 整包 race | 服务 Context 完成后 14.164 秒通过，取代较早 11.757 秒的阶段结果 |
| Nido 专项 race | Proxy、APIBindingAudit、APIProxy 专项 66.061 秒通过；随后删除未来 TaskContext 分支及旧 Interactive 测试字段，定向 race 3.391 秒通过，不扩称全项目 race |
| 当前 race CLI＋惰性核心的完整 PTY | 26.18 秒通过；公共 v2 InputSecret/secretPrompt 已接入，实际方向键、Enter、隐藏输入、Esc、默认 Skip、重复配置、工具与下载绑定及数据保留均完成 |
| PTY 夹具修复回归 | printf 八进制转义原会吞并后随数字，改用四位 octal；新增含默认值的真实 PTY 惰性 UI 契约 race 3.209 秒通过。该问题只在夹具编码，不改生产输入语义 |
| 格式、静态检查 | 全部相关源码 `gofmt -l` 无输出、`git diff --check` 通过；`go vet ./internal/components/proxy ./internal/nido` 通过 |
| Linux amd64 构建 | CGO=0 CLI、Nido 和 Proxy 测试程序交叉编译通过；没有 Linux 原生执行，不计为原生 race |
| 当前真实核心夹具 | 已迁移到新合同，只做编译和辅助假核心测试；没有执行真实核心、原生 updater 或两版本重装场景 |

Dev 随后在本次精简前完成整体本机 `make check`，退出 0，记录为 `build/refactor-evidence/make-check-tui-final-r2.log`。已只读核对全项目 vet/race，以及 Nido、Conda/pip、Proxy 三个 CLI 隔离流程均通过；其中 Proxy 包为 15.995 秒、Nido 包为 133.942 秒，Proxy 真实 PTY 的四类完成标记齐全。该次构建与执行目标为宿主 Darwin arm64，不扩称 Linux amd64 原生验证或 GitHub CI；本 session 未重复执行相同测试，也不将它视为后续精简变更通过。

完整 PTY 日志为 `build/proxy-native-20261019/cli-tui-fixed.log`，已逐项核对以下四类完成标记：默认 Skip 与重复配置；公共 TUI 方向键、密钥隐藏和 Esc 取消；主 TOML Mode/Listener 与下载绑定隔离；quick start CLI 隔离。只有四类均出现且命令退出成功，才计为该完整流程通过；较早纯脚本、入口 mock 和局部方向键测试不替代它。

公共私密输入现在直接走共享桥，不再保留 Proxy 自己的 Secret 能力探测；Install、CheckUpdate 和 controller 操作接入 Context。取消只影响本地等待和后续请求，不能撤销核心已受理的写操作；取消、超时或 EOF 后不自动重发。

本轮没有新增原生 Linux、真实核心升级/重载、真实外部 provider、业务转发或跨平台通过结论；整体原生 Linux 和 CI 验收仍由 Dev 根会话完成后另行记录，尚未提交或发布。下文旧源码的 Linux amd64/arm64 原生 race、真实核心和发行记录继续作为历史证据保留。

## v0.6.0 发行验收（2026-10-18）

本次按明确发布授权，将 quick start、节点链接解析与类别/单工具代理配置纳入 Nido **v0.6.0**，以 Linux amd64 为验收目标。[正式 Release](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.6.0) 于 2026-10-18 03:20:25 UTC 发布，精确标签指向 `dd2d1354116e37c227e43faff8c669858751289b`，latest 已指向该版本。本节记录本次发行，后文各轮“未发布”说明保留其发生时的边界。

公网验证只使用临时 HOME 和软件根，不切换实际入口，不安装系统服务。发行包 CLI 的新流程使用拒绝启动的惰性核心，真实 Mihomo 转发证据沿用下方冻结的 Linux amd64 217.49 秒记录，二者分开判断；本次版本准备没有修改生产逻辑。

新增 `tests/proxy-public-release-isolation.sh` 与显式 `make check-proxy-public-release`，普通离线 CI 不联网运行此入口。33 个外层契约及 13 个 curl 桥契约拒绝平台/UID 错误、无效或缺失输入、已有输出、namespace 失败、假 PASS、错误结果/下载次数/URL/摘要、同版冒充升级、不完整 HTTPS 参数及响应字节不一致。首次安装实际使用 latest；固定版本重复安装、含空格根、可选 v0.5.0 升级分别核验程序字节与原配置/环境/未知文件。临时 HOME 中的 Go 1.27.2 完整 `make check smoke release verify-release`、Go 1.23.12 race 定向回归与模块校验通过；公开源码与历史凭据扫描通过（227 项候选文件）。这些本地打包结果不替代后续公开资产验收。

| 发行验收项目 | 实际结果 |
| --- | --- |
| 自动检查 | [版本提交 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39092652419)、[精确标签 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39093017576) 和[签名发布工作流](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39093017579) 全部成功；条件式 GDS legacy 按既有规则跳过，没有改 GDS 版本或发行流程 |
| 公开资产 | 精确 18 项白名单，逐份真实 HTTPS 下载，并匹配官方 API 的名称、URL、大小与 SHA-256；四平台包、四份校验文件及安装器与本地固定 Go 1.27.2 构建全部逐字一致 |
| 独立验签 | 官方 Cosign v3.0.6 先核对大小与官方摘要，再在临时 HOME 中验证九份新资产的 Sigstore bundle；identity 精确为 `https://github.com/MiracleHYH/CDEToolkit/.github/workflows/release.yml@refs/tags/v0.6.0`，issuer 为 `https://token.actions.githubusercontent.com`。旧 v0.5.0 Linux amd64 包与摘要另以旧标签 identity 完成两次验签 |
| 包结构与来源 | 四平台全部通过单普通成员、权限、归档尾部、Go 主程序身份、GOOS/GOARCH 和 CGO=0 检查；固定版本与 latest 安装器都与标签源码逐字一致。客户端安装器自动检查 SHA-256，本轮额外独立验签，不宣称客户端自动执行 Sigstore |
| Linux amd64 公网安装 | J66 普通用户 UID 1001、Linux `6.8.0-124-generic` x86_64、已有 bubblewrap 0.9.0。私有 HOME/文件系统/PID/user namespace 中完成 latest 首装、固定版本重复安装、latest 重复安装、含空格自定义根和 v0.5.0 → v0.6.0 升级；合计 **10 次真实包/摘要下载**，全部与已验签输入逐字一致 |
| 命令注册与保留 | 五次安装均实际 source 各自的 `env.sh` 并核对 `command -v nido` 精确指向临时根内程序。既有 TOML 注释、环境文件、未知用户文件与模拟外部 Codex 配置、认证、会话、数据库、Git、Shell 和 Mihomo 文件逐字保留 |
| 公开包的新流程 | 使用公开包中的 CLI 和拒绝启动的惰性核心，完成交互 quick start、SS `%2D` 解码、url-test/ISP relay 配置、可编辑 listener、类别/单工具/custom/no-listener/off 配置及数据保留；全部 PASS。配置中出现 `dialer-proxy` 不计作本轮真实链路转发 |
| 清理 | 测试 CLI 均退出，namespace 与所有本轮远端临时目录已删除；专用 SSH 回环传输监听已关闭，本地私有控制目录已删除。未切换实际入口、安装系统服务或修改现有配置、认证、会话和数据 |

Linux amd64 包为 **3,938,351 字节**，SHA-256 `13ef5e093a4f3d430b07b1915b703b89bb3907a6a149f35d367ae103ee91f758`；包内 CLI SHA-256 `0153d4b6c53a960bfa51eb62e856da3ba3792375a3781629a825a7deb8d24bd4`。验签输入、API 元数据、复现比较、namespace 日志、10 次下载及清理证据保留在本地 `build/proxy-release-v0.6.0-20261018/`，不作为发行资产上传。

验收保留了两次失败，未将其计为通过：首次直连 GitHub 遇到 TLS 连接错误，随后仅为本轮创建 `127.0.0.1` 动态端口的 SSH SOCKS 通道，仍使用真实 HTTPS、证书校验和响应字节比较；第二次真实首装成功，但新夹具误用了其他组件的 `installed:false` 字段，未接受 Proxy 实际的 `program/config=missing` 状态。本轮修正了测试断言，新增生产 CLI 实际 JSON 的 6 个通过/拒绝子项及真实环境生成逻辑的 2 个子项；Go 1.27.2 / 1.23.12 定向 race 回归与最终完整公网验收均通过。该修复仅涉及验收源码，已发布程序字节未变，未移动标签或重传资产。

没有保留已确认的 Linux amd64 发布阻塞项。本轮没有重新运行真实 Mihomo；真实 provider、url-test、SS ISP 前置链和 HTTP/SOCKS/HTTPS 转发仍对应下方 217.49 秒冻结证据，有效生产路径未改。17 类节点仅证明指定核心的配置接受，不扩称全部协议实连、任意核心版本或状态迁移。其他平台的新流程真实核心验证不在本次服务器验收范围内。

## Quick start 最终提交审计（2026-10-18）

本轮整理 quick start、节点链接解析、listener 导出、类别/单工具代理配置及其隔离测试，以 Linux amd64 为验收目标。重新复核配置生成、初次独占创建、共享地址校验、Git 文件归属/XDG 冲突、收据补偿、只读预览和各验证入口的判定，没有保留已确认而未修复的问题。版本仍为 `0.5.0`，这些新流程尚未发行；源码推送不创建标签，也不运行发布工作流。

最终审计修复两项问题：

- Git 受管块采用的字符串编码会把原始不可打印字符（例如 U+200B）写成 Git 不接受的 Go Unicode 转义，可能使整个配置无法解析。共享校验现在在写入前拒绝无效 UTF-8 和不可打印字符，既有编码与收据格式保持不变。回归覆盖 U+200B、U+FEFF、无效 UTF-8、已有/未有受管块的字节保留，以及普通中文/希腊文地址的实际临时 `git config --file` 解析；类别/单工具和 dry-run 入口也验证拒绝。
- 真实核心入口原先仅要求父测试 PASS，同名旧测试缺少节点矩阵时可能冒充新证据。现在强制 17 个节点子项逐项精确 PASS；缺整矩阵、缺单项、父项/前缀/后缀冒充均拒绝，纯 mock 契约从 28 扩为 33 分支。此前 217.49 秒的完整真实日志通过了新增证据守卫。

两套 Go 工具链的隔离回归覆盖上述修复；本机完整 `make check smoke` 包含全项目 race、vet、格式/Shell 检查、两套 CLI 夹具及只读包冒烟。源码与历史凭据扫描继续执行。推送后的自动验收使用现有 [check 工作流](https://github.com/MiracleHYH/CDEToolkit/actions/workflows/check.yml)：Linux amd64 Go 1.23.x/1.27.2、保留的 macOS 兼容检查、公开源码扫描和 GDS native 必跑项；可选 legacy 项按原条件运行，具体结论以该源码提交的检查页为准。

下节的原生真实核心记录保留对应冻结输入和产物摘要。本轮唯一新增生产修复是拒绝上述异常代理地址，没有更改已验证的有效 ASCII 地址、配置生成、节点解析或 Mihomo 生命周期路径；修复后的地址处理由新回归和原生 Linux CI 检查，不把普通 CI 冒称真实核心复验。本轮未重新运行真实 Mihomo，也未发布、切换入口、安装系统服务或改动现有配置、认证、会话和数据。

## Quick start 的 Linux amd64 真实 Mihomo 隔离复验（2026-10-18）

按后续授权，新增独立 `TestProxyRealMihomoQuickStart`，在 J66 普通用户 UID 1001、Linux `6.8.0-124-generic` x86_64、Go 1.27.2/GCC 13.3.0 上完成新流程的真实核心验证。首轮 **215.09 秒**通过；加强 ISP 自动测速观测后的最终同场景复验 **217.49 秒**通过，17 个节点配置子项全部 PASS，无跳过、无父子进程 race 报告。本轮未发现新增生产兼容性缺陷，没有修改生产代码或增加模块依赖；只补测试、隔离入口和文档，未发布或改变版本。

使用官方 [Mihomo v1.19.32](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.32) 的 `mihomo-linux-amd64-compatible-v1.19.32.gz`。下载前精确匹配官方发布 API 的名称、URL、大小和摘要：归档 **22,877,876 字节**，SHA-256 `ba3ce607747a07f948fc35780e108a4a7c7f552a38b9bd4d115f313ebcb89c20`；gzip 完整性校验和展开后静态 ELF amd64 检查通过，核心 SHA-256 `7a0d59da2e678d56c899a3db996a2ad8963286c4f3634b0451435db248f13fa1`。实际报告 v1.19.32、Go 1.26.8、`with_gvisor`；官方核心保持原字节，没有重新编译或启用其内部 race。摘要核对不等于签名验证。

新增 `tests/proxy-quickstart-real-linux.sh` 和 `make check-proxy-quickstart-real-linux`。主机仅使用已有 SDK/GCC/bubblewrap，编译使用离线 vendor 和私有构建目录；执行 namespace 只挂载三份只读静态程序（race CLI、race 测试程序、官方核心）与本次空临时目录、私有 proc/dev。不挂载宿主 HOME、源码、系统库、凭据、服务 socket；清空环境和 capabilities，使用独立文件系统/PID/user/**网络**空间，全部 provider、测速、上游与目标均为临时字面 IPv4 回环地址。辅助 SS 服务端也只属于本次临时子进程，不注册系统服务。

| 真实场景 | 最终结果与证据 |
| --- | --- |
| 交互 quick start | 实际 CLI `install` 接收 provider、三入口端口、独立 filter、复杂 SS 链接及 ISP 自定义测试 URL；保存后未启动。核对只生成 listeners、唯一规则 `MATCH,DIRECT`、四个活跃 url-test 组与原始 300 秒默认间隔 |
| 活跃健康检查 | 不沿用旧夹具关闭健康检查的方式。仅把常规外网测速 URL 改为回环；ISP 自定义 `/isp` 单独计数，与显式 `/ready` 探测分离。核对原始默认值后，仅将测试配置中的 ISP 重试间隔缩短为非零的 1 秒，应对 provider 初始化早于首测完成的时序；生产默认值不变。最终确认 `/isp` 自动测速成功，全部健康目标请求合计 13 次 |
| HTTP provider 与过滤 | provider 实际下载 **1** 次并写入唯一缓存。Daily/Download/Relay 各含坏首节点和可用次节点，观察到 3 次坏节点检查；独立 filter 和正确上游连接计数证明健康选点及路由，没有借用 `MATCH,DIRECT` 绕行 |
| SS ISP 前置链 | 主核心 → provider HTTP CONNECT relay → 第二个临时 Mihomo SS AEAD 服务端 → 回环目标。密码 `%2D`、`%3A`、`%40`、字面 `+`、`%2F`、`%252D` 单次解码后完成真实认证；relay 只允许本次 SS/健康目标。关闭 relay 后 ISP 请求失败、目标计数不变，而日常转发继续可用 |
| HTTP/SOCKS/HTTPS | 三个 mixed listener 分别完成 HTTP、SOCKS5 TCP、HTTP CONNECT HTTPS 和 SOCKS HTTPS；业务目标与健康目标采用不同端口/计数。包含后续绑定复验，业务目标共 **32** 次 |
| 类别/单工具/custom | AI→ISP、日常→proxy、单 Codex→download、单 Git→自定义 SOCKS；从临时 `.env`/Git 文件读回真实写入 URL 并转发。确认跨类别不互改、单工具只覆盖自身、空下载类别无伪绑定、`off` 名称可明确选择，移除受管块后原文件字节恢复 |
| 进程与保留 | 核验主核心 PID/UID/boot/birth、程序及全部监听归属；停止后原出生身份退出、端口关闭。卸载只移除核心，配置、provider 缓存、日志等既有组件文件以及外部认证、会话、数据库、Shell/外部 Mihomo 哨兵字节保持 |
| 节点链接矩阵 | 17 项真实核心 `-t` 接受：SS percent/Base64/legacy/2022/obfs/v2ray-plugin，VMess TCP/WS TLS early data，VLESS TLS WS/gRPC/Reality TCP，Trojan WS TLS，Hy2 salamander/pin，HTTP(S)、SOCKS5/H。逐项断言百分号/字面 `+` 字段；只做配置接受，不宣称这些其他协议实际握手或转发成功 |

测试夹具审查修正了两处可能的错误结论：relay 的允许列表必须另含自己的健康目标；ISP 自动 `/isp` 测速不能由显式 `/ready` 流量替代。新入口的 28 分支纯 mock 契约拒绝 namespace 失败、错误架构/非静态产物、缺少 race、旧测试 PASS 冒充、新用例跳过、缺少最终 PASS、子进程报告或报告目录缺失；既有假核心入口明确排除这个真实专项。最终 `make check`（全项目 race/vet/格式/Shell/两套 CLI 隔离）、Go 1.23.12 编译兼容、源码与历史凭据扫描（225 项候选文件）通过。

最终离线 vendor 冻结源码归档 SHA-256 `424da9f1df34e9b2a82c8cd9b321a98e962e503a0fa8e4ce92bb0f46d31fd1a4`，执行后再次逐项核对构建输入与当前源码一致。原生静态 race CLI SHA-256 `38552230cd07cd3e1df2e641893a4479d90273a1489171fab61fc08148f2b08d`，测试程序 `3ed4f53a0c111d1b1393af2a4eaa3f723cd853dddddc9ff06d50e7bb1ea3ba3a`。官方输入元数据、冻结归档、逐文件摘要、构建和完整运行日志保留于本地 `build/proxy-quickstart-real-audit-20261018`，`final` 为最终结果，`attempt-1` 保留加强测速断言之前的首轮通过证据。本次临时服务器/本地源码、SDK、核心及 HOME 定向清理，不改动现有配置、认证、会话和数据。

边界仍为指定核心、Linux amd64、字面 IPv4 回环及 TCP HTTP/SOCKS/HTTPS。未验证真实外部订阅、UDP/QUIC 转发、全部分享链接变体、其他版本或平台；Hy2/Reality 等配置接受不证明可经任意前置节点建立业务链。没有运行真实 Codex/Git 业务或验证其远端服务，只验证受管配置读回后的代理转发与数据保留。本轮静态 CGO 链接器对 `getaddrinfo` 给出系统库提示，实际测试不做 DNS 解析、不挂载系统库并成功执行，不扩称通用静态 CGO DNS 兼容性。

## Quick start 与工具代理配置源码迭代（2026-10-18）

本节记录用户确认设计后的源码与假核心验证阶段，不属于已发布的 v0.4.0 / v0.5.0。该阶段只修改源码、测试和文档，没有修改版本或发行；不切换实际入口，不安装系统服务，不使用真实 Mihomo，也不读取或修改宿主现有配置、认证、会话和数据库。其后的真实新流程复验见上一节；更早各轮真实核心及跨版本结果仍只对应各自历史冻结实现。

新增与调整：

- 安装成功且无配置时直接进行普通可见输入的 quick start；`--yes` 仅安装。完整原生 YAML 预览写 stderr，结构化结果保留独立 stdout。首次引导以独占创建提交，交互期间新出现的配置不会被覆盖。
- 单 provider、各组独立 filter；日常、下载、ISP 前置使用 `url-test` 和常规测试地址，ISP 独立组检测解析后节点的完整出站，允许自定义测试地址。生成器仅启用回环 listeners，所有入口显式指定出站，默认规则唯一为 `MATCH,DIRECT`。
- 纯解析器支持 SS SIP002/旧式 Base64/SS2022、VMess、VLESS、Trojan、Hy2、HTTP(S) 和 SOCKS5(H) 的已实现变体。按组件单次百分号解码、保留字面 `+`，拒绝未知或无法正确映射的参数，无新增第三方依赖。
- listener 名称没有保留契约，快捷配置只枚举 listeners。类别与单工具分别选择，自定义地址始终可用；AI 当前为 Codex，日常为 Git，下载为空。无后台继承、自动启动或端口同步。
- 独立 Git 集成只管理用户级 `[http] proxy` 尾部块。路径位于 Nido 根内、外部已有代理、include、归属不明、并发 XDG 变化均拒绝；收据保存失败时安全补偿。Nido 序列化配置在写入前检查 1 MiB 上限，避免超大代理收据导致下次不可读。
- 显式选择认证 listener 的 `--dry-run` 同样禁止读取凭据，要求使用完整自定义地址；交互选择使用序号或 `listener:名称`，避免 `off`、`keep`、`custom` 或纯数字名称与菜单动作产生歧义。

本机 Go 1.27.2 `make check` 已通过全项目 race、vet、格式与 shell 语法检查、既有 CLI 隔离及新增假核心 CLI 全流程。Go 1.23.12 的 Proxy、Git 集成及 Nido 包普通回归通过。解析器两轮短时 fuzz 合计 87,615 次执行无失败。公开源码及历史凭据扫描通过。

新增 `tests/proxy-quickstart-linux-native.sh` 使用已有 bubblewrap、GCC、原生 SDK 和离线 vendor；构建源码、SDK 只读挂载，HOME/PID/文件系统隔离，网络命名空间共享但夹具仅使用临时回环端口。入口禁止宿主回退，要求指定测试与两份 CLI 夹具实际 PASS；仅允许两项明确记录为未覆盖的 IPv6 环境跳过，其他跳过、失败或缺失结果均拒绝。实际 CLI 只安装接受 `-v/-t`、拒绝启动的假核心，服务管理与下载命令均由禁止调用哨兵守卫。

Linux 首轮在模拟 Runner 的启动测试触发 1 秒期限：原夹具为模拟执行也复制并反复哈希大型 race 测试程序。修复为模拟 Runner 仅使用不可执行的极小原生格式夹具，真实 OS 辅助进程仍使用完整测试程序；没有放宽启动期限。修复后在 J66 普通用户 UID 1001、Linux `6.8.0-124-generic` x86_64、Go 1.27.2/GCC 13.3.0 实际复验通过：

| 本轮 Linux amd64 原生检查 | 实际结果 |
| --- | --- |
| Proxy 全包 race | 153.038 秒通过；原超时的 Probe 测试 0.15 秒通过，16 轮启动授权/exec/停止 85.91 秒通过 |
| 独立 Git 集成 race | 2.197 秒通过，无跳过 |
| Nido 的 Proxy/绑定定向 race | 45.054 秒通过，无跳过；真实核心专项在选择阶段明确排除 |
| 实际 race CLI | 构建元数据确认 Linux amd64、CGO=1、race=true；既有 CLI 隔离与新增 quick start、类别/单工具/自定义配置全流程均通过 |
| 数据保留 | 临时 Codex `.env` 受管块与 Git 受管块可配置、替换及移除；原认证、会话、数据库、外部 Mihomo 配置及 Shell 哨兵字节保留；未调用真实核心、下载器或服务管理器 |
| IPv6 边界 | 仅 `TestLinuxKernelIPv6AndWildcardOwnership/IPv6_loopback` 和 `/IPv6_wildcard` 因 `bind: cannot assign requested address` 未覆盖，不计为 PASS；输出、JSON、环境元数据及 `environment-skips.txt` 均记录，没有其他跳过或失败，不修改宿主网络 |

本轮离线 vendor 源码归档 SHA-256 为 `7926ca6f11033a6e56e3ba22af3511843fe459905624a779f5f4825ce0758ded`；本轮 Proxy 源码、构建依赖与相关夹具在执行后再次核对，与冻结输入一致。原生 race CLI SHA-256 为 `d6e1490a85b73add3cc630043d6a8539f0415c18f4d2821e0f397d0862761023`，只验证配置、拒绝启动的假核心为 `650749ce7906973ca51df8d234336b758aa6d02a6afcdf2c29e11729d5e3aee7`。最终日志、逐文件摘要及冻结归档保存在本地 `build/proxy-quickstart-audit-20261018/final`；首次失败证据另存 `attempt-1`。收尾定向清理本次服务器及本地临时源码、SDK、程序和 HOME，保留构建缓存与上述证据，不涉及任何现有用户数据。

最后修复后，本机再次完整通过 `make check`、Go 1.23.12 三包兼容回归，以及公开源码/历史凭据扫描（221 项候选文件）。该阶段没有新增模块依赖。假核心结果不证明真实协议或业务兼容性；新流程的真实核心专项由上一节单独记录，不能借用下方更早历史结果代替。

## v0.4.0 发行范围（2026-10-18）

独立 Proxy、审计修复和验证入口已纳入 [Nido v0.4.0](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.4.0)，Linux amd64 为服务器验收目标。发行包仍只含 `nido`，不附带或自动安装 Mihomo，不注册服务或切换已有组件入口。下文“未发布”“版本保持 0.3.1”等说明记录对应历史阶段，不代表 v0.4.0 的发行状态。

发行标签精确指向 `b21436368cbbef9f6619bf69f4fe3d863d5b83b2`。[版本提交 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39057418555)、[标签 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39057721425) 及[签名发行工作流](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39057721443) 均通过；Release 为公开稳定版且 `latest` 指向 v0.4.0。此后的验收文档补记不改动该标签或发行程序。

发行后独立复核结果：

| 项目 | 实际结果 |
| --- | --- |
| 公网资产 | 18 项资产白名单无额外项、遗漏或重复；逐项重新经真实 HTTPS 下载，大小与发布 API SHA-256 全部一致 |
| 签名 | 四个平台包、四个摘要文件及安装器共 9 份文件独立 Sigstore 验签通过；身份精确为 `https://github.com/MiracleHYH/CDEToolkit/.github/workflows/release.yml@refs/tags/v0.4.0`，issuer 为 `https://token.actions.githubusercontent.com` |
| 验签工具 | 本次临时使用官方 Cosign v3.0.6，下载前核对官方资产，实际程序的大小及 SHA-256 与官方发布 API 一致；所有 HOME/cache 均为临时路径 |
| 包与来源 | 四平台包结构、Go 主程序身份、平台、CGO=0、版本及摘要均通过 `nido-dist verify`；安装器与 v0.4.0 标签源码逐字节相同 |
| Linux amd64 原生 | J66 的 x86_64 Linux 内核实际运行公网包的 `version/status` 冒烟及完整 CLI 隔离夹具，均通过；程序报告 `0.4.0`，不以交叉编译替代原生执行 |
| 公网安装 | 原生环境中的临时 HOME 真实完成 latest 首装、重复安装和固定版本/带空格自定义目录安装；每次下载的包与摘要先匹配已验签输入，已装程序 SHA 再与包内程序比对；生成的 Nido 配置在重复安装后保持原字节 |
| Proxy 与入口 | 新版帮助、status/config-path、安装/更新查询/卸载/停止的 dry-run 及空组件错误反例通过；这些检查没有再下载资产或创建 Proxy 组件目录。Bash 子 Shell 显式加载环境及重复 PATH 加载通过，未切换实际入口 |
| 数据边界 | 临时认证、会话、数据库、配置、外部 Mihomo 配置及 Shell 哨兵逐字节保留；未调用真实组件或系统服务。现有 Zsh 缺失，明确跳过该原生专项，未安装工具，也不将其写成通过 |

Linux amd64 公网包大小为 **3,796,438 字节**，SHA-256 为 `6b9d5bcd519261679e292a599a3bbde04a7de1e81770c8ff22f39514d2d40f45`；包内 CLI SHA-256 为 `84c7222d8670ccf76540fca86f1eed4f22fa887611477744e0e497514500a407`。安装器为 **4,267 字节**，SHA-256 为 `c5e81a327c49bee086253cef800486c5b8e1eab9257732b688bc9e35e61c65c6`。下载器自身自动校验 SHA-256，不自动验证 Sigstore；上述签名是本次另行独立核验的结果。

原生运行使用已有 bubblewrap，私有文件系统、PID、HOME/TMP；仅本次临时目录可写，系统工具/库/证书只读，不挂载宿主用户 HOME。服务器直连 GitHub TLS 预检失败后，使用本次专用、仅绑定回环的临时 SSH SOCKS 转发访问真实公网 HTTPS；没有复用用户代理、修改服务器代理或降低 TLS 校验。安装测试不 mock 公网内容；CLI 组合夹具中的外部 Codex 安装行为仍为本地假命令。隔离环境首次因缺少 `awk` 的系统链接失败，补齐只读 `/etc/alternatives` 映射后完整通过，未修改系统文件。此轮未重跑真实 Mihomo，真实转发与跨版本证据仍分别对应下方冻结源码验证；不把公网 CLI 安装验收扩称真实核心验证。

本次原生 namespace、临时 HOME、下载、验签工具和辅助脚本已定向清理，临时 SSH 转发已关闭并复查回环端口无监听；正常发行构建产物保留。没有切换实际入口、安装系统服务或修改现有配置、认证、会话和数据。

## 源码提交与 GitHub CI 验收（2026-10-18）

Proxy 实现、审计修复、隔离测试及文档已整理为源码提交 [023afc4](https://github.com/MiracleHYH/CDEToolkit/commit/023afc47ecf90d7d2335ad7e81fcff296538493b) 并推送到 `main`。[该提交的 GitHub Actions](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39056219772) 全部必跑任务通过：Linux amd64 的 Go 1.23.x、Go 1.27.2，现有 Nido macOS 兼容检查，以及源码与历史凭据扫描；可选 GDS 按设计跳过，无需追加 CI 修复。Linux 两套工具链均实际执行 `make check smoke`，覆盖全项目 race、CLI 隔离及本机包校验，补齐当前生产实现的 Linux amd64 全项目 race 证据。

普通 CI 未提供官方 Mihomo 输入，真实核心及跨版本测试按需跳过，不能把该 CI 结果当成新一轮真实核心验证。下方冻结源码的 Linux amd64 真实 CLI race、v1.19.31 → v1.19.32 更新和数据保留证据仍单独保留；源码提交及本节记录未改变该生产实现。版本保持 `0.3.1`，没有推送标签、创建 Release、切换入口、安装系统服务或改动现有配置、认证、会话和数据。

## 跨版本验证后的最终发布审计（2026-10-18）

后续用户明确服务器只采用 **Linux amd64**，因此本次 Proxy 发布验证及阻塞项以该平台为准。macOS 和 Linux arm64 的历史结果继续保留，缺口不作为该部署目标的发布阻塞项，也不作四平台同等验证声明。macOS arm64 补验在真实核心下载、执行前按用户要求取消；只完成了临时 race 构建、假 Runner/报告链回归及发布元数据读取，均不计作真实核心复验。相关临时源码、程序与元数据已清理，没有启动真实 Mihomo。Nido 本身的多平台构建目标不因本次部署范围调整而改变。

本节是当前结论；后文按轮次保留当时的修复与证据。重新审计配置与端点、进程归属、启动授权、停机更新、卸载及 Nido/Codex 组合，并复核最新双架构跨版本夹具、隔离入口和发行工作流。按文档约定的受信核心、受支持回环代理及固定组件目录范围，未发现新的确定生产缺陷，未留下已确认而未修复的源码阻塞项。本轮只补隔离回归和文档，没有改生产源码、模块依赖或组件职责；下方最新 Linux 原生核心证据仍对应当前生产实现。

| 复核项目 | 结论与限制 |
| --- | --- |
| 配置与端点 | YAML、端口、回环地址、认证继承、provider 路径、保留资源、端点歧义和敏感信息处理与策略一致；原生未知节点扩展继续由核心解释 |
| 归属与启动握手 | 先保存 PID/UID/boot/birth 和启动器身份，再授权 exec；EOF、提交失败、身份或记录切换不能放行；逐端点及提交前检查就绪期限 |
| 停机更新 | 运行或身份未知时拒绝；候选版本、格式、配置、原程序、目录及锁在长操作后和提交前复核；成功仍停止，不调用核心热更新或清理未知目录 |
| 卸载与数据 | 不执行核心，只移除快照仍一致的固定普通原生程序；保留配置、记录、日志、缓存及未知数据；新增同字节、同权限但不同 inode 的更新/配置/卸载反例 |
| 组合与独立性 | Proxy 不依赖 Nido/Codex；只读与预览不执行核心；Codex builtin 写入前后复核所选端点，变化时仅条件补偿本次代理块 |
| 验证入口 | 默认跨版本跳过不影响单核心基线；显式提供新核心则必须独立 PASS；新增纯 mock 回归，拒绝跳过、缺失结果及子命令失败造成的假绿，不连接真实 Docker 或执行真实核心 |

累计平台证据汇总如下。交叉编译、辅助进程测试、真实核心及 race 分开判断，不能互相代替；本轮没有重新运行真实 Mihomo。

| 平台 | 原生证据 | 真实核心与跨版本 | 尚缺证据 |
| --- | --- | --- | --- |
| Linux amd64 | 最新同一冻结源码，Go 1.23.12 原生静态 race CLI、Proxy 全包、七项 TestOS、pidfd、IPv6 辅助及 16 轮启停均通过，无跳过 | v1.19.31 完整基线、v1.19.32 修复后基线及 v1.19.31 → v1.19.32 停机更新均通过；新旧核心实际版本、缓存复用、HTTP/SOCKS/HTTPS、启停和卸载保留已核对 | 当前生产实现的全项目 race 已由上述 GitHub 原生 CI 补齐；普通 CI 不会自动重跑真实核心 |
| Linux arm64 | 最新与 amd64 同一冻结源码，Go 1.23.12 原生静态 race CLI、Proxy 全包、七项 TestOS、pidfd、IPv6 辅助及 16 轮启停均通过，无跳过 | 与 amd64 相同的基线及指定跨版本场景均通过 | 未完成原生全项目 race |
| macOS arm64 | 本轮全 Proxy OS 辅助进程 race 24.588 秒通过，七项 TestOS 实际通过；宿主全项目 race 和临时 HOME CLI 隔离通过 | 较早轮次 v1.19.32 真实核心普通测试及父测试 race 通过，当时内部 CLI 为普通构建 | 最终生产修复后的真实核心复验、真实 CLI 全链路 race 及真实跨版本均未完成 |
| macOS amd64 | Go 1.23.12 交叉编译通过 | 没有原生核心执行结果 | 原生 OS、真实核心、race 及跨版本均缺 |

最新 Linux 跨版本两端各完成 HTTP provider **1** 次、目标与受限上游 CONNECT 各 **16** 次，旧、新版本各 8 次目标请求；同入口旧版完整基线为 **3 / 20 / 20**。跨版本实际 E2E 耗时 amd64 **178.94 秒**、arm64 **38.56 秒**。输入来源、冻结归档、产物摘要及详细顺序见下一节，不将同核替换误报为跨版本。

发布阻塞项和证据边界应分别处理：

| 类别 | 当前判断 |
| --- | --- |
| 已确认源码缺陷 | 当前没有未修复项；这是已审计范围内的结论，不是任意核心或配置的安全保证 |
| Linux amd64 部署目标 | 已有最终生产实现的原生 Proxy 全包和真实 CLI E2E race，以及 v1.19.31 → v1.19.32 停机更新证据；配置、进程归属、启停、provider、HTTP/SOCKS/HTTPS 及更新/卸载保留均通过。当前全项目 race 另由上述 GitHub CI 补齐，真实核心内部 race 仍不在范围内 |
| 其他平台及四平台声明 | 不属于本次服务器目标，不阻塞 Linux amd64。macOS amd64 缺原生证据、macOS arm64 缺最终真实核心及 CLI race/跨版本证据，因此仍不作四平台同等验证声明 |
| 本次代码的实际发行 | 本次仅整理源码提交、推送及 GitHub CI 验收，`VERSION` 保持 `0.3.1`，不创建标签或发布。源码提交与 CI 通过不代表本次 Proxy 已进入已有 v0.3.1；后续发行仍须选择新的版本与精确标签，满足干净工作树及现有发行校验，不能复用旧版本表示本次实现 |
| 候选包与线上交付 | 本次变更尚无精确候选发行包、签名及公网安装证据；已有发行工具测试不等于本次资产已交付。以后实际发行仍需对选定提交完成现有发行检查；本轮按约束不执行该流程 |
| 自动化证据 | 现有 CI 的普通 `make check smoke` 不提供官方 Mihomo 输入，因此不会重做原生真实核心或跨版本验证；预构建输入的元数据/符号检查也不证明来自当前源码。最新手动原生验证通过冻结归档与摘要建立对应，未来源码变化须重新判断覆盖范围 |

以下是明确保留的实现及验证边界，不应描述成已消除的问题：

- 真实业务转发仅覆盖 IPv4 回环的 HTTP、SOCKS5 TCP 和 HTTPS；Linux IPv6 证据来自内核辅助测试，macOS 本轮包含地址族不符的拒绝反例。UDP、其他节点协议、外部订阅、任意版本或降级、全部核心数据库迁移均未由这些结果证明。官方 Mihomo 保持发行字节，没有启用其内部 race；来源摘要不等于签名。
- 静态配置和原生格式检查不是 OS 沙箱。未知 outbound、动态 provider 节点、运行时加载及系统库搜索仍依赖受信核心和系统。`-t` 不加载 provider，可能访问普通缓存；监听归属通过不代表 controller、DNS、provider 或上游可用。
- Linux 已测试 pidfd，旧内核 fallback 与 macOS 仍有核验到信号的窗口；Probe 核验不是 socket 租约；文件最后检查与 rename/remove 没有跨系统 CAS 或掉电目录事务。启动就绪期限不等于整个命令的墙钟硬上限。
- 卸载无安装来源清单，只在明确选定的固定路径操作；数据保留实测主要比较已有文件字节，不扩称权限、所有者或新增文件集合不变。核心日志可能含敏感信息，跨组件代理块与归属记录使用条件补偿，非跨文件原子事务。

本轮最终联合验证全部通过：

- Go 1.27.2 全项目 `go test -race -mod=readonly -count=1 ./...`，Proxy 12.353 秒、Nido 13.746 秒；Go 1.23.12 全项目普通测试，Proxy 5.850 秒、Nido 10.399 秒。真实核心、更新核心、预建 CLI 和报告目录的宿主输入均显式移除。
- 单独允许内核只读接口后，macOS arm64 全 Proxy race 24.588 秒通过，七项 TestOS 全部实际执行，没有内核权限跳过；Linux 专有重复 exec 测试仍按平台跳过。此项只运行 Go 辅助程序，不运行真实核心。
- 三个同字节换 inode 反例通过；两个 Linux 入口的 23 个纯 mock 场景已固化在 `internal/nido/proxy_linux_entry_test.go`，随全项目测试通过，并分别用两套工具链定向验证。它们验证判定与清理，不增加新的 Linux 原生或真实核心证据。
- Go 1.23.12 临时构建 Linux/macOS × amd64/arm64 四平台程序通过；临时 macOS arm64 CLI 的 `tests/nido-cli-isolation.sh` 通过。构建产物定向清理，未生成候选发行包或更换实际入口。
- `go vet`、gofmt、Shell 语法、源码公开检查及源代码/历史 gitleaks 扫描、`git diff --check` 通过。公开检查报告 150 项候选文件，未发现凭据泄漏。

所有新增隔离测试只使用临时文件、假命令/Runner 和回环辅助进程；未发布、切换实际入口、安装系统服务或运行真实 Mihomo，未改动现有配置、认证、会话和数据。

## Linux 双架构真实跨版本停机更新（2026-10-18）

按后续授权，新增独立的 `TestProxyRealMihomoCrossVersion`，在 Linux amd64/arm64 原生环境实际完成官方稳定版 [v1.19.31](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.31) 到 [v1.19.32](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.32) 的停机更新。没有发现需要修复的生产缺陷；本轮只补测试、可选隔离入口及文档，没有增加生产依赖、服务管理器或组件耦合。

源码归档 SHA-256 为 `fa73416576d953ebb407ba4a578e5c31cee3f250627cc75b0e5fc57b550b6505`，含离线 vendor 的构建输入为 `e8c2f8eaed4e9d14fcd19fef43283125501f5f85743d5f4c14bf7a96b4337fc7`。两端接收同一份已核验归档，使用原生 Go 1.23.12/GCC 构建 CLI 和两份测试程序，均启用 CGO、静态链接及 race。amd64 使用 J66、UID 1001、GCC 13.3.0、内核 `6.8.0-124-generic` 与已有 bubblewrap；arm64 使用现有 OrbStack、UID 65532、GCC 12.2.0、内核 `7.2.9-orbstack-00391-g2b298d05fbd4`，官方 builder manifest/层重新核验，静态 race 初始化探针通过。工具和隔离细节沿用下文对应原生验证方式。

| 本次原生 race 产物 | SHA-256 |
| --- | --- |
| amd64 `nido` | `2d96eb511da8d48f166874df54eb06dfaf60d4a99353e9d20fabacc170e94f65` |
| amd64 `nido.test` | `d9f96e1bb251565ba4ca439743e10cd13e36075a79c234c63e1c9fdc4542f293` |
| amd64 `proxy.test` | `d4905452f070448a9dc33eb64feed75035426445975d9a8ac67349c68a896911` |
| arm64 `nido` | `28729363506fec832c267b7f5b54262c2fc8b5ddef6450763ebb112316efec10` |
| arm64 `nido.test` | `7010c26b7cf86eb29b8f1f2ca0834aab1847ca82e3f993b0c88eb3459ea1b453` |
| arm64 `proxy.test` | `ff7129c5fedc47325ec0f9d1e042999cfa7844bbe77ecec9cbd3ac1a61ccd14f` |

两个 Linux 入口新增可选 `NIDO_TEST_MIHOMO_UPDATE`。默认仍是原四输入；显式启用后，只额外复制一份独立核验、同架构的静态核心到 `/inputs/mihomo-update`，核心与测试输入共五份程序，不增加宿主目录挂载、库、编译器或外部网络。明确要求跨版本测试独立 PASS，跳过或旧预建测试忽略新变量都会使入口失败。两个真实 E2E 的总时间预算为 race 20 分钟、普通 6 分钟，原有单核心及 Proxy 全包预算不变；14 个纯 mock 正反入口检查与 shell 语法检查通过。

本次四个官方资产都重新下载并与发布 API 的名称、地址、大小和摘要核对；下列为实际输入的 SHA-256，来源和摘要验证不等于签名验证。

| 官方核心 | gzip SHA-256 | 展开程序 SHA-256 |
| --- | --- | --- |
| amd64-compatible v1.19.31 | `04cf9f09671704f839ddbee2e93069dc831a4123a75281e725d1d96ab9ac1afc` | `b341a765412c192685264e038a6aad2ac1c67c12b8aceeb5c6f64955cf43f5ed` |
| amd64-compatible v1.19.32 | `ba3ce607747a07f948fc35780e108a4a7c7f552a38b9bd4d115f313ebcb89c20` | `7a0d59da2e678d56c899a3db996a2ad8963286c4f3634b0451435db248f13fa1` |
| arm64 v1.19.31 | `9e0f11afbf38426b8bd88fdc594678f8161c57eccb4e1b77acb12b493904f1d4` | `1b315bc038d05f84ee86d232f3c3d2b020b5044e9b971bb8fe215b6e6a2148f3` |
| arm64 v1.19.32 | `9dd862e28b46ff7d775f169cceebc28deccaa0a9e804237d421cd2571e0caba0` | `c80df2ca5159567804da883401e0118908fc1155e05df2baf3dbd4d3ecfc3754` |

| 跨版本实际结果 | Linux amd64 | Linux arm64 |
| --- | --- | --- |
| Proxy 全包原生 race、全部七项 TestOS、IPv6/pidfd/故意竞争负例 | 通过，无跳过 | 通过，无跳过 |
| 16 轮启动/exec/停止 | 84.94 秒通过 | 37.89 秒通过 |
| 独立跨版本真实 E2E | 178.94 秒通过 | 38.56 秒通过 |
| 同入口旧版 v1.19.31 完整基线 E2E | 232.04 秒通过，provider/目标/CONNECT 为 3/20/20 | 52.25 秒通过，同为 3/20/20 |
| 实际版本及字节 | v1.19.31 → v1.19.32；已安装程序 SHA 与目标一致 | 同样验证通过 |
| 跨版本业务计数 | HTTP provider 1 次，目标请求 16 次，上游 CONNECT 16 次 | 同为 1 / 16 / 16 |

跨版本流程按以下顺序验证：

1. 旧核心运行 HTTP provider，下载唯一节点缓存，实际完成 HTTP/SOCKS，以及通过 HTTP CONNECT 和 SOCKS 两条代理路径访问同一个 HTTPS 回环目标；停止并确认原出生身份退出、端口关闭。
2. 在更新前，将配置改为引用该缓存的 file provider，并在旧核心上验证、实际运行和转发，再停止。此后更新前后的主配置字节完全相同；HTTP provider 已拒绝请求。
3. 捕获原配置、provider 缓存、日志、进程记录及未知文件字节；停机更新后，确认安装了目标程序、实际报告不同且升序的完整稳定版本，除程序和操作锁之外的既有文件字节保持，状态仍 stopped，旧身份已退出且监听未自动开启。
4. 新核心验证同一配置，启动后按 PID/UID/boot/birth、程序路径及监听归属核对新实例；幂等 start 保持身份，restart 更换身份，HTTP/SOCKS/HTTPS 实际转发均经过受限回环上游。跨版本流程共 16 次目标请求，旧、新版本各 8 次。
5. 整个 file provider 阶段的 HTTP 下载计数保持不变，旧缓存、配置和未知哨兵最终保持原字节。停止、确认端口关闭并卸载后，再比较既有文件和临时 HOME 的认证/会话模拟哨兵。

不同 SHA 不足以算作跨版本：夹具另检查实际完整稳定版本且要求升序；同版本、降级、异常版本后缀由独立回归拒绝。更新前全文件快照只跨停机 update 比较；正常运行允许日志、进程记录和核心数据库变化，运行后只继续约束配置、provider 文件及哨兵。文件字节一致也不扩称目录、权限、所有者或新增文件集合完全不变。

本轮仅证明该相邻稳定版本对、这份原生配置及旧 HTTP 缓存供 file provider 复用的兼容性，不承诺任意版本、降级、其他 provider 格式或全部核心内部状态迁移。官方 Mihomo 未启用 race，本次 Go race 覆盖 Nido/Proxy/测试/启动器路径；真实转发仍为 IPv4 回环，IPv6 证据来自内核辅助测试。所有运行只在临时 HOME 与私有回环 namespace，未安装系统服务、发布、切换实际入口或改动现有配置、认证、会话和数据。

两个完整入口均退出 0，无 OS/IPv6 跳过或非预期 race。Go 1.27.2 全项目 race、vet、gofmt/shell 语法与 Go 1.23.12 全项目普通回归通过；新夹具的同版/降级/异常 token 防伪回归通过。复验前后逐项比较源码、测试、模块文件与两个入口，均与冻结输入一致。本次下载、SDK、构建器、五输入镜像/namespace、远端工作目录和宿主中转已定向清理，无本次进程或容器残留；未执行全局 prune。源码公开检查与 `git diff --check` 通过。

## 最终修复后的 Linux 双架构复验（2026-10-18）

按后续授权，对发布前最终修复后的同一份源码重新完成 Linux amd64/arm64 原生 race 和真实 Mihomo 隔离复验。没有使用上一轮的预构建产物，未发现需要继续修复的新缺陷；本轮没有改动生产源码或测试逻辑。

冻结输入只含 `cmd`、`internal`、模块文件、`.go-version`、`VERSION`、`Makefile` 和两个 Linux 入口；vendor 使用宿主 Go 1.23.12 从已有缓存离线导出，不含用户配置或数据。源码归档 SHA-256 为 `b8c8b70f4f5339808b060479aaea5b71a6d5f2ed1d1c5c55beb8cc693246d91c`，含 vendor 的构建输入为 `b41c61738582fafb30232824c72f252b70d63f655d474c11ac131de3a6cba158`。两端均核对输入摘要；运行前后对照当前源码与归档，生产代码、测试和入口保持一致。

| 原生环境 | 构建与实际执行 |
| --- | --- |
| Linux amd64 | J66 `x86_64`，内核 `6.8.0-124-generic`；Go 1.23.12、GCC 13.3.0、UID 1001。临时 SDK 摘要核验后，使用已有 bubblewrap、只读工具/SDK/源码和临时 HOME/cache/output 离线构建；最终通过 `tests/proxy-linux-bwrap.sh` 执行 |
| Linux arm64 | 现有 OrbStack `aarch64`，内核 `7.2.9-orbstack-00391-g2b298d05fbd4`；Go 1.23.12、GCC 12.2.0、UID 65532。重新核验下文记录的官方 Go builder index、arm64 manifest、config 和全部层；非 root、禁网、只读根和无宿主挂载的临时构建器从 stdin 接收输入；最终通过 `make check-proxy-linux-race` 执行 |

两端的 Nido CLI、`nido.test` 和 `proxy.test` 都重新构建为 `CGO_ENABLED=1` 的原生静态 race 程序。入口核验 CLI 构建元数据与测试包/race/TSAN 符号；arm64 的静态 race 初始化探针也实际通过。Docker 入口显示的宿主 Go 1.27.2 只读取元数据，实际 Linux 编译和执行使用 Go 1.23.12。

| 原生 race 产物 | SHA-256 |
| --- | --- |
| amd64 `nido` | `14ff241472db6d32b15c1f65fd68ec2f4acf5ccb18fc6f40c8d719580ca33d55` |
| amd64 `nido.test` | `0e28c9c64882cddb1cbf1ec1598f9f18f27883d84949657812308221f9bf4396` |
| amd64 `proxy.test` | `1e36d6c51a837f965cf428b659951409a7244fb5733449996af793690fc3f893` |
| arm64 `nido` | `28729363506fec832c267b7f5b54262c2fc8b5ddef6450763ebb112316efec10` |
| arm64 `nido.test` | `f96ee7d4edd367e2685e8da2829012f531943272536bcfbcde7d6b03bfafc89b` |
| arm64 `proxy.test` | `ff7129c5fedc47325ec0f9d1e042999cfa7844bbe77ecec9cbd3ac1a61ccd14f` |

运行阶段只向新文件系统输入上述三份程序和官方 Mihomo，不加入源码、SDK、编译器、宿主 HOME、动态加载器或独立 libc 共享库；使用私有 PID/网络空间、临时 HOME/TMP 和回环夹具。未请求跨架构模拟，未修改内核、ASLR、seccomp 或 Docker 配置。真实核心均重新下载到临时目录：`mihomo-linux-amd64-compatible-v1.19.32.gz` 与 `mihomo-linux-arm64-v1.19.32.gz`，gzip/展开摘要与下文对应记录一致；amd64 另外重核官方 release API 的资产摘要及 22,877,876 字节大小。来源及摘要核验仍不等于签名验证。

| 实际结果 | Linux amd64 | Linux arm64 |
| --- | --- | --- |
| Proxy 全包原生 race | 通过 | 通过 |
| 内核身份与监听 | 全部七个 `TestOS`、pidfd、IPv6 回环与 IPv4/IPv6 通配反例、出生身份篡改及程序替换检查通过，无跳过 | 同范围通过，无跳过 |
| 16 轮授权启动/exec/停止 | 85.11 秒通过 | 38.21 秒通过 |
| 最终修复回归 | Probe 记录/配置/程序/监听变化、逾期就绪、完整版本 token、ELF/Mach-O 加载元数据及系统依赖正反例全部通过 | 同范围全部通过 |
| 真实 CLI/Mihomo E2E | 237.58 秒通过 | 53.74 秒通过 |
| 实际业务计数 | provider HTTP 3 次、目标请求 20 次、受限上游 CONNECT 20 次 | 同为 3 / 20 / 20 |
| 子进程 race | Proxy 包故意竞争负例检出、报告收集检查通过；无非预期报告 | 同范围通过；无非预期报告 |

amd64 另外在同样四输入的 bubblewrap 隔离中单独运行 Nido 包的 `TestIsolatedRaceDetectorCapturesChild`，0.04 秒通过；其子进程退出 66、真实报告被收集器拒绝的断言均通过。arm64 未额外运行这一重复收集器负例，不能将该项写成双架构专项；两端全 Proxy 包各自的故意竞争负例均已通过。

真实 E2E 在两端均覆盖原生 listener、传统 mixed-port、独立 HTTP/SOCKS 端点，HTTP/file/inline provider，HTTP 与 SOCKS5 TCP、HTTPS CONNECT 及 SOCKS 上 HTTPS 转发。file provider 复用已下载缓存，不再请求 HTTP；不可用订阅拒绝业务直连回退。真实安装、更新与卸载均通过最终格式检查；安装与更新实际执行版本解析，卸载不执行核心。

进程记录与内核 PID/UID/boot/birth 逐项对应；重复 start 保持出生身份，无效配置 restart 保留旧实例及转发，有效 restart 更换出生身份并确认旧实例退出；重复 stop 后监听关闭。停机 update 与 remove 逐文件比较原有配置、缓存、日志、进程记录和未知文件的字节，临时 HOME 的认证/会话模拟哨兵也保持一致。该断言不扩展为目录、权限、所有者及新增文件集合完全不变的证明。

本轮补齐了最终补丁后的 Linux 双架构 Proxy 全包与真实 CLI E2E race 证据，不扩称两架构全项目 race。官方 Mihomo 保持发布字节，未对其内部代码启用 race；真实业务转发使用 IPv4 回环，IPv6 结论来自内核辅助测试。该轮使用同一份核心验证停机替换与既有数据保留，后续指定版本对的跨版本兼容验证见上方新节，不能扩称任意状态迁移。Probe 的竞争切换由同轮全包隔离回归覆盖，未在真实核心上制造切换竞争。UDP、真实外部订阅、其他节点协议、旧内核无 pidfd、macOS amd64 原生和 macOS 真实 CLI 全链路 race 等边界仍保留。

两端入口、临时构建器、下载、SDK、race 产物和工作目录均已定向清理；远端复查本次 bubblewrap 与 `/inputs` 进程无残留，Docker 本次 label、唯一 tag 和容器无残留。仅删除本次归属已核验的对象，没有全局 prune、安装系统服务、发布或切换实际入口；没有修改现有配置、认证、会话和数据。源码公开检查与 `git diff --check` 通过。

## 发布前最终源码审计（复验前，2026-10-18）

重新审计当前工作树的配置、端点、下载与格式检查、进程归属、启动授权、停机更新、卸载及 Nido/Codex 组合层。本次只修改源码和文档，使用临时文件、假 Runner、本地 HTTP 夹具及回环辅助进程；没有运行真实 Mihomo 或真实服务。保留独立组件和现有依赖，没有新增 daemon、服务注册、安装清单、版本库或通用事务框架。

本次确认并修复的问题：

| 问题 | 最终处理与回归 |
| --- | --- |
| `probe` 只在请求前观察，实例或记录切换后仍可能报告成功 | 绑定同一记录快照，请求前后核验配置、程序、内核身份和全部代理监听；覆盖同字节记录替换，以及请求期间记录、配置、程序和归属变化 |
| 慢监听检查跨过启动期限后，仍可能提交 running | 逐端点、第二次观察及提交前核验 deadline；首次与第二次延迟就绪均拒绝成功，并仅停止本次出生身份 |
| ELF 内嵌 audit/filter、Mach-O 内嵌 dyld 环境可绕过已有依赖策略 | 拒绝 `DT_AUDIT`、`DT_DEPAUDIT`、`DT_FILTER`、`DT_AUXILIARY` 和 `LC_DYLD_ENVIRONMENT`；纯字节格式测试及假安装/更新/卸载验证不执行、不提交、不删除既有文件 |
| ELF 节表与加载器使用的动态表、字符串表可以指向不同字节 | 要求唯一动态段与动态节、字符串表地址和长度及 PT_LOAD 映射一致；覆盖伪装表、重复表、越界索引，保留静态和系统依赖正例 |
| 恰好为 `/usr/lib`、`/lib` 等系统库目录的 RPATH 被误拒绝 | 允许规范系统目录本身及子路径，继续拒绝外部目录与非规范路径 |
| 版本正则把异常后缀截断成稳定版本，误通过官方版本一致性检查 | 匹配完整空白分隔 token；`v1.2.4-`、`v1.2.4+`、`v1.2.4.`、`v1.2.4/other` 均不能被当作 `v1.2.4` 提交 |

加载器行为核对了 [glibc 主程序 audit 支持](https://sourceware.org/pipermail/libc-alpha/2020-April/112491.html)、[glibc filter 依赖加载](https://github.com/bminor/glibc/blob/master/elf/dl-deps.c)、[Apple dyld 配置解析](https://github.com/apple-oss-distributions/dyld/blob/main/dyld/DyldProcessConfig.cpp)和 [Mach-O 命令定义](https://github.com/apple-oss-distributions/xnu/blob/main/EXTERNAL_HEADERS/mach-o/loader.h)。这些格式测试不运行合成的可执行文件。新缺陷均先复现再修复；probe 另通过临时 Go overlay 去除响应后复核，确认四类变更回归能够检出原来的误报成功。

最终复核结果：

| 范围 | 当前结论 |
| --- | --- |
| 配置与端点 | 对照 Mihomo v1.19.32 的配置、listener、认证与 provider 解码；传统端口、原生 listeners、IPv4/IPv6、认证继承、字段别名、前导零、显式 provider 路径及保留资源检查未发现另一项确定缺陷 |
| 进程归属与启动握手 | PID/UID/boot/birth、映像替换、授权前记录、EOF 撤销、exec/exit 暂态、精确监听及失败清理保持闭合；超时不再采纳迟到的就绪观察 |
| 停机更新 | 初始停机检查后及长操作、每次核心执行、最终提交前复核原锁、目录、程序、配置和候选；运行或身份未知时拒绝更新；不调用 controller 更新、不清理未知目录 |
| 卸载与数据 | 只删除已停止且快照未变、格式及权限符合要求的固定程序；普通文档、链接、共享硬链接、特殊文件和本次新增的加载器反例均被拒绝；保留配置、记录、日志、缓存、未知文件及认证/会话模拟哨兵 |
| 组合与独立性 | Proxy 不导入 Nido 或 Codex；只读查询和 dry-run 不启动核心；Codex builtin 绑定重核端点摘要，补偿限于本次代理块，未引入生命周期依赖 |

严格 deadline 修复后，并行 race 暴露普通测试夹具原有 70 毫秒预算过紧。普通就绪夹具改为 1 秒，仅明确超时专项保留 70 毫秒，新延迟观察反证保留 50 毫秒；没有放宽生产期限。

最终源码的本轮验证均通过：

- Go 1.27.2 全项目 `go test -race -mod=readonly -count=1 ./...`，Proxy 12.089 秒；`go vet`、gofmt 和 shell 语法检查通过。
- Go 1.23.12 全项目普通测试，Proxy 5.803 秒；最低版本工具链构建 Linux/macOS 的 amd64/arm64 四平台程序通过。产物仅位于临时目录，macOS arm64 CLI 的临时 HOME 隔离验证通过。
- 获准读取内核接口后，macOS arm64 的 Proxy 全包 race 25.340 秒通过，全部七个 `TestOS` 实际执行，没有 OS 或 IPv6 权限跳过；Linux 专有的重复 exec 测试在 macOS 按平台跳过，不能算作 Linux 运行证据。
- 常规测试显式移除真实核心及预建 CLI 的输入；本轮没有重跑真实核心。格式、源码公开检查和 `git diff --check` 通过。

各平台累计证据如下。真实核心均为官方 Mihomo v1.19.32，具体来源摘要、环境及轮次详见后文。

| 平台 | 此前实际原生验证 | 本次最终工作树验证与缺口 |
| --- | --- | --- |
| macOS arm64 | 真实核心普通及父测试 race 通过，provider 3 次、目标与上游 CONNECT 各 12 次；当时内部构建的真实 CLI 不是 race | 本次全项目 race、全 Proxy OS 辅助进程 race 与 CLI 隔离通过；未重跑真实核心，也未补真实 CLI 全链路 race |
| macOS amd64 | 无原生核心或 OS 运行证据 | Go 1.23.12 交叉编译通过；不能据此认定原生兼容 |
| Linux amd64 | J66 原生 Go 1.23.12 全项目 race；Proxy 普通/静态 race、全部 TestOS 和真实 CLI/Mihomo E2E 通过，无 OS/IPv6 跳过；每轮 provider 3 次、目标与 CONNECT 各 20 次 | 源码审计阶段交叉编译通过；后续最终补丁原生 Proxy/真实核心 race 已补齐，见上方复验 |
| Linux arm64 | OrbStack 原生普通双工具链验证及 Go 1.23.12 静态 race 的 Proxy 全包、全部 TestOS、真实 CLI/Mihomo E2E 通过，无 OS/IPv6 跳过；race E2E 54.17 秒，provider 3 次、目标与 CONNECT 各 20 次 | 源码审计阶段交叉编译通过；后续最终补丁原生 Proxy/真实核心 race 已补齐，见上方复验；未跑 arm64 全项目 race |

按当前文档契约，本次未留下已确认但未修复的源码缺陷；以下仍是发布判断必须保留的边界：

- 历史结果只对应各轮当时的源码。最终补丁后的 Linux 双架构 Proxy 与真实核心 race 已在上方后续复验中补齐；macOS amd64 原生、macOS 真实 CLI 全链路 race、最终补丁的 Linux 全项目 race 仍缺证据。源码审计阶段没有运行真实服务，后续真实核心也仅在临时 HOME 与私有回环 namespace 中运行。
- UDP、真实外部订阅、其他节点协议、除前节指定版本对以外的跨版本升级和旧内核无 pidfd 分支未完整实测；官方 Mihomo 内部没有重新编译启用 race，发布摘要核验也不是签名核验。
- 配置和格式检查不构成 OS 沙箱。未知 outbound、动态 provider 资源、运行时加载、系统动态库搜索仍由核心或系统管理；`-t` 也可能刷新普通缓存，不提供失败时缓存零写入保证。启动就绪只核验导出的代理 TCP listeners，不证明 controller、DNS、provider 或上游全部可用。
- macOS 和无 pidfd 的 Linux 保留身份检查到发信号的窗口；探测检查到连接没有 socket 租约；文件最后检查到 rename/remove 之间也没有跨系统通用 CAS 保证。同用户非合作修改、无可观察时间戳变化的瞬时恢复和掉电后的目录持久性不在事务保证内。启动期限限制就绪授权，正在执行的 OS 查询及安全清理可延长返回耗时。
- 卸载不记录安装清单，不能证明任意合法原生程序曾由 Nido 安装；只在用户明确选择的固定程序路径内操作。跨组件代理块与归属记录采用条件补偿，并非跨文件原子事务。日志保留核心原文，仍可能包含敏感信息。

本次构建与测试只使用临时产物、临时 HOME 和回环端口，没有发布、切换实际入口、安装或启动真实服务，也没有修改现有配置、认证、会话和数据。

## 历史与范围

已阅读 Dev 聊天中的相关决策：Nido 采用单目录；Codex 是外部辅助集成；Proxy 和可选 GDS 是内置组件；此前 Nido 与 Codex 审计明确排除了 Proxy 内部适配。更早的双目录、迁入、挂载恢复和版本仓库方案已经被后续单目录方案取代。

基线的有效入口是 `internal/nido` 与 `internal/builtin`；后者的 Proxy 适配已由本轮独立组件替代。`proxy/pm.sh`、安装脚本、Python 工具和公开 YAML 属于冻结的旧部署，不能作为当前 Go CLI 已提供的功能。旧 `proxy/README.md` 中的 server-toolkit、stk、pm、tmux 与 supervisord 用法也不能视为 Nido 使用说明。

审查覆盖 Go 的下载校验、程序安装与更新、配置识别、进程启停、状态查询、CLI 与 Codex builtin 代理衔接；对照了 Mihomo 官方源码，其中更新路径另以官方稳定发布 `v1.19.32` 核验。隔离复现只使用临时文件、假控制器和自建辅助程序，没有运行真实 Mihomo，也没有访问用户私有代理配置。

## 基线发现

### P1 官方更新绕过文件边界

位置：`internal/builtin/proxy.go:176`、`:181`、`:202`。

Nido 在提交 `/upgrade` 前检查了程序、配置、进程和 controller 归属，但上游更新还会操作程序旁的 `meta-backup` 和 `meta-update`。这些路径没有经过相同的检查。

触发条件与影响：

| 已有路径 | 上游行为 | 影响 |
| --- | --- | --- |
| `meta-backup` 是指向外部目录的符号链接 | 备份打开该目录下的 `mihomo` | 可能截断并覆盖 Nido 目录外的文件 |
| `meta-backup/mihomo` 是符号链接或共享硬链接 | 复制备份使用 `O_TRUNC` | 可能覆盖链接关联的用户文件 |
| `meta-update` 已有未知文件 | 创建目录失败后仍执行清理 | 可能递归删除此前已有内容，即使更新失败 |

临时目录中四个用例均证实 Nido 仍提交更新 POST。测试只证明危险前置条件未被拦截；越界写入和删除的结论来自[官方更新实现](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/updater/update_core.go)，没有对真实用户数据执行破坏性验证。

这违反当前“内部路径不得通过符号链接访问”和“保留未知文件”的约束。若保留官方热更新，必须把它涉及的全部工作路径与清理范围纳入管理，且不能只验证主程序。

### P1 macOS 语言环境改变导致停止失效

位置：`internal/builtin/process_darwin.go:28`、`:36`、`:64`，`internal/builtin/manager.go:332`。

进程出生标识来自 `ps lstart` 的本地化文字。代码既未固定语言环境，又要求固定字段数量，并将日期文字保存后直接比较。

已用临时辅助程序复现：

- 中文 locale 下日期输出只有六个字段，启动后的归属检查失败。
- 在 `LC_ALL=C` 下启动，再用法语 locale 停止，出生字符串改变，被当作 PID 已复用；`Stop` 返回成功并删除记录，原进程仍在运行。

最低修补应固定 `LC_ALL=C`。正式设计应采用内核提供的结构化出生时间，避免本地化和秒级日期充当进程身份。

### P2 特殊日志文件阻塞启动

位置：`internal/builtin/process.go:40`。

代码先以阻塞 `O_WRONLY` 打开 `output.log`，再检查是否普通文件。如果该路径是没有 reader 的 FIFO，打开操作永久等待，后面的类型检查无法执行，同时占住组件操作锁。

隔离测试中，启动在接入 FIFO reader 前一直不返回；接入后才报“组件日志不是普通文件”。应在不会阻塞的打开方式下复核文件类型和 inode，拒绝 FIFO、设备、socket、符号链接和共享硬链接。

## 基线产品契约与实现缺口

### 配置可解析不等于配置有效

位置：`internal/builtin/manager.go:196`，`internal/builtin/proxy.go:46`。

空配置、负端口、超范围端口，以及 `mode: certainly-invalid` 都可能得到 `configured=true` 且无问题提示。目前只解析了一小部分字段，未调用 Mihomo 自身的语义验证。`configured` 含义因而过强，应拆分文件状态、结构检查和引擎验证结果。

### 启动成功不代表监听已就绪

位置：`internal/builtin/process.go:58`、`:64`，`internal/builtin/manager.go:257`、`:264`，`internal/nido/cli.go:589`。

启动只完成派生、一次身份检查与记录写入，`cmd.Wait()` 的错误被丢弃。自建程序在 600 毫秒后退出，`Start` 约 7 毫秒就返回成功；900 毫秒后状态是 `installed=true, configured=true, running=false`，没有退出原因。

若命令承诺服务已启动，这是 P2 行为错误。应在有限期限内确认受管进程仍存活、目标监听属于它，并报告提前退出或超时。监听就绪仍不能证明订阅或远端网络可用。

### 端点识别不能覆盖被接受的 Mihomo 配置

位置：`internal/builtin/proxy.go:18`、`:55`、`:64`。

公开的 `proxy/config.example.yaml` 和 `proxy/config.isp.example.yaml` 都通过当前配置检查，但只使用原生 `listeners`，所以 `ProxyEndpoint()` 无法找到地址，`nido codex proxy builtin` 失败。它们不是自动迁入来源，但展示了被接受的有效配置形式与端点导出之间的不一致。

另一个语义错误是忽略 `allow-lan`：官方传统端口在 `allow-lan: false` 时固定采用 IPv4 回环，当前代码却可能根据 `bind-address: ::1` 导出 IPv6 地址。[传统监听实现](https://github.com/MetaCubeX/mihomo/blob/Meta/listener/listener.go)应作为端点推导的依据。

### 官方更新返回成功早于重启完成

位置：`internal/builtin/proxy.go:181` 至 `:196`。

Mihomo 在替换文件后先返回成功，再异步关闭并重启；Linux/macOS 正常通过 `exec` 保留 PID。Nido 收到成功后只检查磁盘程序格式，没有等待新版本或监听恢复，可能在随后重启失败时已经报告更新完成。Linux 替换与 exec 之间还可能短暂出现 `(deleted)`，当前进程检查会拒绝该状态。[升级路由](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/upgrade.go)、[重启实现](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/restart.go)。

因此，不能将问题简单描述为“每次更新都会换 PID”；当前支持的两个系统正常情况下不会。真正缺失的是完成条件和更新过渡期的处理。

## 基线已有能力与其边界

值得保留的是独立组件目录、显式启停、稳定文件锁、下载大小与摘要检查、平台格式检查、停止状态下原子替换、PID 复用保护、controller 请求禁用外部代理及重定向，以及卸载保留配置数据的原则。Codex `.env` 仍通过独立集成修改，Proxy 没有反向依赖 Codex。

以下属于尚未完成的产品能力：配置创建与验证入口、restart、日志查询、当前和最新版本展示、明确的端点选择、启动失败诊断。它们不应因为旧 Bash 脚本中存在类似命令而算作 Go 版已经实现。

路径检查和实际操作之间的竞态、进程派生与记录提交之间的崩溃窗口，以及 Darwin listener 地址族核验仍需在实现阶段覆盖。本轮没有将未完成利用验证的风险计为确定缺陷。

## 基线验证

基线 `go test -race -count=1 ./...` 通过，所有现有正式测试通过。新增复现位于临时副本，分别覆盖官方更新路径、配置误判、启动提前退出、macOS locale 和 FIFO 阻塞。公开 YAML 的端点问题也在临时根目录复现。

这些结果说明基线测试未覆盖上述场景，不能据其通过认定 Proxy 已可接管运行。后续修复依据[Proxy 组件说明](proxy-design.md)实现，记录如下。

## 本轮修复

Proxy 已从 `internal/builtin` 拆分到 `internal/components/proxy`。`internal/nido` 负责 CLI 与组合，Codex 只经端点接口显式集成，Proxy 没有反向依赖。

| 基线问题 | 当前源码处理 |
| --- | --- |
| 官方热更新绕过文件边界 | 删除 controller 更新路线，统一停止状态下验证候选并原子替换；不操作或清理未知 `meta-backup`、`meta-update` |
| macOS 本地化出生日期导致误判 | 使用内核结构化出生信息、用户与系统启动标识；未知身份保留记录并报错 |
| FIFO 日志导致阻塞 | 使用非阻塞、不跟随链接的打开方式，随后核验普通文件、用户与硬链接 |
| 配置可解析被当成已有效 | 拒绝空配置、重复键、多文档、非法端口和不支持入站；静态状态与显式核心验证分开 |
| 启动只完成派生 | 一次性管道授权前保存身份，有限等待全部导出代理监听归属；提前退出、超时与停止失败分别处理 |
| 原生 listeners 无法导出端点 | 统一传统端口与 mixed/http/socks listeners，明确 allow-lan、IPv4/IPv6、认证和默认选择 |
| 官方更新响应早于重启完成 | 停机更新成功后仍保持停止；后续由用户显式 start，不再把控制器响应视为运行完成 |

配置入口支持隐藏 URL 的原生 provider 模板和私有 YAML 文件导入；捕获候选字节交给 `mihomo -t`，复核源文件及目标快照后提交。provider、规则及已支持资源路径限制在组件内，禁止覆盖程序、配置和进程记录。链接、共享硬链接与特殊文件不能作为这些受管文件输入。

已接入 `configure`、`config-path`、`validate`、`restart`、`endpoint NAME`、`logs --tail`、`probe --url` 与 `update --check`。status、endpoint 和 dry-run 保持本地只读观察；普通状态不执行核心验证，不把 `config=readable` 宣称为已通过核心或网络检查。配置、安装、更新不自动启动；remove 保留配置、日志、运行历史与未知数据。

## 再次完整审计及修复

对首次实现再次审查了配置与端点、进程归属、启动授权、停机更新和卸载。新增发现与处理如下：

| 发现 | 修复及隔离回归 |
| --- | --- |
| listener/provider 的上游解码接受大小写、下划线别名，可能绕过认证、TLS 和路径策略 | 策略字段只接受规范名称；覆盖 `USERS`、`private_key`、provider `PATH` 等变体 |
| 前导零 YAML 整数可能按八进制解码，导出端点却按十进制解释 | 拒绝前导零整数，区分传统整数端口和 listeners 十进制字符串 |
| provider 缺省路径、大小写受管名以及固定数据库链接未被完整检查 | HTTP/file provider 要求显式相对路径；保留名不区分大小写；检查已知固定缓存、数据库的链接、硬链接和文件类型；内联证书不再被误当成路径 |
| 长操作丢失原锁后可能采纳替换后的锁、目录或目标；写入前重新捕获也可能覆盖新目标 | 从持锁 inode 传递原目录快照，目标在首次进程检查前捕获；原子写入使用最初 expected 快照，覆盖慢 Inspect、下载、版本检查与提交交接场景 |
| Linux 运行映像被替换后，删除标记导致连精确停止都失败 | 保留映像变化标记，状态报告 unknown，停止仍以出生身份核验；增加磁盘替换及原生辅助进程回归 |
| 启动后记录被替换或阶段提交失败可能留下本次新进程 | 清理仅针对本次已授权的出生身份，独立于变化后的记录；不覆盖新记录，停止失败仍明确报告 |
| Stop/Restart 在等待或核心验证后可能采纳新记录 | 解码和授权使用同次捕获；发信号前及记录提交时复核；Restart 在验证新配置前绑定原记录 |
| 候选在 `-v` 后被替换仍可能执行 `-t`；更新检查可能把旧版本当更新 | 每次核心执行前复核停机、目录和候选；官方候选版本匹配发布 tag；稳定版本按数值比较，覆盖降级与开发版本 |
| remove 可能删除固定程序路径中的普通用户文档 | 删除前验证原生程序格式、执行权限和原始快照；不执行未知文件，不删除配置、日志、缓存或未知目录 |
| 动态加载器环境可覆盖原生程序依赖检查 | 与配置覆盖变量一并清除 `LD_*`/`DYLD_*`，保留普通代理和证书变量 |
| CLI 预览与实际授权不一致，或确认后端点已变仍写入旧地址 | 共用私有配置及认证探测预检；按具体启停操作对齐预检；builtin 绑定在确认后、代理块写入后复核配置摘要，变化时安全补偿本次修改 |

修复保持在 Proxy 自身和 Nido 的组合层；未增加持久守护进程、安装清单、版本仓库、通用事务框架或组件间生命周期依赖。

## 隔离验证与剩余边界

新增隔离测试覆盖配置与端点策略、敏感诊断、缺配置无创建、私有输入、符号链接、共享硬链接、FIFO、受管文件冲突、候选及配置并发变化、错误平台与摘要、停止更新保留未知目录、PID 复用、跨启动残留、身份不明拒绝操作、启动握手、监听归属、提前退出与超时。CLI 测试使用假核心执行接口，OS 测试只使用临时回环辅助进程和临时文件；发布下载由本地 HTTP 夹具代替。

本次复审新增回归后，最终验证通过：

- `make check smoke`：格式检查、vet、全项目竞态测试、CLI 临时 HOME 隔离和原生发行包冒烟。
- Go 1.23 的 `go test ./...`：最低支持工具链回归。
- `go test -race -count=1 -v ./internal/components/proxy`：在获准读取内核身份接口的环境完整执行，21.488 秒通过；全部 OS 用例实际运行，包含 locale、精确监听、启动授权失败、父进程 EOF、端口占用、零码与非零码提前退出、映像原子替换后的精确停止和两个独立根。普通受限 sandbox 下这些内核读取被拒绝时，只跳过对应 OS 测试；产品本身仍拒绝身份未知的启动，不降级校验。零码退出测试的期限包含 race 工具默认退出延迟，不改生产启动期限。
- Linux amd64/arm64、macOS amd64/arm64 构建通过。Linux 本轮进行交叉编译，原生 OS 运行验证在 macOS。
- `scripts/public-audit.sh` 源码公开检查通过。

启动成功前另行复核程序快照；磁盘程序与启动摘要不一致时报告 unknown，仍允许按内核身份停止。这是磁盘文件比较，不是运行映像摘要证明。启动、就绪和停止阶段写入记录时检查原锁、目录及预期快照，验证或等待期间被替换的新记录不会被覆盖。

上述两轮源码审计没有运行真实 Mihomo、加载真实订阅、安装或启动真实服务，也没有改动用户现有配置、认证、会话和数据。后续真实核心兼容验证另行记录如下。配置策略覆盖显式入站、provider、支持的顶层资源和已知固定文件；SSH/TLS outbound 资源、overlay outbound 状态与监听、远端 provider 动态节点仍由核心管理。固定缓存检查不阻止核心刷新普通缓存内容。macOS 和没有 pidfd 的 Linux 仍有检查到发信号之间的窗口，文件快照与原子替换也不构成 OS 沙箱；跨组件代理块和归属记录采用有条件补偿，不能宣称跨文件原子事务。当前没有热 reload、controller 手动刷新或默认 controller，更没有发布及用户服务切换。

## 真实 Mihomo 回环兼容验证

按后续授权，在临时 HOME、软件根、XDG/TMP 目录和临时回环端口运行真实核心，未安装系统服务。使用 [Mihomo 官方 v1.19.32](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.32) 的 `mihomo-darwin-arm64-v1.19.32.gz`，下载大小为 20,756,622 字节，并与官方发布 API 提供的 SHA-256 `3312a6780652c622890fd4357c6a853bbf865464fd047ac7b7f52dab8de18652` 一致。解压后 `-v` 返回 `Mihomo Meta v1.19.32 darwin arm64 with go1.26.8`。摘要核验仍不等于签名核验。

真实验证发现并修复了默认模板的两个问题：

1. select 组中的显式 `DIRECT` 排在 provider 节点之前，首次默认选中直连。现在业务组仅使用订阅 provider，空组设置 `empty-fallback: REJECT`。[上游选择顺序](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/adapter/outboundgroup/parser.go#L183)。
2. 去除直连业务入口后，省略 provider 的 `proxy` 会让订阅下载本身进入 `MATCH,PROXY`，尚未加载的空组拒绝下载。真实日志出现订阅请求匹配 `PROXY[REJECT]` 和 EOF，缓存没有生成。现在模板显式设置 provider `proxy: DIRECT` 完成引导，业务请求仍必须经过订阅节点。[上游下载调用](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/resource/vehicle.go#L139)、[内部 HTTP 路由](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/http/http.go#L81)。

新增 `internal/nido/proxy_real_test.go`，显式执行入口为：

```sh
NIDO_TEST_MIHOMO=/absolute/path/to/verified/mihomo make check-proxy-real
```

测试默认跳过，不下载核心；临时 CLI 构建使用已缓存模块并关闭模块下载。官方来源由运行前的独立摘要核验保证，测试内部再计算本地摘要以绑定实际安装字节。没有读取现有 Proxy 或 Codex 配置；临时 HOME 的认证、会话均为本测试生成的模拟数据。

| 真实验证场景 | 结果与证据 |
| --- | --- |
| 临时 CLI 安装、配置及 `-t` | 真实核心接受模板和原生配置；未知 mode 被拒绝，旧配置字节保留 |
| 模板 `common` mixed listener + HTTP provider | 本地订阅被取回并生成含唯一上游节点的缓存 |
| 传统 mixed-port + file provider | 禁用订阅服务器后重启，复用上述缓存；没有新增 HTTP 订阅请求 |
| 独立 HTTP/SOCKS listeners + inline provider | 具名端点协议正确，两种入口实际可转发 |
| HTTP、SOCKS5 TCP、HTTPS CONNECT、SOCKS 上的 HTTPS | 三种配置各四次请求，共 12 次目标请求及 12 次上游 CONNECT；每次均检查目标响应与上游计数，排除 DIRECT 假通过 |
| 新缓存路径且订阅不可用 | 核心可以监听，但目标访问失败；目标与上游计数不增长，没有意外直连 |
| 停止、同一核心的停机更新及卸载 | 所有测试监听在停止后关闭；更新、卸载前后的配置、缓存、记录、日志及未知文件逐字节一致，临时认证和会话模拟数据保留 |

`make check-proxy-real` 通过；另以 `go test -race -count=1 -v ./internal/nido -run '^TestProxyRealMihomo$'` 实际重跑通过。两次成功运行的计数均为 provider HTTP 请求 3 次、目标请求 12 次、上游 CONNECT 12 次。失败的前两次诊断运行也完成精确停止及临时目录清理。全项目竞态测试、Go 1.23 全项目测试、格式及源码公开检查再次通过。

上述真实核心运行平台为 macOS arm64，Linux 原生验证在下一节记录。验证范围为回环夹具中的 HTTP/file/inline provider、HTTP 出站节点及 HTTP/SOCKS5 TCP 入站，不包含 UDP、真实订阅、其他加密节点协议或跨版本更新。provider 在启动后加载，`-t` 和监听就绪均不保证 provider 已可用；测试有限等待其缓存加载，再检查实际转发。未发布、切换实际入口或改动任何现有配置、认证、会话和数据。

## Linux 原生隔离验证（2026-10-17）

使用本机已运行的 OrbStack Docker engine，在原生 Linux arm64 内核 `7.2.9-orbstack-00391-g2b298d05fbd4` 上执行；engine 为 Docker 29.8.2。宿主交叉编译的目标与 engine、镜像均为 `linux/arm64`，没有使用跨架构模拟，也没有安装或启动 Docker daemon、systemd 或宿主服务。

核心来自 [Mihomo 官方 v1.19.32](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.32) 的 `mihomo-linux-arm64-v1.19.32.gz`，大小为 20,809,592 字节，与发布 API 提供的 gzip SHA-256 `9dd862e28b46ff7d775f169cceebc28deccaa0a9e804237d421cd2571e0caba0` 一致。解压程序 SHA-256 为 `c80df2ca5159567804da883401e0118908fc1155e05df2baf3dbd4d3ecfc3754`，格式为静态链接 ARM aarch64 ELF。容器原生 `-v` 返回 `Mihomo Meta v1.19.32 linux arm64 with go1.26.8`。以上是来源与摘要核验，不是签名核验。

新增入口：

```sh
NIDO_TEST_MIHOMO=/absolute/path/to/verified/linux-mihomo make check-proxy-linux
```

`tests/proxy-linux-isolation.sh` 只使用现有 engine、本地已核验核心及已缓存 Go 模块。临时 `FROM scratch` 镜像仅含 CLI、两份测试程序与核心，不拉取基础镜像。测试以 UID/GID 65532 运行，根目录只读，可写与执行文件位于临时 `/tmp`；真实核心的 HOME、软件根、XDG 和 TMP 都位于该临时目录。容器使用私有 PID namespace、`--network none`、默认 seccomp、清空 capabilities 和 `no-new-privileges`，无宿主目录挂载、端口映射或特权设置。订阅、上游与目标只在容器回环通信；通配监听反例也仅存在于该隔离网络。`--init` 回收子进程，测试进程不占用 PID 1。

buildx 的可写状态另设临时 `BUILDX_CONFIG`，避免修改现有 Docker 配置。清理仅根据本次 Docker 实际返回的 CID 和带本次归属 label 的唯一镜像 tag；不操作已有容器、镜像或全局缓存。清理失败使测试失败并保留本次记录。`TestOS` 跳过被视为失败，避免把权限缺失误报为验证成功。

原生实跑发现并修复了两类此前交叉编译未暴露的竞态：

| 发现 | 修复与回归 |
| --- | --- |
| 启动器合法 exec 正好跨越两次 `/proc/PID/exe` 读取，启动被误判为程序变化 | 为明确的 exec/退出内核暂态增加内部分类；局部短重读并受原启动期限约束。出生身份不匹配、权限错误或稳定的未知程序继续拒绝；新增确定性回归及 16 轮 Linux 真实辅助进程 start/exec/stop |
| SIGTERM 后退出线程暂未收敛，停止过早失败；子项 ENOENT 或 `Threads: 0` 还可能被误当作完全退出 | 子项缺失先复核整个 PID 目录，不保留会被误判为 gone 的 ENOENT 错误链；Z/X 仅在 `Threads: 1` 及二次出生身份、UID、死状态一致时判退出。其他暂态只有限等待；不据此发信号、写 stopped 或授权新启动，超时保留记录 |

没有增加 SIGKILL、持久守护者、安装清单或组件间生命周期依赖。发信号前仍要求完整进程身份一致，Linux 支持时使用 pidfd 绑定具体进程。内核暂态的独立只读复查未发现新的确定安全漏洞。

| 原生验证场景 | 实际结果 |
| --- | --- |
| Proxy 全包与全部 `TestOS` | Go 1.27.2、Go 1.23.12 分别构建并原生执行，均通过，无 OS 用例跳过 |
| pidfd、进程出生身份与监听归属 | `pidfd_open` 和 `pidfd_send_signal(signal 0)` 实际可用；篡改出生时间、boot、UID 均拒绝停止，原进程仍活；IPv4/IPv6 精确回环及通配地址反例全部通过，无 IPv6 跳过 |
| 同字节程序原子替换 | 磁盘摘要未变仍识别运行映像 deleted 状态；报告 unknown、拒绝重复启动，同时可精确停止原出生身份 |
| 真实 Mihomo 启停 | status、记录和内核出生身份一致；重复 start 不换身份；无效配置 restart 保持旧进程及转发；有效 restart 换身份且旧身份退出；重复 stop 幂等，所有代理监听关闭 |
| HTTP/file/inline provider | HTTP provider 实际下载并生成缓存；禁用订阅服务器后 file provider 复用相同缓存，restart 不产生新下载；独立 HTTP/SOCKS listeners 与 inline provider 可用；空订阅不能回退直连 |
| HTTP、SOCKS5 TCP、HTTPS CONNECT、SOCKS 上的 HTTPS | 每轮 20 次目标请求和 20 次受限上游 CONNECT；各请求同时校验响应和计数，排除 DIRECT 假通过。两轮 provider HTTP 请求均为 3 次 |
| 停机更新与卸载 | 同一真实核心的 update 保持停止，remove 仅移除程序；配置、缓存、进程记录、日志、未知数据以及临时 HOME 认证/会话模拟文件逐字节保留 |

两轮完整 Linux 入口均成功退出并清理本次容器、镜像 tag 和临时构建目录；真实 E2E 分别耗时 3.50 秒及 5.71 秒。后续全项目 `go test -race ./...`、Go 1.23 全项目测试、macOS Proxy 全包 OS 竞态回归（23.005 秒，全部 `TestOS` 实际执行）、四平台 Go 1.23 编译、格式、vet、shell 语法及源码公开检查均通过。

上述一轮 Linux 原生覆盖为 arm64，且为非 race 测试；其时 Linux amd64 只完成编译。后续 amd64 与 Linux race 验证见下一节。旧内核无 pidfd 分支未在旧内核上原生验证。回环夹具不覆盖 UDP、真实外部订阅、其他节点协议或跨版本更新。没有安装系统服务、发布或切换实际入口，没有修改现有配置、认证、会话和数据。

## Linux amd64 与 race 原生验证（2026-10-17）

使用现有 J66 主机的 x86_64 Linux 内核 `6.8.0-124-generic`、Intel Xeon E5-2680 v4/KVM、bubblewrap 0.9.0 和 GCC 13.3.0。没有使用 amd64 用户态模拟。Go 1.23.12 官方 Linux amd64 工具链解压到本次独有 `/tmp` 目录，压缩包 SHA-256 为 `d3847fef834e9db11bf64e3fb34db9c04db14e068eeb064f49af747010454f90`，与官方发布元数据一致；未注册或安装到系统路径。

核心为 [官方 v1.19.32](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.32) 的 `mihomo-linux-amd64-compatible-v1.19.32.gz`，大小 22,877,876 字节，gzip SHA-256 `ba3ce607747a07f948fc35780e108a4a7c7f552a38b9bd4d115f313ebcb89c20` 与发布 API 一致；解压后 SHA-256 为 `7a0d59da2e678d56c899a3db996a2ad8963286c4f3634b0451435db248f13fa1`。原生执行输出 `Mihomo Meta v1.19.32 linux amd64 with go1.26.8`。来源和摘要核验不等于签名核验。

在临时 HOME、临时构建缓存、禁止模块联网的隔离空间中，从当前源码及预存 vendor 构建普通版和 race 版。构建与全项目测试只读映射源码、SDK、现有系统编译工具及库，不映射宿主 HOME 或用户数据；写入仅限本次临时输出目录及私有 `/tmp`。真实核心入口进一步缩小为四个静态程序的只读映射，不包含系统库、源码或 SDK。独立文件系统、PID、网络、`/proc`、`/dev` 与 `/tmp` HOME 由现有 bubblewrap 创建，清空环境和 capabilities；namespace 失败不回退宿主执行。

新增 `make check-proxy-linux-native`，支持相同源码的普通版与 race 预构建输入；原 Docker 入口增加 `make check-proxy-linux-race`。两个入口均核验执行环境架构（engine 或 uname）、静态 ELF、CLI 构建字段和实际测试程序符号，拒绝关键用例跳过。race 版 CLI、测试和一次性启动器均启用检测；Mihomo 保持官方字节，未重新编译，其内部并发不在 Go race 结论内。具体构建与运行命令见[验证说明](proxy-design.md#linux-race-与原生主机入口)。

本轮发现并修复的验证问题如下；没有修改生产组件逻辑或增加生产依赖：

| 发现 | 修复及证据 |
| --- | --- |
| Go 1.23 的 `go test -c` 程序缺少完整 `go version -m` 字段，被入口误拒绝 | CLI 保留构建字段检查；测试改用准确包入口、`runtime.raceinit` 及 `__tsan_init` 符号核验，普通模式拒绝混入 race 程序；四个输入都检查静态同架构 |
| 父测试启用 race 不能证明临时 CLI 已启用；脱离父进程的启动器报告也可能漏检 | 强制检查 race CLI 输入；测试在自身临时根之外持有私有报告目录，全部清理后检查子进程报告；真实 E2E 给私有 CLI 构造固定 GORACE。故意竞争的独立子进程必须被真实检测器以 66 退出，并被收集器拒绝 |
| TSAN 会在 TestMain 前创建 `log_path` 的父目录，umask 022 下得到 0755 | 原生探针确认此行为。父测试改用 stderr，由外层退出码判失败；测试先建立并核验 0700 目录，再让辅助进程记录到该目录。现存公开目录及链接仍严格拒绝 |
| tmpfs 连续 chmod 可能处在同一粗时钟刻度，ctime 实际未变却被测试要求拒绝 | 私有夹具在有限期限内观察原始 ctime 变化，再单次验证快照拒绝；两版本宿主工具链各连续 20 次通过。不把快照检查本身作为轮询条件，不放宽生产检查 |

全项目测试环境另补齐只读系统 `awk` alternatives 和项目 `.go-version`；权限夹具采用其预期的创建掩码。未安装工具或修改 Codex 源码来适配这些环境差异。文件快照比较当前观察值，不能证明已恢复且没有留下可观察时间戳变化的瞬时修改历史。

| 最终实际运行 | 结果 |
| --- | --- |
| amd64 普通版四产物入口 | Proxy 全包、全部 `TestOS` 和真实 Mihomo E2E 通过；E2E 为 68.92 秒 |
| amd64 静态 race 四产物入口 | Proxy 全包、全部 `TestOS`、故意竞争子进程检出及真实 Mihomo E2E 通过；E2E 为 235.07 秒，无非预期 race 报告 |
| 进程与内核专项 | 两轮均实际通过 pidfd signal 0、IPv4/IPv6 精确归属与通配反例、篡改出生身份拒停、同字节映像替换后精确停止及 16 轮 start/exec/stop；没有 OS 或 IPv6 跳过 |
| 两轮真实核心业务与数据 | 每轮 provider HTTP 请求 3 次、目标请求 20 次、上游 CONNECT 20 次；HTTP/file/inline provider、HTTP/SOCKS/HTTPS 转发、重复启停、失败 restart 保留旧实例、成功 restart 更换出生身份、停机 update/remove 保留全部快照均通过 |
| 原生 Linux 全项目 race | Go 1.23.12 的 `go test -race -mod=vendor -count=1 ./...` 全部通过；Proxy 210.249 秒、distribution 147.700 秒、Codex 3.622 秒、Nido 22.814 秒 |
| 宿主与入口回归 | Go 1.27.2 全项目 race、格式、vet、shell 语法、公开源码检查和 `git diff --check` 通过；纯 mock 覆盖缺 race、错误测试包、错误架构、动态 ELF、关键跳过及 namespace 失败拒绝等入口边界 |

真实核心运行结束后，各入口已清理本次 namespace 和临时四产物目录；本次下载、临时 SDK 和远端构建目录也已删除。上述一轮 Linux race 的原生架构为 amd64，其时 arm64 仅完成非 race 原生验证，后续记录如下。没有重新编译或检测 Mihomo 内部并发，也没有扩展到 UDP、真实外部订阅、其他节点协议、跨版本更新或旧内核无 pidfd 分支。未安装系统服务、发布、切换实际入口或修改现有配置、认证、会话和数据。

## Linux arm64 原生 race 验证（2026-10-18）

使用本机已有的 OrbStack Linux VM，engine 为 `linux/aarch64`，内核 `7.2.9-orbstack-00391-g2b298d05fbd4`；镜像与实际执行的静态 ELF 均为 arm64。没有请求跨架构模拟，也没有改变内核、ASLR、seccomp 或 Docker 配置。

构建工具为 Docker Official `golang:1.23.12-bookworm` 的 arm64 内容：index SHA-256 `167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db`，arm64 子 manifest `ac2b6b754dbe064bd10a97c2f46f9253a5e8220565fc3ae4edf8b362b87bbe2f`。Docker 直接拉取遇到 TLS 超时后，改由宿主下载同一 manifest、config 和各层，逐项核验摘要，再以本次唯一 tag 导入 OCI 内容；这属于来源及内容完整性核验，不是签名验证。构建器实际返回 `go1.23.12 linux/arm64`、GCC `12.2.0` 和 UID `65532`。

源码只输入 `cmd`、`internal`、`go.mod`、`go.sum` 与本次离线 vendor，通过 stdin 解包到临时 `/tmp`；无宿主 bind 挂载。构建器以非 root、只读根目录、默认 seccomp、清空 capabilities 和 `no-new-privileges` 运行，网络关闭，HOME、缓存和输出均在私有 tmpfs。没有安装系统工具、启动服务或读取现有 Proxy/Codex 数据。`CGO_ENABLED=1`，三个程序均使用 `-race -mod=vendor -trimpath -buildvcs=false -ldflags='-linkmode=external -extldflags=-static'`；构建输出为三份 race 程序和一个初始化探针。探针实际启动成功。

本次源码归档（不含 vendor）SHA-256 为 `0a9023f46eb9af889fd032a9900840bac4e04e9576a99650655f1e21234f064c`；三份 race 产物如下：

| 产物 | SHA-256 |
| --- | --- |
| `nido` | `9ed305fba8452b1587bc0669427e944ecc87eef6931375c8b539f5e576c545af` |
| `nido.test` | `b7c19a859145c3a1fccdd1978540d66fa8b6f3946baf27b8e50e4ed9069f5670` |
| `proxy.test` | `82c0fba9f728f8a83b048f2db5cbdaeae4adb8b4dd90a77ddc93f112261bad87` |

宿主 Go 1.27.2 只用于入口的元数据与符号读取；真正构建和运行的程序使用上述 Linux Go 1.23.12。入口分别核验 CLI 的 `-race=true`、`CGO_ENABLED=1`、`GOOS=linux`、`GOARCH=arm64`，以及测试包入口和 Go race/TSAN 符号。

真实核心继续使用官方 `mihomo-linux-arm64-v1.19.32.gz`，本轮重新下载并核验 gzip SHA-256 `9dd862e28b46ff7d775f169cceebc28deccaa0a9e804237d421cd2571e0caba0`，解压程序 SHA-256 为 `c80df2ca5159567804da883401e0118908fc1155e05df2baf3dbd4d3ecfc3754`。原生版本输出为 `Mihomo Meta v1.19.32 linux arm64 with go1.26.8`。

执行已有 `make check-proxy-linux-race`：最终 scratch 镜像只含三份 race 程序和官方 Mihomo，不含探针、源码、编译器、独立 libc 共享库或动态加载器。使用 UID/GID 65532、只读根目录、临时 HOME/TMP、私有 PID namespace、`--network none`、默认 seccomp 和清空 capabilities；无宿主 bind 挂载、端口映射或额外权限。所有订阅、上游和目标均为容器回环夹具。

| 本轮实际验证 | 结果 |
| --- | --- |
| Proxy 全包与 race 检测器 | 全部通过；故意竞争的子进程实际被检出，报告收集检查通过，无非预期 race 报告 |
| 进程与监听归属 | pidfd signal 0、IPv4/IPv6 精确回环与通配反例、篡改出生时间/boot/UID 拒停、同字节程序替换后精确停止通过；全部 `TestOS` 实际执行，没有 OS 或 IPv6 跳过 |
| 重复派生与授权交接 | 16 轮 start/exec/stop 通过，耗时 38.51 秒；父进程 EOF、记录提交失败、早退、监听占用及两个独立根的归属测试通过 |
| 真实 Mihomo 进程 | status、记录和内核身份一致；重复 start 保持出生身份，无效配置 restart 保留旧进程和转发，有效 restart 更换出生身份并确认旧实例退出，重复 stop 后监听关闭 |
| provider 与代理转发 | HTTP/file/inline provider 均加载成功；离线 file provider 复用 HTTP 缓存；HTTP、SOCKS5 TCP、HTTPS CONNECT、SOCKS 上 HTTPS 共 20 次目标请求及 20 次受限上游 CONNECT，provider HTTP 请求 3 次；失败订阅没有直连回退 |
| 更新、卸载与数据 | 同一真实核心的停机 update 和 remove 通过；配置、缓存、记录、日志、未知文件及临时认证/会话模拟文件逐字节保留 |
| 完整入口 | 成功退出，真实 E2E 为 54.17 秒；格式、shell 语法、源码公开检查与 `git diff --check` 通过 |

本轮未发现需要修改生产组件代码的新缺陷，未增加生产依赖或组件耦合。该结论覆盖此 OrbStack VM、内核和 Go 1.23.12 下实际执行到的 Proxy、Nido、测试及启动器路径；未声称 arm64 全项目 race、所有 Linux 内核或裸机均已验证，也未对官方 Mihomo 内部代码启用 race。真实外部订阅、UDP、其他节点协议、跨版本更新与旧内核无 pidfd 路径仍不在本轮范围内。

本次构建与测试容器、临时镜像引用，以及宿主下载、源码副本和构建输出目录均已定向清理；按本次 label 与唯一 tag 复查无残留容器或镜像引用，没有执行全局 prune。未安装系统服务、发布或切换实际入口，未修改现有配置、认证、会话和数据。
