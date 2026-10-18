# Conda 组件目标版本 P0 隔离验收历史（2026-10-18）

此前 P0 阶段使用已发行 v0.8.0 与当时未发布的源码修复构建分别验收，只修改源码和隔离测试夹具。该阶段没有发布、安装到真实机器、连接 Zenith-Dev、切换真实源或迁移数据。历史发行验收见 [v0.8.0 发行记录](conda-release.md)；[v0.8.1 发布候选与公开包验收](conda-v0.8.1-release.md)另行记录，不能将下表中的源码候选结果归于新公开包。

## 输入与隔离

| 输入 | 固定值及来源 |
| --- | --- |
| 原发行包 | v0.8.0 Linux amd64，标签提交 `32a0feb6408b813cb04e92011979d450750587cf`；重新核对官方 API 大小/摘要、18 文件允许列表，以及选中三项资产的 Sigstore 身份、提交和标签 |
| 原发行程序 SHA256 | `b8c89387a6cbb46ddeb728993377352b0ccb965c384ae1f0357252ac1e9d3b1b` |
| 当时的修复构建 | 基于 `18250dbbbea3db577c40b273da29b4eee52ee78a` 的本地源码修改，Go 1.27.2、CGO=0；该次候选不是发行版，当时 VERSION 为 0.8.0 |
| 修复程序 SHA256 | `5576eaef190306f3adf36f8b8878b3550c1361cf04038ec5b51c0d4c51ecfacd` |
| 实际上游 | 官方 Miniconda `Miniconda3-py312_24.1.2-0-Linux-x86_64.sh`，实际验证 Conda **24.1.2**、Python **3.12.1**、pip **23.3.1**；安装器官方 SHA256 为 `b978856ec3c826eb495b60e3fffe621f670c101150ebcbdeede4f961f22dc438` |
| 完整运行时 | `/runtime/miniconda`；同一路径、同一完整安装，包含所有 Conda/Python/pip 依赖；不使用宿主 base |

容器固定普通 UID/GID 1000，根文件系统只读，禁用外部网络，移除 capabilities，启用 no-new-privileges。仅挂载只读测试输入与新建证据目录，HOME、配置和测试数据使用私有 tmpfs。没有挂载真实 HOME、Conda、SSH、工作区或 Docker socket。容器 loopback 仍存在。

- **normal**：完整运行时种子解到私有 `/runtime` tmpfs，可写；代表正常配置查询。
- **strict**：同一完整 prefix 位于只读镜像层，由内核拒写；所有 Nido 配置目标仍在私有可写夹具中。这样才能测试 Nido 正常保存配置，同时阻止上游写入安装目录。

宿主为 Apple arm64，Linux amd64 通过 Docker VM 仿真执行；这不等同于原生 Linux amd64 或真实 GPFS 验收。guest x86 strace 不支持 ptrace，本轮使用已核验的 arm64 strace 和私有 loader，跟踪仿真器在 VM 发出的真实文件/网络系统调用。工具与可重建镜像源码见 [运行时夹具](../tests/fixtures/conda-p0-runtime/README.md)。

两模式 prefix 的 26,741 个条目具有相同集合、类型、UID/GID、内容及链接；normal 解包受 umask 022 影响，只有 `pkgs/.constructor-build.info`、`pkgs/urls`、`pkgs/urls.txt` 从 0664 变为 0644，owner 的读写位相同，无目录 mode 差异。独立重建清单精确复现了两模式摘要，见 `evidence/runtime-normal-strict-manifest-diff.json`。因此运行时摘要按同模式比较两套程序及各自前后，不要求 normal/strict 的完整 metadata 摘要相同。

## 配置形态与检查范围

目标形态没有显式 `CONDARC`、`PIP_CONFIG_FILE`、活动 prefix 或存储覆盖变量。用户 `.condarc` 包含 `channels: [defaults]`、清华 main/r/msys2 默认频道及原有社区映射，保留 SUSTech NVIDIA 地址；原始 `custom_multichannels`、`envs_dirs`、`pkgs_dirs` 键不存在。两份 pip 用户文件均为原 iVolc index；另用不同地址证明新用户配置优先于 legacy 文件。

