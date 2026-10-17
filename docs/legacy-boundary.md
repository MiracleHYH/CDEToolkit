# Bash 过渡边界

本轮约束是不切换实际入口，所以当前仍可能被实际入口引用的 Bash 文件继续原位保留。
它们是冻结的旧实现，不是 Go 的安装器、下载 helper、配置后端或服务后端。
不得为补齐 Go 的功能再改一遍 Bash，也不得让 Go 通过 subprocess 调用 toolkit.sh、pm.sh、
lib/codex.sh、lib/lifecycle.sh 或 Python 解包/发布选择脚本。

新发布路径为 `make dist` → 仅包含 `cdet` 的发行包。构建、测试和发行不携带旧库、菜单、
任务查询、火山模块、Skill、GDS CUDA 编译器或任何真实配置、认证与数据。

Go 内部只保留一套生命周期：

- `Install` 与 `Setup` 共用发现、接管和登记；只有缺失或明确选择安装源时才进入 artifact 流程。
- 组件更新与 CDEToolkit 自更新共用发布选择、下载、摘要校验、解包、归属校验和原子文件事务。
- 卸载使用同一文件事务的 remove 操作，不复制旧 Bash 的服务控制、递归 purge 或安装快照引擎。
- 模块生成、环境渲染、目录推导和元数据提交共用函数，服务控制继续作为显式操作。

实际入口未来经独立授权切换并验证后，才有条件删除旧入口及其依赖库。
旧版使用文档保存在 `legacy-shell.md`，不会进入新的单文件发布包。

CI/发布迁移已完成：check.yml 使用 Make/Go 检查，release.yml 发布四平台 Go 程序包、
SHA-256 和 Sigstore bundle。旧 Bash 的发布器、PTY/平台/真实服务测试及专属夹具已清理。
GDS 的模拟 worker 检查拆为显式选择的 tests/gds-isolation.sh，不进入默认 CI。
scripts/check.sh 只转交 make check；保留该路径是因为旧自更新预检仍要求它存在。
scripts/public-audit.sh 继续承担源码/历史泄漏检查，不承担安装、解包或服务管理。
旧入口和 GDS 运行所需的库保持原样，不能因 CI 已切换就删除。
本次删除清单与后续条件见 [go-ci-release.md](go-ci-release.md)；迁移前审计记录见 [go-safety-audit.md](go-safety-audit.md)。

`cdet` 的 POSIX 安装器位于 `scripts/install.sh`，只负责 HTTPS 下载、SHA-256 校验和调用已校验的 Go CLI。命令链接、首次初始化与自身更新仍由 Go 的同一文件事务实现；不调用任何旧 Bash 模块。旧入口未切换。
