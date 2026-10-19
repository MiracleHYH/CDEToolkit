# Nido v0.10.1

本版本修复 Proxy Quick Start 的 SS2022 链接兼容，以及 Codex 数据目录已关联、命令未发现时的错误菜单与提示。发行目标为 Linux amd64。

## 主要变化

- SS 分享链接兼容导出器附加的顶层 `type=tcp`，保留 SS2022 百分号编码、密钥链和插件配置，不改变 UDP 行为；空值、其他传输、重复及未知参数仍明确拒绝。
- Codex 命令发现、状态和官方更新预检统一：没有保存入口时，从当前 PATH、显式 CODEX_INSTALL_DIR、默认 ~/.local/bin 查找，不扫描历史软件包。已保存入口失效时不静默切换到另一个程序。
- Codex 页面按实际能力显示动作：缺少命令时提供官方安装到当前 CODEX_HOME，确认 standalone 入口后提供官方更新；不覆盖 npm、Homebrew 或未知来源的命令。目录不可用时阻止安装与配置写入。
- 已关联目录可以重新发现命令，显示候选并确认后刷新关联及 Nido PATH；保留运行代理。取消不保存，不移动、删除或修改 Codex 的认证、会话和数据。
- 增加命令发现、渠道判断、动态菜单、真实终端交互及保护数据的隔离回归测试。

## 安装与更新

官方入口：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

GitHub 加速入口：

```sh
curl -fsSL https://gh-proxy.org/https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

两条命令无需额外参数，进入方向键交互，选择实际安装目录、网络设置和可选 Shell 自动加载。安装后按输出加载实际目录的 env.sh，新终端在已选择 Shell 注册时可直接使用 `nido`。已安装用户在 `nido` 主菜单选择“检查更新”。更新 Nido 不更新或启动组件。

## 验证边界

正式发布流程在原生 Linux amd64 执行全量 vet、race、交互与 CLI 隔离测试、发行包 smoke 和源码／历史凭据审查，再签名发布程序包、摘要和自包含安装器。独立公开验收校验精确标签、发行提交和 Sigstore 签名，并验证正式程序的本地 API 契约。

公网安装及自更新验收使用私有 HOME、普通用户和临时 Linux amd64 容器，保护配置、认证、会话及数据哨兵。两条原样 `curl | sh` 命令在真实 PTY 中验证自定义目录、Shell 注册及取消；执行后比对已安装程序与已验签发行包。管道本身不验证 Sigstore，预抓取验证不能保证后续管道响应已在执行前验签。第三方加速入口应仅选择自己信任的服务。

组件测试使用假核心与假上游，不连接真实 Mihomo、订阅或 Codex 服务，不操作 Zenith，不读取或修改真实组件数据。