| 检查 | 断言 |
| --- | --- |
| default 持久性 | Conda/pip 独立捕获；dry-run 不保存；重复 setup、切源、reset、冷 Shell 不覆盖首次快照；恢复后比较七个有效镜像字段与各自模式原生基线 |
| 切源 | official/tuna 的受支持映射生效，defaults 不混入原清华列表；NVIDIA 等其他映射按原规则保留；不偷偷替换 default |
| 目录原地修改 | 从缺失键开始写 `envs_dirs`、`pkgs_dirs`、pip `global.cache-dir`；实际上游首选值匹配；不导出存储覆盖变量；不创建数据目录 |
| 数据及释放管理 | 已有环境 history/state、缓存哨兵和悬空 Python 链接保持；off/reset 不恢复旧 native 值，也不删除 default |
| 冲突 | `CONDA_ENVS_PATH`、`CONDA_PKGS_DIRS`、`PIP_CACHE_DIR` 分别导致明确拒绝，native 与 Nido 文件内容/权限不变 |
| 自定义 CONDARC | 标准及非标准显式文件、两套 prefix；TLS、客户端证书/私钥、proxy、超时、默认包、未知键和注释、mode/Gid 保护；原子替换源文件后实时读取；存储键同步 source/user |
| 环境保护 | 保存并恢复原 `CONDA_CHANNEL_ALIAS`；切源不改变 `CONDA_SSL_VERIFY`、`REQUESTS_CA_BUNDLE`、`HTTP_PROXY`、`HTTPS_PROXY` 或其有效 TLS 值 |
| 失败重试 | source 父目录拒写时保留有效 default，撤回已写 user 文件，不提交选择或改变 env；解除临时故障后重试成功 |
| 运行时完整性 | 所有运行时文件内容、链接、类型、mode、UID/GID 的前后摘要一致；结合原始 file/network trace 判断写入意图 |

Conda 24.1.2 不支持 `conda config --prefix`；脚本根据 help 检测后，使用仅对子进程设置的 `CONDA_PREFIX` 验证另一环境的原生读取优先级，不将这个结果冒充 CLI `--prefix` 支持。

## 发现与源码修复

1. **隐式用户配置切官方源仍合并旧列表。** 原发行包只同步普通用户文件的 `channels`，后置 overlay 的 `#!final` 不清除之前已合并的 `default_channels`。目标形态下实际 defaults 同时包含官方三项与原清华三项，`validate` 正确报告失败。修复使镜像操作把完整七字段同步到受管用户文件；预设使用本次有效上下文，保留私有频道及全部非镜像节点，default 继续用首次持久快照。不会改写系统或其他不受管文件，其他来源仍需通过 validate 核实。
2. **镜像验证多余触发环境目录写入意图探测。** 原发行包无条件查询 `envs_dirs`；修复源码按实际管理项查询，镜像单项仍完整核对七个镜像字段，不再触发无关探测。实际管理 envs 时仍调用原生解析，不模拟上游目录语义。
3. **测试入口适配。** 补齐隐式用户配置、双 pip 文件及缺失目录键；旧版 prefix 选择使用其支持的方式。原发行包失败证据与源码修复证据分开保存。

Conda 24.1.2 解析 `envs_dirs` 时通过 `a+` 打开 base 的 `conda-meta/history` 测可写性。normal 可打开但内容不变；strict 返回 EROFS，上游捕获错误后改变默认 envs 候选顺序。因此两模式各与自身基线比较，不能要求两个原生 JSON 完全一致，也不能声称查询从未发出写入意图。普通 Nido CLI 不为上游进程提供拒写或断网沙箱。

`custom_multichannels` 中的 `defaults`、`local` 是上游计算的保留映射；本轮固定夹具中的 local 为空。default 比较有效镜像语义，不要求把原本缺失的键恢复为缺失，也不冻结以后新增或删除 Conda build 目录造成的 local 变化。

## 验收结果

| 程序 | normal | strict | 结论 |
| --- | --- | --- | --- |
| 已发行 v0.8.0 | 7/8，目标配置组失败 | 7/8，目标配置组失败 | 官方源切换保留原清华 defaults，阻断目标部署 |
| 当时未发布的源码修复构建 | 8/8 | 8/8 | 历史源码 P0 通过，不能替代 v0.8.1 公开包验收 |

修复源码共 16 组通过，重新从最终源码构建的 Linux 程序与已验收候选程序逐字一致。原发行包两模式的其他组全部通过；目标组记录官方源验证失败后继续完成目录与 default 流程，最后统一报告失败。不能将源码修复的结果回填为发行包通过。

源码两模式的原始 trace 分别约 344 万、346 万行，均完整归类且没有 unfinished/resumed 遗漏。每侧 58 次只含七个镜像字段的查询均没有打开 base history 写入；57 次已知 history 探测来自 controller 正向检查及显式请求 envs 的基线/目录查询。normal 打开成功但内容与权限等摘要未变，strict 全部返回 EROFS；没有其他安装目录写入意图。配置区外仅有失败的 `/dev/tty` 探测；网络记录为 IPv6 loopback 能力 bind 和失败的本机 nscd Unix socket 查询，没有 IP connect 或 sendto/sendmsg。

