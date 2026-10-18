# Nido v0.8.2 发行记录

2026-10-19：**发布准备中，仅面向 Linux amd64。**本版包含内部 API listener 绑定、未绑定禁用规则及发布前审计修复。源码审计的 141 个顶层测试及 CLI 隔离夹具已通过；标签、公开资产、签名和公网验收尚待完成。本页准备状态不表示版本已经发布或可下载，不替代各阶段实际证据。

| 项目 | 状态 |
| --- | --- |
| 版本、目标 | v0.8.2，Linux amd64 |
| 源码审计与离线隔离 | PASS，141 个独立顶层测试，无跳过 |
| 冻结提交、标签 | 待发布流程记录 |
| 发行包与签名 | 准备中 |
| 官方直链安装与自更新 | 待正式包隔离验收 |
| 文件加速安装与自更新 | 待正式包隔离验收 |
| 正式包的 API 禁用、绑定与失败保护 | 待隔离验收 |
| 真实机器部署、配置修改 | 未执行 |

## 本版变化

- 从 `proxy bind download` 或 `proxy bind --tool nido` 选择一次无认证、具名 listener，后续 Nido 自身、Proxy、GDS 及 Nido 主动查询 Codex 版本的 GitHub API 自动使用该入口；API 保留官方 URL，文件资产仍采用原 GitHub 加速策略。
- 没有绑定文件或 listener 为空时禁用上述 API。代理变量、文件加速前缀和 `--proxy` 不能替代启用步骤。`download show --json` 显示静态 `api_state`、`api_listener`、`api_proxy`，未绑定是正常 disabled 状态，不是普通 doctor 故障。
- 绑定只保存名称于私有 `download-proxy.toml`，不改主配置 schema、不保存凭据。确认前后绑定被创建、替换、编辑、删除、改权限或增加硬链接时，拒绝覆盖并保留并发编辑。
- 高级临时 API 代理同样限制官方 API 域名、HTTPS/443、Host、重定向及 TLS；响应 Close 时校验失效不能返回可用元数据或触发网络回退。
- 公开发行入口明确区分 current 与 legacy，补齐 doctor 退出码和验收夹具检查。完整问题、修复和证据见 [API 代理审计](nido-api-proxy-audit.md)。

Conda/pip 的持久 default、镜像切换、缓存及 envs 路径原地修改沿用既有组件；不会因新增 API 绑定而改写这些外部配置。v0.8.1 的资产、网络及 Conda P0 结果保留在[历史记录](conda-v0.8.1-release.md)，不将其当成本版正式包的验收。

## 发行资产与验证

计划白名单为六项：`nido-linux-amd64.tar.gz`、同名 `.sha256`、`install.sh` 及三份对应 `.sigstore.json`。包内只包含一个普通可执行文件 `nido`。本版不发布 macOS 或 arm64 包，不删除或重新解释历史版本的四平台资产。

正式发布后应记录冻结提交、工作流身份、资产大小、SHA-256 和独立 Sigstore 验签结果，并逐字比较实际下载与已验签输入。当前安装器和 Nido 客户端自身只做各自的摘要及格式检查，不自动验证 Sigstore；从第三方取得脚本时，不能仅凭同一入口提供的包和摘要认定官方真实性。

以下为正式资产可用且核验后使用的固定版本命令。安装器只获取文件，不依赖尚未启用的 API：

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

升级保留主配置、环境文件、组件和外部数据，也保留已存在的 API sidecar；**不会自动绑定 listener**。v0.8.1 没有新开关，可在正式发布后通过已有可用代理执行一次升级：

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

本版正式包的直链、加速、真实旧包升级、API 功能与失败保护结果将在执行后补记。失败保留现有程序、配置和可证明的用户并发编辑，不自动启动、重启、切源或提供通用回滚。此次发行不部署服务或修改真实机器配置，不将假核心或回环夹具计作真实 Mihomo、订阅、公网完整链路、原生目标机或 GPU 验收。
