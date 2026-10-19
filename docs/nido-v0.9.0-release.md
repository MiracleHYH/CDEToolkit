# Nido v0.9.0 发行记录

本版面向 Linux amd64，增加交互式安装、组件配置菜单与可选 Bash/Zsh 注册。本文先记录发行范围与验收标准；发布和公网验收结果按实际执行补充，pending 不表示已经通过。

| 项目 | 状态 |
| --- | --- |
| 版本与目标 | v0.9.0，Linux amd64 |
| 功能提交的原生 CI | [6b8092f 对应运行](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39188967202) 已通过；发行提交待复核 |
| 发行提交、标签与发布工作流 | pending |
| 六项资产、独立 Sigstore 验签与包身份核验 | pending |
| README 直链的 latest 交互安装 | pending |
| README gh-proxy.org 链的 latest 交互安装 | pending |
| 自定义目录、可选注册与新 Shell 的 nido 命令路径 | pending |
| 重复安装、保护哨兵与不启动组件 | pending |
| Zenith-Dev 或真实组件部署 | 不执行 |

## 本版变化

- 安装引导询问软件目录、GitHub 文件下载方式与可选 Shell 注册，最终确认默认取消。回答使用 `/dev/tty`，不会消费 `curl | sh` 中的脚本内容；确认前不创建软件目录、不下载发行包。
- 默认目录为 `~/.nido`，可输入自定义绝对路径。安装后显示实际根的环境加载命令；安装子进程不能修改父 Shell。
- Shell 注册默认关闭，明确同意后才追加受管加载块，保留原内容、权限和 uid/gid。仅对读取对应启动文件的新 Shell 生效；Bash 登录 Shell 若未加载 `.bashrc`，仍需自行接入。
- Codex、Conda、Proxy、GDS 与 GitHub 文件策略提供独立配置入口。已有状态默认保留，配置前执行既有校验，取消或输入结束不继续提交；Proxy 确认摘要不展开订阅、节点凭据或完整 YAML。
- 显式参数、无终端和 `--yes` 继续支持自动化，`--dry-run` 不进入交互。API listener 门控、Codex 独立代理、GDS 可选和组件不自动启动的边界保持不变。

## 公开资产与安装验收

发行白名单为 `nido-linux-amd64.tar.gz`、对应 `.sha256`、`install.sh` 及三份 `.sigstore.json`，共六项资产。正式验收先绑定准确 tag 与提交，独立核对官方发布元数据、三份签名、归档身份及脚本源码一致性；公网脚本和包在执行前与已验签输入比较。

两条 README 命令需分别在临时 Linux amd64 环境的真实 PTY 中执行，实际访问 latest 公开 URL，交互选择自定义目录并同意 Bash 注册。验收核对安装版本、`bin/nido` 位置、实际环境加载命令、新 Bash 中的命令解析和注册块唯一性。临时 Codex 配置、认证、会话、数据库、Conda/pip 及其他数据哨兵应保持原样；仅临时 Shell 配置中的受管加载块允许按选择增加。不得启动 Codex、Mihomo、GDS 或系统服务。

客户端仍只验证包摘要，不自动验证 Sigstore。第三方可同时替换脚本、包与摘要；加速首装必须信任转发服务。独立验签属于本次发行验收，不代表普通客户端已经具备相同校验。

本版不在 Zenith-Dev 安装、迁移或启动真实组件，不修改现有配置、认证、会话和数据。临时环境的安装与 Shell 注册不等于真实机器部署。
