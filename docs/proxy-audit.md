# Proxy 组件审查

审查基线为 `9dd3951`，日期为 2026 年 10 月 17 日。基线结论为 Proxy 只有基本生命周期骨架，不能据此宣称组件构建完成。当前源码已改用独立的 `internal/components/proxy`，后续补充了临时 HOME 中的真实核心验证。最终审计汇总如下，后文保留各轮发现、修复和验证证据；没有发布或切换入口。

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
| Linux amd64 | 最新同一冻结源码，Go 1.23.12 原生静态 race CLI、Proxy 全包、七项 TestOS、pidfd、IPv6 辅助及 16 轮启停均通过，无跳过 | v1.19.31 完整基线、v1.19.32 修复后基线及 v1.19.31 → v1.19.32 停机更新均通过；新旧核心实际版本、缓存复用、HTTP/SOCKS/HTTPS、启停和卸载保留已核对 | 最新双架构复验范围是 Proxy 全包及真实 CLI E2E；全项目 Linux race 为较早轮次证据 |
| Linux arm64 | 最新与 amd64 同一冻结源码，Go 1.23.12 原生静态 race CLI、Proxy 全包、七项 TestOS、pidfd、IPv6 辅助及 16 轮启停均通过，无跳过 | 与 amd64 相同的基线及指定跨版本场景均通过 | 未完成原生全项目 race |
| macOS arm64 | 本轮全 Proxy OS 辅助进程 race 24.588 秒通过，七项 TestOS 实际通过；宿主全项目 race 和临时 HOME CLI 隔离通过 | 较早轮次 v1.19.32 真实核心普通测试及父测试 race 通过，当时内部 CLI 为普通构建 | 最终生产修复后的真实核心复验、真实 CLI 全链路 race 及真实跨版本均未完成 |
| macOS amd64 | Go 1.23.12 交叉编译通过 | 没有原生核心执行结果 | 原生 OS、真实核心、race 及跨版本均缺 |

最新 Linux 跨版本两端各完成 HTTP provider **1** 次、目标与受限上游 CONNECT 各 **16** 次，旧、新版本各 8 次目标请求；同入口旧版完整基线为 **3 / 20 / 20**。跨版本实际 E2E 耗时 amd64 **178.94 秒**、arm64 **38.56 秒**。输入来源、冻结归档、产物摘要及详细顺序见下一节，不将同核替换误报为跨版本。

发布阻塞项和证据边界应分别处理：

| 类别 | 当前判断 |
| --- | --- |
| 已确认源码缺陷 | 当前没有未修复项；这是已审计范围内的结论，不是任意核心或配置的安全保证 |
| Linux amd64 部署目标 | 已有最终生产实现的原生 Proxy 全包和真实 CLI E2E race，以及 v1.19.31 → v1.19.32 停机更新证据；配置、进程归属、启停、provider、HTTP/SOCKS/HTTPS 及更新/卸载保留均通过。上表中最新 Linux 全项目 race 的证据范围仍如实保留，不将组件/E2E 结果扩称全项目 |
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
