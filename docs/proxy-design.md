# Proxy 组件实现说明

Proxy 是 Nido 内置的本地显式代理组件。实现位于 `internal/components/proxy`：Nido 管理初次安装、原生配置、精确进程和本地端点，运行期操作通过 Mihomo 原生 controller 完成；Mihomo 管理节点协议、规则、provider、连接与缓存。组件自 v0.4.0 提供，quick start 与节点链接解析自 v0.6.0 提供；历史类别/单工具绑定也始于 v0.6.0，已从 Proxy 页面及 CLI 移除，工具侧代理选择由相应工具与根菜单负责。本文描述 v0.10.2 的 Quick Start 配置契约；历史真实核心结果不能替代本轮验证。各阶段结果见[审查记录](proxy-audit.md)。

## 职责与目录

| 对象 | 责任 |
| --- | --- |
| `internal/nido` | 软件根、一级组件页面、输入和输出及组件组合 |
| `internal/components/proxy` | 初次安装、配置与快照、端点、精确启停、受限 controller 操作与本地状态 |
| Mihomo | 完整节点与路由语义、provider 更新、原生稳定版升级、连接、核心数据 |
| `internal/integrations/codex` | 经显式选择，将端点写入自己的受管 `.env` 块 |
| `internal/integrations/gitproxy` | 维护用户级 Git HTTP 代理受管块及精确撤销，不依赖 Proxy 生命周期 |

组件不依赖 Codex、CLI 或旧 `internal/builtin` Proxy。默认使用普通用户权限，只提供本机回环 HTTP/SOCKS 代理，不注册服务、常驻监管或更改全局代理；启停与原生升级均由用户显式发起。多个 Nido 根彼此独立；端口冲突必须失败。

```text
<NIDO_HOME>/proxy/
  mihomo                 # 当前程序
  config.yaml            # 唯一持久主配置，原生 Mihomo YAML
  config.yaml.back       # 最近一次实际替换前的配置原文，固定单份
  process.json           # 最近一次受管启动的身份、阶段与摘要
  output.log             # 核心原始输出
  .lock                  # 稳定的短期操作锁
  providers/             # 默认模板的 provider 缓存路径
  meta-backup/           # 原生核心升级可能创建的固定备份结构
  meta-update/           # 原生核心升级使用的临时结构；不接管未知内容
  ...                    # 核心数据与用户文件
```

Nido 的 `config.toml` 不复制端口、订阅或节点。配置摘要只用于比较磁盘与受管启动快照，不是版本仓库。新建组件目录采用 `0700`，配置、记录和日志采用 `0600`，程序采用 `0700`。修改前检查路径、普通文件、共享硬链接、预期快照及目录身份；Nido 写入使用同目录私有临时文件和原子替换。原生升级的固定工作目录由 Mihomo 使用，Nido 不递归清理未知目录，也不把目录名字本身当作归属证明。

这些检查不构成操作系统沙箱，也不代表已审计 Mihomo 的所有扩展字段。回环监听同样不能隔离同机不同用户，需要隔离时由用户配置认证或系统权限。

## 配置及验证

保留原生 YAML，不另造节点或订阅语言：

- `proxy install` 首次交互安装成功后提供 Quick Start 与 Skip，默认选择 Skip；选择 Quick Start 才开始配置。`--yes` 仅安装，之后可执行 `proxy configure`。安装和配置均不自动启动核心。
- `proxy quickstart` 与兼容别名 `proxy configure` 直接进入 Quick Start，已有配置也可反复运行。高级用户直接编辑 `proxy config-path` 输出的固定 `config.yaml`，再显式验证、reload 或 restart。输入由公共终端驱动统一管理，provider URL、ISP 节点链接等凭据字段使用公共私密输入；确认前显示端点、端口和链路摘要，不展开订阅与节点凭据，stdout 保持独立结果或 JSON。非交互时已有配置只显示路径，无配置则提示需要交互配置或自行编辑。
- Proxy 页面与 CLI 不再提供完整 YAML 导入；`--file` 明确拒绝。高级配置直接编辑固定文件，不复制外部附属资源，也不另建配置编辑器。

Quick Start 在收集输入前捕获配置 revision，运行中允许先收集输入和预览候选；写入前必须明确停止组件，不隐式 stop、restart 或 start。最终保存时，在组件锁内复核停止状态、组件目录身份、程序、原配置、备份与候选快照；交互或验证期间发生的并发修改、安全文件冲突与身份变化均拒绝覆盖。

实际替换已有配置时，先将旧配置的完整原字节保存到固定 `config.yaml.back`，再以同目录私有临时文件原子替换 `config.yaml`；备份只保留一份，每次成功替换覆盖上一份备份，不解析或重排旧 YAML。无旧配置时只新建主配置；候选与现有配置逐字节相同时不写主配置或备份。取消、候选校验失败或保存前复核失败，不改主配置及备份。已有备份也必须通过权限、普通文件、归属及链接检查，不能用不安全对象作为备份目标。

主配置与备份分别原子替换，两个文件不构成跨掉电事务。备份写入失败时不替换主配置；备份成功后，若主配置替换失败或机器掉电，可能留下旧主配置和已更新的备份。Nido 明确报告保存失败，后续不自动回滚，也不将这种状态描述为整次操作原子成功。用户自行编辑固定配置不经过这套自动备份流程。

`QuickStartOptions` 传递 provider URL、日常 filter/端口、可选下载 filter/端口、可选 ISP 链接/端口/前置 filter/前置测试 URL，以及可选 `ControllerConfig`。生成器无文件、网络和进程操作，用 YAML encoder 构造数据，不拼接用户输入。新 CLI 总是提供 controller：默认地址为 `127.0.0.1:9090`，secret 在 CLI 侧用 `crypto/rand` 生成 32 字节并编码，重复 Quick Start 复用安全的既有 controller 地址与 secret。纯生成器的 nil controller 只为旧调用兼容，不表示新 CLI 默认关闭管理功能。业务端口有效且互不重复，controller 不能占用同一代理监听地址和端口；冲突明确失败。filter 必须显式非空，`.*` 表示全部；保留 Mihomo 正则语法，最终由核心验证，不用 Go regexp 错拒绝 lookahead。首次配置仍只允许原子新建，收集输入后被其他写入者创建的文件会保留并导致报错。

