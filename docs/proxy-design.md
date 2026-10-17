# Proxy 组件实现说明

Proxy 是 Nido 内置的本地显式代理组件。实现位于 `internal/components/proxy`：Nido 管理 Mihomo 程序、原生配置、精确进程和本地端点；Mihomo 管理节点协议、规则、provider、连接与缓存。本说明对应 v0.4.0 起的独立组件，基线问题及修复记录见[审查记录](proxy-audit.md)。真实核心验证仅在临时 HOME 和回环夹具中执行，发行及安装不自动切换实际组件入口。

## 职责与目录

| 对象 | 责任 |
| --- | --- |
| `internal/nido` | 软件根、命令分发、输出及组件组合 |
| `internal/components/proxy` | 程序获取、配置验证、端点、显式启停、停机更新、本地状态 |
| Mihomo | 完整节点与路由语义、provider 刷新、连接、核心数据 |
| `internal/integrations/codex` | 经显式选择，将端点写入自己的受管 `.env` 块 |

组件不依赖 Codex、CLI 或旧 `internal/builtin` Proxy。默认使用普通用户权限，只提供本机回环 HTTP/SOCKS 代理，不注册服务、自动重启或更改全局代理。多个 Nido 根彼此独立；端口冲突必须失败。

```text
<NIDO_HOME>/proxy/
  mihomo                 # 当前程序
  config.yaml            # 唯一持久主配置，原生 Mihomo YAML
  process.json           # 最近一次受管启动的身份、阶段与摘要
  output.log             # 核心原始输出
  .lock                  # 稳定的短期操作锁
  providers/             # 默认模板的 provider 缓存路径
  ...                    # 核心数据与用户文件
```

Nido 的 `config.toml` 不复制端口、订阅或节点。配置摘要只用于比较磁盘与受管启动快照，不是版本仓库。新建组件目录采用 `0700`，配置、记录和日志采用 `0600`，程序采用 `0700`。修改前检查路径、普通文件、共享硬链接、预期快照及目录身份；写入使用同目录私有临时文件和原子替换。更新只清理本次创建的随机对象，不递归清理未知目录。

这些检查不构成操作系统沙箱，也不代表已审计 Mihomo 的所有扩展字段。回环监听同样不能隔离同机不同用户，需要隔离时由用户配置认证或系统权限。

## 配置及验证

保留原生 YAML，不另造节点或订阅语言：

- `proxy configure` 首次交互隐藏 provider 订阅 URL，生成一个 `common` mixed listener，默认 `127.0.0.1:7890`，配一个 provider、代理组和兜底规则。已有配置时仅显示路径及验证方法，不覆盖。
- `proxy configure --file /absolute/config.yaml` 采用私有普通文件，要求 group/other 无访问权限。仅在停止状态捕获候选快照，验证后原子保存；不复制外部附属资源。

模板的 HTTP provider 显式设置 `proxy: DIRECT` 取回订阅，避免首次取回也依赖尚未加载的订阅组。业务 select 组仅使用订阅 provider，默认选择其中首个节点，空组使用 `REJECT`，避免显式 `DIRECT` 抢占默认选择或订阅不可用时意外直连。模板不启用 controller，不提供订阅格式转换或第三方转换服务。普通配置诊断不回显订阅、节点密码或 secret；核心验证输出有长度和时间限制，错误采用安全诊断。`output.log` 保留核心原文，显式查询或分享日志时仍需注意敏感信息。

| 层次 | 已实现的结论 |
| --- | --- |
| 静态检查 | 最大 4 MiB、单一非空 mapping、无重复键、端口合法、端点名称及地址唯一、已支持入站只监听回环 |
| 核心检查 | 受管程序执行 `-t -d <组件绝对目录> -f -`，输入捕获的原字节；失败不提交配置 |
| 运行确认 | 受管进程仍存活，全部导出代理监听属于该进程，地址及地址族匹配 |

静态检查通过不代表核心接受配置；核心检查通过不代表端口能绑定或远端可达。特别是 `-t` 不会执行 HTTP/file provider 的启动加载，不能将它通过解释为已下载、加载了节点。缺少程序时不能宣称核心验证完成；缺少或空配置时不调用核心生成默认配置。`-t` 仍可能按上游行为访问数据库或缓存，因此只在明确验证、配置、启停预检及更新中调用，不用于普通状态查询。[官方 CLI](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/main.go)、[初始化行为](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/config/initial.go)、[provider 初始化](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/executor/executor.go)。