原发行包每模式的 history 探测为 68 次，镜像与目录混合查询为 16 次；源码分别降为 57 次、4 次。四份 trace 均没有 unfinished/resumed 遗漏，网络和配置区外探测类型一致；同模式的两套程序使用相同运行时摘要与脚本输入。

Go 1.27.2 的最终 `make check` 通过，包含格式/脚本语法、vet、全量 race 和临时 CLI 套件；这些源码检查与真实上游矩阵分开记录。

独立 Go 回归补齐第二工具捕获失败不持久保存部分 default、后置 native 预检失败不提前 checkpoint，以及主配置提交后 env 刷新失败可重试修复。相关 `-race` 矩阵最初 61 个顶层测试全部通过；其中 env 刷新故障使用 Darwin 临时文件不可变标记，其他平台明确跳过。链接、超限、凭据权限、并发编辑/chgrp、原生双文件补偿由源码 Go 测试覆盖，不能说成每项都在原发行包和真实旧上游上逐一注入。新增实际上游 Go 回归也在指定容器中完成 official→tuna→default 验证。

## 复现与证据

准备已经独立验真的运行时镜像与 Linux 程序后执行；入口不下载、不调用宿主 Conda，不回退到宿主执行：

```sh
NIDO_P0_IMAGE=nido-conda-p0:24.1.2-amd64-final \
NIDO_P0_BINARY=/absolute/path/to/trusted-linux-amd64-nido \
NIDO_P0_OUTPUT=/absolute/path/to/new-evidence-directory \
NIDO_P0_LABEL=published-v0.8.0 \
NIDO_P0_PARALLEL=1 \
NIDO_P0_STRACE=1 \
NIDO_P0_STRACE_COMMAND=/usr/local/bin/nido-p0-strace \
make check-conda-p0
```

原生 x86_64 Linux 使用 `/usr/bin/strace`；关闭追踪会明确标注未做 syscall 审计，不应据此声称完成相同严格验收。`NIDO_P0_PARALLEL=1` 仅并行运行同一程序的两个独立模式，默认逐模式运行；两套程序应控制总并发以适应 VM 内存。每次输入复制到只读挂载，并记录二进制/脚本 SHA256、镜像 ID、宿主/引擎架构、模式、命令输出、运行时探针与结果。原始 `%file,%network` trace 不证明每次 `write(fd)` 的内容；需结合文件摘要及路径/进程分类，保留相对路径和 Go 线程归属的解释边界。

本轮忽略的本地证据根为 `build/conda-p0-24/`：

- `verified-release/receipt.json`、`verify-release.log`：发行来源与验签；`evidence/runtime-provenance.md`：运行时信任链、内核拒写和 trace 正向探针。
- `binary-sha256.txt`、`source-base-commit.txt`、`source-working.diff`、`source-build-info.txt`：两个程序的独立来源。
- `accept-release-v3/`、`accept-source-v3/`：相同最终输入矩阵下的 normal/strict 证据，各含 result、命令输出和 trace；早期 v1/v2 保留问题发现与测试契约修正记录。
- `trace-audit-v3/`：原始 trace 的只读分类与哈希；`final-summary.json` 汇总四场结果及来源。
- `source-final-check-v3.log` 与 `../conda-p0-failure-matrix.log`：源码全量检查和补偿矩阵。

## 部署条件与回滚边界

v0.8.0 原发行包含已复现问题，**不满足目标形态直接部署条件**。本次 v0.8.1 发布候选包含修复，须单独核验正式资产并重跑相同旧上游双模式矩阵，再判断公开包的软件兼容条件；该状态见 [v0.8.1 记录](conda-v0.8.1-release.md)。即便新包全部通过，仍须在真实环境确认 GPFS 用户配额与写入、原子 rename/link、锁及 fsync 能力，持久软件路径在平台重建后的保留方式，交互 Shell 平台 hook 是否会在其实际条件下再次改 pip，以及激活现有 energy 等环境后的路径覆盖。P0 不安装或激活这些真实环境，也不修复历史悬空链接。

本轮只验证配置读取与写入，不验证真实证书可用性、镜像连通、下载、求解或包安装；这些也不能从旧版/新版的静态配置成功推断。

`default` 只恢复首次镜像语义，不是整个配置和数据的备份。缓存/envs 修改是原地持久操作；off/reset 只停止管理，保留原生值。自动失败补偿只撤回尚未提交意图的本次安全写入，拒绝覆盖并发用户编辑；合法 default 可保留。主配置已提交后的 env 刷新失败保留意图及 native 值，修复故障后 init 可重试。历史 P0 阶段未发布；本次发布授权仍不包含现场部署、切源、数据迁移或真实机器配置修改。
