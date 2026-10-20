# Nido 使用指南

本指南对应 v0.10.1；本次变化与验证边界见 [v0.10.1 发行说明](nido-v0.10.1-release.md)。

## 安装与打开菜单

[README](../README.md#安装) 提供官方及 GitHub 加速两条无额外参数入口。生成的自包含脚本携带压缩 Go 程序，POSIX 包装只识别平台、解包、校验并调用，不联网、不提供另一套菜单。Go 界面选择实际软件目录、GitHub Accelerate、Proxy、可选 Bash/Zsh 自动加载并确认。首次脚本的传输方式由所选 URL 决定，不能从管道推断，也不因后续设置重新下载。

安装成功后按输出加载实际目录：

```sh
. '/实际软件目录/env.sh'
nido
```

`nido` 是日常管理入口；组件状态在各自页面直接展示，一级动作按视觉分组平铺。首页 Nido 分组提供 GitHub Accelerate、Proxy 和检查更新；有新版才询问是否更新，无新版则提示已是最新版。Shell 自动加载在安装时选择，初始化和受管环境刷新由配置流程完成。使用 ↑↓、Enter、Esc 和 Ctrl+C；确认默认取消，只有路径或 URL 等内容使用文本字段。完成操作后保留页面与焦点，长内容可用 PgUp/PgDn 滚动，Ctrl+R 明确刷新状态。

进入和返回不写配置、不启停服务。状态仅读本地数据和受管 core 的本地 API，不隐式查询远端最新版或测速，也不后台轮询；Codex 版本通过其本地命令查询。软件目录默认 ~/.nido，外部数据留在工具自己的目录。`NO_COLOR` 或 `TERM=dumb` 使用无颜色显示，无 TTY 时只使用明确子命令，不回落为编号问答。

## Proxy

未安装时选择“安装 Proxy”，自动下载并校验 Mihomo 内核，不另设内核安装入口。首次安装后默认可 Skip，自行编辑显示的 config.yaml；也可选择 Quick Start 使用推荐规范。Quick Start 保留订阅、URI、listeners、分流和可选链路，已有配置也能重新生成。

Quick Start 为日常和可选下载入口生成 `Proxy`、`Download` 组。可选 ISP 入口直接引用分享链接中的节点，无名称时使用 `isp-node`；需要前置时，再生成 `ISP-Relay` 组并通过节点的 `dialer-proxy` 引用它。“ISP 前置测试地址”只测试前置节点访问目标的能力，不是完整 ISP 链路检测。各端口、订阅筛选及前置测试地址通过交互配置；已有手工配置不会在启动或更新时重新生成。

替换时只留最近一份 config.yaml.back，无配置、无变化、取消或校验失败不更新备份。备份和主配置均有安全与并发检查，不对两文件宣称整体掉电原子性。配置完成仍需明确启动，运行中更换配置需确认停止，不隐式热重载。

高级用户直接编辑原生 YAML 或查看日志文件，仍需符合 Nido 用户级运行限制；不提供 YAML 导入和日志菜单。Proxy 只管理自身，工具的代理设置在使用方配置。

状态区直接显示 core 和组选择链 `组 -> 下游组 -> 节点: 最近延迟（时间）`，只展示已有测量；缺失、循环或多分支会明确提示，不累加延迟。更新只有“更新内核”和“更新订阅”，分别委托原生稳定通道和 Provider 手动刷新；新生成配置的 interval 为 0。重载、重启、停止和启动均直接列出。这些原生更新使用 core 自身网络规则，需要内核运行，不会为更新偷偷启动服务；失败不自动重试。

删除 Proxy 会确认停止受管内核后删除程序，保留配置及数据，停机失败不删除。再次安装可复用保留配置，不强制重新 Quick Start。

## Codex

选择目录关联、Codex 运行代理、官方安装更新或解除关联。自动发现为 CODEX_HOME → ~/.codex → 自定义路径；用户可跳过。关联不移动目录，不读取内部认证、会话或数据库，不管理 daemon。实际更新直接委托官方脚本，不为更新增加可选的 GitHub 发布 API 前置条件；查询当前与最新版本是独立动作。

数据目录关联与程序入口分别判断。未发现命令时，只提供在当前 CODEX_HOME 官方安装，保留现有数据；确认 standalone 入口后才显示官方更新。npm、Homebrew 或来源未明的程序继续使用原安装方式，不被官方脚本覆盖。命令从当前 PATH、显式 CODEX_INSTALL_DIR 和默认 ~/.local/bin 发现，不扫描历史包版本。点击“重新发现 Codex 命令”可在确认后刷新同一目录的命令记录与 Nido PATH，保留运行代理；取消不保存。

运行代理只修改 .env 的 Nido 受管块，冲突或归属不明则停止。解除关联保留外部数据；存在受管块时只清理仍可证明归属的字节。由 Nido 发起的官方安装、更新和版本查询使用 Nido Proxy，临时环境不读取或改写 Codex .env；安装目录参数仍明确传给官方工具，不使用 GitHub 文件加速前缀。

## Conda/pip

快捷修改 Conda/pip 镜像、Conda 环境目录、Conda 包缓存和 pip 缓存。界面区分 Nido 保存值与上游实际结果；显式验证才调用本地 Conda/Python 查询。

只更新配置，不创建或搬迁环境缓存，不安装 Python/Conda，不干预依赖求解。已有镜像 default 快照和私有频道保护保留；停止管理不等于撤销已经写入的原生配置。改变目录后已有内容仍留原处。

## Nido 网络设置

- GitHub Accelerate = None：GitHub 文件直连。
- GitHub Accelerate = HTTPS 前缀：拼接加速地址后直连加速站，不使用 Proxy；预填 https://gh-proxy.org。
- GitHub Accelerate = Proxy：GitHub 文件保持官方地址，引用同一份 Proxy URL。

三条文件路线互斥，无 Auto、叠加或失败回退。Proxy 设为 None 或通用代理 URL，启用时预填 http://127.0.0.1:7890。所有对外 API 和需要代理的本次子命令使用 Proxy；GitHub API 不拼加速前缀。None 清除本次调用继承的旧代理，回环管理请求始终直连。

Nido 更新和 Mihomo 首次安装的文件下载使用 GitHub Accelerate。Proxy 首装使用随 Nido 固定并核验大小与 SHA-256 的内核包，不查询 API；后续原生更新使用 core 自身规则。Nido 的版本 API 查询独立使用 Proxy，Codex 官方安装更新也仅使用 Proxy。这些设置不改 Codex 运行代理、Git 代理、包镜像或全局环境。

## 自动化接口

日常优先使用菜单；脚本可用明确命令：

```sh
nido status --json
nido download proxy http://127.0.0.1:7890 --yes
nido download set none --yes
nido download set https://gh-proxy.org --yes
nido download set proxy --yes
nido update --check
nido codex setup --home /absolute/path/to/.codex --yes
nido conda envs /absolute/path/to/envs --yes
```

--yes 仅跳过已确定操作的确认；--json 不进入菜单；--dry-run 不下载安装、写配置或启停服务。上游查询并非防写沙箱。认证地址不要写入共享脚本、日志或终端历史。

## 数据与发行边界

Nido 根保存程序、少量配置、env.sh 和内置 Proxy。外部 CODEX_HOME、Conda 环境、缓存与项目数据不与软件目录混用。卸载内置程序保留配置及数据，不递归清理未知文件。

GDS 仅保留独立 Go 源码，不注册、不默认编译、不发行。旧 Bash 业务入口退役，必要安装引导、生成环境片段与测试脚手架保留。[退役说明](legacy-boundary.md)

更新和安装均做有界下载、摘要与程序验证；客户端不验证 Sigstore。第三方同时转发包和摘要时不能由摘要独立证明来源。当前终端需要显式加载 env.sh，Shell 注册不修改已运行的父进程。
