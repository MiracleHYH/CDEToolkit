# 旧 Bash 边界

当前 Go 实现位于 `internal/cdet`，只管理安装根和组件存储根。旧 Bash 入口及仍被引用的库冻结保留，不参与 Go 安装、下载、解包、组件迁入或发行。

- `toolkit.sh`、根 `install.sh`、`codex-launcher.sh`、Proxy/GDS 脚本与旧库可能仍被已有部署引用，不在本次源代码收敛中切换或安装。
- `scripts/install.sh` 是 Go CLI 的 POSIX 下载器，与根目录旧安装器不同；它将目录选择和持久写入交给 Go。
- `scripts/check.sh` 转交 Make/Go；`scripts/public-audit.sh` 用于源码和历史审查。
- GDS 仅显式选择；旧 CUDA 模块不进入默认 Go 安装、测试或发行包。
- 旧原地接管、多根映射、服务定义改写和分散路径参数不再作为 Go 协议提供；旧开发状态不原位兼容。

保留源文件不表示 Go 会调用它们，也不表示允许修改真实服务。旧入口未来退役时应依据当时的引用和部署情况独立处理。

现行协议见 [设计](go-design.md)、[安全边界](go-safety-audit.md) 和 [CI 与发行](go-ci-release.md)。
