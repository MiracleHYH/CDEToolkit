# Nido v0.9.0 发行记录

2026-10-19：[**v0.9.0 已作为 stable/latest 发布**](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.9.0)，面向 Linux amd64，增加交互式安装、组件配置菜单与可选 Bash/Zsh 注册。原生源码 CI、发布工作流、独立验签，以及 README 直链和 gh-proxy.org 两条公开命令的真实 PTY 隔离安装均已通过。公网安装验收运行于 macOS ARM 宿主上的 Linux amd64 仿真容器，原生 CI 单独列出，不将仿真执行写成原生服务器验收。

| 项目 | 状态 |
| --- | --- |
| 版本与目标 | v0.9.0，Linux amd64 |
| 功能提交的原生 CI | [6b8092f 对应运行](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39188967202) PASS |
| 发行提交与标签 | v0.9.0 → `4dd7723b07b712978763e15a98dd86b6b126e859` |
| 发行提交的原生 CI | [main 检查](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39189791707) 与 [tag 检查](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39190651275) PASS |
| 签名发布工作流 | [39190651293](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39190651293) PASS，2026-10-19 02:41:02 UTC 发布 |
| 六项资产、独立 Sigstore 验签与包身份核验 | PASS，三份签名的提交与 tag 引用一致；脚本源码及本地资产另行逐字比较通过 |
| README 直链的 latest 交互安装 | PASS，真实公网下载、PTY 回答、自定义目录与 Bash 注册 |
| README gh-proxy.org 链的 latest 交互安装 | PASS，同一加速前缀获取脚本、包和摘要，未替换为本地响应 |
| 自定义目录、可选注册与新 Shell 的 nido 命令路径 | PASS，目录包含空格、单引号和字面 `$`；输出激活命令与新 Bash 均解析到实际 `bin/nido` |
| 重复安装、保护哨兵与不启动组件 | PASS，两条链各首装与重装一次；各 12 个保护文件及属性不变，临时 `.bashrc` 仅增加唯一受管块 |
| 本版自更新、真实 Mihomo/GPU 行为 | 本轮未执行，不复用旧版结论 |
| Zenith-Dev 或真实组件部署 | 未执行 |

## 本版变化

- 安装引导询问软件目录、GitHub 文件下载方式与可选 Shell 注册，最终确认默认取消。回答使用 `/dev/tty`，不会消费 `curl | sh` 中的脚本内容；确认前不创建软件目录、不下载发行包。
- 默认目录为 `~/.nido`，可输入自定义绝对路径。安装后显示实际根的环境加载命令；安装子进程不能修改父 Shell。
- Shell 注册默认关闭，明确同意后才追加受管加载块，保留原内容、权限和 uid/gid。仅对读取对应启动文件的新 Shell 生效；Bash 登录 Shell 若未加载 `.bashrc`，仍需自行接入。
- Codex、Conda、Proxy、GDS 与 GitHub 文件策略提供独立配置入口。已有状态默认保留，配置前执行既有校验，取消或输入结束不继续提交；Proxy 确认摘要不展开订阅、节点凭据或完整 YAML。
- 显式参数、无终端和 `--yes` 继续支持自动化，`--dry-run` 不进入交互。API listener 门控、Codex 独立代理、GDS 可选和组件不自动启动的边界保持不变。

## 公开资产与安装验收

发行白名单为 `nido-linux-amd64.tar.gz`、对应 `.sha256`、`install.sh` 及三份 `.sigstore.json`，共六项资产。独立核验记录位于 `build/release-v0.9.0/verified-attempt2/receipt.json`：六项官方资产、三份签名、签名证书的提交与 tag 引用、stable/latest 身份及 Linux amd64 包身份均通过；发布安装器与标签源码相符。

本地构建比较作为独立步骤执行：公开安装前，脚本对已验签的安装器、归档和摘要与本地构建分别执行 `cmp`，全部成功。对应摘要记录在 `build/release-v0.9.0/public-interactive-attempt1/local-public-comparison.sha256`，与独立验签记录一同保留。

| 资产 | 字节 | SHA-256 |
| --- | ---: | --- |
| `nido-linux-amd64.tar.gz` | 4,173,524 | `5db4db70f75dc55be32968e6901331ffe4db69ef98931f6906662af5c548293b` |
| `nido-linux-amd64.tar.gz.sha256` | 90 | `f9e682bcbbaef93b41ebfd2492a2f6646eaaf52e41464f450472594d821d46ce` |
| `install.sh` | 10,483 | `d6c8867c68075f48da277c8239972416e10471c5c0f09798b33e4fda7b622a2f` |

包内及两条安装链落盘的程序 SHA-256 均为 `c0b8998f55127d9f2a2aafa5d4bd953660112f50e5cf04daf803281b00bda866`，程序报告 `nido 0.9.0`。

## 公网 PTY 隔离结果与边界

实际验收于 2026-10-19 02:56:23–02:56:53 UTC 完成，容器退出 0。`build/release-v0.9.0/public-interactive-attempt1/container-evidence/interactive-acceptance.json` 的总结果和 direct、accelerated 两个场景均为 `complete=true`。每条链各完成一次首装与一次重复安装，共 12 次真实公网文件 GET；脚本、包和摘要的响应在执行前逐字匹配独立验签输入，不以本地文件代替失败下载。请求和核对摘要分别保留在 `direct-http.tsv` 与 `accelerated-http.tsv`。

两条 README 命令均在真实 PTY 中执行，交互选择含空格、单引号及字面 `$` 的自定义目录并同意 Bash 注册。安装输出包含实际目录与正确引用的激活命令；该命令在临时子 Shell 中执行后，`command -v nido` 指向所选目录的 `bin/nido`。新的交互 Bash 读取临时 `.bashrc` 后，无需手动 source 即可解析到同一入口，且原 Bash 配置继续生效。重复安装保留同一个加载块，没有重复追加。安装器不会修改测试调用者或用户当前父 Shell 的环境。

每个场景的 12 个临时保护文件均保持原字节、mode、uid/gid，包括 Codex 配置、认证、`.env`、会话与数据库哨兵，Conda/pip 数据及外部 Mihomo/GDS 配置。临时 `.bashrc` 仅按明确选择增加 Nido 受管块，保留其原内容及属性。`status --json` 确认 Codex/Conda 未配置、Proxy/GDS 未安装；禁止组件执行的日志为空。没有执行 Codex、Mihomo、GDS、Conda/pip 或系统服务。

环境为普通 UID `10001`、只读容器根、只读已验签输入、私有可写临时 HOME/软件根与执行目录，不挂载宿主 HOME、组件数据、Docker socket 或 GPU。公开 URL 下载允许外网；Linux amd64 程序由 macOS ARM 上的仿真容器执行。该项证明两条公开命令的交互安装与 Bash 注册，不代表原生 Zenith-Dev 部署、Zsh 公网注册、全部网络环境可达、本版真实旧客户端自更新、Mihomo 转发或 GPU 行为验收。原生 Linux amd64 的全量竞态、PTY 夹具、包与 API 合同检查由上列 CI 独立完成。

客户端仍只验证包摘要，不自动验证 Sigstore。第三方可同时替换脚本、包与摘要；加速首装必须信任转发服务。独立验签属于本次发行验收，不代表普通客户端已经具备相同校验。

本次未在 Zenith-Dev 安装、迁移或启动真实组件，未修改现有配置、认证、会话和数据。临时环境的安装与 Shell 注册不等于真实机器部署。
