# 旧 Bash 与历史 Go 实现边界

> **本页说明历史源码的保留范围，不是旧入口的安装或迁移指引。** 新 Nido 不自动迁移旧 Bash 部署；已有真实入口、配置和运行数据不因源码修改或安装新版本而切换。

当前 Go CLI 位于 `internal/nido`，采用单一软件目录：Codex 属于外部辅助集成，Proxy、可选 GDS 属于内置组件。旧双目录 Go 核心已退出当前实现，不提供原地状态升级或目录迁入。

- `toolkit.sh`、根 `install.sh`、`codex-launcher.sh`、Proxy/GDS 脚本与旧库冻结保留，可能仍被旧部署引用。它们不进入 Nido 包，不是新 Go CLI 的组件后端。
- `scripts/install.sh` 是独立的 Nido POSIX 下载器，只下载 CLI 并初始化单目录，不调用根目录 Bash 安装器。
- `scripts/check.sh` 转交 Make/Go；`scripts/public-audit.sh` 用于源码与历史审查。
- GDS 始终显式可选。旧 CUDA 源码与模拟测试可以保留，不形成默认安装或服务启动。
- 旧原地接管、双根清单、完整目录迁入、数据库路径检查、挂载重定位和事务回滚不再作为 Nido 协议提供。

保留源文件不表示允许修改真实服务。旧入口未来退役时，需要依据当时的部署引用独立处理，不能只因新 Go 不再依赖便删除用户运行中的文件。

现行行为见 [设计说明](go-design.md)、[使用指南](usage.md) 和 [构建发行](go-ci-release.md)。[旧 Bash 使用说明](legacy-shell.md)、[双目录审计](two-root-audit.md) 与[更早安全审计](go-safety-audit.md) 仅是历史资料，不应作为 Nido 操作指南。
