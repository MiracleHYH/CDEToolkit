# Nido 内部 API 代理：发布前审计

2026-10-19，v0.8.2 的**源码审计及 Linux amd64 隔离验收通过**，相关修复已正式发布。已修复确认期间绑定变化、显式临时代理的请求范围，以及响应关闭阶段校验失败被忽略的问题；141 个互不重复的顶层测试及 CLI 夹具全部通过，无跳过。v0.8.2 仅面向 Linux amd64，后续原生 CI、发布工作流、独立验签、正式包 API 隔离及公网直链、gh-proxy.org 加速安装自更新也已通过，分项及首次配额失败见[发行记录](nido-v0.8.2-release.md)。本报告保留源码审计当时的证据边界，不将其混作公开资产验收；v0.8.1 的历史行为和结果保持原记录。

## 功能与保护边界

`nido proxy bind download` 或 `nido proxy bind --tool nido` 让用户选择一次具名、无认证 listener。持久记录只保存名称，位于软件根的私有 `download-proxy.toml`，权限为 `0600`，不保存 URL 或凭据，也不新增主 `config.toml` 字段。旧版本可以忽略并保留该文件。

没有绑定文件或 listener 为空时，内部 API 功能禁用。Nido 自身、Proxy、GDS 中依赖 GitHub API 的在线安装、更新和版本查询，以及 Nido 主动进行的 Codex 版本查询，均受这个开关约束；显式 `--proxy`、GitHub 文件前缀或代理环境变量不能自行启用它。普通 Codex 更新未绑定时跳过可选 API 预查询，仍可按原有代理和确认流程委托官方安装脚本；`codex update --check` 未绑定时明确失败。

默认持久路由在每次 API 操作时重新解析 listener 名称和端口，核验受管实例、配置与监听就绪。API 使用官方 URL，发行包和 checksum 继续采用原 GitHub 文件策略。`download show --json` 的 `api_state` 只表达 `disabled` 或 `configured` 静态状态，后者不证明服务或网络就绪；未绑定不是普通 `doctor` 的故障。

高级单次 `--proxy URL` 存在明确例外：它要求先有有效、私有且非空的绑定记录，但本次传输使用明确给出的代理，**不核验所保存 listener 的运行状态**。它仍受绑定快照、API 目标、重定向和证书校验约束，不持久写入 URL 或凭据，也不将未启用状态变成已启用。

这些规则不修改全局代理、用户自行运行的 curl/Git/Conda/pip、Codex 程序业务流量或官方安装脚本内部请求。用户显式 `proxy probe --url` 保持独立诊断。文件自举 `install.sh` 不依赖 API；本地 artifact、dry-run、普通只读及无需 API 的同版本操作保留各自离线边界。

## 三项实质修复

1. **绑定快照跨越确认。** 在确认前捕获 sidecar 的存在性、文件身份、内容、权限和单链接归属，提交时复核同一快照。确认期间创建、编辑、同内容替换、删除、改权限或增加硬链接，都拒绝提交；切换、移除和重复选择均不能重新读取后接纳并覆盖并发修改。失败后保留用户当时的编辑，不把旧快照写回去。
2. **显式临时代理也限制 API 目标。** 专用传输只接受 `https://api.github.com` 的默认端口或显式 `443`，校验请求 Host，拒绝 URL 凭据、片段、非 API 域名及其他端口。客户端拒绝自动重定向，底层每次 RoundTrip 仍复核目标，避免调用方覆盖重定向回调后越界。TLS 证书验证强制开启，清除继承的固定 ServerName，让 HTTPS 代理跳与最终 API 跳分别核验名称；不采用 `InsecureSkipVerify`。
3. **传播响应 Close 校验失败。** `githubdownload.Get` 在正文、大小和长度检查后，必须成功关闭响应才返回数据。绑定或受管进程在 Close 时失效，会返回可追溯但脱敏的错误，丢弃已读字节，不触发 `auto` 或其他网络回退。成功和失败路径均只关闭一次；读入本身已失败时，清理错误不覆盖原始错误。

相关源码与用例：

- [绑定与快照](../internal/nido/api_proxy.go)、[跨确认及 Close 回归](../internal/nido/api_binding_audit_test.go)。
- [显式 API 客户端](../internal/nido/api_scope.go)、[目标与 TLS 回归](../internal/nido/api_explicit_scope_test.go)。
- [GitHub 下载](../internal/githubdownload/download.go)、[响应关闭回归](../internal/githubdownload/response_close_test.go)。

## 验收入口修复

Proxy 公开发行 harness 的 `NIDO_TEST_PROXY_RELEASE_BINDING_MODE` 现在默认为 `current`，只接受 `current|legacy`。历史 v0.8.1 及此前包显式选择 `legacy`；模式写入环境证据并明确传入 namespace，不根据失败结果自动降级。入口契约补验显式 current、默认 current 和非法模式，历史 mock 与示例继续明确 legacy。

