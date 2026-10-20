# Go 开发、验证与发行

> 当前发行版本为 [v0.10.2](nido-v0.10.2-release.md)。正式发行须通过精确标签检查、签名和独立的公开产物验收。旧版本证据不能直接用于新版本。

## 默认检查

```sh
make check
go run ./cmd/nido-dist audit --repository .
```

根模块只构建 Nido、Proxy 和外部集成。`make check` 包括格式、vet、race、CLI/Conda/Proxy 假程序隔离；Linux amd64 上另外执行 `make smoke` 验证发行包。测试使用临时 HOME 和假上游，不应继承真实组件测试路径或读取真实配置。

公开审查已迁入 Go，扫描 Git 可发布候选文件，不读取忽略的运行目录，不展示匹配秘密内容。安装 gitleaks 时继续执行源快照与全历史脱敏扫描；未安装会明确说明只完成内置检查，不宣称完整凭据扫描通过。

## GDS 与旧代码

现行 Go GDS 源码隔离在 standalone/gds 独立模块，不加入根 workspace，不参加根 go test ./...、默认 CI 或发行。主命令、状态、帮助及根模块 NVML 依赖已退出。GDS 自动发行工作流移除，不操作旧部署。

旧根 Bash 安装器、toolkit、lib/modules、旧Proxy/GDS业务后端与仅供其使用的资产已退役。历史由 Git 保留。现代隔离测试 Shell、自包含程序的POSIX解包引导、Go生成env.sh与上游Codex官方脚本是明确保留项，不构成业务双实现。

## 本地打包检查

```sh
make dist
make smoke
```

以上打包及 smoke 命令在 Linux amd64 上执行。其他开发平台只做交叉打包时使用 `make dist TARGET_OS=linux TARGET_ARCH=amd64`，不直接执行生成的 Linux 程序。

发行工具位于 cmd/nido-dist，负责包结构、平台、版本、程序与安装器校验。包只含 Nido，不含配置、认证、会话、运行数据或 GDS。安装器由已校验 Linux amd64 包与仓库模板确定性生成，验证不能再把源模板与含载荷的 install.sh 直接比较；公开包验收从精确标签源码和独立验签的包重建安装器再比较。运行打包检查不会发布 GitHub Release。

Linux amd64 是发行目标；其他平台编译成功不代表真实运行验收。macOS ARM 上通过 Linux amd64 仿真容器完成的结果应明确标为仿真，不能记为原生 Linux amd64 CI。

## 隔离与发行流程

安装器和主菜单检查覆盖两条无参数入口、三种显式网络方式、特殊自定义目录、Shell自愿注册、取消和重复安装；所有下载使用假响应或已核验的显式输入。网络合同覆盖 API/资产路由、无跨模式回退、Codex代理独立、失败保留和归属检查。

需要运行 Linux 专项时，只使用已存在的容器运行时、临时私有 HOME/网络和假程序；不启动宿主daemon、不安装系统服务、不连接Zenith。公开网络验收为独立显式步骤，不属于本轮执行范围。

GitHub check 工作流覆盖最低 Go 1.26.x 与固定 Go 1.27.2、源码与完整历史审查和隔离验证。两组 Go 均执行 `make check smoke`，固定版本额外用本地包验证完整 API 路由合同，并校验 runner 为原生 Linux amd64。统一终端框架使用固定正式 v2 版本，不再兼容旧 Go 1.23；已生成的发行程序运行无需 Go。release 工作流只有版本标签才触发签名发布；普通分支推送仅触发检查，不发布。

## 统一 TUI 前的阶段性验证（2026-10-19，历史）

| 检查 | 结果与范围 |
| --- | --- |
| macOS ARM64，Go 1.27.2 `make check` | 通过：全量 race、vet、格式与三个组件 CLI 隔离夹具 |
| Go 1.23.12 `go test ./...` | 通过：根模块全部包；Proxy 专项另通过 race |
| Linux ARM64 源码测试 | 通过：根模块全部包；不代表发行平台验收 |
| Linux amd64 仿真安装器 | 通过：30 项包安全场景、19 项真实 PTY 场景及代理脱敏检查；所有响应均为夹具 |
| Linux amd64 仿真更新与打包 | 通过：API/网络/下载、自更新失败保护、更新后退出旧菜单，以及本地包 `verify --smoke` |
| 依赖与源码审查 | `go mod verify`、`git diff --check`、内置公开审查通过；根依赖不含 GDS/NVML；本地未安装 gitleaks，未完成全历史扫描 |

该阶段完整 API 验收夹具**尚未通过**：已验证 27 项零请求/零写入拒绝、9 次正向查询、三种模式的假核心下载及 6 项失败保留；受管进程归属检查因 amd64 仿真中内核显示 QEMU 启动器而拒绝。未跳过或放宽该检查，完整结果仍为 `passed: false`，不能把局部通过作为完整验收。这些数字不代表新的原生 core 更新合同已经通过。