共享 HTTP provider 名为 `subscription`，缓存为 `providers/subscription.yaml`，显式 `proxy: DIRECT` 取回订阅。新模板的 provider `interval: 0` 取消定时拉取，由用户显式更新；仍启用 300 秒常规 URL 健康检查，组的 URLTest 行为保持独立。`interval: 0` 不表示禁止启动联网：有效缓存可直接使用，缓存缺失或解析失败时，核心初始化仍可能下载。高级 YAML 的既有 interval 不自动改写，也不通过删除缓存模拟更新。[原生 Fetcher](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/resource/fetcher.go#L53-L106)、[定时条件](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/resource/fetcher.go#L200-L215)。原生 provider 内容仍需用户自行提供，不增加订阅转换器或第三方服务。默认测试 URL 为 `https://www.gstatic.com/generate_204`，三类组按需生成：

| 组 | 候选与筛选 | 测试 |
| --- | --- | --- |
| `Proxy` | provider 的日常 filter | 常规 URL，300 秒 `url-test` |
| `Download` | 可选，provider 的独立下载 filter | 常规 URL，300 秒 `url-test` |
| `ISP-Relay` | 可选，provider 的独立前置 filter | 用户选择前置测试 URL，默认常规 URL，300 秒 `url-test` |

各 provider 组只引用共享 provider，filter 不放到 provider 上，避免相互裁剪候选；空组使用 `REJECT`。ISP 保留分享链接中的节点名称，无名称时使用 `isp-node`；节点与内置目标或推荐组重名时明确拒绝。`endpoint-isp` 直接引用该节点，不再为单个 ISP 节点增加 `url-test` 组。需要前置时，将 ISP 节点的 `dialer-proxy` 设置为 `ISP-Relay`，链路为服务器 → 前置节点 → ISP → 目标；不生成已废弃的 relay 类型组。交互仅在启用前置时询问“ISP 前置测试地址”，对应 `QuickStartISP.TestURL`；该检测由前置候选节点访问目标，用于选择前置节点，不代表完整 ISP 链路健康检查。UDP/QUIC ISP 节点要求前置具备相应承载能力，HTTP 或仅 TCP 前置不因此自动获得 UDP 支持，配置检查也不证明真实链路可达。

生成器默认仅提供 mixed listeners `endpoint-proxy`（7890）、可选 `endpoint-download`（7891）、可选 `endpoint-isp`（7892），均显式监听 `127.0.0.1`。日常、下载入口分别引用 `Proxy`、`Download` 组，ISP 入口直接引用解析出的 ISP 节点；端口来自输入，可改变。默认不生成传统 `port` / `mixed-port` / `socks-port`。新模板另外生成回环 controller 和非空 secret，用于原生运行期管理；controller 不属于业务代理 listener，也不加入工具端点候选。所有 listener 名称都可以修改或删除；之后安装、更新和启停不重新生成用户 YAML，也没有第二份端口/节点配置副本。

规则仅为核心内部 HTTPS 更新增加两个精确目标，存在 `Download` 组时使用它，否则使用 `Proxy`；最后仍是 `MATCH,DIRECT`。普通 listener 流量继续使用 listener 指定的组或节点，不靠这两条规则分流。保留这两条规则是因为原生内核更新按 Mihomo 自身规则出站，仅有 `MATCH,DIRECT` 时不会经过上述代理组：

```yaml
rules:
  - AND,((IN-TYPE,INNER),(DST-PORT,443),(DOMAIN,github.com)),Download
  - AND,((IN-TYPE,INNER),(DST-PORT,443),(DOMAIN,release-assets.githubusercontent.com)),Download
  - MATCH,DIRECT
```

上述建议以 v1.19.32 已初始化的 inner tunnel、正常目标地址和匹配到的官方主机为边界。原生 updater 通过核心内部 HTTP 发出版本及包请求；inner `HandleTcp` 成功返回管道后，后续代理拨号失败不会触发 HTTP helper 直拨重试。helper 的直拨分支是 inner 未初始化或目标地址设置失败等 `HandleTcp` 的同步错误，不能将它描述为“代理失败自动直连”。若官方以后重定向到这两个域名以外的主机，该目标未命中规则时仍会落到 `MATCH,DIRECT`，所以不承诺所有核心网络或未来重定向都经过代理。[原生 updater 请求](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/updater/update_core.go#L164-L188)、[HTTP helper](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/http/http.go#L74-L81)、[INNER 管道及错误边界](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/listener/inner/tcp.go#L21-L40)。

节点链接解析器支持 SS SIP002/旧式 Base64（已支持的 obfs、v2ray-plugin）、VMess Base64 JSON、VLESS、Trojan、Hysteria2/Hy2、HTTP(S)、SOCKS5/SOCKS5H。它按协议拆分认证、地址及参数后逐字段解码一次，保留字面 `+`，例如 `%2D` → `-`、`%252D` → `%2D`；不先对整串解码。未知、重复、同义冲突或协议/传输不适用的字段明确拒绝，不静默忽略；不承诺所有协议扩展。解析不解析 DNS、不执行程序，生成后仍须核心检查。核心验证输出保留既有长度和时间限制；`output.log` 保留核心原文。

SS 分享链接兼容导出器附加的顶层 `type=tcp`，将其归一化为原有 SS 配置，不新增 `network` 或关闭 UDP。SS2022 的百分号转义和冒号分隔密钥链保持原样；其他 `type` 值、空值及插件内部未知字段仍拒绝。该兼容字段不是 [SIP002](https://shadowsocks.org/doc/sip002.html) 的必需参数；密钥链遵循 [SIP023](https://shadowsocks.org/doc/sip023.html)。

| 层次 | 已实现的结论 |
| --- | --- |
| 静态检查 | 最大 4 MiB、单一非空 mapping、无重复键、端口合法、端点名称及地址唯一、已支持入站只监听回环 |
| 核心检查 | 受管程序执行 `-t -d <组件绝对目录> -f -`，输入捕获的原字节；失败不提交配置 |
| 运行确认 | 受管进程仍存活，全部导出代理监听属于该进程，地址及地址族匹配 |

静态检查通过不代表核心接受配置；核心检查通过不代表端口能绑定或远端可达。特别是 `-t` 不会执行 HTTP/file provider 的启动加载，不能将它通过解释为已下载、加载了节点。缺少程序时不能宣称核心验证完成；缺少或空配置时不调用核心生成默认配置。`-t` 仍可能按上游行为访问数据库或缓存，因此仅在已停止后的首次安装、配置保存、显式验证及 Start 预检中调用，不用于普通状态查询或运行期 reload／原生更新。[官方 CLI](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/main.go)、[初始化行为](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/config/initial.go)、[provider 初始化](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/executor/executor.go)。

本地策略支持传统 `mixed-port`、`port`、`socks-port`，以及 `listeners` 的 mixed/http/socks。至少存在一个有效回环代理入口。传统端口在 `allow-lan: false` 时固定采用 IPv4 回环；启用 `allow-lan` 时必须显式绑定字面回环 IP，IPv6 使用带方括号的 `bind-address: '[::1]'`。独立 `listeners` 不受 `allow-lan` 限制，必须逐项显式设置字面回环 `listen`。[传统监听](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/listener/listener.go)、[listeners 默认地址](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/listener/inbound/base.go)。

HTTP/file provider 必须显式设置组件根内的相对 `path`，inline provider 不要求文件路径；不依赖上游按 URL 生成的隐式缓存名。provider、规则和已支持的顶层 TLS 文件资源不能覆盖 `mihomo`、`config.yaml`、`config.yaml.back`、`process.json`、`output.log`、`.lock` 或核心固定缓存与数据库，保留名比较不区分大小写。已有目标不能是链接、共享硬链接或特殊文件；核心固定文件也在静态检查中核验。`tls.custom-certifactes` 按上游拼写接受内联证书字符串列表，内容交给核心检查。

策略控制的 listener/provider 字段要求规范的小写连字符名称，拒绝上游结构解码器接受但会绕过精确字段检查的大小写或下划线别名。传统端口要求 YAML 整数；所有端口拒绝前导零整数，避免 YAML 八进制与导出地址不一致。listeners 仍接受引号包围的十进制端口。[结构解码](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/common/structure/structure.go)、[固定数据路径](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/constant/path.go)。

未知节点、规则及扩展字段保持原字节交核心验证，不递归猜测所有名为 `path` 的字段。上述策略只覆盖显式入站、provider、已支持的顶层资源和已知固定文件。SSH/TLS outbound 的文件、overlay outbound 的状态目录和额外监听，以及远端 provider 动态引入的节点扩展，仍属于 Mihomo 的原生能力；这些检查不能将不可信核心或配置变成文件系统和网络沙箱。

当前明确拒绝启用 TUN、透明代理、其他入站类型、非回环监听、TLS/Reality 代理入站、脚本、外部 UI、controller 的 TLS/Unix/pipe 入口，以及 YAML 别名与合并键。HTTP controller 必须监听字面回环地址并配置非空 secret。高级 YAML 可以不配置 controller；这种配置仍可安装、验证及显式启停，页面只将原生运行期管理显示为不可用，不自动增补配置或启用服务。

## 一级页面与原生运行期管理

Proxy 使用公共动态组件页面的一级布局。确认程序未安装且运行已停止时，页面只提供“安装 Proxy”。已安装页面仅提供 Quick Start、更新内核、更新订阅、重载配置、重启、停止、启动和“删除 Proxy，保留配置与数据”。公共页面每次动作后重新读取状态与动作列表，安装、删除后无需退出页面重进。core 版本、分组选择链和已有延迟属于状态展示，不再增加操作子菜单。同页还只读显示核心列出的 HTTP/file provider 名称、类型及核心报告的更新时间；缺时间标为未报告，不用该时间推断刷新成功、节点健康或网络可达。

Proxy 不提供工具代理绑定、YAML 导入、文件日志菜单或相应 CLI；`proxy bind`、`--tool`、`--file` 和 `proxy logs` 均不作为可用接口。工具代理的 `chooseProxy` 由 Codex／根菜单负责。高级用户编辑页面显示的固定 `config.yaml`，日志直接查看页面显示的 `output.log` 路径。没有 listener 探测、节点编辑、selector 切换、测速榜或连接面板；保留的低层只读诊断命令不意味着这些功能进入页面。

键盘、输入、确认、任务取消和终端恢复均归公共 v2 Session 所有，Proxy 不另建终端运行时或生产明文输入后备路径；凭据直接调用公共 `secretPrompt` 桥，显式注入的 ScriptDriver 只用于隔离测试。controller 请求、首次 Install 及 CheckUpdate 接入公共任务 Context，但取消只能停止本地等待和后续请求，不能撤销已由核心接受的 provider 更新、reload 或原生升级；结果不确定时保留原有“未确认”边界。

运行期客户端只访问当前根内受管实例的回环 controller，使用配置中的 secret；它不从用户输入接收任意控制 URL，不走环境代理或 Nido 的下载网络模式，不跟随重定向。每次操作核对程序、配置、进程出生身份与 controller 归属，操作结束重新确认实例。secret 的 Bearer 认证是 Mihomo 的原生全权限认证；Nido 限定的是自身调用，不声称核心服务器也只开放这份白名单。[认证与原生路由](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/server.go#L112-L135)。

provider 页面读取 `GET /providers/proxies`，显式更新使用对应名称的 `PUT /providers/proxies/{name}`；规则 provider 使用 `GET /providers/rules` 和 `PUT /providers/rules/{name}`，不生造单项 GET 路由。更新仅接受核心已列出的 HTTP/file provider，不把 Inline 或内部 Compatible provider 当作可下载订阅。批量更新逐项报告成功、失败或取消后未执行，不以部分成功冒称全部成功。读取和更新各自的 HTTP 结果分开判断，204 不代表节点可达。健康检查、节点延迟与 provider 下载是不同能力，本轮页面不为了展示延迟主动发测速请求。[官方 provider 路由](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/provider.go#L16-L62)、[规则 provider](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/provider.go#L106-L151)。

分组展示只按 `GET /proxies` 报告的 `now` 递归展开当前选择链至最终节点，读取已有 history 的最近延迟及时间；不修改节点或策略组，不发延迟请求，也不将各跳延迟相加。负载均衡、未知类型、循环、缺失节点或未测量分别标明，不用 `DIRECT` 或零延迟冒充成功。选择链不等同于完整网络链路，不据此推断 ISP `dialer-proxy` 前置链。API、返回字段及核心原生升级的兼容边界以已核对的 v1.19.32 为基准，不把未验证版本的相同路径当作完整兼容证明。

## 端点与状态

`Endpoint` 返回名称、协议、回环 host、端口、认证标记及来源配置摘要，不含凭据。`endpoint [NAME]` 不指定名称时仅允许唯一候选，不再优先 `common`；多个入口必须明确选择。传统端口仍名为 `mixed`、`http`、`socks` 并参与运行归属检查，但 `ListenerEndpoints` / `proxy endpoints --json` 只导出原生 listeners，快捷绑定不得从传统端口猜测候选。mixed/http 导出 HTTP URL，SOCKS 导出 `socks5h` URL；没有保留的 listener 名称。

认证标记表示配置了认证；`listeners.users` 省略时继承顶层认证，显式空列表关闭继承。按来源地址豁免认证时仍保守标记为已配置。单端点导出和无凭据探测拒绝认证入口；绑定流程可通过公共私密字段输入认证信息，非交互须提供完整自定义地址，不从 YAML 提取认证信息。

`endpoint` 只表达配置意图。`status` 只观察本地文件、进程和监听，不执行核心验证、下载或修复，分别表达：

- `program=missing/present/invalid`；
- `config=missing/readable/invalid`，其中 `readable` 只表示静态检查通过；
- `runtime.state=stopped/starting/running/exited/unknown`，以及监听观察和 `config_changed`。

核心验证结果不持久化成容易失真的健康布尔值。`probe --endpoint NAME --url URL` 才显式通过已就绪端点测试指定 HTTP/HTTPS 目标，无名称时要求唯一，不跟随重定向。探测绑定同一进程记录快照，响应后复核配置、程序、内核身份和全部代理监听，变化时拒绝报告成功。检查与连接之间没有 OS socket 租约，不能保证已发送的请求绝不遇到替换监听；结果也不能证明所有远端可用。

工具使用代理属于调用方职责，Proxy 仅导出 listeners。Codex／根菜单的 `chooseProxy` 组合端点选择与自定义地址输入：没有 listener 时仍可让用户输入 `protocol://[username:password@]host:port`，不会为了工具配置安装或启动 Proxy。类别、Git 或 Nido 网络配置的历史入口不再由 Proxy 页面及 CLI 承担；具体工具支持以其各自文档为准。

Nido 主 `config.toml` 的 Download 配置保存两个独立字段：`GitHubAccelerate` 负责 GitHub 文件路线，取 `none`、`proxy` 或无认证的 HTTPS 前缀；`ProxyURL` 负责 API 和其他 Nido HTTP 的通用代理，空值表示直连。二者可以同时配置，`--github-proxy` 与 `--proxy` 不是互斥模式，也不把代理叠加到前缀连接上：

| GitHubAccelerate | GitHub 文件请求 | API／其他 Nido HTTP |
| --- | --- | --- |
| `none` | 直连官方文件 URL | 独立使用 ProxyURL，未设置则直连 |
| `proxy` | 通过 ProxyURL 请求官方文件 URL；缺 ProxyURL 时拒绝 | 独立使用 ProxyURL |
| HTTPS 前缀 | 直连此前缀拼接的官方文件 URL，不叠加 ProxyURL | 独立使用 ProxyURL，未设置则直连 |

`--proxy none` 可对当前动作明确禁用通用代理，但不能让缺少 ProxyURL 的 `GitHubAccelerate=proxy` 静默变为直连。固定版本核心首次安装只构造文件路线，API 请求数为零，不需要为它先创建 APIHTTP。显式版本查询才使用独立 API 路线；坏文件前缀也不应阻断无关的 API 请求。没有 Auto、自动探测或失败回退。

旧 `download-proxy.toml` sidecar 已退役，不再读取、迁移、重写、删除或采用其中的 listener 路线；保留旧文件名只用于诊断及防止覆盖未知用户数据。少量可唯一解释的旧主配置字段只在读取时做窄转换，新写入仅保存上述两个字段；旧主配置的 listener 名称不能沿用，须重新填写 ProxyURL。受管 listener 在 Codex 的代理选择中只是一个候选 URL，Nido 不将它保存为持久 listener 名称，也不通过旧 sidecar 隐式绑定本组件。

Nido 请求客户端明确忽略环境代理，按当前动作构造并复核主配置快照。本地 artifact、dry-run 与普通只读不构造联网客户端。网络设置不写全局代理环境，也不替用户修改 Codex、Git 或系统代理；涉及外部安装命令时，通用 ProxyURL 的进程环境由相应调用方显式构造。长驻菜单每个 Proxy 动作完成后恢复原 HTTP/API 客户端及策略，关闭临时空闲连接，避免前一动作的路线污染下一动作。配置不代表代理已就绪；status/doctor 不联网。旧三模式与 v0.8.2 sidecar 的隔离证据只属于对应历史实现。

Codex 写入已关联 HOME 的受管 `.env` 块，工具侧快捷选择只从 listeners 读取候选并始终支持自定义；唯一无认证 listener 可按该工具的显式非交互协议采用，多候选必须明确选择。确认后写入前及写入后复核所选端点与配置摘要；变化时拒绝提交归属记录，并只撤销本次可证明未被并发修改的代理块。

Git 集成仅维护用户级配置末尾的 `[http] proxy` 标记块和精确收据，保留其他字节；读取 `GIT_CONFIG_GLOBAL` 或默认用户级路径，并检查 XDG 全局配置冲突。它不执行 Git，不解析系统/仓库配置，不处理 SSH 远端；用户级 include、外部已有代理或被编辑的受管块会拒绝写入。保存收据失败时只撤销仍与本次快照一致的修改，不覆盖并发编辑。工具配置不是全局代理设置，不能保证压过 Git 自己的仓库/系统/环境优先级。

## 启动、停止与 restart

启动顺序为：取得组件锁 → 捕获程序和配置 → 静态及核心验证 → 安全打开日志 → 提交启动身份 → 授权 exec 核心 → 有限等待进程和全部导出代理监听。日志以 `O_NONBLOCK`、`O_NOFOLLOW` 打开后核验类型、用户和硬链接，FIFO 不能将启动卡在文件打开阶段。

一次性启动器通过私有管道等待授权。父进程先原子提交 PID 与出生信息，才允许其 exec Mihomo；管道 EOF 或授权超时取消启动。启动器不常驻。进程记录同时保存 launcher 和目标核心，使合法的 exec 过渡仍可被 status、stop 识别。存活但未就绪的实例阻止重复派生，身份不明时保留记录并报错。

进程身份核验包含 PID、当前用户、系统启动标识、内核出生信息和可执行路径。Linux 使用 `/proc`，支持时使用 pidfd 将信号绑定具体进程；macOS 从结构化内核接口读取出生信息，通过 `lsof` 核验监听的归属、地址及地址族，不再解析本地化 `ps` 日期。macOS 和缺少 pidfd 的 Linux 仍存在检查与按 PID 发信号之间的窗口，不能宣称原子身份保证。

Linux exec 和线程组退出期间，`/proc` 的两次读取可能不一致。只对已识别的内核暂态有限重读；子项 `exe/stat/status` 消失必须复核整个 PID 目录，不能据此判定进程已退出。僵尸或退出状态还需明确单线程及第二次出生身份、用户和死状态一致，`Threads: 0` 或缺失不作为退出证据。暂态不授权发信号、写入 stopped 或重复启动；等待结束仍无法核验时保留记录。权限错误和未知程序不作为暂态重试。

默认启动就绪期限为 10 秒，只有本实例拥有全部所需代理监听且短暂复核后仍存活才返回成功。每个监听检查、第二次观察及提交前均复核期限，超过期限的成功观察不能授权 running；正在执行的 OS 查询和失败后的安全停止仍可能增加返回耗时，期限不是硬实时返回保证。端口可连接本身不足以证明归属。提前退出提供安全诊断；超时或启动后记录提交失败时，仍按本次已授权的内核出生信息清理新进程，即使记录已被其他写入者替换；变化后的记录不会被覆盖。停止仅发送 SIGTERM 并有限等待，不隐式 SIGKILL。失败或身份不明时保留记录，不能报告假成功。

Linux `/proc/exe` 的删除标记作为映像变化状态保留，报告 unknown 并阻止重复启动，但不妨碍精确停止原出生身份。程序摘要核验的是磁盘文件与启动记录，不等于运行中映像的摘要证明；macOS 的路径与出生信息也不提供这一证明。

重复 start 对配置未变且已就绪的实例幂等；重复 stop 对已确认退出的实例幂等。记录保留最近一次运行历史，后续状态可以区分正常停止与观察到的退出；没有常驻监管者，不承诺永久保存所有退出码。

磁盘配置变化时报告 `config_changed`。配置文件写入仍须先明确停止，不因增加 controller 而开放运行中覆盖文件。显式 restart 先做静态检查，再停止原受管实例，由 Start 在停止后执行核心配置检查并启动；不在运行实例旁执行可能触碰共享缓存的 `-t`。停止后检查或启动失败明确报告停机，保留配置和日志，不暗中回退或恢复运行。运行中的 validate 同样仅做静态检查，并明确核心检查尚未完成。

显式 reload 使用固定 `PUT /configs?force=true`，JSON 仅为当前根内 `config.yaml` 的绝对 `path`，不接受任意路径、payload 或 force 选项。controller 地址、secret 摘要等控制字段须与受管启动快照一致；这些字段变化时要求本地 restart。旧运行记录只在完整配置摘要仍匹配时绑定 controller，不猜测已改变的 secret。重载先经过 Nido 的配置策略及快照检查，之后由核心应用，成功后复核实例与所需监听再更新记录。[官方参数](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/configs.go#L385-L430)。

`force=true` 用来应用本地策略支持的传统端口、allow-lan 与 bind-address；`force=false` 仍会应用独立 listeners，不能将它理解为跳过所有监听变化。reload 不重建 controller，也不保存磁盘配置。重载可能暂时拒绝新连接、关闭发生变化的监听及使用对应 provider 的既有连接；不承诺无损。原生 ApplyConfig 对部分加载失败只记日志，204 不能证明完整 YAML 的所有语义生效；没有完整配置读回 API，也没有 `/listeners` 路由。请求失败或执行后复核不通过时明确报告状态未确认，不自动重试、回滚或 restart。[执行顺序与 force](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/executor/executor.go#L79-L115)、[监听更新](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/executor/executor.go#L177-L198)、[provider 连接处理](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/adapter/provider/provider.go#L143-L165)。

## 初次安装与核心原生更新

首次在线安装的空来源及兼容字符串 `latest` 均使用 Nido 内置、已核验的 **Mihomo v1.19.32**，不是运行时查询的最新版本。私有固定表记录 Linux/macOS amd64/arm64 的官方资产名、压缩包大小和 SHA-256，不增加可变 Service 描述符；Linux amd64 固定为 `mihomo-linux-amd64-compatible-v1.19.32.gz`、22,877,876 字节、SHA-256 `ba3ce607747a07f948fc35780e108a4a7c7f552a38b9bd4d115f313ebcb89c20`。2026-10-19 已重新通过[官方 Release API](https://api.github.com/repos/MetaCubeX/mihomo/releases/tags/v1.19.32) 核验四平台名称、精确大小及摘要，并用[官方资产列表](https://github.com/MetaCubeX/mihomo/releases/expanded_assets/v1.19.32) 交叉核对 SHA；Linux amd64 的纯 HEAD 响应另确认精确 Content-Length。没有下载或执行真实核心。

安装也接受用户提供的本地独立程序/gzip 包。在线下载使用固定官方版本 URL，先核大小和压缩包 SHA-256，再限制解压大小、检查原生格式并核对 `mihomo -v` 与固定版本一致。ELF 的动态段、动态节及字符串表必须指向相同的可加载字节，拒绝内嵌 audit/filter 加载扩展；Mach-O 拒绝内嵌动态加载器环境命令。系统库目录本身及其子路径可用于依赖检查，外部目录和非规范路径被拒绝。格式与摘要检查不等于签名验证，也不能隔离恶意程序。

首次在线安装只使用文件下载 HTTP 与明确的 GitHubAccelerate policy，不调用 GitHub Release API 或构造 APIHTTP，避免尚无 Proxy 时的 API 自举循环。若用户显式选择 `GitHubAccelerate=proxy`，其 ProxyURL 仍须可用，失败不回退；HTTPS 前缀与独立 ProxyURL 可同时保存，但文件请求只直连前缀。`Latest`／`CheckUpdate` 及 `proxy update --check` 保留为显式高级版本查询，使用独立 ProxyURL 构造 API 客户端，但不参与安装；普通 status 不联网查询。菜单动作结束恢复原客户端与策略。核心原生更新的联网由 Mihomo 自己执行，不继承 Nido 的文件加速或通用代理设置，也不由 Nido另建下载或替换后备路径；新 Quick Start 只通过上述两条 INNER 规则建议其出站组。

常规更新统一调用受管 controller 的 `POST /upgrade?channel=release`，不传 force。目前只在 Linux amd64/arm64 启用；macOS 无等价运行映像文件身份核验时，在发送请求前明确不可用。200 只表示请求已被接收，不能直接报告更新成功；Nido 继续有限等待核心完成原生替换和 exec，核对同一受管出生身份、固定程序的原生格式与版本、Linux `/proc/<pid>/exe` 与固定程序的文件身份、controller 及全部所需代理监听，并要求稳定的再次观察，才刷新受管记录。不能证明归属或完成时报告未确认并保留记录，不接管陌生进程，也不另起核心掩盖失败。[官方升级请求](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/upgrade.go#L24-L49)。

本轮删除重复的 PrepareUpdate/CommitUpdate 下载与停机替换机制；更新不接受本地 `--artifact` 或校验参数，首次 Install 的本地程序/gzip 导入仍保留。核心原生升级使用上游下载、备份和替换流程，Nido 不声称独立校验了该次上游资产的 SHA-256，也不承诺失败时核心未触碰程序。更新期间可能中断连接；同版更新不加 force 强行重新覆盖，没有 Nido 版本仓库或 rollback 命令。

原生升级前，`meta-update` 必须不存在；`meta-backup` 允许缺失，或仅包含可核验的固定核心备份结构，使正常重复升级不被自己的旧备份阻断。已有目录和程序须为本 UID、普通对象，无符号链接或共享硬链接；备份程序须满足原生格式要求。未知文件、其他目录结构或归属不明时拒绝调用上游，Nido 不主动清理这些目录。[上游固定结构](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/updater/update_core.go#L117-L159)。

remove 在同一组件锁内捕获固定程序与原运行记录，精确停止记录中的出生实例、确认退出后才删除该程序。停止失败、进程身份不明、程序或记录快照发生变化时均保留程序，不能先删后停或改认另一实例。程序必须通过原生格式、执行权限及安全文件检查；路径被普通文档或无效程序占用时拒绝删除，不执行文件来探测归属，也不引入安装清单。

删除只移除 `mihomo`，保留主配置、`config.yaml.back`、provider、缓存、日志、运行历史、原生升级备份及未知文件；停止本身可将受管运行记录更新为已停止。再次安装复用已有配置和所有保留数据，不重新生成 YAML、重置 provider 或清空日志；安装仍保持停止。文件格式不能证明某个任意原生程序确由历史某次 Nido 安装，这一归属边界不变。

## 环境、命令与验证边界

当前共享 v2 终端框架使项目最低 Go 版本为 **1.26.0**，本轮整合使用 Go 1.27.2。后文 Go 1.23 的构建、race 和元数据处理说明仅记录对应历史实现与历史入口，不代表当前源码仍支持该工具链；新的本地及 Linux 构建须满足当前 `go.mod` 下限。

版本查询、核心检查和正式启动清除 `CLASH_*`、`SAFE_PATHS`、`SKIP_SAFE_PATH_CHECK` 及 `LD_*`/`DYLD_*` 动态加载器覆盖，防止环境覆盖已检查的配置、路径策略或动态依赖。普通代理和证书环境仍按组件调用传递，Nido 不向 `env.sh` 默认写入 HTTP_PROXY，也不增加 Proxy env/exec 包装层。

| 命令 | 完成条件 |
| --- | --- |
| `proxy install [--artifact FILE]` | 候选程序检查并安装；首次交互安装后选择 Quick Start 或默认 Skip，`--yes` 只安装；不启动 |
| `proxy configure` | Quick Start 的兼容入口；不接受 YAML 文件导入 |
| `proxy quickstart` | 显式直接进入重复 Quick Start，沿用相同快照、备份及停止边界 |
| `proxy config-path` | 输出固定路径，不创建文件 |
| `proxy validate` | 停止时完成静态及核心检查；运行中只做静态检查并提示核心检查未知；不宣称网络可用 |
| `proxy start / stop / restart` | 达到上述明确进程和监听条件 |
| `proxy status [--json]` | 只读本地观察 |
| `proxy groups [--json]` | 经受管 controller 读取 core 版本、选择链和已有节点延迟，不测速 |
| `proxy providers [--json]` | 经受管 controller 读取 proxy/rule provider 列表 |
| `proxy provider-update [all\|proxy NAME\|rule NAME]` | 显式刷新已有 HTTP/file provider；无参数只在交互下选择，逐项报告结果 |
| `proxy reload` | 固定配置原生重载并复核实例和代理监听，可能影响连接；不重新执行核心或保存配置 |
| `proxy endpoint [NAME]` | 输出无歧义、未配置认证的端点；拒绝导出凭据 |
| `proxy endpoints [--json]` | 只读列出 listeners，缺失时提示并返回空列表 |
| `proxy probe [--endpoint NAME] --url URL` | 显式选择端点测试指定目标，无名称时要求唯一 |
| `proxy update --check` | 本地版本与远端稳定版查询 |
| `proxy update` | 调用原生 release 升级；确认核心完成 exec 与受管身份、程序及监听后才报告完成，不接受本地包更新 |
| `proxy remove` | 同锁精确停止并确认退出后只删除程序；失败不删，重装复用配置与数据 |

`--dry-run` 只做静态和本地预检，不执行核心、不下载、不启动、不写锁或配置；核心验证和远端版本明确保留为未知。JSON 与普通输出共用业务结果，诊断不混入敏感核心原文。

常规隔离测试使用临时根、假核心、发布 HTTP 夹具和回环辅助进程，覆盖配置策略、文件边界、候选快照、进程身份、握手、监听与失败处理。

本轮体验后精简、固定首装与安全删除仅使用假核心、临时 HOME 和本地 API 或纯 mock 夹具；不运行真实 Mihomo、SSH、发行包或公网安装。官方元数据及 HEAD 核验没有下载核心正文。测试区分原生请求受理、exec 完成、进程归属和实际监听，不能以一次 HTTP 成功替代生命周期完成。本轮结果见[审查记录](proxy-audit.md)，不将下列历史真实核心或发行结果扩称为当前实现的兼容性结论。

当前 Go 1.27.2 定向 race 已通过 Remove 生命周期 2.411 秒、固定安装与取消 1.867 秒、Quick Start／整改 4.763 秒；Linux 验证入口纯 mock 19.496 秒通过。后续页面／CLI 专项 3.726 秒、新规则专项 2.000 秒、首次安装单次确认／API 零请求／`--proxy none`／前缀与独立 ProxyURL 共存专项 2.347 秒均通过；最终包含同页 HTTP/file provider 名称、类型及核心更新时间、动态动作、删除和固定首装的专项 2.981 秒通过。真实 QS 夹具迁移只完成编译、三个纯辅助用例 race 1.502 秒，以及 Linux amd64 测试程序交叉编译，没有运行真实核心。固定下载变更的格式和差异检查无输出。上述结果不扩称整包、全项目或 Linux 原生通过；Dev shell 的最终真实 PTY、原生 Linux 与整体 CI 仍待统一验收，尚不能据此形成发布结论。

以下为同日精简前的历史阶段证据，仅对应当时源码：服务 Context 完成后，Go 1.27.2 Proxy 整包 race 14.164 秒通过；Nido 的 Proxy、APIBindingAudit、APIProxy 专项 race 66.061 秒通过，随后删除未来 TaskContext 分支和旧 Interactive 测试字段的定向 race 3.391 秒通过。相关源码格式/差异检查及 Proxy、Nido 的 vet 通过。Linux amd64 的 CGO=0 CLI、Nido 与 Proxy 测试程序仅完成交叉编译，没有原生执行。

该精简前阶段的 race CLI 与惰性假核心完整真实 PTY 流程 26.18 秒通过，日志为 `build/proxy-native-20261019/cli-tui-fixed.log`，同时包含默认 Skip/重复配置、方向键/Enter/隐藏输入/Esc、主 TOML Mode/Listener 与下载绑定、CLI 隔离四类完成标记。夹具修复了 Bash printf 八进制吞并后随数字的问题，改为四位 octal；含默认值的真实 PTY 惰性 UI 契约 race 3.209 秒通过。此历史结果不覆盖后续删除 Proxy 绑定、固定首装和动态页面的改动，不能替代它们的最终 PTY、原生 Linux 或 CI 验收；后续工作由 Dev 根会话统一完成。

controller 改造前的 2026-10-19 本地阶段在 macOS arm64 和临时 HOME 内完成：Go 1.27.2 的 Proxy 整包 race 通过（10.496 秒），Go 1.23.12 的 Proxy 整包 race 通过（10.719 秒）；Nido 的 `TestProxy(CLI|QuickStart|Remediation|Bind)` 及菜单前参数拒绝专项分别通过两版 race（34.768 / 33.951 秒）。其中两阶段停机更新的通过结果对应已替换的旧机制，不能证明新的原生升级。该阶段还验证客户端路由恢复、TLS 与重定向边界、实际子 PTY 和退出码、Linux 入口的缺失证据拒绝，完整 CLI＋假核心脚本、vet、格式/差异与 shell 语法检查通过。Linux amd64 仅静态交叉编译，未执行原生 Linux、真实 Mihomo、发行包或公网安装；这些边界继续适用。

### 历史 Linux amd64 公网发行包与安装验证（本轮未执行）

本节保留既有发行验收入口和历史兼容协议；本轮未发布，也没有执行公网安装。精简后的 Proxy 已移除绑定菜单与 CLI，下述历史包的类别／单工具绑定结果不作为本轮菜单验收条件；新包须使用同一源码的最终夹具重新验收。

发布后的显式验收使用现成的普通用户 Linux amd64、bubblewrap、curl、tar 及校验工具，不安装工具或系统服务。事先独立核验并将以下五个公开输入放入同一目录：该版本的 `install.sh`、`nido-linux-amd64.tar.gz` 与同名 `.sha256`、从对应源码构建的 `tests/fixtures/proxy-core` 假核心（命名为 `fake-mihomo`），以及同一源码的 `tests/proxy-quickstart-cli-isolation.sh`。

```sh
NIDO_TEST_PROXY_RELEASE_INPUTS=/absolute/verified-public-inputs \
NIDO_TEST_PROXY_RELEASE_VERSION=0.6.0 \
NIDO_TEST_PROXY_RELEASE_BINDING_MODE=legacy \
NIDO_TEST_PROXY_RELEASE_OUTPUT=/absolute/new-evidence-directory \
bash tests/proxy-public-release-isolation.sh
```

历史脚本的 `NIDO_TEST_PROXY_RELEASE_BINDING_MODE=current|legacy` 是当时绑定协议的兼容标签，名称 `current` 不代表本轮产品契约；上例的 `legacy` 保留旧包 `no_targets` 行为。本轮已经移除 Proxy 绑定，并改用两个独立网络字段，不能将这套旧绑定判定直接用于新包。复现历史发行须使用对应冻结源码；未来新包的菜单、字段与完成标记须以同一源码的最终夹具重新验收，不能通过旧标签冒充兼容。

可选输入 `nido-previous.tar.gz` 与 `NIDO_TEST_PROXY_PREVIOUS_VERSION=0.5.0` 必须同时提供；旧包同样应先独立核验大小与摘要。入口只流提取其中唯一的 `nido` 成员，在临时旧根初始化后添加配置注释与未知文件，再验证公网升级逐字节保留配置、环境文件与未知数据。需要出网代理时，只接受为本轮测试创建的 `NIDO_TEST_PROXY_RELEASE_SOCKS=socks5h://127.0.0.1:PORT`，不继承用户代理环境。

脚本使用私有 HOME、文件系统、PID/user namespace、清空环境并删除 capabilities；不挂载宿主 HOME、凭据、会话、数据库、GPU 或服务 socket。该公网验收显式共享网络以取得真实 HTTPS 响应，不宣称网络隔离。curl 桥只允许官方仓库该精确版本与 latest 的 Linux amd64 Nido 包及其校验文件，限制输出到本次安装临时目录，并将每份真实响应与预先核验的输入逐字节比较，不用本地内容代替公网响应。latest 必须指向受验版本。

历史验证包含默认目录首次安装、重复安装、latest、含空格的自定义目录、可选旧版升级，以及发行包 CLI 的版本、摘要、help、只读 Proxy 状态/端点和安装预览。每次安装实际 source 自己的 `env.sh` 并确认 `command -v nido` 指向该临时根。当时的公开包夹具还运行 quick start、节点解析和类别/单工具配置；这些已经删除的业务入口不再作为新包可用功能或当前验收条件。惰性核心只接受版本/配置验证并拒绝启动，**不计作真实 Mihomo 兼容性或转发证据**。所有认证、会话、数据库、外部代理、Git 与 shell 模拟数据需逐字节保留。历史证据目录保留输入摘要、环境、真实下载清单、安装与 CLI 日志，真实核心结果另行记录。

### Quick start 的 Linux amd64 离线隔离验证

Quick Start 的离线隔离保留独立入口，要求已有普通用户 Linux amd64、Go SDK、GCC、bubblewrap 和完整离线 vendor 源码，不下载、安装工具或使用真实 Mihomo。入口及夹具须来自同一源码；本轮去除 Proxy 绑定后的真实 PTY 最终复验尚待 Dev 完成，旧类别／单工具绑定标记只属于历史阶段：

```sh
NIDO_TEST_QUICKSTART_SOURCE=/absolute/offline-vendored-source \
NIDO_TEST_QUICKSTART_GO=/absolute/go/bin/go \
NIDO_TEST_QUICKSTART_OUTPUT=/absolute/new-evidence-directory \
make check-proxy-quickstart-linux-native
```

入口只复制公开源码白名单与 vendor，源码/SDK 在 namespace 内只读；临时 HOME、软件根及构建缓存可写，宿主 HOME、认证、会话、数据库和服务 socket 不挂载。使用私有文件系统/PID/user namespace；网络与宿主共享，所有夹具只使用临时回环端口，不宣称网络隔离。清空继承环境和真实核心输入，强制 vendor、关闭模块下载。

Proxy 包、节点解析与 Nido 定向测试启用 race；真实核心专项在选择阶段排除，不以跳过冒充通过。构建 race CLI 后执行同源码 CLI 和 Quick Start 夹具，假核心只接受版本和配置验证，拒绝启动。必须有测试包和指定用例的 PASS 及两份 CLI 完成标记。跳过白名单仅限 Proxy 包中 `TestLinuxKernelIPv6AndWildcardOwnership/IPv6_loopback` 与 `/IPv6_wildcard`；名称、包、日志及同用例输出中的 IPv6 不可用消息必须精确匹配，并含已分类的地址族/协议不支持、无可用地址、无设备或无路由系统错误，其他跳过、缺少理由或任何失败均拒绝。允许的两项只记录为未覆盖，在输出、环境元数据、完整 JSON/日志及 `environment-skips.txt` 保留理由和跳过记录，不计为 IPv6 PASS，不修改宿主网络。缺工具或 namespace 拒绝直接失败，不回退宿主执行。新证据目录必须不存在，完整日志、JSON、源码摘要和环境元数据保留，成功或失败只清理本次临时目录。

### Quick start 的 Linux amd64 真实核心隔离入口（当前未执行）

本节保留显式真实核心入口，历史通过结果只对应当时源码。当前真实 QS 夹具已迁移为纯 `QuickStart` 生成，再由 `Service.Configure` 保存捕获配置，不再驱动已删除的旧 PTY 配置菜单或 `proxy bind`；生命周期和回环转发仍由对应夹具检查。本轮只编译当前夹具，三个纯辅助用例 race 1.502 秒及 Linux amd64 测试程序交叉编译通过，没有执行真实 Mihomo。未来显式验收须准备同一源码构建的 Linux amd64 静态 race `nido`、`nido.test`（沿用下方构建命令），以及已自行核验摘要的本地官方 Mihomo；主机已有 Go 与 bubblewrap 时执行：

```sh
GO=/absolute/go/bin/go \
NIDO_TEST_LINUX_BIN_DIR=/absolute/static-race-binaries \
NIDO_TEST_MIHOMO=/absolute/verified/linux-amd64-mihomo \
NIDO_TEST_QUICKSTART_REAL_OUTPUT=/absolute/new-evidence-directory \
make check-proxy-quickstart-real-linux
```

入口限定原生 Linux amd64 普通用户，核验三份输入的静态 ELF 架构、CLI package/race 元数据，以及测试程序中 `TestProxyRealMihomoQuickStart`、race 和 TSAN 符号。仅以只读方式映射这三份程序，另有本次新建的可写临时根、私有 `/proc` 和设备；不挂载源码、宿主 HOME、库、凭据或服务 socket。清空环境、删除 capabilities，使用独立文件系统/PID/user/网络 namespace；测试只在其回环临时端口通信。不会下载、构建、安装工具、调用服务管理器或回退宿主执行。

先在 namespace 内查询 Mihomo 版本，再精确运行新用例；要求父用例和全部 17 个节点配置子项各有精确名称的 PASS，防止尚无节点矩阵的旧版同名测试程序冒充本轮证据。缺少任一子项或进程最终 PASS，任何 skip/fail、命令非零退出或 race 报告都会失败。输出目录必须不存在，保留三份输入摘要、类型、构建元数据、环境、Go/Mihomo 版本及完整测试日志；子进程 race 报告在失败后单独保留。退出只删除本次临时目录。该入口不包含历史跨版本更新专项，实际覆盖的节点协议与结果见[审计记录](proxy-audit.md)。

以下是已有真实核心验证入口；历史通过结果不能替代新增功能的真实兼容性验证。真实核心测试显式执行：

```sh
NIDO_TEST_MIHOMO=/absolute/path/to/verified/mihomo make check-proxy-real
```

该测试默认跳过，不自动下载核心。需先自行核验官方原生程序，并准备已缓存的 Go 依赖；测试构建关闭模块下载。测试只在临时 HOME、软件根、XDG 及 TMP 目录运行，provider、受限 HTTP 上游、HTTP/HTTPS 目标均使用临时回环端口，所有正向请求都检查上游连接与目标计数，避免直连造成假通过。停止及卸载验证保留临时配置、缓存、未知文件和认证/会话模拟数据。显式运行时缺少内核身份权限会失败；清理无法确认进程退出时保留测试目录和记录。

当前夹具与新生命周期契约同步：旧 `proxy update --artifact ...` 改为明确拒绝，并核对原程序和数据不变；不再把本地包替换当作原生 updater 的成功验证。restart 的核心语义错误在停止旧出生实例后才由 `-t` 检出，失败保持停止；恢复原配置字节后，仍须显式 start。以下入口说明的是可执行测试范围，本轮没有运行这些真实核心用例。

已有相同源码构建的 CLI 时，可额外设置 `NIDO_TEST_NIDO=/absolute/path/to/nido`；测试会安全读取并复制到自己的临时目录，跳过内部 Go 构建。默认仍从当前源码构建。

已有 Linux Docker engine 时，可从宿主机执行原生 Linux 隔离检查：

```sh
NIDO_TEST_MIHOMO=/absolute/path/to/verified/linux-mihomo make check-proxy-linux
```

该入口按 engine 实际架构交叉编译测试和 CLI，再在同架构 Linux 内核上执行，不请求跨架构模拟。临时 `FROM scratch` 镜像默认仅含四个产物，显式跨版本模式另加一份核验后的目标核心；无需基础镜像、容器内 Go 或系统包。容器使用非 root 用户、只读根目录、临时可执行 `/tmp`、默认 seccomp、私有 PID namespace 和 `--network none`；不挂载宿主目录、不映射端口、不授予额外能力。短命 `--init` 负责回收子进程；不会安装或启动 Docker daemon、systemd 等服务。buildx 状态也定向到本次临时目录。

Linux 入口先运行 Proxy 全包及内核专项，再运行真实核心 E2E；`TestOS` 权限跳过会使入口失败。IPv6 回环在测试 namespace 不可用时只跳过对应子项并明确报告。清理仅使用本次实际 CID 和私有 image tag，不运行全局 prune；清理失败报错并保留记录。默认入口为原生非 race 验证，宿主交叉编译不等于 Linux race 覆盖。

### Linux race 与原生主机入口

race 检查需要 Linux C 工具链及满足当前 Go >= 1.26.0 下限的 SDK。两个显式入口均接受 `NIDO_TEST_LINUX_BIN_DIR`，目录内包含从同一源码构建的 `nido`、`nido.test`、`proxy.test`；预构建输入只会复制到本次临时目录。入口核验静态 ELF 架构；CLI 通过 `go version -m` 核验 package，race 模式还必须具有 `CGO_ENABLED=1`、`-race=true`。历史 Go 1.23 测试程序曾缺少这些构建字段，入口因此保留 `go tool nm` 对包入口及 race 运行时、TSAN 初始化符号的检查；这不表示当前源码仍能用 Go 1.23 构建。测试程序必须保留符号表。CLI 及一次性启动器同样启用检测，不能只把父测试程序编译为 race。

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

arm64 还需实际启动 race 运行时，不能仅凭 ELF 和符号核验宣称兼容。历史 Go 1.23 的构建帮助将 linux/arm64 race 限定为 48-bit VMA；当时在实际内核与默认 seccomp 下先执行静态 race 探针，再由故意竞争负例确认检测器有效。当前编译器与内核组合须重新判断，不能借用该历史结论。构建工具可以放在单独的临时容器中，通过 stdin 输入筛选后的源码及离线 vendor，构建时禁网；导出三份程序后，由仅含这些程序及核验核心的入口执行，不向测试容器加入工具链或库。具体历史版本与结果见审计记录。

原生 Linux 主机已有 bubblewrap 时，`make check-proxy-linux-native` 使用独立脚本执行，默认要求预构建的三个非 race 静态程序；设置 `NIDO_TEST_LINUX_RACE=1` 可复用上面的 race 输入：

```sh
NIDO_TEST_LINUX_RACE=1 NIDO_TEST_LINUX_BIN_DIR="$nido_race_bins" NIDO_TEST_MIHOMO=/absolute/path/to/verified/linux-mihomo make check-proxy-linux-native
```

此入口按 `uname` 核验原生架构，使用现有 bubblewrap 创建新文件系统、PID 和网络空间，仅以只读方式映射本次四个或显式跨版本的五个程序，并提供 `/proc`、私有 `/dev` 和临时 `/tmp` HOME。清空环境及 capabilities；namespace 创建失败直接退出，不回退到宿主执行。不会连接远程主机、下载程序、安装服务或修改已有 HOME。真实核心与辅助进程不会存活于测试 PID namespace 之外。

race 模式为每个隔离实例配置私有 `NIDO_TEST_RACE_REPORT_DIR` 和固定 `GORACE`。测试在全部清理完成后检查该目录，启动器、辅助进程的报告即使未传回父进程退出码也会导致失败；真实 E2E 的私有 CLI 明确使用同一报告目录。目录必须是规范临时根下、属于当前用户的 0700 普通目录，拒绝链接与目录替换。故意触发竞争的独立子进程负例验证检测器退出码 66 及报告收集；其报告在另外的临时目录中清理。默认测试不接受或传递现有宿主 `GORACE` 给真实 CLI。

显式运行 race 模式时，执行到的 Nido、Proxy、测试及启动器 Go 代码启用 race 检测；官方 Mihomo 程序保持原发布字节，未重新编译为 race，不能据此推断其内部并发没有问题。这是入口范围说明，不表示本轮已经执行该原生或真实核心检查。

### 两版本重装兼容验证（保留历史入口名）

显式增加第二份已独立核验的较新官方核心，可运行保留原名称的 `TestProxyRealMihomoCrossVersion`；`NIDO_TEST_MIHOMO_UPDATE` 也为兼容既有入口继续保留。当前语义是停止、移除程序后以第二版本首次安装，不再执行已删除的本地 artifact 更新，更不把该场景称为原生 updater 验证。未设置第二份输入时，两版本用例跳过：

```sh
NIDO_TEST_MIHOMO=/absolute/path/to/verified/older-mihomo \
NIDO_TEST_MIHOMO_UPDATE=/absolute/path/to/verified/newer-mihomo \
make check-proxy-real
```

Linux 两个入口也接受 `NIDO_TEST_MIHOMO_UPDATE`，仍使用上面的 `NIDO_TEST_LINUX_BIN_DIR` 和 race 开关。显式启用后，输入从四个程序变为五个；新核心同样必须是绝对路径的普通、同架构静态 ELF，复制后才进入私有 namespace，不新增宿主挂载、外部网络或服务。该兼容用例缺少 PASS 或被跳过时，入口失败；旧的预构建测试程序不能忽略新变量后误报成功，PASS 标记也不能单凭旧名称推断为原生 updater 证据。两个真实 E2E 共用测试进程时，显式两版本模式的总预算为普通 6 分钟、race 20 分钟；原 Proxy 全包和单核心模式的预算不变。

测试先用旧核心下载 HTTP provider 缓存，再停机改成引用该缓存的 file provider，并在旧版本实际运行同一配置。随后显式 `stop → remove → install`：remove 只移除旧程序，install 导入第二版本核心；核验程序 SHA、实际完整稳定版本升序、状态仍停止、监听关闭，以及配置、旧缓存、日志、记录和未知文件保留。新核心再验证并运行同一配置，检查进程归属、幂等启停与 restart、HTTP/SOCKS/HTTPS 转发；HTTP provider 已拒绝请求且下载计数必须不再增加，旧缓存保持原字节。最后停止并卸载，核对配置、缓存、未知文件和临时认证/会话哨兵保留。运行本身允许更新日志、进程记录及核心数据库，不将更新前全文件快照跨运行比较。

不同程序字节不足以证明两版本：实际报告的版本也须为不同且升序的完整稳定版本，同版、降级和异常版本后缀均不能通过夹具身份检查。将来运行所得结果只覆盖该版本对重装后的配置与缓存复用，不证明 `/upgrade` 的真实下载、覆盖或 exec，也不承诺任意版本、降级、所有 provider 格式或其他核心状态迁移。

本轮没有执行真实 Mihomo。原生 updater 成功路径目前只有 fake controller 与 fake image 的隔离测试；当前重装夹具与下文历史停机更新事实分开记录。

实际版本、执行结果和剩余验证范围见[审查记录](proxy-audit.md)。回环验证不代表真实订阅、所有节点协议或其他平台的兼容结论，也不代表已经发布或切换用户服务。

v0.4.0 基线的 Linux amd64/arm64 曾使用统一冻结源码完成原生静态 race 的 Proxy 全包与真实 CLI/Mihomo E2E 复验；配置、进程归属、启停、provider、HTTP/SOCKS/HTTPS 和停机更新/卸载的数据保留检查通过，另有 v1.19.31 → v1.19.32 停机更新与旧 provider 缓存复用证据。这些历史结果不扩称本次新流程、全项目 race、Mihomo 内部 race、真实 IPv6 转发或任意版本/全部状态迁移；源码、产物摘要及具体范围见审查记录。
