# Nido v0.9.1 发行记录

2026-10-19 12:06:22（Asia/Shanghai）：[**v0.9.1 已作为 stable/latest 发布**](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.9.1)，面向 Linux amd64。原生 CI、签名发布、独立验签和 README 两条无参数命令的公开交互安装均已通过。公网安装运行于 macOS ARM 宿主上的 Linux amd64 仿真隔离容器，与 GitHub 原生 CI 分别记录，不代表已在 Zenith-Dev 部署。

| 项目 | 状态 |
| --- | --- |
| 版本与目标 | v0.9.1，Linux amd64 |
| 下载菜单功能提交的原生 CI | [`12b1421` 对应运行](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39196015980) PASS |
| 发行提交与标签 | v0.9.1 → `383a1d405a2350c23c116c2aab178da5012b0545` |
| 发行提交的原生 CI | [main 检查](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39197304553) 与 [tag 检查](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39197304680) PASS |
| 签名发布工作流及 stable/latest 身份 | [39197304681](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39197304681) PASS，公开元数据核验一致 |
| 六项资产、独立 Sigstore 验签与包身份核验 | PASS，三份签名的证书提交与 tag 引用一致，标签脚本及本地构建逐字比较通过 |
| README 直链 latest 交互安装 | PASS，无额外参数，编号选 `1`，保存及实际下载策略均为 `direct` |
| README gh-proxy.org latest 交互安装 | PASS，无额外参数，编号选 `2`，保存及实际下载策略均为 `https://gh-proxy.org` |
| 自定义目录与命令注册 | PASS，特殊字符目录、实际根激活命令和新 Bash 的 `nido 0.9.1` 均核对通过 |
| 保护哨兵、重复安装与不启动组件 | PASS，两条链各首装和重装一次，各 12 个哨兵及属性不变；注册唯一，API 保持禁用 |
| 本版自更新、真实 Mihomo/GPU 行为 | 本轮未执行，不复用旧版结论 |
| Zenith-Dev 或真实组件部署 | 未执行 |

## 本版变化

- README 保留 GitHub Releases 长地址，直链和 `gh-proxy.org` 两条命令均为 `curl -fsSL URL | sh`，日常安装无需 `-s -- --github-proxy` 等额外参数。
- 安装器下载菜单依次提供 `1` 仅官方直连、`2` 内置 `gh-proxy.org`、`3` 内置 `ghfast.top`、`4` 自定义 HTTPS 前缀、`5` 自动有限回退。选择自定义后才询问地址。
- 下载菜单直接回车只为本次使用 `auto`，保留既有策略；明确选择才保存。显式参数和环境变量仍用于自动化，且不被菜单覆盖。
- 自定义安装目录、可选 Bash/Zsh 注册、最终默认取消及从 `/dev/tty` 读取回答等边界沿用 v0.9.0；安装不关联外部 Codex、不安装或启动组件。

## 公开资产与安装验收

`build/release-v0.9.1/verified/receipt.json` 记录六项官方资产、三份 Sigstore 签名、签名证书的提交与 tag 引用、stable/latest 身份以及 Linux amd64 包身份均核验通过。公开安装器与标签源码一致，安装器、程序包和摘要均与本地发行构建逐字比较通过。发行白名单仍为程序包、摘要、安装器及三份 `.sigstore.json`。

| 资产 | 字节 | SHA-256 |
| --- | ---: | --- |
| `nido-linux-amd64.tar.gz` | 4,173,520 | `c70337cf78abb6a0f022fa6aa16fa2e14ddcacdf4b785f264ec172f3939d66a0` |
| `nido-linux-amd64.tar.gz.sha256` | 90 | `47242305fdcd1471c62a63d9ba49cba428df33cbd3e7d10c38d2854ffc7ab0c6` |
| `install.sh` | 11,762 | `e2d2f615280184fe0d1adb5e64b9d2a5ed4a73111274a9ff2bc843939b25b58a` |

公开包内与两条安装链落盘的程序 SHA-256 均为 `b88d2fbf3c24514e15967f3a0a5f08d3b274510aed206fa0c296865800c505a9`，版本为 `nido 0.9.1`。

实际公网 PTY 验收于 2026-10-19 12:07:38–12:08:09（Asia/Shanghai）完成，容器退出 0。`build/release-v0.9.1/public-interactive-acceptance/container-evidence/interactive-acceptance.json` 的总结果与 direct、accelerated 两个场景均为 `complete=true`。每条链各首装和重装一次，共 12 次真实公网文件 GET；每个脚本、包和摘要响应在执行前与独立验签输入逐字匹配，未使用本地文件代替真实下载。请求证据分别保存在 `direct-http.tsv` 与 `accelerated-http.tsv`。

两条命令都不附加参数：直链在编号菜单选择 `1`，脚本、包及摘要均经官方 URL 下载；加速链选择 `2`，三类文件均使用 `gh-proxy.org` 前缀。`download show --json` 的 saved/effective 分别为 `direct` 和 `https://gh-proxy.org`，两个场景的 API 均为 `disabled`。

自定义软件根含空格、单引号和字面 `$`。输出激活命令在临时子 Shell 中执行后，`command -v nido` 指向实际根的 `bin/nido`；新的交互 Bash 读取临时 `.bashrc` 后也解析到同一入口并报告 `nido 0.9.1`。原 Shell 配置内容继续生效，重复安装保留唯一加载块。安装器不改变调用者父 Shell 的环境。

每个场景的 12 个保护哨兵保持原字节、mode、uid/gid，覆盖 Codex 配置、认证、`.env`、会话和数据库，以及 Conda/pip 数据、外部 Mihomo/GDS 配置。临时 `.bashrc` 只按明确选择增加受管块并保留原内容与属性。`status --json` 确认外部集成未配置、内置组件未安装；禁止组件执行的日志为空。

## 验证边界

源码的 29 个真实 PTY 隔离场景覆盖各菜单选项、优先级、取消、空值、目录和注册委托，属于夹具测试。公开 latest 验证另按上节执行了直连与 `gh-proxy.org` 两种选择，不代表已验证 `ghfast.top`、任意自定义前缀或全部网络环境。

公开安装环境使用普通 UID `10001`、只读容器根与核验输入、私有可写临时 HOME/软件根/执行目录，不挂载宿主 HOME、真实组件数据、Docker socket 或 GPU。Linux amd64 程序在 macOS ARM 宿主上仿真执行；原生全量竞态、PTY、包和 API 合同验证由 GitHub Linux amd64 CI 独立完成。公网 Bash 注册不代表完成 Zsh 公网注册、真实旧客户端自更新、Mihomo 转发或 GPU 验收。

本次未访问 Zenith-Dev，未运行 Codex、Mihomo、GDS 或系统服务，未改动现有配置、认证、会话和数据。客户端仍只验证包摘要，不自动验证 Sigstore；独立验签是发行验收的一部分，不等同普通客户端能力。第三方可同时更换脚本、包和摘要，使用加速入口需要信任对应服务。