未启用及 off 状态的 `doctor` 用例先确认 fixture 的环境、下载策略和静态组件没有其他问题，再要求退出码为 0、issues 为空。公开入口 mock 补齐 `file` 命令的存在性；若意外执行仍明确失败，真实入口的工具前置条件不变。

`make fmt-check` 纳入 bootstrap 脚本语法检查，`make check-nido-proxy-bootstrap` 提供专用入口。bootstrap 按完整名称固定十一组既有契约，缺少或跳过均失败；新增审计用例由独立矩阵执行，避免前缀匹配悄悄扩大或漏改统计。所有编译必须成功结束后再冻结输入，记录源码、程序和脚本摘要；入口本身不证明预构建输入来自当前源码。

## 本轮验证状态

证据根为 `build/api-proxy-final-audit-20261019/`。以下按顶层测试统计，子场景及重跑不重复累计：

| 范围 | 顶层测试 | 当前结果 |
| --- | ---: | --- |
| githubdownload | 17 | PASS |
| Proxy API client 与分路由 | 14 | PASS |
| GDS API 分路由 | 4 | PASS |
| 自更新库 | 10 | PASS |
| Codex 集成 | 45 | PASS |
| Nido 回归、审计及公开入口契约 | 40 | PASS，无跳过 |
| 固定 bootstrap 契约 | 11 | PASS，无跳过 |
| 源码 Nido CLI、Proxy CLI current、历史 v0.8.1 legacy 与 sidecar 兼容 | 单独记录 | PASS |

已完成库测试 90 个、Nido 主矩阵 40 个、bootstrap 契约 11 个，共 141 个互不重复的顶层用例。禁用矩阵在缺少绑定和空 listener 两种状态下，结合七种代理或文件策略条件覆盖 224 个在线入口调用：HTTP、拨号、确认读取、组件或候选执行及配置数据修改均为零。跨确认覆盖六种并发编辑和切换、off、同值三种操作；临时代理覆盖 HTTP/HTTPS 两跳与坏证书；关闭阶段失效不能返回可用元数据。公开入口契约使用命令和 namespace mock，不是公网发行包验收。

最终汇总为 [receipt.json](../build/api-proxy-final-audit-20261019/receipt.json)，测试清单和日志在 `evidence/`，固定十一组的记录在 `bootstrap/container-evidence/`。三个容器的最终退出码均为 0，输入摘要与冻结程序一致；源码、程序及脚本摘要保存在证据根。`make fmt-check`、项目路径的 `go vet`、shell 语法和 `git diff --check` 均通过。

源码 Nido CLI 和 current Proxy CLI 通过；独立验签的真实历史 v0.8.1 程序通过 legacy Proxy CLI。旧版读取新配置并忽略 sidecar 后，`config.toml`、`env.sh`、`download-proxy.toml` 三份摘要一致。源码 CLI 使用 `Version=0.8.1` 测试标记，同号历史程序的 SHA-256 为 `e412dd4d37ffaa2baa7add444d1e1e398fb65877b6e7ca48156c79392c68c964`；源码程序不是该公开发行产物，不能仅凭版本字符串混用两者证据。

执行环境为 Linux amd64 仿真容器、UID/GID `10001`、只读根、临时 HOME/cache、`--network none`，仅允许回环 HTTP/TLS/CONNECT 夹具。使用假核心、受控 runner 和本地最小版本候选，不执行真实 Mihomo。常规 Codex 权限保留夹具使用其正常 `umask 022`，父目录仍为私有 `0700`；其他专项保持私有创建策略。

首次 Nido 主矩阵为 38/40 个顶层用例通过，两项失败来自入口夹具缺少 `file` mock、绝对 `/tmp` 位于只读根，原始日志保留在 `attempt-1/`。修复只补 mock 存在性并为测试容器提供独立 `/tmp` tmpfs，不放宽真实入口前置条件或产品保护；完整 40 项已重新通过。首次 `go vet ./...` 扫入 `build/` 中缓存 SDK 的故意非法测试包；该失败日志也保留。随后对项目路径 `./cmd/... ./internal/... ./tests/fixtures/proxy-core` 的 vet 已通过，没有删除缓存或隐藏原始失败。

## 后续使用边界

本地 artifact 免于 Nido 的 API 下载，但仍会执行候选版本查询，已有配置时可能执行核心验证。格式、架构和 SHA-256 检查不能证明来源可信，也不能把任意核心或配置变成无网络、无文件访问的纯数据；部署前仍须独立核验来源和兼容性。

失败补偿只保护本次可证明属于 Nido、快照仍一致的修改和候选；不自动启动、重启、切换代理或恢复用户并发编辑，不提供通用回滚。上述源码审计阶段未发布、部署或修改真实机器配置，也不证明真实 Mihomo 启动、订阅、公网完整链路、原生目标机或 GPU 验收通过。后续发行状态单独记录，操作流程见[自举方案](nido-proxy-bootstrap.md)。
