# GDS 独立源码

GDS 在此目录保留单机 GPU 空闲显存保留的源码、协议与专属测试。自 2026 年 10 月 19 日起，当前 Nido 不注册 GDS，不提供 `nido gds`；根构建、测试、CI 和发行流程均不接入它。这里没有当前安装、更新、迁移、清理既有部署或自动发布的入口。

真实 GPU、驱动兼容、实际显存分配与释放、外部任务竞争和负载影响尚未验收。源码保留、编译成功及 CPU 假驱动测试均不等于可在真实 GPU 上部署使用。

## 模块边界

本目录是嵌套 Go 模块，module 为 `github.com/MiracleHYH/CDEToolkit/standalone/gds`。根模块不通过 require/replace 引入它，也没有将其加入根 `go.work`；根目录的 `go test ./...` 不遍历本模块。开发检查需进入本目录并显式设置 `GOWORK=off`。

| 位置 | 保留内容 |
| --- | --- |
| `cmd/gds` | 独立程序源码与入口测试；当前只编译，不执行生产程序或元数据命令 |
| `internal/gdsapi` | 配置、采样、进程身份与许可协议 |
| `internal/gdsruntime` | 状态机、monitor、CPU worker 协议与故障释放逻辑 |
| `internal/gdsruntime/nvidia` | Linux amd64 cgo 的 NVML/CUDA 绑定及假驱动测试 |
| `internal/gdsutil` | 文件、锁、时钟与进程核验 |
| `internal/components/gds` | 历史管理服务及 CPU 夹具；没有 Nido 注册或公开管理命令 |
| `internal/githubdownload` | 为模块自包含保留的历史下载 helper 副本及测试 |
| `VERSION`、`.go-version` | 保留的组件版本与工具链记录，不触发构建、标签或发行 |

NVML 依赖仅在本模块的 `go.mod` / `go.sum` 中维护，根 Nido 不再携带它。原生 NVIDIA 文件受 `linux && amd64 && cgo` 约束，其他平台或禁用 cgo 时使用明确 unsupported 的适配。CUDA Driver API 在运行时加载；此处保留该实现，不运行真实驱动检查。

`internal/githubdownload` 保留隔离前 GDS 的下载行为。根 Nido 本轮下载三模式整改不会同步修改这份停用 GDS 源码，也不会使 GDS 接入新策略；不能将根 Nido 的当前下载说明套用于此历史副本。

## 只编译与依赖检查

以下命令在仓库根目录开始，使用已有 Go 和已有依赖缓存，禁止自动下载依赖或切换工具链。缓存不足时应直接失败。`go.mod` 保留 Go 1.23 的最低版本声明，`.go-version` 记录 1.27.2；本轮迁移另用已有 Go 1.23.0 完成编译检查。

```sh
cd standalone/gds
GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 \
  go test -mod=readonly -run '^$' ./...
GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go list -mod=readonly -deps ./...
```

第一条没有匹配的测试，只检查测试程序编译；第二条只解析 Linux 原生依赖，不编译或执行驱动路径。两者均不运行生产 GDS 或其元数据。完整包测试需要单独审查执行范围，不能把 `go test ./...` 当作等价的编译检查。

[tests/linux-native-isolation.sh](tests/linux-native-isolation.sh) 是显式的普通用户 Linux amd64 namespace CPU 假驱动测试入口，需要已有 SDK、编译器和离线 vendor。它运行 `internal/...` 的 CPU/假驱动测试，`cmd/gds` 测试仅编译；生产程序只构建和静态检查，不执行生产 GDS 或元数据，不构建 Nido。缺少隔离条件时失败，不回退到宿主执行，不安装工具或组件。本轮未执行该 Linux 原生入口。

## 历史记录

2026 年 10 月 18 日发布的 GDS gds-v0.1.0 与 Nido v0.5.0，以及当时的 CPU 验收和签名出处，保留在 [发行记录](../../docs/gds-release.md) 和 [审计记录](../../docs/gds-audit.md)。技术协议与历史管理接口见 [设计记录](../../docs/gds-design.md)。其中的旧 `nido gds` 命令和 Make 目标均不适用于当前源码。

旧公开发行 harness 仅以不可执行文本保存在 [archive/gds-public-release-isolation.sh.txt](archive/gds-public-release-isolation.sh.txt)，供追溯当时的验证过程。`scripts/gds-release-build.sh` 和 GDS 发布工作流已移除；保留历史文件不授权重跑公网验收、安装组件、创建标签或发布资产。
