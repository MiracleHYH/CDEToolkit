# Nido v0.8.2 发行记录

2026-10-19：[**v0.8.2 已作为 stable/latest 发布**](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.8.2)，仅面向 Linux amd64。本版包含内部 API listener 绑定、未绑定禁用规则及发布前审计修复。源码审计、原生 CI、发布工作流、主机独立验签、正式包 API 隔离验收，以及公网直链和 gh-proxy.org 加速安装、自更新均已通过。首次加速更新因代理上游 GitHub 账户配额耗尽返回 403；配额重置后，用相同脚本和包在新隔离目录复验通过，原失败证据保留。

| 项目 | 状态 |
| --- | --- |
| 版本、目标 | v0.8.2，Linux amd64 |
| 源码审计与离线隔离 | PASS，141 个独立顶层测试，无跳过 |
| 发行源码 | `635f62f533e54703971d7897c1f2779fe45f1999`，已推送 |
| Linux amd64 原生 CI | [39152505430](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39152505430)，成功；历史 opt-in 未选择而跳过 |
| 标签 | v0.8.2 → `635f62f533e54703971d7897c1f2779fe45f1999` |
| 发布工作流 | [39152921460](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39152921460)，成功 |
| 发行资产与独立验签 | PASS，六项资产、三份签名及本地构建字节比较 |
| 官方直链安装与自更新 | PASS，latest 首装、重复安装及真实 v0.8.1 → v0.8.2 自更新 |
| gh-proxy.org 加速链 | PASS，配额重置后 latest 首装、重复安装及真实 v0.8.1 → v0.8.2 自更新；首次 HTTP 403 和保护结果保留 |
| 正式包的 API 禁用、绑定与失败保护 | [39153295471](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39153295471) PASS，70 个禁用场景、4 项正向查询，完整检查与清理通过 |
| 可选私人文件前缀 | PASS，仅 tagged/latest/repeat 安装及数据保护；不计 API 或自更新验收 |
| 真实机器部署、配置修改 | 未执行 |

## 本版变化

- 从 `proxy bind download` 或 `proxy bind --tool nido` 选择一次无认证、具名 listener，后续 Nido 自身、Proxy、GDS 及 Nido 主动查询 Codex 版本的 GitHub API 自动使用该入口；API 保留官方 URL，文件资产仍采用原 GitHub 加速策略。
- 没有绑定文件或 listener 为空时禁用上述 API。代理变量、文件加速前缀和 `--proxy` 不能替代启用步骤。`download show --json` 显示静态 `api_state`、`api_listener`、`api_proxy`，未绑定是正常 disabled 状态，不是普通 doctor 故障。
- 绑定只保存名称于私有 `download-proxy.toml`，不改主配置 schema、不保存凭据。确认前后绑定被创建、替换、编辑、删除、改权限或增加硬链接时，拒绝覆盖并保留并发编辑。
- 高级临时 API 代理同样限制官方 API 域名、HTTPS/443、Host、重定向及 TLS；响应 Close 时校验失效不能返回可用元数据或触发网络回退。
- 公开发行入口明确区分 current 与 legacy，补齐 doctor 退出码和验收夹具检查。完整问题、修复和证据见 [API 代理审计](nido-api-proxy-audit.md)。

Conda/pip 的持久 default、镜像切换、缓存及 envs 路径原地修改沿用既有组件；不会因新增 API 绑定而改写这些外部配置。v0.8.1 的资产、网络及 Conda P0 结果保留在[历史记录](conda-v0.8.1-release.md)，不将其当成本版正式包的验收。

## 发行资产与验证

实际白名单为六项：`nido-linux-amd64.tar.gz`、同名 `.sha256`、`install.sh` 及三份对应 `.sigstore.json`。包内只包含一个普通可执行文件 `nido`。本版不发布 macOS 或 arm64 包，不删除或重新解释历史版本的四平台资产。

GitHub 发布元数据保存在 `build/release-v0.8.2/published-release.json`。独立验签结果 `build/release-v0.8.2/verified/receipt.json` 确认六项资产、三份签名、签名证书的提交与 tag 引用、精确白名单及本地构建比较均通过；正式 API CI 的六份输入也与该独立核验目录逐字相同。主要资产如下：

