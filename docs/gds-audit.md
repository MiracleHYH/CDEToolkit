# GDS 源码隔离与历史审查

GDS 自 2026 年 10 月 19 日起只保留 [独立模块源码](../standalone/gds/README.md)。当前 Nido 不注册 GDS，不提供 `nido gds`；根构建、测试、CI 与发行不接入 GDS。本轮不安装、迁移、清理既有部署，不自动发布，也不运行真实 GPU 或生产 GDS 元数据。

以下“源码隔离记录”描述当前边界；其后全部为 2026 年 10 月 18 日各阶段的历史记录，命令、路径、“当前”及“最终”均指各自当时的基线。原审查最终核对基线为 `b214363`，其 GDS 相关源码与开始审查时的 `1000e96` 一致；公开发行基线另为下文的 `60a71a9`。历史修复和验收事实保留，不据此恢复当前 Nido 入口或宣称真实 GPU 已验收。技术设计见 [GDS 源码设计](gds-design.md)，资产出处见 [历史发行记录](gds-release.md)。

## 源码隔离记录（2026-10-19）

Go 实现、C 绑定与专属测试迁到 `standalone/gds`，模块名为 `github.com/MiracleHYH/CDEToolkit/standalone/gds`；原 `GDS_VERSION` 原样迁为模块内 `VERSION`。嵌套模块不加入根 `go.work`，不由根 require/replace 引用；开发检查在该目录显式使用 `GOWORK=off`。NVML 依赖和相关校验和归入独立模块，根 Nido 不再携带它。

54 个原 GDS 源码与测试文件逐一对照迁移前 Git 基线，内容变化仅为 Go import 的模块前缀；保留的 `internal/githubdownload` 是为模块自包含复制的历史 helper，不随根 Nido 本轮下载三模式整改更新。组件管理服务保留为源码与测试对象，不向 Nido 注册命令。

本轮使用已有本地 Go 1.23.0 和依赖缓存，设置 `GOTOOLCHAIN=local`、`GOWORK=off`、`GOPROXY=off`、`GOSUMDB=off`，完成以下检查：

| 检查 | 结果与边界 |
| --- | --- |
| Darwin arm64、CGO0，`go test -run '^$' ./...` | 7 个包均通过编译检查，无匹配测试执行 |
| Linux amd64、CGO0，逐包 `go test -c` | 7 个测试程序均交叉编译成功，未执行 |
| Linux amd64、cgo，`go list -deps ./...` | 依赖可离线解析，NVML 仅属于独立模块；未编译或执行原生驱动路径 |
| 模块与源码完整性 | 离线 `go mod tidy`、gofmt、差异检查通过；没有旧根模块 import 或 go.work |

这些结果只证明迁移完整性、指定平台的测试编译与依赖解析。未重跑下文历史发行验收，未联网、SSH、提交或发布，未执行生产 GDS、元数据命令或真实 GPU 操作。

普通用户 CPU 假驱动隔离入口迁至 [standalone/gds/tests/linux-native-isolation.sh](../standalone/gds/tests/linux-native-isolation.sh)，当前不接入根 CI 或 Make。它只显式运行内部 CPU/假驱动测试，`cmd/gds` 测试仅编译；生产程序只构建与静态检查，不执行元数据，不构建 Nido。旧公开发行 harness 归档为不可执行的 [gds-public-release-isolation.sh.txt](../standalone/gds/archive/gds-public-release-isolation.sh.txt)，发行构建脚本和工作流已移除。

真实 GPU、兼容驱动组合、实际显存分配与释放、外部任务竞争和负载影响仍未验收。历史 CPU 结果与本轮编译检查均不能替代硬件验收。

## 公开发行补验（2026-10-18）