本地策略支持传统 `mixed-port`、`port`、`socks-port`，以及 `listeners` 的 mixed/http/socks。至少存在一个有效回环代理入口。传统端口在 `allow-lan: false` 时固定采用 IPv4 回环；启用 `allow-lan` 时必须显式绑定字面回环 IP，IPv6 使用带方括号的 `bind-address: '[::1]'`。独立 `listeners` 不受 `allow-lan` 限制，必须逐项显式设置字面回环 `listen`。[传统监听](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/listener/listener.go)、[listeners 默认地址](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/listener/inbound/base.go)。

HTTP/file provider 必须显式设置组件根内的相对 `path`，inline provider 不要求文件路径；不依赖上游按 URL 生成的隐式缓存名。provider、规则和已支持的顶层 TLS 文件资源不能覆盖 `mihomo`、`config.yaml`、`process.json`、`output.log`、`.lock` 或核心固定缓存与数据库，保留名比较不区分大小写。已有目标不能是链接、共享硬链接或特殊文件；核心固定文件也在静态检查中核验。`tls.custom-certifactes` 按上游拼写接受内联证书字符串列表，内容交给核心检查。

策略控制的 listener/provider 字段要求规范的小写连字符名称，拒绝上游结构解码器接受但会绕过精确字段检查的大小写或下划线别名。传统端口要求 YAML 整数；所有端口拒绝前导零整数，避免 YAML 八进制与导出地址不一致。listeners 仍接受引号包围的十进制端口。[结构解码](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/common/structure/structure.go)、[固定数据路径](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/constant/path.go)。

未知节点、规则及扩展字段保持原字节交核心验证，不递归猜测所有名为 `path` 的字段。上述策略只覆盖显式入站、provider、已支持的顶层资源和已知固定文件。SSH/TLS outbound 的文件、overlay outbound 的状态目录和额外监听，以及远端 provider 动态引入的节点扩展，仍属于 Mihomo 的原生能力；这些检查不能将不可信核心或配置变成文件系统和网络沙箱。

当前明确拒绝启用 TUN、透明代理、其他入站类型、非回环监听、TLS/Reality 代理入站、脚本、外部 UI、controller 的 TLS/Unix/pipe 入口，以及 YAML 别名与合并键。可选 HTTP controller 必须监听字面回环地址并配置非空 secret；生命周期不依赖它。

## 端点与状态

`Endpoint` 返回名称、协议、回环 host、端口、认证标记及来源配置摘要，不含凭据。默认优先选择 `common`，否则仅允许唯一候选；多个入口要求 `proxy endpoint NAME` 显式选择。传统端口名称分别为 `mixed`、`http`、`socks`。mixed/http 导出 HTTP URL，SOCKS 导出 `socks5h` URL。

认证标记表示配置了认证；`listeners.users` 省略时继承顶层认证，显式空列表关闭继承。按来源地址豁免认证时仍保守标记为已配置。CLI 端点导出、无凭据探测和 Codex builtin 绑定要求无认证入口；需要凭据时采用用户显式选择的地址流程。

`endpoint` 只表达配置意图。`status` 只观察本地文件、进程和监听，不执行核心验证、下载或修复，分别表达：

- `program=missing/present/invalid`；
- `config=missing/readable/invalid`，其中 `readable` 只表示静态检查通过；
- `runtime.state=stopped/starting/running/exited/unknown`，以及监听观察和 `config_changed`。

核心验证结果不持久化成容易失真的健康布尔值。`probe --url URL` 才显式通过已就绪的默认端点测试指定 HTTP/HTTPS 目标，不跟随重定向，也不输出目标路径、查询参数或底层错误。探测绑定同一进程记录快照，响应后复核配置、程序、内核身份和全部代理监听，变化时拒绝报告成功。检查与连接之间没有 OS socket 租约，不能保证已发送的请求绝不遇到替换监听；结果也不能证明所有远端可用。

`nido codex proxy builtin` 在组合层读取默认端点，再沿用 Codex 已有的代理块规则。确认后写入前及代理块写入后复核端点及配置摘要；配置变化时拒绝提交归属记录，并只安全撤销本次代理块修改，不覆盖并发用户编辑。Proxy 停止时仍可绑定，但提示运行状态；不启动组件。修改端口后需重新绑定，Proxy 不主动改写外部 HOME，两个组件没有生命周期依赖。