| 资产 | 字节 | SHA-256 |
| --- | ---: | --- |
| `nido-linux-amd64.tar.gz` | 4,153,606 | `69b1ce27e9d1972fc6abc4891f265bc19dbff12ae891a0e300112f961cf111ae` |
| `nido-linux-amd64.tar.gz.sha256` | 90 | `a0885b7488c58910c066c261c53d9883d6580af8e9ca98b6f58db486af3e7138` |
| `install.sh` | 7,695 | `359229442d543c1ea901686b51236a81dfbd4a044bb76f908cf0a97e1146fc87` |

包内 v0.8.2 程序 SHA-256 为 `ef06257ad6d2154a3e44a1083b223d6265dce18fb9189d280133bcc77bf25857`；用于真实升级的独立验签 v0.8.1 程序为 `e412dd4d37ffaa2baa7add444d1e1e398fb65877b6e7ca48156c79392c68c964`。两者均为对应公开发行输入，不以源码重编旧版本替代。

当前安装器和 Nido 客户端自身只做各自的摘要及格式检查，不自动验证 Sigstore；从第三方取得脚本时，不能仅凭同一入口提供的包和摘要认定官方真实性。

以下固定版本命令仍应在核验来源后使用。安装器只获取文件，不依赖尚未启用的 API：

```sh
curl --http1.1 -fsSL \
  https://github.com/MiracleHYH/CDEToolkit/releases/download/v0.8.2/install.sh \
  -o /absolute/path/to/verified-install.sh
# 完成独立来源与签名核验后：
sh /absolute/path/to/verified-install.sh --version 0.8.2 --github-proxy direct
. "${NIDO_HOME:-$HOME/.nido}/env.sh"
nido version
```

使用自己信任的文件加速服务时，将脚本 URL 写成 `https://mirror.example.invalid/https://github.com/MiracleHYH/CDEToolkit/releases/download/v0.8.2/install.sh`，并将安装器的 `--github-proxy` 指定为该 HTTPS 前缀。替换占位地址并保留核验步骤；文档不预设私人服务。

## 从 v0.8.1 升级

升级保留主配置、环境文件、组件和外部数据，也保留已存在的 API sidecar；**不会自动绑定 listener**。v0.8.1 没有新开关，可通过已有可用代理执行一次升级：

```sh
nido update --version 0.8.2 --github-proxy direct --proxy http://127.0.0.1:PORT
```

将 PORT 换成实际端口。这里 `direct` 保持官方 URL，API 和资产仍经显式通用代理；它是旧客户端的行为。也可用固定 v0.8.2 文件安装器重装同一软件根。旧版忽略 sidecar 的兼容性不意味着旧版自动具备 API 分路由或禁用规则。

升级进入 v0.8.2 后，若尚未绑定，`nido update --check`、Proxy/GDS 在线安装更新及 Codex `update --check` 会明确提示未启用。已有 Proxy 时查看 listeners 并选择一次：

```sh
nido proxy endpoints --json
nido proxy bind download
nido download show --json
nido update --check
```

持久路由要求受管 Proxy 与所选 listener 已就绪；绑定操作本身不会启动服务。没有 Proxy 时，先准备独立核验的本地 Mihomo 包，通过绝对路径 `--artifact` 安装，再显式配置、验证、启动，最后绑定，避免首次 API 依赖循环。完整命令、架构与摘要要求见[自举方案](nido-proxy-bootstrap.md)。

已有有效、私有且非空的绑定记录后，`--proxy URL` 可临时覆盖 API 路由；本次覆盖不核验所保存 listener 是否运行，但仍核验 sidecar、API 范围和证书。未绑定时传入该参数仍不能启用 API。移除使用 `nido proxy bind download --proxy off --yes`，清空受管 listener 并恢复禁用。

普通 Codex 更新在未绑定时跳过 Nido 的可选 API 预查询，官方安装/更新脚本保留原有独立代理和确认流程；Codex 运行流量、用户自行运行的 curl/Git/Conda/pip 及显式 probe 不受本开关管理。

## 验收与部署边界

