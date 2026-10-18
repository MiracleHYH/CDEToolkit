# Conda / pip 组件审计（2026-10-18）

本功能自 Nido v0.8.0 发行。本文保留各阶段的历史审计与隔离验证记录，其中“未发布”等措辞描述当时的操作范围，不是公开发行资产的公网验收结论。历史发行记录见 [v0.8.0 说明](conda-release.md)，本次发布候选与待完成的公开包验收见 [v0.8.1 说明](conda-v0.8.1-release.md)。

发行后的目标版本 P0 验收见 [Conda 24.1.2 / pip 23.3.1 隔离验收](conda-p0-acceptance.md)。该轮发现 v0.8.0 在隐式用户配置下切官方源会残留旧列表，修复纳入 v0.8.1 发布候选；原发行包、当时未发布的修复构建与新公开包的结论分别记录。

首轮审计覆盖 default 持久化、镜像切换、原生路径修改、配置与数据保护。当时未提交、推送或发布版本，未修改真实机器配置。功能约定见 [Conda 设计](conda-design.md)。

## 发现与修复

| 问题 | 修复后的行为 |
| --- | --- |
| 首次 default 与新选择一起在原生频道写入后保存，中断可能丢失原始地址 | 先保存 default 并同步软件目录，再开始原生写入；后续失败保留有效快照，不提交失败选择 |
| 确认期间原始配置改变，保存的 default 仍是确认前状态 | 首次确认后重新读取；已有持久 default 永不覆盖 |
| Conda 迁移映射未被快照覆盖，同一旧 URL 可能解析到不同仓库 | 新快照纳入 `migrated_channel_aliases` 和 `migrated_custom_channels`；验证完整镜像语义 |
| 切预设使用首次频道列表，删除后来新增频道 | official/tuna 基于本次有效频道与镜像上下文，只映射受支持的公共源；default 独立恢复首次快照 |
| 非标准原 CONDARC 不再加载，私有命名频道落到公共 alias | 预设保留当前私有 custom channel、multichannel、alias 与迁移映射，验证其解析上下文 |
| 仅凭 URL 中 `/pkgs/main` 等片段误改私有仓库 | 回退识别限制为明确公共域名和标准路径；显式上下文中的已知映射仍可转换 |
| 转义 token 路径可能带到公共镜像 | 按解码后的路径段识别并移除 token；不转发原认证及查询参数 |
| 缓存、envs 或 pip 操作顺便重写 Conda 频道 | 显式操作范围只应用指定项；重复同值命令可重新应用该项，其他项保留后续手工修改 |
| off/reset 后尚未 source，原显式 CONDARC 中的旧路径仍覆盖新设置 | 过渡状态仍同步已知原文件；不修改不可变 overlay |
| pip INI 缩进 section 外观被误判，破坏上一配置的多行值 | 按上游 continuation、缩进与 section 规则识别，歧义输入安全失败 |
| 原子替换保留 mode 却丢失原组所有权 | 保留原 Gid；无法保留或检测到并发 chgrp 时拒绝覆盖 |
| native 修改接受上游实际忽略的 Conda 文件名 | 预检只接受 `.condarc`、`condarc`、`.yaml`、`.yml` 目标 |
| pip 原始 URL 的 Python repr 解码不完整 | 精确还原转义的 Unicode 值，拒绝实际控制字符，保持合法原地址 |
| pip 验证误判配置优先级，漏掉 install/download 等独立缓存覆盖 | 按上游优先级核对索引冲突；缓存核对所有命令的目录覆盖 |
| setup dry-run 绕过 env.sh 归属检查 | 与真正保存使用相同状态、归属及快照预检，预览不写入 |
| overlay 内容一致但已公开或增加硬链接仍可使用 | 校验私有权限、当前用户所有权和单一链接；保存选择前再次核对内容 |

## 隔离验证

