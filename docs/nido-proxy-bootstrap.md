# Nido → Proxy → API 自举方案

适用目标为 Linux amd64：先通过可信文件加速入口安装 Nido，用固定版本的本地 Mihomo 包安装 Proxy，再显式配置、启动 Proxy，选择一次 listener，供后续 Nido 内部 GitHub API 自动使用；发行包和摘要继续走文件加速入口。不会将用户自行发起的请求纳入该代理。

**版本边界：本流程对应正在准备发布的 v0.8.2，仅面向 Linux amd64；v0.8.1 没有 listener 持久绑定和未绑定禁用规则。**当前源码审计和隔离回归已通过，发行、签名及公网验收状态见 [v0.8.2 发行记录](nido-v0.8.2-release.md)。下面的 v0.8.2 下载命令供正式资产可用并完成来源核验后执行，准备状态不表示 URL 已可用。文件加速服务只需支持 GitHub 文件路径，不要求转发 `api.github.com`。主 `config.toml` schema 保持不变，新绑定使用独立私有文件，升级不会自动选择 listener。本文是后续操作方案，未在真实机器部署。

## 1. 通过文件入口安装 Nido

把占位 origin 换成自己信任的 HTTPS 文件加速入口。以下固定 Nido v0.8.2，先下载脚本，完成独立来源核验后再执行：

```sh
nido_file_origin='https://mirror.example.invalid'
nido_bootstrap_dir=$(mktemp -d "${TMPDIR:-/tmp}/nido-bootstrap.XXXXXX")
nido_bootstrap_dir=$(cd "$nido_bootstrap_dir" && pwd -P)
curl -q --http1.1 --fail --silent --show-error --location \
  --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 10 --max-time 180 \
  --output "$nido_bootstrap_dir/install.sh" \
  "$nido_file_origin/https://github.com/MiracleHYH/CDEToolkit/releases/download/v0.8.2/install.sh"
```

执行前按 [发行记录](nido-v0.8.2-release.md)核对官方签名、版本和资产来源；不要仅信任同一文件代理提供的脚本与摘要。安装客户端不会自动验证 Sigstore 签名，HTTPS 和 SHA-256 也不能替代来源认证。确认后执行：

```sh
sh "$nido_bootstrap_dir/install.sh" --version 0.8.2 --github-proxy "$nido_file_origin"
. "${NIDO_HOME:-$HOME/.nido}/env.sh"
nido version
nido download show
```