## 启动、停止与 restart

启动顺序为：取得组件锁 → 捕获程序和配置 → 静态及核心验证 → 安全打开日志 → 提交启动身份 → 授权 exec 核心 → 有限等待进程和全部导出代理监听。日志以 `O_NONBLOCK`、`O_NOFOLLOW` 打开后核验类型、用户和硬链接，FIFO 不能将启动卡在文件打开阶段。

一次性启动器通过私有管道等待授权。父进程先原子提交 PID 与出生信息，才允许其 exec Mihomo；管道 EOF 或授权超时取消启动。启动器不常驻。进程记录同时保存 launcher 和目标核心，使合法的 exec 过渡仍可被 status、stop 识别。存活但未就绪的实例阻止重复派生，身份不明时保留记录并报错。

进程身份核验包含 PID、当前用户、系统启动标识、内核出生信息和可执行路径。Linux 使用 `/proc`，支持时使用 pidfd 将信号绑定具体进程；macOS 从结构化内核接口读取出生信息，通过 `lsof` 核验监听的归属、地址及地址族，不再解析本地化 `ps` 日期。macOS 和缺少 pidfd 的 Linux 仍存在检查与按 PID 发信号之间的窗口，不能宣称原子身份保证。

Linux exec 和线程组退出期间，`/proc` 的两次读取可能不一致。只对已识别的内核暂态有限重读；子项 `exe/stat/status` 消失必须复核整个 PID 目录，不能据此判定进程已退出。僵尸或退出状态还需明确单线程及第二次出生身份、用户和死状态一致，`Threads: 0` 或缺失不作为退出证据。暂态不授权发信号、写入 stopped 或重复启动；等待结束仍无法核验时保留记录。权限错误和未知程序不作为暂态重试。

默认启动就绪期限为 10 秒，只有本实例拥有全部所需代理监听且短暂复核后仍存活才返回成功。每个监听检查、第二次观察及提交前均复核期限，超过期限的成功观察不能授权 running；正在执行的 OS 查询和失败后的安全停止仍可能增加返回耗时，期限不是硬实时返回保证。端口可连接本身不足以证明归属。提前退出提供安全诊断；超时或启动后记录提交失败时，仍按本次已授权的内核出生信息清理新进程，即使记录已被其他写入者替换；变化后的记录不会被覆盖。停止仅发送 SIGTERM 并有限等待，不隐式 SIGKILL。失败或身份不明时保留记录，不能报告假成功。

Linux `/proc/exe` 的删除标记作为映像变化状态保留，报告 unknown 并阻止重复启动，但不妨碍精确停止原出生身份。程序摘要核验的是磁盘文件与启动记录，不等于运行中映像的摘要证明；macOS 的路径与出生信息也不提供这一证明。

重复 start 对配置未变且已就绪的实例幂等；重复 stop 对已确认退出的实例幂等。记录保留最近一次运行历史，后续状态可以区分正常停止与观察到的退出；没有常驻监管者，不承诺永久保存所有退出码。

配置变更通过 restart 生效，不提供热 reload。磁盘配置变化时报告 `config_changed`；restart 先绑定原进程记录，再验证新配置，验证失败保持旧进程。验证期间记录变化时拒绝停止，不能将新记录对应的实例纳入本次重启。停止后启动失败明确报告停机，保留配置和日志，不暗中回退整份配置。

## 安装与停止更新

安装来源为 Mihomo 官方稳定发布或用户提供的本地独立程序/gzip 包。在线获取检查稳定发布身份、平台资产、大小、SHA-256、解压大小和程序格式；候选版本来自 `mihomo -v`，在线候选须与发布 tag 一致。ELF 的动态段、动态节及字符串表必须指向相同的可加载字节，拒绝内嵌 audit/filter 加载扩展；Mach-O 拒绝内嵌动态加载器环境命令。系统库目录本身及其子路径可用于依赖检查，外部目录和非规范路径被拒绝。格式与摘要检查不等于签名验证，也不能隔离恶意程序。