- Go 单元及组合测试使用临时 HOME、软件根、原生文件与可控的只读 Runner；不调用真实安装器或联网镜像。
- 新增组合回归在 `internal/nido/conda_audit_test.go`，覆盖无关命令保护、同值重放、确认窗口、首次写失败时 default 先落盘、保存失败补偿、取消无副作用、私有频道与凭据隐藏。
- 原生回归覆盖双文件写入中途失败时精确补偿、用户并发编辑及 chgrp 拒绝、权限和所属组保留、配置注释与无关字段保留、不创建缓存或环境数据目录。
- 最终 `make check smoke` 全量检查通过；最低 Go 1.23 的 Conda 包与 Nido Conda 组合 race 检查通过。日志保存在 `build/conda-audit-final-check.log`、`build/conda-audit-go123.log`。
- 另用现有 Conda 26.1.1 与 agent 环境的 pip 26.2.1 / Python 3.12，在临时 HOME 中核对原生配置合并、镜像切换、重复 setup、default 恢复与路径保留；不联网、不安装包、不创建数据目录。记录在 `build/conda-audit-upstream.log`。
- 对非标准私有 CONDARC 单独核对：私有命名频道、旧 alias 迁移在 official/tuna 后仍解析到原私有地址；删除临时原文件后，default 仍能恢复首次解析结果并通过 validate。记录在 `build/conda-audit-context.log`。Linux/macOS 的 amd64、arm64 交叉编译不代表其他平台完成实际上游运行验收。

## 边界

旧五字段快照按原样兼容，不重捕或覆盖；无法补回旧快照未记录的迁移规则。操作可能因中断保留未激活的私有 overlay，reset 不删除历史文件或数据。

后续修复已经解决原自定义 CONDARC 被遮蔽的问题，见下节。验证不测试镜像下载、包完整性或所有平台及上游版本；更早的管理员镜像 `#!final` 及显式环境覆盖仍由上游优先级决定。

## 自定义 CONDARC 非镜像设置修复

存在原显式文件时，保留同一 CONDARC 路径，原地仅替换七个镜像字段，不把证书、私钥路径、代理或其他设置复制进 Nido。这个方式保持标准文件的去重与优先级、`--prefix` 的动态搜索、`$CONDARC` 引用，以及原文件后续编辑的实时生效。没有显式来源时继续使用已有不可变镜像 overlay。

原文件路径关联持久保存，首次 setup conda/all 可记录未激活的来源，新 Shell 无原值 marker 时仍可复用。显式 Conda 镜像操作可换到当前外部文件；pip、路径、off/reset 或活动 setup 不偷偷更换关联。原文件必须存在并可安全修改；目录、链接、硬链接等不安全输入明确拒绝。缺失源仍可 reset，已保存 default 不丢失。原镜像在首次原生改写前落盘，失败补偿、其他节点和注释、原 mode/Gid 保留，以及所有镜像字段的凭据传播检查继续生效。off/reset 不回滚已写原生镜像或目录。

新增回归位于 `internal/nido/conda_source_audit_test.go` 及原生配置测试，覆盖来源选择、跨 Shell、实时原子替换、作用范围、dry-run、秘密不复制、失败补偿与数据保护。真实 Conda 26.1.1 离线验收脚本为 `tests/conda-source-upstream-isolation.py`，用临时 HOME、Conda root、两个 prefix、证书及数据验证：

- 非标准与标准 `.condarc` 两种来源下 official/tuna/default/off/reset 不改变 TLS、客户端证书、代理、超时、创建默认包列表及目录的实际值。
- 标准配置不因重复加载提升优先级，`--prefix` 和 `$CONDARC` 引用保持原语义。
- 原文件原子替换后新非镜像配置立即读取，未知键、注释、文件权限和数据夹具保留。

该真实隔离验收已通过，日志为 `build/conda-source-upstream.log`；最终 `make check smoke`、Go 1.23 定向 race 和 Linux/macOS amd64、arm64 四平台编译均通过。全量及最低 Go 版本记录为 `build/conda-source-final-check.log`、`build/conda-source-go123.log`。没有发布或修改真实机器配置。

## 发布前最终审计

本轮继续只修改源码并做隔离测试，未提交、推送、发布或修改真实机器配置。新增回归分别位于 `internal/nido/conda_final_audit_test.go`、`internal/integrations/conda/final_audit_test.go` 及原生文件测试。