以上历史阶段没有提交、推送、发布、切换真实入口、连接 Zenith 或安装/启动真实组件。所有验证使用临时 HOME、假程序和隔离数据哨兵；该阶段临时 Docker 镜像与专属缓存卷已精确清理，本地忽略的验证日志保留。

## 统一方向键界面的本地阶段验证（2026-10-19，未发布）

当前 Go 1.27.2 源码的专项验证已覆盖：

- macOS ARM64 完整 `make check` 通过：格式与 Shell 语法、全量 vet/race、Nido/Codex 官方假安装器、Conda/pip 与 Proxy CLI 隔离，包含真实 Proxy 方向键、隐藏输入和取消流程。证据 `build/refactor-evidence/make-check-tui-final-r2.log`。`go mod verify`、`git diff --check` 和内置公开源码审查通过；本地没有 gitleaks，未重新执行完整凭据/历史扫描。
- 公共 UI 模型与真实 PTY：方向键、默认取消、Esc、Ctrl+C、恢复终端、窄屏/中文/缩放、无颜色、粘贴和编辑、秘密字段不回显、无 TTY 不读输入、状态与动作串行和更新后退出旧程序。
- Linux amd64 仿真：21 个非交互安装保护场景、16 个真实 PTY 安装场景。两条安装入口均为回环/固定输入夹具，不访问公网；包含自定义目录、三种持久网络模式、Shell 自愿注册及带认证代理脱敏。所有 PTY 不回答 OSC/DSR，不能通过夹具回应掩盖初始化等待。
- 最新公共 UI 全套及 Codex 页面三项 Linux PTY 验证：关联、默认取消、Esc 取消；只执行假命令，外部配置/数据哨兵保留。
- Conda/pip 最新源码的 Linux amd64 普通与 race 验证分别通过 66 组 Nido、90 组库测试及 CLI 隔离；1 个既有 Darwin 故障注入用例在 Linux 跳过，2 个真实运行时 opt-in 不执行。取消继承会话 context，不继续查询或进入未开始的提交；详见 `build/conda-ui-v2-20261019/receipt.json`。全局关闭由公共 Session 协调，即使当前操作返回正常取消也会结束页面栈，不继续下一动作。
- 最新源码 Linux amd64 包、本地自包含安装器生成及重建比较通过；普通用户、只读根文件系统、禁网容器内的包 `verify --smoke` 通过，不创建真实组件或软件目录。所有包仅存于忽略的本地构建目录，未上传或签名。
- 19 项当前源码 Linux 网络/原生 controller 委托合同无跳过通过，覆盖三模式、无回退、首次安装失败保护、原生更新不走 Nido 下载和 Provider/reload 的委托。
- 完整 CLI 宏已完成 27 项拒绝零请求/零写入、9 次 API 查询、3 种假核心下载和 6 项失败安装保护；到启动身份核验时，当前 ARM 宿主的 `/proc/exe` 指向 QEMU，产品按设计拒绝，整体 `passed:false`。保留全部原生升级/reload 合同供后续原生 Linux amd64 CI 执行，不把局部通过改成完整通过。另有独立假 core 两次同 PID/birth 自替换和单份备份/reload 夹具运行通过，不能代替 Nido 完整验收。
- 独立 Proxy 审查与两组 race：受管实例、回环 secret、单份原生备份、未知更新结果不重试、组选择链和取消边界；使用假 Runner 与 httptest。

代表性测量：macOS ARM64 实际 Nido 程序热启动首帧 56–67 ms，500 ms 空闲零输出、峰值约 18 MiB；首次冷启动约 403 ms。Linux amd64 仿真实际产物约 11.15 MB、首帧约 297 ms、峰值约 27.5 MiB；自包含安装首帧在并行构建负载下约 0.9–1.4 秒（含解包和摘要校验）。这些是本地测量，不是原生服务器性能保证；无业务轮询和空闲重绘。

当前安装脚本约 6.17 MB，相比约 4.68 MB 的发行 tar 增加约 32%，仅一次外层传输即可进入 Go TUI。用户端不需要下载 Go SDK。SDK 仅在源码构建隔离目录，官方来源、大小及 SHA 已核验。

本地阶段证据保留在忽略的 `build/tui-final-evidence/`、`build/bootstrap-ui-linux/evidence/` 与 `build/refactor-evidence/`。该阶段尚未运行 GitHub CI，没有公开发布或操作真实组件；本地仿真不能替代后续原生 Linux amd64 的完整受管进程身份与 CLI 宏验收。

收尾已精确移除本轮 7 个 SDK/验收镜像和专属缓存卷，Conda 组件另创建的 7 个临时容器也已清理；未使用 Docker prune，预先存在的基础镜像和其他任务资源保留。清理记录位于 `build/refactor-evidence/docker-task-*-cleanup.log`。
