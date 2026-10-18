# Nido 使用指南

GDS 保持可选。Nido v0.5.0 起支持独立 Linux amd64 组件的官方下载安装、空闲观察和有限许可显存保留。首版 GDS 为 gds-v0.1.0；真实 GPU 验收尚未完成。旧 Shell/CUDA worker 不作为新 CLI 后端。

从 v0.3.0 起，Nido 使用单目录设计和 `nido` 命令。旧 `cdet` 版本不会被自动迁移；本指南描述 Nido 的安装和使用。

独立 Proxy 组件自 v0.4.0 提供；本指南中的 quick start、节点链接解析和类别/单工具绑定自 **v0.6.0** 提供。安装 Nido 不会自动安装或启动 Mihomo，也不会切换已有组件入口。

## 初始化与 Shell 环境

自 v0.7.0 起，安装器支持 GitHub 下载策略，CLI 支持 `nido download` 与 `nido update`。以下命令安装最新版并在当前 Bash / Zsh 中启用 `nido`。

选择一个独立软件目录，默认 `~/.nido`。首次安装选择官方直链或第三方加速链，不能指望脚本内的回退修复外层脚本下载失败。

官方直链：

```sh
curl --http1.1 -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh -s -- --github-proxy direct && . "${NIDO_HOME:-$HOME/.nido}/env.sh"
```

第三方加速链：

```sh
curl --http1.1 -fsSL https://gh-proxy.org/https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh -s -- --github-proxy https://gh-proxy.org && . "${NIDO_HOME:-$HOME/.nido}/env.sh"
```

安装后检查版本：

```sh
nido version
```

