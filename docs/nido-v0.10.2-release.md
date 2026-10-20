# Nido v0.10.2

本版本调整 Proxy Quick Start 的自动配置结构和交互。发行目标为 Linux amd64。

## 主要变化

- 日常、下载和可选 ISP 前置组统一为 `Proxy`、`Download`、`ISP-Relay`。
- ISP listener 直接引用节点，不再生成仅含一个节点的包装组。保留分享链接中的节点名称，无名称时使用 `isp-node`；与内置目标或推荐组重名时明确拒绝。
- ISP 自定义测试地址用于前置候选组，仅在启用前置时询问。该测量用于选择前置节点，不代表完整 ISP 链路的健康检查。
- 保留原默认端口：管理地址 `127.0.0.1:9090`，代理入口 `7890`、可选下载入口 `7891`、可选 ISP 入口 `7892`，均可交互修改。
- 保留内核更新的两条 GitHub HTTPS 内部请求规则，优先使用 `Download`，没有下载组时使用 `Proxy`；其他流量仍按既有 listener 和规则处理。
- 保留停止确认、并发修改保护、最近一份 `config.yaml.back`、隐藏输入及摘要脱敏。更新 Nido 不会重新生成或重载已有 Proxy 配置。

## 安装与更新

直链：

```sh
curl -fsSL https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

GitHub 加速：

```sh
curl -fsSL https://gh-proxy.org/https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh | sh
```

两条命令均无需额外参数，通过方向键选择下载方式、自定义目录和可选 Shell 注册。已安装用户在 `nido` 主菜单选择“检查更新”。新模板在显式执行 Quick Start 并确认保存时生效，已有配置不会因程序更新被覆盖。

## 验证边界

正式发行工作流在原生 Linux amd64 执行完整检查、凭据审查和发行包验证，再签名发布程序包、摘要及自包含安装器。独立公开验收验证签名、API 契约、README 两条安装命令、自定义目录、命令注册，以及从上一正式版本更新的数据保留。

常规组件和交互测试使用临时 HOME、惰性核心及回环夹具，不代表真实订阅或 ISP 节点可达性。此次发布不操作 Zenith 的配置或运行组件。
