# Nido / Proxy 下载自举（当前未发布源码）

本说明描述当前源码，不代表公开版本已包含这些变化；历史实际行为见独立发行记录。

## 第一次安装 Nido

通过 README 直链或可信加速 URL 取得自包含安装脚本。极薄 POSIX 包装只解包校验并运行 Go 程序，不另行联网或实现菜单。Go 界面选择软件目录、GitHub Accelerate、Proxy、可选终端自动加载并确认。后续设置不改变本次已经完成的脚本传输，也不推断外层 curl 地址。

GitHub Accelerate 为 None、HTTPS 前缀或 Proxy：分别直连官方文件、拼接前缀后直连加速站、使用通用 Proxy 访问官方文件；三者不叠加、不自动回退。加速前缀预填 https://gh-proxy.org，允许修改。Proxy 独立保存 None 或通用代理 URL，启用时预填 http://127.0.0.1:7890，允许修改；所有 Nido 对外 API 和需要代理的本次子命令使用它。GitHub API 不拼加速前缀。

## 首次安装 Proxy

在首页 Nido 分组配置 GitHub Accelerate，再在 Proxy 页面选择“安装 Proxy”，自动下载并校验 Mihomo 内核。首次安装使用随 Nido 固定并独立核验大小、SHA-256 的内核包，不查询 GitHub API；因此可只通过加速前缀完成自举，不依赖尚未安装的内核或通用代理。后续版本查询仍使用 Proxy，Nido 不会自动换源或为了自举启动内核。

没有受管 Proxy 时可先提供自己已有的通用代理地址。不要求通用代理由 Nido 管理，不扫描旧服务器目录，不读取旧代理 sidecar。隔离测试可使用明确指定并有独立 SHA-256 的本地假程序，不能跳过摘要、程序或架构检查。

安装后有配置则保留复用；无配置可选择 Quick Start 或默认 Skip。配置与启动分别确认，高级用户自行编辑显示的 config.yaml。删除 Proxy 先停止内核再删除程序，保留配置和数据；重新安装可复用它们。

## 后续更新

Nido“检查更新”先查询最新版，有新版再询问是否替换自己的受管程序，无新版显示已是最新版。

Mihomo 下载候选与校验仅用于首次安装。后续“更新内核”委托运行中 core 的原生稳定通道，“更新订阅”委托 Provider 手动刷新，均使用 core 自身网络规则；本地 controller 直连，不传入 Nido GitHub 加速前缀，不为更新偷偷启动内核。结果无法确认时报错且不自动重试，不建立另一套版本仓库、回滚或通用事务。

Quick Start 可重复执行，替换前只保留最近 config.yaml.back。Proxy 不配置 Codex、Git 或 Nido 的代理。Codex 官方安装更新使用 Nido Proxy 的临时环境；Codex 运行 .env 由其自己的配置入口单独管理，不套 GitHub 文件加速前缀。