`update --check` 显式执行本地版本查询并联网获取最新发布，按稳定语义版本数值比较，较旧发布不能被当作可用更新；无法比较的开发版本明确报错。不改变组件；普通 status 不执行这一步。下载使用标准代理环境或本次 `--proxy`，不自动启动代理。

实际更新统一要求停止状态：准备私有临时程序 → 检查格式与版本 → 使用候选检查现有配置 → 复核目录、程序、配置和候选快照 → 原子替换。准备或验证失败不覆盖原程序；成功后仍停止，随后由用户显式 start。没有版本仓库和 rollback 命令。

Nido 不调用 `/upgrade`。官方热更新会管理自身工作目录、覆盖程序并异步 exec，不适合作为当前单文件替换边界。采用停止更新也消除了 Nido 操作未知 `meta-update`、`meta-backup` 目录的路径。[官方更新流程](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/updater/update_core.go)。需要借用旧代理下载时，先另行获取本地包，再 stop，通过 `--artifact` 更新。remove 仅移除已停止组件固定程序路径中、格式和执行权限检查通过且快照未变的程序，保留配置、日志、运行历史、缓存和未知文件。该检查不能证明任意原生程序来自某次安装；路径被普通文档或无效程序占用时拒绝删除，不执行文件来探测归属，也不引入安装清单。

默认模板关闭 controller；provider 周期刷新由 Mihomo 负责。当前没有 controller 热更新、手动 provider 刷新或 reload 命令，不删除缓存来模拟刷新。

## 环境、命令与验证边界

版本查询、核心检查和正式启动清除 `CLASH_*`、`SAFE_PATHS`、`SKIP_SAFE_PATH_CHECK` 及 `LD_*`/`DYLD_*` 动态加载器覆盖，防止环境覆盖已检查的配置、路径策略或动态依赖。普通代理和证书环境仍按组件调用传递，Nido 不向 `env.sh` 默认写入 HTTP_PROXY，也不增加 Proxy env/exec 包装层。

| 命令 | 完成条件 |
| --- | --- |
| `proxy install [--artifact FILE]` | 候选程序检查并安装；不配置或启动 |
| `proxy configure [--file FILE]` | 停止状态下验证保存；已有配置不被交互覆盖 |
| `proxy config-path` | 输出固定路径，不创建文件 |
| `proxy validate` | 静态与核心检查通过；不宣称网络可用 |
| `proxy start / stop / restart` | 达到上述明确进程和监听条件 |
| `proxy status [--json]` | 只读本地观察 |
| `proxy endpoint [NAME]` | 输出无歧义、未配置认证的端点；拒绝导出凭据 |
| `proxy logs [--tail N]` | 有界读取普通日志，不执行内容 |
| `proxy probe --url URL` | 显式通过默认端点测试指定目标 |
| `proxy update --check` | 本地版本与远端稳定版查询 |
| `proxy update [--artifact FILE]` | 停止状态完成程序替换，保持停止 |
| `proxy remove` | 已停止，只移除程序 |

`--dry-run` 只做静态和本地预检，不执行核心、不下载、不启动、不写锁或配置；核心验证和远端版本明确保留为未知。JSON 与普通输出共用业务结果，诊断不混入敏感核心原文。

常规隔离测试使用临时根、假核心、发布 HTTP 夹具和回环辅助进程，覆盖配置策略、文件边界、候选快照、进程身份、握手、监听与失败处理。

真实核心测试显式执行：

```sh
NIDO_TEST_MIHOMO=/absolute/path/to/verified/mihomo make check-proxy-real
```

该测试默认跳过，不自动下载核心。需先自行核验官方原生程序，并准备已缓存的 Go 依赖；测试构建关闭模块下载。测试只在临时 HOME、软件根、XDG 及 TMP 目录运行，provider、受限 HTTP 上游、HTTP/HTTPS 目标均使用临时回环端口，所有正向请求都检查上游连接与目标计数，避免直连造成假通过。停止及卸载验证保留临时配置、缓存、未知文件和认证/会话模拟数据。显式运行时缺少内核身份权限会失败；清理无法确认进程退出时保留测试目录和记录。

已有相同源码构建的 CLI 时，可额外设置 `NIDO_TEST_NIDO=/absolute/path/to/nido`；测试会安全读取并复制到自己的临时目录，跳过内部 Go 构建。默认仍从当前源码构建。