已发布 [GDS gds-v0.1.0](https://github.com/MiracleHYH/CDEToolkit/releases/tag/gds-v0.1.0) 与 [Nido v0.5.0](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.5.0)，两个标签均精确指向 `60a71a9f54e4f04c538fe49232eaf2a0d4b884ad`。发布在冻结的独立检出中完成，没有纳入共享工作区其他会话正在修改的 Proxy/Git 功能。

发布前另修复三项问题：

- Linux `/proc/<pid>/stat` 读取可能在进程退出窗口返回 ESRCH。子项读取的 ESRCH/ENOENT 先复查整个 PID 目录，目录仍在时保持过渡状态；只有确认目录消失才视为退出。权限、损坏、UID 或出生变化仍保留错误，未放宽归属条件。
- CUDA 假驱动测试的 `t.Run` 子测试不继承父 goroutine 的 OS 线程锁。逐个绑定线程，并验证错误线程释放被拒绝、原线程仍能清理和再次分配；生产 worker 的线程绑定及 Free 检查保持原语义。
- Nido 升级和重复初始化会重写当前有效 TOML、丢失注释。当前配置与生成环境一致时直接保留原始字节，官方 v0.4.0 升级与重复安装均已实测。

| 验证 | 最终结果 |
| --- | --- |
| [冻结源码 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39088168342) | Linux Go 1.23 / 1.27.2、macOS Go 1.27.2、源码/历史扫描、普通用户原生隔离全部成功 |
| [GDS 发布 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39088481928) | 精确标签、两次离线构建、glibc 2.31 加载、普通用户原生隔离、签名验签及 4 项资产发布全部成功 |
| [Nido 发布 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39089003828) | 全量回归、四平台包、原生冒烟、审计、签名验签及 18 项资产发布全部成功 |
| J66 普通用户原生源码验证 | UID 1001，Linux x86_64 6.8.0-124-generic，Go 1.27.2、GCC 13.3.0；GDS 253 项测试及子测试、Nido GDS CLI 28 项通过，零失败、零跳过 |
| 公开下载资产 | 两个 Release 所有资产的 API SHA-256、大小与固定 URL 通过；sidecar 严格匹配；GDS 程序及 Nido 四个平台包均与本地冻结源码构建字节一致 |
| 版本入口 | GDS 发布后 Nido `latest` 仍为 v0.4.0；Nido 发布后为 v0.5.0；最新安装器与标签源码一致 |
| 普通用户公开程序隔离验收 | 官方 Nido v0.4.0 → v0.5.0、默认 latest 重复安装；GDS 默认官方安装、只读更新查询、指定/默认更新、本地 0.0.0 测试版本 → 官方 0.1.0 及移除全部成功 |

[GDS 标签复核](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39088481925) 和 [Nido 标签复核第 2 次执行](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39089003809/attempts/2) 均已成功。Nido 标签复核首次在固定镜像下载时遇到 ECR `toomanyrequests: Data limit exceeded`（Docker exit 125）；仅重跑失败任务后，相同源码的可复现构建与完整原生隔离检查通过，没有修改已发布制品或标签。

正式 GDS 程序 SHA-256 为 `504bb2c05599947b2bfe4578f40a3e12fcd65192eb50f62ee7e2f5ed407237d7`。原生源码清单逐文件匹配发布提交，清单 SHA-256 为 `e89daa549ca5fe26eb0509c9d3639aafd7722f7914193975ce8cefed90a4c2c9`。NVIDIA 假库另通过 Go 1.23 的 race 随机排序重复验证（147 项，零失败和跳过）；中止的 Go 1.27 仿真编译未计入验收。

公开验收使用私有 HOME、PID、设备与临时软件根，显式共享公网；没有 GPU 设备、宿主 HOME 或服务 socket。Nido/GDS 配置注释、env.sh、未知文件、Codex 认证/会话/数据库、Proxy 与 Shell 哨兵逐字保持。仅执行 GDS 元数据，不调用真实探针、monitor 或 worker。旧本地测试版本不代表历史官方 GDS 发行。首次验收因隔离后的 awk alternatives 链接不可达而中止；最终入口仅为已有 awk 创建私有别名，完整重跑成功。

最终证据位于本地 `build/gds-release-20261018/`：`evidence-native-release/`、`evidence-public/`、公开资产摘要、CI 结果和清理证明。远端本任务的 SDK、vendor、源码与临时程序已清理，相关 PID namespace 和测试映像均无残留。退出后的 namespace 编号可能复用，最终清理结合任务独有的程序、源码和 SDK 挂载路径核验，不单凭历史编号认定进程归属。现有入口、配置、认证、会话与数据没有因本次发布而改变；真实 GPU、驱动组合、显存释放与负载影响仍未验收。客户端自动核验 HTTPS、API 摘要与 sidecar；Sigstore 签名在发布 CI 验证，客户端当前不自动验签。

以下保留发布前各阶段的历史记录，其中“未发布”描述对应当时的执行范围。

## 历史实现与修复

当时源码增加 `internal/components/gds`、`internal/gdsapi`、`internal/gdsutil`、`internal/gdsruntime` 和独立 `cmd/gds`，删除仅承载 GDS 的旧 Go builtin 框架。Nido 管理层保持纯 Go，NVML 与 CUDA Driver API 只进入独立 Linux amd64 cgo 组件。这些 GDS 路径现已整体迁入 `standalone/gds`。

- UUID 贯穿配置、探针、worker 与记录，GPU 进程排除要求精确出生身份及新鲜列表中的本 worker PID。
- CPU 状态机按本次连续有效观察累计空闲，忙、失效、观察空档和重启都重新计时。
- worker 在记录提交和 ALLOCATE 前不分配；绝对 CLOCK_BOOTTIME 许可、EOF 和独立 watchdog 处理父进程死亡、暂停与原生调用挂住。
- 显存分配前后检查余量，只管理私有 context，不执行计算 kernel。自动让出仍有观察窗口，可靠使用前需 stop 成功。
- 稳定锁、文件快照、并发复核和原子替换覆盖程序与配置；卸载拒绝未知文本和共享硬链接，保留配置、日志与历史。
- monitor、worker 及未确认的显式辅助进程分别核验；退出不明保持 unknown，候选检查无法回收时保留其映像。
- dry-run 执行本地静态预检，不执行程序、加载驱动或写锁；安装不配置或启动，更新只在停止状态完成。

新实现不调用冻结 Shell，不向 Codex 全局 AGENTS 写入规则，也不迁移旧部署。旧脚本问题由退出新执行链处理，未声称历史脚本本身已被修复。真实 NVIDIA GPU 上的显存释放、驱动故障和负载影响尚未实测。

## 历史实现后的隔离验证

验证日期为 2026 年 10 月 18 日。临时 HOME、软件根、假 NVML/CUDA 共享库和 CPU worker 均与现有安装分离，没有调用真实驱动或 GPU，没有注册系统服务、切换实际入口或发布。

| 环境 | 实际结果 |
| --- | --- |
| macOS arm64，Go 1.27.2 | `make check` 通过，包括 vet、全项目 race、临时 HOME 的 CLI 隔离；后续 GDS 修复另完成相关包 race 复测，最终 runtime 全包 race 11.117 秒通过 |
| Nido 四平台 | Linux/macOS × amd64/arm64 的 `CGO_ENABLED=0` 构建通过；Nido 依赖图没有 cgo 文件或 NVIDIA 原生适配 |
| Linux amd64 仿真，官方 Go 1.23 bookworm 镜像 | 最终 NVIDIA 假库及 cmd 两包合计 39 个测试及子测试通过，零失败、零跳过；独立 cgo ELF 构建和无驱动元数据查询通过 |
| Zenith-Dev 原生 Linux amd64，Go 1.23.12、GCC 11.4.0、内核 5.4.250 | 最终 GDS 六包 race：122 个测试及子测试通过，零失败、零跳过；包含 OSRunner → monitor → CPU worker 的安装、授权、保留、外部活动释放、停止、更新和卸载完整链路 |
| 同一原生 Linux 全项目回归 | Codex、Nido、Proxy、distribution 均通过 race。首轮仅发现 runtime 的最终调度阶段被清理覆盖，修复后所有受影响 GDS 包重新通过上述完整 race；未把首轮失败描述为全绿 |
| 同一原生 Linux 构建与 CLI | 独立 GDS cgo ELF 与 Nido 纯 Go 程序构建通过；临时 HOME 的 `tests/nido-cli-isolation.sh` 通过；GDS 元数据为 `0.1.0-dev`、协议 1，ELF 直接动态依赖仅 libc |

当时原生 Linux 验证在新建的临时目录中离线执行：SDK 取自固定官方镜像，依赖由当时模块缓存生成 vendor；子进程只得到临时 HOME、TMPDIR、构建缓存与必要 PATH。主要命令如下，仅记录旧仓库布局，不作为当前执行入口：

```text
go test -race -json -count=1 ./...
go test -race -json -count=1 ./internal/gdsruntime ./internal/components/gds \
  ./internal/gdsutil ./internal/gdsapi ./internal/gdsruntime/nvidia ./cmd/gds
```

原生最终源码清单覆盖 `cmd`、`internal`、`go.mod` 和 `go.sum`，与工作区逐文件匹配；清单 SHA-256 为 `9961a73f65c866d75c633ae748a7bce34a4ebeb2f518197015d217f0f5f6757b`。SDK 来源镜像为 `public.ecr.aws/docker/library/golang@sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db`。源码传输产生的 AppleDouble 元数据不参与 Go 构建或源码清单。

仿真环境将受测程序的 `/proc/exe` 显示为 `/opt/orb/qemu-x86_64`，并可能因哈希耗时超过观察间隔而无法进入 holding。相同环境中的未修改 Proxy 基线也复现授权身份拒绝。没有为此放宽生产校验，原生 Linux 验证补齐了这些进程与时序路径。

上述首轮原生测试以 UID 0 执行。额外普通用户检查被主机 `/tmp` 的既有 0700 权限阻止，mount namespace 也不被允许；没有改动主机权限或放宽组件策略，不能将首轮结果扩称普通用户原生验收。后续普通用户原生补验见下节。GDS 设备锁仍要求用户可访问并安全创建固定 `/tmp/nido-gds-<euid>` 目录。全项目回归中，真实 Mihomo 测试按未提供核心而跳过，另有一项无法由 root 模拟权限失败的既有测试跳过；首轮 GDS 最终六包没有跳过。

真实 GPU 验收仍包括：兼容驱动版本、真实显存余量、外部任务创建与分配竞争、GPU lost、释放延迟和长期资源开销。CPU 假库证明绑定与策略行为，不能替代这些检查。独立 GDS 发行源尚未建立，安装和更新只接收显式本地程序；日志在启动前轮转，不承诺连续运行时的硬大小上限。

## 历史完整复审与普通用户补验

2026 年 10 月 18 日再次对 Go 实现逐层审计，并交叉检查组件、运行时和原生适配之间的身份与退出交接。本轮仅修改源码和运行 CPU 隔离测试。

| 级别 | 发现 | 修复与回归 |
| --- | --- | --- |
| P1 | worker 的 `os.File` 包装可能被 GC 终结器提前关闭，设备锁随之提前解除 | 通过 `F_DUPFD_CLOEXEC` 保留无终结器的原始描述符直到进程退出；CPU 子进程反复 GC，验证父描述符关闭后仍互斥、进程退出后才解除 |
| P1 | worker 分配入口仅核 CUDA 设备，分配前后设备模式、MPS 或外部进程可能已经变化 | 分配前完整 Probe；分配后要求唯一 compute PID 为当前 worker 且无 graphics/MPS，失败在原 OS 线程清理；假库记录确认只分配和清理一次 |
| P1 | native `Free` 失败后再次调用可返回成功；协议错误还可能吞掉清理错误 | 清理错误保持粘性并合并原始原因；错误线程调用保留原线程的清理机会 |
| P1 | worker 异常返回后停止 watchdog，同步错误日志阻塞可能无限保留 context 或设备锁 | 异常路径的独立硬退出围栏持续至进程退出；CPU 子进程同时注入清理失败、父管道关闭与满 stderr 验证有限退出 |
| P1 | helper 只在超时且回收失败后登记，父 CLI 在此前死亡会丢失归属 | metadata/probe 使用 FD 3 门，先提交内核身份再授权；CPU 父死与拒绝授权测试验证不执行未授权查询、已登记 helper 可精确停止 |
| P1 | 公开 Probe 和直接前台 run 未完整参与组件互斥，可与更新或卸载交错 | 显式辅助调用持管理锁；standalone 初始化在发布身份前也持管理锁；探测完成重新核验配置与程序 |
| P1 | 当前 boot 的状态损坏、身份不匹配或进程记录缺失可能被忽略，从而掩盖未确认退出 | 状态携带 boot 并严格绑定进程出生及 token/UUID/摘要；异常现场阻止新查询与修改；Stop 仍释放精确核验的已知进程，最终保留异常并报错；旧 boot 恢复单独验证 |
| P2 | runtime 每次发布重新捕获快照，会接纳并覆盖外部编辑；受管启动也有父子交接窗口 | 本轮记录持续绑定上次提交的 inode/内容与目录 guard，父子握手绑定已有状态摘要或缺失标记；三类记录被替换或原地编辑时释放并退出，保留编辑 |
| P2 | 授权与取消同时就绪时，select 可能随机接收已经排队的授权 | helper、monitor、worker 在接收前后复核取消，已取消的排队授权不执行查询或分配 |
| P2 | 最后 Check 与 rename/unlink 之间仍可能删除迟到的新文件 | Linux 使用 `renameat2` NOREPLACE/EXCHANGE；冲突保留被移出的 inode 并报告绝对恢复路径；卸载先移到随机恢复名再核对，日志轮转采用安全备份与隔离移除 |
| P2 | 任意有效 ELF 放在程序路径下即可被更新或删除 | 新增 `installed.json` 绑定已验证程序的摘要与协议；缺失、不匹配、提交中断和并发编辑均保留现场并拒绝危险操作 |
| P2 | ELF loader 仅按 `/lib/` 前缀判断，路径穿越可逃逸；NVML 进程结构中的 MIG 证据未检查 | 拒绝非规范 loader 与内嵌 NUL；v2/v3 拒绝有效 MIG instance ID，补 v1/v2/v3 ABI 回退和无效 PID 测试 |

`renameat2` 不可用时生产路径明确失败，不回退到可能覆盖文件的普通 rename。程序与安装凭证采用两次局部提交；中断导致摘要不匹配时拒绝继续执行，不自动接管或回滚。普通编辑器的迟到原子替换有恢复内容；不能保证同 UID 的非合作进程持有旧 FD，并在最后核验之后任意延迟写入的所有时序，也不将这些保护当作抵御同 UID 恶意代码的权限边界。

当时原生补验入口为 `tests/gds-linux-native-isolation.sh`，由 `make check-gds-linux-native` 调用；该路径和 Make 入口现已移除，迁移后的显式 CPU 入口见本页“源码隔离记录”。历史 harness 拒绝 root、非 Linux amd64、缺少 namespace 能力或已有证据目录；仅挂载只读公共源码、已有 SDK/编译工具以及新建的可写 work。测试环境拥有私有 PID、网络、最小 `/dev` 与 `/tmp`，没有宿主 HOME、服务 socket 或 GPU 设备。假 NVML/CUDA 测试通过包内入口注入路径，不提供生产环境变量后门。

最终普通用户原生环境为 J66，UID 1001，Linux x86_64 6.8.0-124-generic，Go 1.23.12、GCC 13.3.0。SDK 仍取自上述固定官方镜像，依赖离线生成并经过 `go mod verify`。没有改动宿主 `/tmp` 权限、账号或系统配置。

| 最终复审验证 | 结果 |
| --- | --- |
| 普通用户 Linux amd64 GDS 六包 `-race -count=1` | 218 个测试及子测试通过，零失败、零跳过；runtime 76.896 秒、组件 46.009 秒、原生假驱动 3.422 秒 |
| 同一普通用户 Nido GDS CLI 专项 | 2 项 race 测试通过，零失败、零跳过 |
| 同一原生 namespace 构建与验收 | 全库 vet、独立 GDS cgo ELF、Nido CGO0 构建及 CLI 隔离通过；只执行 GDS 元数据查询，`native=true`、协议 1、版本 `0.1.0-dev` |
| macOS 全项目回归 | `make check` 通过，包含 vet、race、格式、CLI 隔离；末次组件局部修复另通过 `-race -count=1`（3.493 秒）及全库 vet |
| Nido 四平台 | 最终源码 Linux/macOS × amd64/arm64 的 CGO0 构建通过，Nido 依赖图无 cgo 文件 |

普通用户验证和 root 首轮验证分开记账，未将仿真、跳过或中间版本结果混算。最初 namespace 测试因 `/etc/alternatives/cc` 未挂载而失败，最终入口只在私有 `/test-bin` 中为既有 GCC 提供 `cc` 别名；没有安装工具或放开宿主配置。测试源码清单覆盖 `cmd`、`internal`、`tests`、`scripts`、`.github`、Makefile、VERSION 和模块文件，与最终工作区逐文件匹配，清单 SHA-256 为 `571b1a3eb7ab550f0145f5365684756877cdf2d09b0730ee5e37c58eecb15964`。

完整日志、namespace/设备清单、程序元数据和清理证明保留在本地 `build/gds-audit-20261018/evidence-complete/`。验证结束后从宿主检查无测试映像或该测试 PID namespace 遗留，随后删除远端新建的源码、SDK、vendor、程序与证据目录；本地传输归档及 vendor 副本也已清理。全程没有发布、切换入口、安装启动真实服务、调用真实 GPU 或修改现有配置、认证、会话与数据。

## 历史约定与初审边界

已逐页读取 Dev 聊天 `01a14497-4109-7991-b62f-5038a3cc8189`。与本组件有关的最终约定是：

- Nido 使用单一软件目录，内置组件在自己的固定子目录中运行。
- Codex 属于外部集成，Nido 只提供目录关联、环境、代理与官方安装更新辅助。
- GDS 显式可选，没有默认安装或启动依赖。
- 不恢复双目录、迁移和通用事务体系，不为旧部署建立自动接管路径。

Dev 最后记录发布 Nido v0.3.1；当前源码还包含之后独立完成的 Proxy 组件。已有版本发布和 Proxy 验证不构成 GDS 可用的证据。

| 初审实现 | 初审时的能力 | 当时缺少的部分 |
| --- | --- | --- |
| `internal/builtin` | 本地 ELF/Mach-O 文件安装、更新、删除，少量进程记录处理 | 专用配置、GPU 探测、连续空闲判定、worker 管理、故障释放 |
| `internal/nido` | 将 `nido gds` 分发至上述通用残留实现 | 专用命令与可解释的调度状态 |
| `gds/gds.sh` | 旧 monitor、tmux/direct 模式与空闲占用 | 未接入 Nido；存在下列正确性问题 |
| `gds/src/scheduler.cu` | 分配显存并持续运行单线程 CUDA kernel | 无父进程故障保护、设备身份握手或租约 |
| `tests/gds-isolation.sh` | 假编译器、假 GPU 查询、CPU worker 的少量旧链路测试 | 不能证明 Go GDS 或真实 GPU 行为 |

`internal/builtin/manager.go:198` 的 `Start` 明确拒绝启动；安装后 `Status` 也明确报告调度未适配。这项拒绝是合理的保护，不能通过直接启动 CUDA worker 来补齐。

## 历史旧调度实现的问题

以下 P1 问题会破坏设备选择、活动识别或资源让出；部分需要特定 GPU 环境触发。表中的硬件场景来自源码与 NVIDIA 接口语义推演，本轮没有运行真实 GPU 复现。

| 级别 | 位置 | 问题与触发条件 | 新设计要求 |
| --- | --- | --- | --- |
| P1 | `gds/gds.sh:170`、`:578`；`gds/src/scheduler.cu:135` | 探测使用 nvidia-smi 物理 index，worker 使用继承环境后的 CUDA ordinal。双卡设置 `CUDA_VISIBLE_DEVICES=1`、`gpu_device=0` 时可能监测卡 0、分配卡 1 | 全链路使用完整 UUID，并在 worker 内复核 |
| P1 | `gds/gds.sh:624`、`:776` | 把唯一可见 GPU PID 当作自身 GPU PID并持续排除，没有证明归属。MPS 下会把共享 server 当作 worker，之后忽略其他 client | 只排除精确核验的本次 worker；不支持的 PID 视图拒绝占用 |
| P1 | `gds/src/scheduler.cu:189`；`gds/gds.sh:834` | worker 释放依赖 monitor EXIT trap；只 SIGKILL direct monitor 后，worker 没有自行退出条件 | 私有控制管道与有限续租，父进程消失或停止推进时自行退出 |
| P1 | `gds/gds.sh:739`、`:170` | nvidia-smi 没有超时。探针阻塞时，循环到不了失败释放分支 | 隔离探针、有界等待，探针失败停止续租 |
| P1 | `gds/src/scheduler.cu:152`；`gds/gds.sh:852`；`gds/global-agents.md:5` | 已占 25% 显存后，新任务可能在下一轮探测前申请显存并 OOM。要求忽略占用直接启动不能消除竞争 | 自动让出只承诺尽力；可靠使用前先停止并确认释放 |
| P2 | `gds/gds.sh:800`、`:868` | 重启恢复旧 idle_since，将未观察的停机时间计入连续空闲；系统墙钟变化也影响计时 | 只累计本次运行中的连续成功观察，使用单调时间 |

CUDA ordinal 会受可见设备列表和顺序影响，见 [CUDA 环境变量](https://docs.nvidia.com/cuda/cuda-programming-guide/05-appendices/environment-variables.html)。MPS 将 client 的 GPU 活动记到 server，见 [MPS 进程归属说明](https://docs.nvidia.com/deploy/mps/appendix-tools-and-interface-reference.html)。这两项接口事实不能用“进程名称透明”或“只有一个 GPU 进程”替代。

另外，当前只查询 compute apps，不能据此排除图形 context 等活动；无效配置静默回落默认值、worker 启动失败后缺少有界退避、持续 kernel 的功耗与竞争缺少真实测量，也不适合直接继承。

## 历史 Go 程序管理的问题

| 级别 | 位置 | 已验证行为 | 修正方向 |
| --- | --- | --- | --- |
| P2 | `internal/builtin/manager.go:147` | 在临时根的 `gds/gds` 放置权限 0600 的普通用户文本，`gds remove --yes` 仍删除它 | 删除前检查受支持程序格式、执行权限和目标快照；拒绝未知普通文件 |
| P2 | `internal/builtin/manager.go:84`、`:115`；`files.go:97` | 通过临时 Go overlay，在更新的第二次停止检查时写入用户编辑；Update 仍返回成功并覆盖编辑 | 慢操作后及替换前核验目标文件、目录与锁快照；变化则保留现场 |
| P3 | `internal/nido/cli.go:603` | 不存在的 artifact 以及必定失败的 start，其 dry-run 都可返回成功 | 预览执行只读静态预检，动态能力明确为未知 |

现有锁缺少稳定路径与硬链接复核，进程身份也没有 boot ID。它们不足以作为新 GDS 的基础。Proxy 已实现更完整的局部文件和进程保护，但不应因此将两种组件合并为通用生命周期引擎。

## 历史初审测试证据与缺口

本轮使用现有 Go 1.27.2 工具链，在临时目录、假 Runner 与本地夹具范围执行：

- `internal/builtin` 全包普通测试与 race 测试通过。
- `internal/distribution` 全包测试通过。
- Nido 的 `TestStatusAndDryRunDoNotInitialize`、`TestInitSingleRootAndExternalAssociation`、`TestStatusRejectsIgnoredComponentOptions`、`TestCLIRejectsAmbiguousOptionsAndUnsafeInstallPreview` 通过。
- 临时 CLI 复现未知文本误删；临时 overlay 测试复现并发编辑被覆盖。复现成功表示确认缺陷，不表示已有保护通过。
- 旧脚本语法检查通过；旧 `check-gds` 要求 Linux，本机 macOS 没有执行该测试，也没有用现有业务容器替代隔离环境。

现有旧隔离测试主要覆盖编译指纹变化、延迟 exec 后就绪和 TERM 清理。尚未覆盖 UUID 重排、MPS、外部任务让出、父进程 SIGKILL、探针阻塞、PID 复用、空闲重计时与真实 CUDA 分配。CI 的 GDS 任务仅在手动选择后运行，且仍测试旧 Shell。

以上是初审阶段的原始证据。后续实现的 CPU 协议、文件保护和假驱动回归分别验证新路径；真实 GPU 能力仍需另行验收。后续验证只在临时 HOME、软件根和 CPU 夹具中执行，不安装或启动实际组件，不修改既有 Codex 配置、认证、会话与运行数据。
