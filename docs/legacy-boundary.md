# 已退役实现的边界

Nido 的业务实现统一使用 Go。根目录旧 `install.sh`、`toolkit.sh`、Codex launcher、旧 `lib/`、`modules/`、Proxy/GDS Shell 后端、Python helper、系统服务模板与平台路径样例均已从当前源码移除。旧实现可通过 Git 历史查阅，不再提供安装入口或独立发布包。

`scripts/install.sh` 只保留自包含 Go 程序的解包、校验与调用模板，发行载荷由 Go 工具生成。安装选择全部由同一 Go 界面完成，不再保留下载前 Shell 问答。Go 生成的 `env.sh` 和受管 Shell 加载块属于环境接口；Codex 官方安装脚本属于上游接口。这些都不是第二套 Nido 业务后端。现代隔离验收脚本仍是测试脚手架，不进入发行包。

公开源码与历史凭据审查由 `go run ./cmd/nido-dist audit --repository .` 执行。内置检查只报告文件名，不输出命中的内容；安装 gitleaks 后额外执行临时源码快照与全 Git 历史的脱敏扫描。CI 必须安装 gitleaks，不能把本地未安装时的内置检查等同于完整凭据审查。可通过 `NIDO_PUBLIC_FORBIDDEN_PATTERN` 增加项目特定的禁止模式。

现行使用方式见 [使用指南](usage.md)、[Go 设计](go-design.md) 与[构建发行](go-ci-release.md)。[双目录审计](two-root-audit.md)、[更早安全审计](go-safety-audit.md) 及各历史发行记录只描述当时版本，不能作为当前操作指南。GDS 的保留源码不属于默认 Nido 编译、集成或发布内容。

本次移除仅涉及仓库源码，不检查、注销或删除任何开发机中可能仍存在的旧服务、Shell 注册、配置、认证、会话或运行数据。