已有源码证据位于 `build/api-proxy-final-audit-20261019/`：90 个库测试、40 个 Nido 回归、11 个固定专项，包含 224 个禁用入口场景；另有源码 CLI current、独立验签历史 v0.8.1 legacy 及 config/env/sidecar 三项摘要兼容检查。其 Linux amd64 为仿真容器，普通 UID、只读根、临时 HOME/cache、无外网，仅回环 TLS/CONNECT、假核心和最小版本候选；不是本版公网发行包验收。

Linux 原生 CI 的预发布候选包专项另通过 70 个禁用入口场景和 4 项 API 正向查询，完整检查、清理和证据导出均成功，范围标为 `pre-release-local`。其归档 SHA-256 与本版发布元数据相同；结果位于 `build/release-v0.8.2/native-main-ci/nido-api-pre-release-1.27.2-39152505430-1/contract/container-evidence/result.json`。该项仍使用回环 TLS/CONNECT，不验证公网 API，也不取代发布后的独立验签和正式包专项。

正式公开包的 [API 隔离验收](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39153295471) 已通过，证据为 `build/release-v0.8.2/formal-api-ci/nido-api-acceptance-39153295471-1/`。输入范围为 `verified-public`，明确未使用源码 Nido 二进制；70 个禁用场景、4 项正向查询、完整检查、清理与证据导出均成功。在原生 Linux x86_64 runner 的普通 UID `10001`、只读根、临时 HOME、`--network none` 中执行，API 为回环 TLS 夹具，核心为专用 CONNECT 测试程序。它验证正式包的门控、选择、分路由和失败保护，不测试公共 API 网络或真实 Mihomo。

公网安装及真实旧版自更新使用另一轮 Linux amd64 仿真容器，允许公网并保持私有 HOME/软件根。`build/release-v0.8.2/public-network/container-evidence/direct-result.json` 记录 direct 全部通过，包括 latest 首装、重复安装、Shell 接入、真实已验签 v0.8.1 升级、同版本离线跳过、拒绝降级、失败保护和假上游 Conda CLI。

首次加速链已完成首装、重复安装及 `update --check`，但旧 v0.8.1 客户端实际更新请求固定 tag 元数据时 HTTP 403；原目录中的 `accelerated-update-transfer-failure.json` 仍记录操作退出 1、`complete=false`、`preservation_verified=true`，不改写为成功。

四次匿名只读 HTTP 诊断确认：官方 latest/tag 均为 200，加速 latest 为 200，加速 tag 为 403。403 正文明确为 GitHub API rate limit exceeded，响应头为 limit/used `5000`、remaining `0`；TLS 校验成功，请求用时约 0.634 秒。证据指向该代理上游 GitHub 账户配额耗尽，既非 TLS 超时，也非 URL 拼接错误。原始诊断保存在 `build/release-v0.8.2/gh-proxy-403-readonly-20261019/receipt.json`，每个 URL 只查询一次，没有凭据、重试或 Nido 执行。

响应头给出的配额重置时间后，未修改生产源码、验收脚本或正式包，使用全新目录 `build/release-v0.8.2/public-accelerated-after-quota-reset/` 复验。容器退出 0，`container-evidence/result.json` 与 `accelerated-result.json` 记录 accelerated 首装、重复安装、Shell 接入、真实旧包自更新、失败保护、离线同版本跳过、拒绝降级、假上游 Conda CLI 和外部数据保护全部通过。本次成功不删除首次失败，也不把其原因概括成私人文件代理故障。

另通过未公开地址的私人 HTTPS 文件前缀执行 tagged、latest 和重复安装，共 12 次完整 GET，响应在执行前逐字匹配独立验签输入。有效及未知 sidecar、配置和完整临时 HOME 均保留，私有测试目录已清理，内外层退出码均为 0。证据为 `build/release-v0.8.2/private-install-only/evidence/container-evidence/result.json`，明确 `api_requests=0`、`api_validation=false`、`self_update_validation=false`；只计文件安装验收，不计 API、自更新或真实核心验证，公开文档不记录私人地址。

失败保留现有程序、配置和可证明的用户并发编辑，不自动启动、重启、切源或提供通用回滚。此次发行不部署服务或修改真实机器配置，不将假核心或回环夹具计作真实 Mihomo、订阅、公网完整链路、原生目标机或 GPU 验收。
