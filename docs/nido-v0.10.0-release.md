# Nido v0.10.0

本版本将 Nido、安装器和组件统一为方向键交互，收敛菜单、网络路线与 Proxy 的职责。发行目标为 Linux amd64，无需在开发机安装 Go。

## 主要变化

- 使用自包含安装器：一次下载后进入 Go 交互，选择自定义目录、GitHub Accelerate、Proxy 和可选 Shell 自动加载。直链与 GitHub 加速入口均无需额外参数。
- 主菜单直接显示网络设置，合并检查更新；组件页面直接显示状态，并平铺操作。
- GitHub 文件三条路线互斥：None 直连、仅拼接 HTTPS 加速前缀、引用 Nido Proxy。API 查询与官方外部安装更新命令只使用 Nido Proxy，不使用 GitHub 加速前缀，也不继承环境中的旧代理。
- Proxy 自动安装固定并校验的 Mihomo 内核，更新内核、更新订阅和重载委托 core 原生接口。移除 Proxy 的工具代理管理、YAML 导入和文件日志查看入口；删除组件保留配置与数据。
- Codex 继续作为外部集成，不迁移目录、不管理 daemon、不解析认证或会话数据库。官方安装更新只临时配置子进程代理环境。
- GDS 源码保留在独立模块，退出默认构建、集成与发行。旧 Bash 业务实现退役，只保留极薄的 POSIX 解包引导和测试脚手架。

## 安装

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

GitHub 加速入口：

```sh
curl -fsSL https://gh-proxy.org/https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

安装子进程不能改变父终端的环境。安装结束后执行输出中指向实际目录的环境加载命令，随后输入 `nido`。选择 Shell 自动加载后，新终端可直接使用该命令。

## 验证边界

标签发布在原生 Linux amd64 执行全量 vet、race、交互与 CLI 隔离检查、发行包烟测和凭据审查，然后签名发布程序包、摘要和安装器。独立公开验收从精确标签校验 Sigstore 签名，并使用正式程序和回环夹具验证三种网络路线、API 隔离、进程身份及失败的数据保护。

两条公网安装命令另在临时容器与私有 HOME 的真实 PTY 中验收，覆盖自定义目录、Shell 注册及取消。先核验同一 URL 的已签发布字节，执行原样 `curl | sh` 后再比对安装程序与已验签程序；原始管道本身不额外验证 Sigstore，也不能把预抓取核验说成第二次管道响应已在执行前验签。

所有组件网络与生命周期验收均使用假上游、假核心和数据哨兵，不代表真实 Mihomo 转发、订阅服务或 Codex 官方服务已做端到端验收。不会连接 Zenith 或读取、修改真实配置、认证、会话与数据。