需要自定义软件目录时，先选择 `NIDO_HOME`；使用 `--root` 时也必须加载对应根中的 `env.sh`，详见 [初始化说明](usage.md#初始化与-shell-环境)。这一步只安装 Nido，保存本次明确选择的文件前缀，不安装 Mihomo、不关联 Codex、不改镜像，也不启动服务或编辑 Shell 配置。

## 2. 准备固定、可信的本地 Mihomo 包

默认 `nido proxy install` 需要查询 Mihomo 发布 API。新源码未绑定时 API 功能禁用，会在联网、候选执行、确认或写入前提示未启用；显式 `--proxy`、`--github-proxy` 或终端代理变量也不能在无绑定时启用它。因此首次自举使用本地包。前一步的 `install.sh` 直接获取固定文件，不依赖 API，仍可使用。

从可信来源先选定具体稳定版本，独立核对版本、文件名、Linux amd64 兼容性和 SHA-256。优先选择该发行实际提供的 `amd64-v1` 或 `amd64-compatible` 包；不要假定无后缀 amd64 包适合所有 CPU。ELF 架构检查不能证明 v1/v3 指令集兼容。若来源提供签名，按其可信发布流程另行验签；Nido 本地 artifact 安装不会代做这一步。

下面的 `X.Y.Z`、架构后缀和摘要都必须替换为已核实值。固定下载 URL 可以通过同一文件入口取得，不需要查询 API：

```sh
mihomo_version='X.Y.Z'
mihomo_arch='amd64-v1'
mihomo_sha256='REPLACE_WITH_VERIFIED_64_HEX_SHA256'
mihomo_name="mihomo-linux-$mihomo_arch-v$mihomo_version.gz"
curl -q --http1.1 --fail --silent --show-error --location \
  --proto '=https' --proto-redir '=https' --tlsv1.2 \
  --connect-timeout 10 --max-time 180 \
  --output "$nido_bootstrap_dir/$mihomo_name" \
  "$nido_file_origin/https://github.com/MetaCubeX/mihomo/releases/download/v$mihomo_version/$mihomo_name"
printf '%s  %s\n' "$mihomo_sha256" "$nido_bootstrap_dir/$mihomo_name" | sha256sum -c -
nido proxy install --artifact "$nido_bootstrap_dir/$mihomo_name" --sha256 "$mihomo_sha256" --yes
```

也可将已经可信核验的包放在其他位置，通过绝对路径传入。`--artifact latest` 仍会调用在线发布 API，不能代替本地文件；本地模式也不需要传 `--github-proxy`。

本方案明确提供 SHA-256，尽管现有 CLI 的本地摘要参数可省略。摘要核对输入文件本身，gzip 输入应填写压缩包摘要。Nido 检查普通文件、大小、gzip 校验、展开大小、原生架构和受支持的动态依赖，再将候选复制到私有路径执行 `-v`。本地候选的版本字符串不绑定官方 tag，也不能证明来源；应在准备文件时完成可信版本与摘要的核对。

`--yes` 让安装只完成核心落盘，不进入交互 quick start，不启动 Proxy。若软件根已有配置，安装还会用候选核心验证它。核心版本查询和 `-t` 都会执行程序，不能将任意 artifact 当作无需信任的纯数据；这里保证的是 Nido 本地安装路径不查询发布 API，不保证任意程序或配置绝对无网络访问。

## 3. 显式配置、验证和启动

可以运行 `nido proxy configure` 交互生成首次配置，也可以导入已经准备好的私有 YAML：

```sh
chmod 600 /absolute/path/to/proxy-bootstrap.yaml &&
nido proxy configure --file /absolute/path/to/proxy-bootstrap.yaml --yes &&
nido proxy validate &&
nido proxy start --yes
nido proxy status --json
nido proxy endpoints --json
```

配置输入必须是当前用户拥有的私有普通文件，采用支持的回环 HTTP/SOCKS/mixed listener。已有配置和数据保护规则见 [Proxy 使用说明](usage.md#内置-proxy)。`configure`、`validate` 都不启动服务；核心验证可能访问数据库或缓存。`validate` 通过也不证明端口已绑定、provider 已加载或远端可达。

上述 `&&` 使配置或验证失败时不继续启动。已有主配置时，`configure` 不带 `--file` 只显示路径；带 `--file --yes` 会在停止状态验证后原子替换整个主 YAML，不会合并其中的节点或其他设置。复用此流程前应核对完整输入，避免将首次配置覆盖到已有实例。

首次 provider 获取也需要可用的网络路径。quick start 默认用 `DIRECT` 获取 HTTP provider；如果订阅本身依赖尚未可用的代理，应先准备不依赖这条订阅的完整本地配置或节点，避免新的循环依赖。

只有明确的 `start` 才启动受管实例，不注册 systemd、Supervisor 或自动重启。按实际 YAML 选择 listener，不从多个端点中随意取第一个。以下用 quick start 常见名称 `endpoint-proxy` 举例，名称可以替换：

```sh
nido proxy endpoint endpoint-proxy
nido proxy probe --endpoint endpoint-proxy \
  --url https://api.github.com/repos/MiracleHYH/CDEToolkit/releases/latest
```

`endpoint` 只读取配置，停止时也能导出地址；它不代表网络就绪。`probe` 要求受管实例及监听就绪，但只证明该次目标响应，后续仍需执行实际版本查询。内部 API 持久绑定仅支持无认证的具名 listener；已有有效非空绑定后才可用高级临时参数选择认证入口，不能将凭据保存到该绑定中。

## 4. 选择一次 listener，后续内部 API 自动使用

以下命令从 v0.8.2 提供。交互选择 listener 的端口即可，无需手写代理 URL：

```sh
nido proxy bind download
nido download show --json
nido update --check
nido update
```

也可在明确 listener 名称后运行 `nido proxy bind download --endpoint endpoint-proxy --yes`；`nido proxy bind --tool nido` 使用同一项绑定。交互提供 listener、保持现状和移除选项，不保存自定义 URL 或凭据。`download show --json` 的 `api_state` 为 `disabled` 或 `configured`；没有 sidecar 或 listener 为空时为 `disabled`，`api_listener` 和 `api_proxy` 为空。`configured` 只表示已有非空配置；名称和当前解析的地址不证明服务已启动、配置已重启生效或网络可达。

绑定独立保存在软件根的 `download-proxy.toml`，权限为 `0600`：

```toml
schema = 1
product = "nido-api-proxy"
listener = "endpoint-proxy"
```

完成绑定后，每次 Nido 自身、Proxy、GDS 以及 Nido 主动查询 Codex 版本的 GitHub API 操作都会重新解析名称和端口，检查受管进程及配置就绪。API 保留官方 URL，经 listener 访问，不套用文件前缀；checksum、archive 等资产继续采用已保存的 `github_proxy` 策略。无需将文件策略改为 `direct`。listener 删除、改为认证、服务停止或配置修改后尚未重启时明确失败，不直连、不改走文件前缀、不自动启动或重启。已是最新版时实际更新会跳过，`--check` 成功不等于完整包下载或替换通过。

GDS 保持可选；只有需要安装它时才执行安装命令。对已安装的 GDS 可查询或更新：

```sh
nido gds install --yes
nido gds update --check
nido gds update --yes
```

GDS 的程序安装和更新不启动 GPU 工作进程；实际更新须满足组件停止条件，真实 GPU 能力仍需单独验收。这些命令使用 GDS 自己的官方发行信息，不把 Nido 的 `latest` 当作 GDS 版本。

移除绑定使用：

```sh
nido proxy bind download --proxy off --yes
```

移除将受管文件的 listener 清空并禁用 API，不删除未知文件；有归属、权限、路径或内容冲突时拒绝覆盖。未绑定时，依赖 API 的版本查询、在线安装和更新会在网络请求、候选执行、确认和写入前提示未启用；不能通过显式代理参数、环境代理变量或文件前缀绕过。普通 status/doctor 不联网，未绑定属于正常禁用状态，不算 doctor 故障。本地 artifact、dry-run、普通只读和无需 API 的同版本操作可用。旧版本会忽略并保留这个独立文件，不具备自动 API 绑定或未绑定禁用规则。

此配置只用于 Nido 发起的上述 GitHub API 请求，不设置系统代理或 HTTP(S)/ALL_PROXY，不修改用户的 curl、Git、Conda/pip，也不改变 Codex 官方安装/更新脚本的独立代理及程序运行网络。`nido codex update --check` 未绑定时明确失败；普通 `nido codex update` 未绑定时跳过 Nido 的可选版本 API 预查询，官方脚本仍按原有独立代理和确认流程执行，其内部请求不纳入本开关。用户显式 `proxy probe --url` 也保留为独立诊断。Codex/Git 的工具级持久代理仍需独立操作。已有有效非空绑定后，原有单次 `--proxy URL` 才可用作 API 高级临时覆盖，不写入该文件，也不能在未绑定时启用 API。

从 v0.8.1 升级时，旧客户端尚无自动绑定和禁用开关。若已有可用网络代理，可在 v0.8.2 正式发布后使用 `nido update --version 0.8.2 --github-proxy direct --proxy http://127.0.0.1:PORT`；这里 `direct` 只保持官方 URL，请求仍走显式通用代理，API 与资产一起走它。也可使用第 1 节固定版本的文件安装器重新安装到同一软件根。升级保留配置和 sidecar，但不会自动绑定；进入 v0.8.2 后应按本节选择 listener，否则 API 功能保持禁用。没有 Proxy 时先按第 2、3 节从可信本地 artifact 安装、配置、启动，再绑定；不能用新版 `--proxy` 绕过启用步骤。

## 5. 更新 Proxy 自身时先准备本地包

Proxy 正在运行时，可以用已有通用代理查询核心版本：

```sh
nido proxy update --check
```

若获取新核心要依赖当前 Proxy，先保持它运行，用通用代理获取所选固定版本的官方资产，并完成可信来源、摘要及兼容性核验。文件准备完成后再执行：

```sh
nido proxy stop --yes &&
nido proxy update --artifact /absolute/path/to/verified-new-mihomo.gz --sha256 SHA256_HEX --yes &&
nido proxy start --yes
```

不要先停止唯一可用代理，再要求它替自己在线下载；实际更新本来就要求停止状态。`--artifact` 更新成功后仍保持停止，配置、缓存和未知数据保留；上例只在前两步均成功后执行显式启动。若替换失败，应检查错误和现存状态，不把服务自动启动或切源作为补偿。

## 验证状态与范围

本次三项实质修复、验收入口调整及 141 项源码隔离结果见[内部 API 代理发布前审计](nido-api-proxy-audit.md)。v0.8.2 正式发行包的签名、下载和公开网络结果另见[发行记录](nido-v0.8.2-release.md)，不把源码夹具通过当作正式资产验收。

发布前审计已补齐验收入口的静态约束：`make fmt-check` 包含 bootstrap 脚本语法检查，新增 `make check-nido-proxy-bootstrap` 调用隔离入口；该入口按完整名称固定十一组既有契约，继续拒绝缺失或跳过，新审计用例由独立矩阵执行。健康 fixture 的 disabled/off 状态要求 `doctor` 返回 0 且无 issues，先检查环境、下载策略和组件静态状态，避免其他故障掩盖 API 状态判断。三项功能修复、141 个顶层测试（含固定十一组）及 CLI 夹具已通过 Linux amd64 隔离验收，无跳过；详见[发布前审计报告](nido-api-proxy-audit.md)。证据在 `build/api-proxy-final-audit-20261019/`，该源码审计阶段未发布或部署，v0.8.2 后续发行结果单独记录。

公开 Proxy 发行包入口新增 `NIDO_TEST_PROXY_RELEASE_BINDING_MODE=current|legacy`，默认 `current`；历史 v0.8.1 等包须显式设置 `legacy`。它按选择严格验收，不因失败切换模式。普通 bootstrap 专项可使用：

```sh
NIDO_BOOTSTRAP_TEST_BINARY=/absolute/path/to/trusted-linux-amd64-nido.test \
NIDO_BOOTSTRAP_SDK=/absolute/path/to/linux-amd64-go-sdk \
NIDO_BOOTSTRAP_OUTPUT=/absolute/path/to/new-evidence \
make check-nido-proxy-bootstrap
```

先等待当前源码的编译命令成功，再冻结测试程序和脚本、记录摘要后运行；入口不负责构建或证明输入与当前源码一致。其普通用户、只读根、临时 HOME、无外网与假核心边界保持如下述记录。

2026-10-19，“未绑定即禁用 API”规则已完成 Linux amd64 仿真隔离验收。缺少绑定文件、空 listener 两种状态，结合环境代理、显式代理、GitHub 直链和文件前缀等七种条件，覆盖 Nido、Proxy、GDS 和 Nido 的 Codex 版本查询共 224 个在线调用场景；HTTP 请求、网络拨号、候选或组件执行、确认读取及配置数据改写均为零。另验证了 disabled/configured 静态状态、doctor 正常处理未启用、off 恢复禁用，以及临时代理不能自行启用功能。

相关 Nido 回归 43 组和 Codex 集成回归 45 组通过；最终 11 组专用用例无跳过通过，并补验两项 Codex CLI 元数据路径，证明它不依赖或改写 Codex 运行时代理表达式。实际 Nido CLI 与 Proxy CLI 夹具也通过：未绑定的 Codex `--check` 不调用 curl，普通 Codex 更新跳过 Nido API 预查询，仍可运行已明确选择的官方安装器夹具。

证据在 `build/api-disabled-20261019/`；最终门控和绑定复验在 `final-bootstrap/`，退出码为 0，结果中的 `unconfigured_api_disabled_tested` 为 true。使用 UID/GID 10001、只读根、临时 HOME/cache 和 `--network none`，所有 HTTP/TLS/CONNECT 为回环夹具。CLI 使用带测试版本号的源码构建，不是新的公开发行包。常规 Codex 权限保留测试按其既有夹具使用 `umask 022`，其父目录仍为私有 0700；原测试日志保留，未为通过测试放宽产品保护。Go vet、shell 语法和 diff 检查通过。

该轮不发布或部署，不操作真实配置，不执行真实 Mihomo，也不替代公网全链路或原生目标机验收。用例见 [api_gate_test.go](../internal/nido/api_gate_test.go)、[Codex 元数据客户端](../internal/integrations/codex/latest_http.go)及[隔离入口](../tests/nido-proxy-bootstrap-isolation.sh)。下述是该规则加入前的历史结果，保留各自当时的范围。

2026-10-19，此前的持久 API 绑定及相关回归已在 Linux amd64 仿真容器中通过。容器使用 UID/GID `10001`、只读根、临时 HOME/cache 和 `--network none`；HTTP/TLS/CONNECT 目标均为回环夹具，宿主不执行 Nido，不访问真实用户配置。

| 回归范围 | 顶层测试 | 结果 |
| --- | ---: | --- |
| Nido 组合层、绑定、CLI、自更新与外部数据保护 | 38 | 全部 PASS |
| Proxy API client、元数据分路由及原有下载校验 | 14 | 全部 PASS |
| GDS 元数据分路由、分页及失败拒绝 | 4 | 全部 PASS |
| 自更新元数据分路由、资产和候选校验 | 10 | 全部 PASS |

专项覆盖一次持久选择后重新创建 Manager、无额外代理参数的版本查询及候选更新，确认 API 使用所选 listener、资产使用文件策略。坏证书、代理关闭、请求前入口变化及响应 EOF/关闭时进程记录归属变化均拒绝成功，不直连或自动启停；未知 sidecar 内容、不安全权限和链接拒绝覆盖，外部工具配置与未知数据保持不变。本地 artifact、dry-run 和只读操作保留独立边界；回归中发现并修复了未初始化根下只读 GDS 查询的兼容问题。

实际 CLI 的 `current` 模式和真实已发行 v0.8.1 的 `legacy` 模式均通过。v0.8.1 能读取新源码保存的主配置，忽略并保留 API sidecar；兼容测试后的 `config.toml`、`env.sh`、`download-proxy.toml` 三项 SHA-256 均与操作前一致。Go vet、shell 语法和 diff 检查通过。日志及汇总保存在 `build/api-listener-binding-20261019/evidence/`，其中 `result.json` 的 `passed` 为 true。

用例包括 [api_proxy_test.go](../internal/nido/api_proxy_test.go)、[Proxy API client 测试](../internal/components/proxy/api_client_test.go)及各组件的 `api_http_test.go`。上述 66 个顶层测试、两组 CLI 夹具与兼容检查使用不启动服务的核心夹具、受控 runner 和本地最小版本候选，不证明真实 Mihomo 启动或公网全流程通过。

该轮最终增加绑定 dry-run 的未知 sidecar 预检后，八组专用入口全部通过，没有跳过用例；当时源码 CLI 的 `current` 模式也再次通过。该轮最终证据在 `build/api-listener-binding-20261019/final-bootstrap/`，`outer-exit.txt` 为 0，`container-evidence/result.json` 的 `passed` 和 `persistent_api_binding_tested` 均为 true。保留容器启动前的隔离参数和输入摘要，只挂载测试程序、本地 SDK 与证据目录；这项历史通过不覆盖随后新增的未绑定禁用规则。

此前显式 `--proxy` 流程的历史隔离结果如下，与新持久绑定结果分别保留：

2026-10-19 使用本地 Go 1.27.2 交叉编译源码测试程序，在 Linux amd64 仿真容器中通过三组专项测试（其中 CONNECT 测试含四种场景）。容器使用普通 UID、只读根、临时 HOME/cache，`--network none`，只挂载测试程序、本地 Linux SDK 和证据目录。宿主不执行 Nido，不挂载真实用户配置或源码。

- 本地 artifact：格式夹具由受控 runner 执行安装、配置和启动契约，Nido HTTP 调用为零；保存的文件前缀保持不变。该 runner 不绑定或转发真实监听端口。
- 坏摘要：在任何核心执行或 HTTP 调用之前拒绝，不发布核心，不改 Nido 配置。
- CONNECT 正常：保留官方 URL、Host、SNI 并验证测试 CA；显式 `direct` 覆盖保存值和环境中的文件前缀。版本查询只访问 API，固定版本更新再经同一通用代理获取 API、摘要及完整 archive，并验证替换后的字节。候选是本地构建的最小版本命令夹具（0.5.0 → 0.6.0），不是公开发行包。
- CONNECT 失败：未受信任证书、代理返回 502、监听端口关闭均失败，不直连、不切文件前缀、不启停 Proxy。旧程序、Nido 配置、Conda/pip 配置、证书、缓存、envs 和未知数据的目录项、字节、权限、UID/GID 与快照一致，未遗留更新候选。

历史用例位于 [proxy_bootstrap_test.go](../internal/nido/proxy_bootstrap_test.go)，[隔离入口](../tests/nido-proxy-bootstrap-isolation.sh)当前固定运行包含持久绑定和未启用门控的十一组契约测试。入口要求已编译的 Linux amd64 `nido.test`、可执行的本地 Linux SDK 和新证据目录，不下载依赖。此前三组测试及四种 CONNECT 场景的完整日志、容器隔离参数和输入摘要仍保存在 `build/proxy-bootstrap-20261019/final/`；当时的 `outer-exit.txt` 为 0，`container-evidence/result.json` 的 `passed` 为 true。

上述源码测试不下载或执行真实 Mihomo，不复验公开发行包全链路。操作命令仍是后续方案；隔离用例不证明真实核心启动、订阅加载、公网完整链路、目标机原生 Linux、GPU 或实际部署通过。该测试阶段未发布、部署或修改真实机器配置；v0.8.2 的后续发行与正式包验收状态见[发行记录](nido-v0.8.2-release.md)。
