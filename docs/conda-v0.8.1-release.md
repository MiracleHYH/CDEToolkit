# Nido v0.8.1：Conda 镜像合并修复

本文记录 v0.8.1 的发行及公开包验收。当前处于发布候选阶段；标签、正式资产、安装和自更新结果须在实际完成后补充。没有部署到 Zenith-Dev 或其他真实机器，没有切换真实镜像、调整真实路径或迁移数据。

## 修复内容

v0.8.0 在无显式 CONDARC、用户 `.condarc` 已有清华 `default_channels` 的目标形态下，切换 official 会把原三项与官方三项合并。后置 overlay 的 `#!final` 阻止更后来源，却不清除之前的列表。v0.8.1 镜像操作将完整选中上下文同步到标准用户文件及受管显式 CONDARC；official/tuna 保留本次私有频道、别名和迁移映射，default 继续恢复首次持久快照。七字段为 `channels`、`default_channels`、`custom_channels`、`custom_multichannels`、`channel_alias`、`migrated_channel_aliases`、`migrated_custom_channels`。

validate 改为按当前管理项查询，镜像单项仍完整验证七字段，只在实际管理 envs/pkgs 时查询对应目录。这样避免镜像检查多余触发旧 Conda 的 base 可写探测；实际 envs 查询仍使用原生解析，不模拟上游合并语义。

原始 default 不重捕或覆盖；已有五字段快照仍只恢复其实际记录的内容，不补造迁移字段。缓存及 envs 仍是原生配置原地修改，不创建、搬迁或删除数据；off/reset 保留已写 native 值。证书、私钥、代理和未知键、注释、mode/Gid、并发检查与失败补偿边界沿用原设计。没有新增 Conda 安装、初始化、环境修复或迁移功能。使用命令见 [指南](usage.md#conda--pip)。

## 发行与验收状态

| 项目 | 当前记录 |
| --- | --- |
| 源码 P0 历史证据 | 修复源码在 Conda 24.1.2 / Python 3.12.1 / pip 23.3.1 的 normal、strict 模式各 8/8；原 v0.8.0 各 7/8，问题组失败。详情见 [P0 历史](conda-p0-acceptance.md) |
| 冻结提交、VERSION 与 v0.8.1 标签 | 待冻结与核对 |
| 本次源码、公开敏感信息检查及 CI | 待本次最终检查与 CI |
| 四平台正式包、摘要、安装器和 Sigstore | 待发布后逐项独立核验，不用本地候选通过代替 |
| macOS 原生双链安装、自更新 | 待使用正式 v0.8.0 升级至 v0.8.1；直链及 `gh-proxy.org` 分别执行 |
| Linux amd64 双链安装、自更新 | 待使用正式资产在隔离 Docker 中执行，并记录宿主与实际执行架构 |
| v0.8.1 公开 Linux 包旧上游 normal/strict | 待同一已验签程序完成完整矩阵与运行时前后核对 |

以上状态仅在有对应原始证据后更新。[v0.8.0 发行时的通过项目](conda-release.md#v080-历史发行记录)保留为当时的平台、版本与配置范围，不替代本次新增目标形态或新资产验收。

## 验证入口

使用固定 [.go-version](../.go-version) 工具链先完成 `make check smoke release verify-release`，再由正式标签工作流执行发布检查、签名与上传。资产执行前独立核对发行 API 的大小/摘要、文件允许列表、对应标签的 workflow/issuer/commit/ref 身份和 Sigstore bundle，并与相同冻结源码的本地构建比较。普通安装客户端当前不验证 Sigstore；验收方的独立验签不改变加速服务的信任边界。

公开安装与自更新须提供已经独立验真的新、旧正式资产。latest 下载入口必须实际解析到目标新版本，测试失败不得改用本地文件冒充公网成功：

```sh
NIDO_ACCEPT_INPUT=/absolute/path/to/verified-v0.8.1/assets \
NIDO_ACCEPT_OLD_INPUT=/absolute/path/to/verified-v0.8.0/assets \
NIDO_ACCEPT_VERSION=0.8.1 \
NIDO_ACCEPT_OLD_VERSION=0.8.0 \
NIDO_ACCEPT_OUTPUT=/absolute/path/to/new-public-evidence-directory \
make check-nido-public-release
```

Docker 验收另设置 `NIDO_ACCEPT_MODE=docker NIDO_ACCEPT_PLATFORM=linux/amd64`。入口在临时 HOME 和软件根中分别验证 direct、`gh-proxy.org`、重复安装、Shell 注册与 PATH 幂等、真实旧版本更新、失败保留、同版本离线跳过和 Conda CLI 夹具。新旧程序均须来自其正式发行；公网安装结果与已验签程序逐字比较。

指定旧上游验收从同一已验签 Linux amd64 发行包提取 Nido，使用已有完整运行时镜像：

```sh
NIDO_P0_IMAGE=nido-conda-p0:24.1.2-amd64-final \
NIDO_P0_BINARY=/absolute/path/to/verified-v0.8.1/linux-amd64/nido \
NIDO_P0_OUTPUT=/absolute/path/to/new-runtime-evidence-directory \
NIDO_P0_LABEL=published-v0.8.1 \
NIDO_P0_STRACE=1 \
NIDO_P0_STRACE_COMMAND=/usr/local/bin/nido-p0-strace \
make check-conda-p0
```

运行时与 trace 工具的来源、原生 x86_64 strace 参数、模式差异及重建方式见 [运行时夹具](../tests/fixtures/conda-p0-runtime/README.md)。normal 使用私有可写完整 prefix，strict 由内核保护安装目录；测试区均允许写入。没有真实 HOME、SSH、宿主 Conda 或服务 socket 挂载；运行时配置验收禁用外部网络。安装公网验收与离线上游矩阵必须分别记录，不能互相替代。

## 部署与恢复边界

公开包通过新矩阵后，才能确认上述固定上游、固定配置形态的软件兼容条件；不自动满足真实 Zenith-Dev 部署条件。现场仍须确认 GPFS 配额与写入、原子 rename/link、锁及 fsync 能力，软件目录的持久保留方式，平台改源 hook 的职责，以及激活已有环境后的覆盖变量和悬空链接。隔离结果不证明镜像联网、求解、包下载或真实证书可用，也不等同于原生 Linux 或 GPFS 验收。

`default` 只恢复首次镜像语义，off/reset 只停止管理。目录修改不会自动还原历史键或移动数据。失败补偿拒绝覆盖用户并发编辑，系统拒绝撤销时报告未恢复部分；合法首次 default 可以保留。主配置已提交而 env 刷新失败时保留意图和 native 值，修复故障后可重新 init。完整人工撤销须事先保存相关原文件和 metadata，并独立处理数据；本次发行范围不包含部署或真实机器配置修改。