| 发现 | 修复 |
| --- | --- |
| 新增 pip default 的中间保存吞掉同预设下 source A→B 的差异，或提前把活动来源切到尚未应用的 B | 原生比较保留操作开始时的配置；中间记录只追加 default，已有活动来源直到原生应用成功后才提交 |
| default 的后置映射 `#!final` 留住标准用户文件后来新增的私有映射和迁移规则 | 显式 default 恢复标准用户及显式源文件的完整已捕获镜像字段，保留所有非镜像节点；旧五字段快照不杜撰未捕获的迁移规则 |
| 新 Shell 没有 CONDARC 环境变量时，预设查询忽略已保存的原 source | 发现阶段显式读取正在配置的 source；validate 仍核对真实调用环境，不偷偷注入期望值 |
| 慢查询之后覆盖用户刚新增的私有频道 | 捕获前后核对原生文件，检查贯穿到准备完成后的实际写入之前；发现并发变化则不写原生文件 |
| 首次 setup 可以记住缺失、目录或链接型 source | 显式来源关联也做安全有界读取检查；活动 setup 与无关 pip 操作不重关联、不因缺失来源阻断，reset 仍可释放 |
| 原镜像 URL 的 `%2F` 等转义路径被展开成不同地址，公共预设也可能匹配失败 | 按转义路径边界截断频道名并保留原路径表示，正确匹配公共预设且不传递原凭据和查询参数 |
| pip 预设名和保存 URL 可互相矛盾 | 校验带名称的预设、default 与 custom 组合，保留旧无名称 URL 配置兼容 |
| dry-run 允许生成超过读取上限的文件，源文件消失后可能重建为只有镜像的文件 | 在原生预检阶段检查输出大小并再次要求已关联 source 存在，任何目标失败都不先写另一个文件 |
| 新文件已硬链接发布但临时链接清理失败，写入可能脱离补偿；清理可能删除并发替换的临时文件 | 发布后的错误尝试安全撤销并准确报告无法补偿的情况；清理校验父目录和临时文件身份 |

标准测试不要求安装 Conda。可选的真实回归通过 `NIDO_TEST_REAL_CONDA=/absolute/path/to/conda go test ./internal/integrations/conda -run '^TestFinalAuditRuntime'` 在临时 HOME 中验证原生合并和 default 恢复，同时保留证书、代理与数据。最终全量、最低 Go 版本及真实上游证据分别记录在 `build/conda-prerelease-final-check.log`、`build/conda-prerelease-go123.log`、`build/conda-prerelease-upstream.log`。

最终 `make check smoke`、Go 1.23 定向 race、四平台交叉编译全部通过；Conda 26.1.1 的来源保护验收和 default 映射恢复回归也通过，后者日志为 `build/conda-prerelease-runtime.log`。未进行镜像网络下载或包安装。

补偿不能覆盖并发用户编辑，也无法强行突破操作系统拒绝删除或写入；这类错误会明确报告已发生的写入和未能安全补偿的目标，不宣称完全未写入。主配置已提交后的环境刷新失败按既有规则保留意图和原生设置，可重新 init。验证不涉及镜像下载、包安装或真实机器部署。

## v0.8.1 发布候选补充

目标版本 P0 复现了后置 overlay 的 `#!final` 不清除之前已合并镜像列表的问题。镜像操作现将完整选中七字段同步到标准用户文件及受管显式源，official/tuna 使用本次上下文，default 仍使用首次持久快照；证书代理等非镜像节点和数据保持原保护边界。不改写更早的系统文件，其他来源冲突仍通过实际 validate 报告。

validate 按当前管理项选择上游查询键。镜像单项保留完整七字段检查，只有实际管理 envs/pkgs 时才查询对应目录。真实 envs 查询仍保留 Conda 24.1.2 的可写探测与回退顺序，不把普通配置查询声称为系统调用拒写沙箱。

新增回归覆盖隐式用户配置的 presets/default、NVIDIA 私有映射、按项查询，以及捕获和提交阶段的失败重试。完整 Conda 24.1.2 / Python 3.12.1 / pip 23.3.1 运行时中的历史源码 normal/strict 两模式均为 8/8；原 v0.8.0 两模式各为 7/8。新版本的资产验签、直链和加速安装、真实旧包自更新以及公开包双模式验收仍须按 [v0.8.1 发行记录](conda-v0.8.1-release.md)逐项完成，不提前视为通过。