需要自定义目录时，先执行 `export NIDO_HOME=/absolute/path/to/nido`，再运行上面的安装命令。固定版本可在 `&&` 之前的脚本参数中补充 `--version X.Y.Z`；脚本也可从对应 `releases/download/vX.Y.Z/install.sh` 获取。源码构建方式见 [README](../README.md#开发)。

安装器也接受 `--root /absolute/path/to/nido`，优先于 `NIDO_HOME`；使用该参数时，命令末尾也要加载指定目录的 `env.sh`。首次初始化只接受不存在或空目录，不把普通数据目录或旧 toolkit 目录变成 Nido。重复初始化保留配置，并刷新自己的程序和环境文件；用户修改过的 `env.sh` 不被静默覆盖。

初始化不安装组件、不自动关联 Codex、不启动服务、不编辑 Shell 文件。需要新 Bash / Zsh 自动生效时，自行将下面一行加入对应的 `~/.bashrc` 或 `~/.zshrc`：

```sh
. "$HOME/.nido/env.sh"
```

自定义目录请替换路径。配置 Codex 后重新加载同一文件，才能更新当前终端的环境；CLI 无法直接修改父 Shell。

`nido env bash` / `nido env zsh` 输出同样的环境片段。未关联 Codex 时，只配置 Nido 自身路径；关联后增加 `CODEX_HOME` 和已识别的 Codex 命令目录，不生成 Codex 包装入口。重复加载不会不断增加 PATH。

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

`direct` 只访问官方 GitHub；`auto` 先短时尝试官方地址，API 请求失败后只尝试 `https://gh-proxy.org`，发行包、摘要等资产请求才会继续尝试 `https://ghfast.top`，不会无限重试。自定义 HTTPS 前缀只通过该服务转发完整的公开 GitHub URL，例如 `https://gh-proxy.org/https://github.com/...`。前缀可以含路径，但不能带账号、密码、查询参数或片段，也不接受 HTTP/SOCKS 代理地址。

优先级为本次 `--github-proxy`、`NIDO_GITHUB_PROXY` 环境变量、已保存配置、默认 `auto`。显式设置空的 `NIDO_GITHUB_PROXY` 表示直连。`nido download set VALUE` 保存后续下载策略；更新命令上的选项只影响本次请求。已有 Nido 重跑安装脚本但未显式指定策略时，脚本本次使用 `auto` 下载，保留软件中的既有策略；显式 flag 或环境变量则传给 `init` 保存。安装脚本本身不会解析 TOML。自动回退只在已经执行的安装脚本内部生效，外层 `curl` 需由用户选择能访问的来源。

`download show` 同时显示保存值和本次实际值。错误的下载偏好只阻止使用它的联网操作，不阻止 Codex 或离线状态查看；可通过 `download set` 修正，`doctor` 会报告无效的实际策略。Go 下载不会在收到响应头后仅因传输超过 8 秒而切换到第三方；连接和响应头有短期限，整个请求仍有大小与时间上限。加速服务返回的跳转也会检查认证参数和目标域名，自定义入口不能将下载跳转到任意外部站点。

加速前缀只作用于支持的 GitHub API 与发行包请求，不生成全局代理环境、不改写任意 URL，也不替换 Codex 的官方安装地址。HTTP/SOCKS 网络代理仍是独立概念；需要时可为单次自身更新使用 `nido update --proxy http://127.0.0.1:7890`。

Nido 自身更新只替换自己的程序，保留管理配置、生成环境、内置组件和外部数据。安装脚本与自身更新均核对下载包和摘要；当前客户端**不验证 Sigstore 签名**。通过第三方前缀时，该服务可接触并替换所转发的内容；尤其加速首装会直接执行第三方返回的脚本。HTTPS 和 SHA-256 检查不消除这项信任边界，请仅使用自己信任的服务。发布工作流的签名不等于安装客户端已经验签。

更新期间若入口、程序目录或候选文件被替换，操作停止；不会覆盖该替换，也不会在失败清理时删除未知文件。版本查询和确认使用同一次入口身份快照；下载固定为已确认的版本，不在最后一步重新选择另一个 latest。

## 目录与所有权

```text
<Nido 软件目录>/
  bin/nido
  config.toml
  env.sh
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

`--check` 显式联网查询最新版本，`--dry-run` 不下载或执行安装器。实际更新需要确认，已确认的非交互调用可以加 `--yes`。更新继续委托官方脚本，不维护自己的 Codex 版本仓库或回滚副本。

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

官方安装更新的代理优先级为：本次 `--proxy`、当前终端代理变量、Codex `.env` 中的字面代理变量。例如：

```sh
nido codex update --proxy http://127.0.0.1:7890
```

本次覆盖不写入 `.env`。Nido 的环境片段不全局导出代理，以免影响其他程序。

进程代理变量的小写形式优先于大写形式，显式空值也会覆盖 `.env`，并同步到安装子进程的两种大小写变量。本次 `--proxy` 只覆盖代理端点，保留 `NO_PROXY`。对于变量展开、转义或冲突的 `.env` 代理表达式，Nido 明确报错，不尝试执行或完整解释 dotenv。

## 类别与单工具快捷代理配置

从 listeners 选择一次地址，再应用到一个类别中的全部工具；每个类别独立选择，不隐式复用前一类别的选择：

```sh
nido proxy bind                 # 逐类选择
nido proxy bind ai              # AI：目前为 Codex
nido proxy bind daily           # 日常：目前为 Git
nido proxy bind download        # 下载：目前返回 no_targets，不写配置
```

交互菜单按显示序号选择，也可输入 `listener:名称` 精确选择任意 listener（包括 `off`、`custom`、`keep` 或纯数字名称）。默认回车保持现状。

也可独立配置单个工具：

```sh
nido proxy bind --tool codex
nido proxy bind --tool git
```

菜单始终包含自定义、保持现状和移除 Nido 受管配置。即使 Proxy 未安装、配置不可读或 listeners 为空，也可输入 `protocol://[username:password@]host:port`；认证可省略，支持 HTTP、HTTPS、SOCKS5、SOCKS5H，IPv6 用方括号，认证中的保留字符使用百分号编码。输入和结果正常显示，不隐藏或脱敏。

明确目标后可非交互配置或只读预检：

```sh
nido proxy endpoints --json
nido proxy bind ai --endpoint endpoint-isp --yes
nido proxy bind daily --endpoint endpoint-proxy --dry-run
nido proxy bind --tool git --proxy 'socks5h://127.0.0.1:7892' --yes
nido proxy bind --tool codex --proxy 'http://user:p%40ss@127.0.0.1:7890' --yes
nido proxy bind --tool git --proxy off --yes
```

`--endpoint` 使用当前 YAML 中的 listener 名称，`--proxy` 使用自定义 URL 或 `off`，两者互斥。`--yes` / JSON 非交互模式要求明确选择；不带显式地址的 `--dry-run` 只列出候选，带地址时执行对应工具的只读路径与冲突预检，不写文件或启动服务。

类别绑定是一次批量应用，单工具绑定只影响该工具；之后的显式操作生效，没有后台继承或端口同步。Codex 仍只写已关联 HOME 的 `.env` 受管块。Git 在用户级配置（默认 `~/.gitconfig`，可由 `GIT_CONFIG_GLOBAL` 指定）末尾维护自己的 `[http] proxy` 块，保留其他字节；它适用于 HTTP/HTTPS 远端，不配置 SSH。Nido 不解析仓库级、系统级配置或 include；用户级 include、已有外部代理、受管块被改动及路径安全冲突会停止写入，不强制覆盖。Git 自身的仓库/系统/环境优先级仍由 Git 决定。

## 内置 Proxy

Proxy 独立管理 `<Nido 软件目录>/proxy`，不依赖 Codex，不接管外部 Mihomo 或旧 Bash 环境。从 Mihomo 官方稳定版下载当前平台程序：

```sh
nido proxy install
nido proxy validate
nido proxy start
nido proxy status
```

交互 `install` 无额外预确认，安装成功且没有配置时直接进入 quick start；已有配置不被重建。`install --yes` 只安装核心，之后可运行 `nido proxy configure` 补齐首次配置。`configure` 无 `--file` 且配置已存在时只显示路径和验证方式，不覆盖文件。引导输入正常显示，完整 YAML 预览写入 stderr；JSON 结果单独写 stdout。

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
nido proxy update --artifact /absolute/path/to/mihomo.gz --sha256 SHA256_HEX
```

`--sha256` 接受 64 位十六进制摘要；在线下载也检查官方资产摘要、平台与展开大小。摘要验证不等于签名验证。下载继承当前终端代理，或使用本次 `--proxy URL`，不为了下载自动启动 Proxy。若下载需要当前 Proxy，先另行获取本地包，再停止并使用 `--artifact` 更新。

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

状态按外部 Codex 集成、内置 Proxy 和 GDS 分类，不联网或顺便修复配置。关联 Codex 的版本通过其 `--version` 查询，不宣称认证、会话或联网可用。`doctor` 另外检查生成环境及当前终端的 `CODEX_HOME` 是否一致。

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

官方来源固定为本仓库 `gds-vX.Y.Z` Release，独立于 Nido 的 `latest`。下载同时核对 API SHA-256、大小、严格摘要文件、ELF 和候选版本；Sigstore bundle 供独立验签，自动摘要校验不等于客户端验签。下载使用当前终端代理，或本次 `--proxy URL`，不会为下载启动任何服务。

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