已有 Linux Docker engine 时，可从宿主机执行原生 Linux 隔离检查：

```sh
NIDO_TEST_MIHOMO=/absolute/path/to/verified/linux-mihomo make check-proxy-linux
```

该入口按 engine 实际架构交叉编译测试和 CLI，再在同架构 Linux 内核上执行，不请求跨架构模拟。临时 `FROM scratch` 镜像默认仅含四个产物，显式跨版本模式另加一份核验后的目标核心；无需基础镜像、容器内 Go 或系统包。容器使用非 root 用户、只读根目录、临时可执行 `/tmp`、默认 seccomp、私有 PID namespace 和 `--network none`；不挂载宿主目录、不映射端口、不授予额外能力。短命 `--init` 负责回收子进程；不会安装或启动 Docker daemon、systemd 等服务。buildx 状态也定向到本次临时目录。

Linux 入口先运行 Proxy 全包及内核专项，再运行真实核心 E2E；`TestOS` 权限跳过会使入口失败。IPv6 回环在测试 namespace 不可用时只跳过对应子项并明确报告。清理仅使用本次实际 CID 和私有 image tag，不运行全局 prune；清理失败报错并保留记录。默认入口为原生非 race 验证，宿主交叉编译不等于 Linux race 覆盖。

### Linux race 与原生主机入口

race 检查需要 Linux C 工具链。两个显式入口均接受 `NIDO_TEST_LINUX_BIN_DIR`，目录内包含从同一源码构建的 `nido`、`nido.test`、`proxy.test`；预构建输入只会复制到本次临时目录。入口核验静态 ELF 架构；CLI 通过 `go version -m` 核验 package，race 模式还必须具有 `CGO_ENABLED=1`、`-race=true`。Go 1.23 的测试程序缺少这些构建字段，因此改用 `go tool nm` 核验各自的包入口及 race 运行时、TSAN 初始化符号；测试程序必须保留符号表。CLI 及一次性启动器同样启用检测，不能只把父测试程序编译为 race。

以下示例在原生 Linux、已有 Go/GCC/静态 C 链接库及缓存依赖时准备输入，不安装工具链或系统包：

```sh
nido_race_bins=$(mktemp -d)
export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
CGO_ENABLED=1 go build -race -mod=readonly -trimpath -buildvcs=false -ldflags='-linkmode=external -extldflags=-static' -o "$nido_race_bins/nido" ./cmd/nido
CGO_ENABLED=1 go test -c -race -mod=readonly -trimpath -buildvcs=false -ldflags='-linkmode=external -extldflags=-static' -o "$nido_race_bins/nido.test" ./internal/nido
CGO_ENABLED=1 go test -c -race -mod=readonly -trimpath -buildvcs=false -ldflags='-linkmode=external -extldflags=-static' -o "$nido_race_bins/proxy.test" ./internal/components/proxy
NIDO_TEST_LINUX_BIN_DIR="$nido_race_bins" NIDO_TEST_MIHOMO=/absolute/path/to/verified/linux-mihomo make check-proxy-linux-race
```

Docker race 入口默认使用四产物 scratch 镜像，显式跨版本时为五个程序，不复制宿主 libc 或动态加载器；无法生成静态 race ELF 时明确失败，不自动安装依赖。它只运行与现有 engine 同架构的程序。

arm64 还需实际启动 race 运行时，不能仅凭 ELF 和符号核验宣称兼容。Go 1.23 的构建帮助将 linux/arm64 race 限定为 48-bit VMA；在本次内核与默认 seccomp 下先执行静态 race 探针，再通过完整测试中的故意竞争子进程负例确认检测器有效。构建工具可以放在单独的临时容器中，通过 stdin 输入筛选后的源码及离线 vendor，构建时禁网；导出三份程序后，由仅含这些程序及核验核心的入口执行，不向测试容器加入工具链或库。具体版本与结果见审计记录。

原生 Linux 主机已有 bubblewrap 时，`make check-proxy-linux-native` 使用独立脚本执行，默认要求预构建的三个非 race 静态程序；设置 `NIDO_TEST_LINUX_RACE=1` 可复用上面的 race 输入：

```sh
NIDO_TEST_LINUX_RACE=1 NIDO_TEST_LINUX_BIN_DIR="$nido_race_bins" NIDO_TEST_MIHOMO=/absolute/path/to/verified/linux-mihomo make check-proxy-linux-native
```

此入口按 `uname` 核验原生架构，使用现有 bubblewrap 创建新文件系统、PID 和网络空间，仅以只读方式映射本次四个或显式跨版本的五个程序，并提供 `/proc`、私有 `/dev` 和临时 `/tmp` HOME。清空环境及 capabilities；namespace 创建失败直接退出，不回退到宿主执行。不会连接远程主机、下载程序、安装服务或修改已有 HOME。真实核心与辅助进程不会存活于测试 PID namespace 之外。

race 模式为每个隔离实例配置私有 `NIDO_TEST_RACE_REPORT_DIR` 和固定 `GORACE`。测试在全部清理完成后检查该目录，启动器、辅助进程的报告即使未传回父进程退出码也会导致失败；真实 E2E 的私有 CLI 明确使用同一报告目录。目录必须是规范临时根下、属于当前用户的 0700 普通目录，拒绝链接与目录替换。故意触发竞争的独立子进程负例验证检测器退出码 66 及报告收集；其报告在另外的临时目录中清理。默认测试不接受或传递现有宿主 `GORACE` 给真实 CLI。

本次执行到的 Nido、Proxy、测试及启动器 Go 代码启用 race 检测；官方 Mihomo 程序保持原发布字节，未重新编译为 race，不能据此推断其内部并发没有问题。

### 跨版本停机更新验证

显式增加第二份已独立核验的较新官方核心，即可运行 `TestProxyRealMihomoCrossVersion`；未设置时只保留原真实核心流程，跨版本测试跳过：

```sh
NIDO_TEST_MIHOMO=/absolute/path/to/verified/older-mihomo \
NIDO_TEST_MIHOMO_UPDATE=/absolute/path/to/verified/newer-mihomo \
make check-proxy-real
```

Linux 两个入口也接受 `NIDO_TEST_MIHOMO_UPDATE`，仍使用上面的 `NIDO_TEST_LINUX_BIN_DIR` 和 race 开关。显式启用后，输入从四个程序变为五个；新核心同样必须是绝对路径的普通、同架构静态 ELF，复制后才进入私有 namespace，不新增宿主挂载、外部网络或服务。跨版本测试缺少 PASS 或被跳过时，入口失败，旧的预构建测试程序不能忽略新变量后误报成功。两个真实 E2E 共用测试进程时，显式跨版本模式的总预算为普通 6 分钟、race 20 分钟；原 Proxy 全包和单核心模式的预算不变。

测试先用旧核心下载 HTTP provider 缓存，再停机改成引用该缓存的 file provider，并在旧版本实际运行同一配置。停机更新后核验已安装程序 SHA、实际完整稳定版本升序、状态仍 stopped、监听关闭，以及原有配置、缓存、日志、记录和未知文件的字节。随后新核心验证并运行同一配置，检查进程归属、幂等启停与 restart、HTTP/SOCKS/HTTPS 转发；HTTP provider 已拒绝请求且下载计数必须不再增加，旧缓存保持原字节。最后停止并卸载，核对配置、缓存、未知文件和临时认证/会话哨兵保留。运行本身允许更新日志、进程记录及核心数据库，不将更新前全文件快照跨运行比较。

不同程序字节不足以证明跨版本：实际报告的版本也须为不同且升序的完整稳定版本，同版、降级和异常版本后缀均不能通过夹具身份检查。结果只覆盖实际运行的版本对及这份原生配置，不承诺任意版本、降级、所有 provider 格式或其他核心状态的迁移兼容。

实际版本、执行结果和剩余验证范围见[审查记录](proxy-audit.md)。回环验证不代表真实订阅、所有节点协议或其他平台的兼容结论，也不代表已经发布或切换用户服务。

最终修复后的 Linux amd64/arm64 已使用统一冻结源码完成原生静态 race 的 Proxy 全包与真实 CLI/Mihomo E2E 复验；配置、进程归属、启停、provider、HTTP/SOCKS/HTTPS 和停机更新/卸载的数据保留检查通过。后续另行验证了 v1.19.31 → v1.19.32 的停机更新与旧 provider 缓存复用。结果不扩称全项目 race、Mihomo 内部 race、真实 IPv6 转发或任意版本/全部状态迁移；源码、产物摘要及具体证据见审查记录最前的验证节。
