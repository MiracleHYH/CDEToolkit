# Nido v0.8.0：Conda / pip 配置管理

Conda / pip 外部配置集成自 Nido v0.8.0 提供，包含在 Linux/macOS amd64、arm64 的 Nido 包中。Nido 不安装 Conda、初始化 Shell、安装包或迁移环境与缓存。日常命令见 [使用指南](usage.md#conda--pip)，配置格式与保护边界见 [设计说明](conda-design.md)。

本文保留 v0.8.0 的历史发行记录。后续 [目标版本 P0 验收](conda-p0-acceptance.md) 发现：无显式 CONDARC、用户 `.condarc` 已有其他 `default_channels` 时，v0.8.0 切源可能合并保留旧地址；完整七字段同步及按项查询修复已随 [v0.8.1](conda-v0.8.1-release.md) 发行，正式新包的旧上游双模式均通过。v0.8.0 的原失败结论保留，各公网端点的实际结果与限制在新记录中分别列出。

## 发行功能

- 首次 `setup` 或直接切源前，将 Conda 和 pip 原有有效镜像分别持久保存为 `default`。重复 setup、后续切换和 reset 不覆盖快照，新 Shell 与重启后仍可选择。
- Conda 提供 default、official（别名 conda）和 tuna；pip 另支持 ustc、自定义 HTTPS simple index，官方别名为 pypi。预设保留当前频道范围及私有映射；公共源转换不带入原认证、查询或 token。
- 保留自定义 CONDARC 的原路径与读取位置，仅原地修改七个镜像字段，证书、代理及其他非镜像配置继续实时读取。显式 default 恢复受管标准用户与显式源文件的完整已捕获镜像字段，清除后来新增的映射；旧五字段快照只恢复其已记录内容。
- Conda `envs_dirs` / `pkgs_dirs` 与 pip `global.cache-dir` 原地修改，不创建或搬迁数据目录。off/reset 保留已写入的原生值、default 和原 CONDARC 关联；显式选择 default 才恢复首次镜像。
- 静态状态不执行上游程序；`validate` 只读核对调用者当前环境。预览、并发检查、私有文件保护与失败补偿覆盖原生写入，新复制的镜像凭据不得传播到他人可读文件。

七个镜像字段为 `channels`、`default_channels`、`custom_channels`、`custom_multichannels`、`channel_alias`、`migrated_channel_aliases`、`migrated_custom_channels`。非镜像节点、注释、权限与所属组保留；Conda YAML 格式可能调整。目录或链接型 CONDARC、不安全目标和覆盖相应路径的环境变量会明确报错。系统或目标环境中的更早 `#!final` 仍按上游规则处理，应通过 validate 核对最终值。

## 验证入口

常规源码和发行包检查使用 [.go-version](../.go-version) 固定的 Go 1.27.2 工具链；历史 Go 1.26 预检不代替本次发行验证：

```sh
make check smoke
make release
make verify-release
```

`make check` 已包含 Conda 单元、race 与临时 HOME 的 CLI 夹具测试，无需真实 Conda/Python 或镜像网络。单独检查指定 CLI 可运行：

```sh
bash tests/conda-cli-isolation.sh /absolute/path/to/nido
```

已有 Conda 时，可显式运行真实配置合并的离线隔离验证。Python 入口默认使用 `agent` 环境：

```sh
conda run -n agent --no-capture-output python tests/conda-source-upstream-isolation.py /absolute/path/to/nido /absolute/path/to/conda
conda run -n agent --no-capture-output python tests/pip-upstream-isolation.py /absolute/path/to/nido
NIDO_TEST_REAL_CONDA=/absolute/path/to/conda go test ./internal/integrations/conda -run '^TestFinalAuditRuntime' -count=1
```

这些入口把 Nido 配置写入临时 HOME、软件根和配置夹具；来源保护脚本另建临时 Conda root、prefix 及证书夹具。pip 入口使用当前 Python 的真实 pip，检查冷进程恢复原 default、原生缓存、证书、代理和 reset 保留。它们不联网、不安装包；单独替换 HOME 不是上游安装目录拒写保证，Conda 24.1.2 查询 envs 会尝试以 `a+` 打开 base history。需要完整运行时与内核拒写边界时，使用 [P0 容器入口](conda-p0-acceptance.md#复现与证据)。标准及自定义 CONDARC 的优先级、证书和代理保护、default 完整恢复等历史通过记录见 [组件审计](conda-audit.md)。这些结果不证明镜像下载、包完整性或所有平台的实际上游行为。

## v0.8.0 历史发行记录

v0.8.0 于 2026-10-18 发布。以下记录区分源码检查、公开资产核验与实际执行的验收；发布前审计保留在历史记录中。

| 项目 | 记录 |
| --- | --- |
| 冻结提交、v0.8.0 标签与 VERSION 一致性 | [`32a0feb`](https://github.com/MiracleHYH/CDEToolkit/commit/32a0feb6408b813cb04e92011979d450750587cf)，标签与 VERSION 均为 v0.8.0 / 0.8.0 |
| 四平台包、摘要、安装器及 Sigstore 签名 | [正式发行](https://github.com/MiracleHYH/CDEToolkit/releases/tag/v0.8.0)共 18 份资产；9 份签名逐项独立核验通过，证书 workflow、issuer、commit、ref、repo、name 与 push 身份匹配；9 份原始资产均与本地固定 Go 1.27.2 构建逐字相同 |
| 最终源码检查与临时 HOME 验证 | 本地 `make check smoke release verify-release`、敏感信息扫描通过；[主分支 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39120086204)、[标签 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39120483982)、[发行 CI](https://github.com/MiracleHYH/CDEToolkit/actions/runs/39120483845)全部通过 |
| macOS darwin/arm64 公开包运行验收 | 直链和 `gh-proxy.org` 完整安装、bash/zsh 注册、PATH 幂等、重复安装及数据保护均通过；每次安装响应执行前均与已验签输入逐字比较 |
| 真实旧版自更新 | 使用已验签正式 v0.7.0 程序，在两种链路下升级到 v0.8.0；检查更新、失败保留、同版本离线跳过、拒绝降级和输出程序逐字比对均通过，无伪旧版本夹具 |
| Conda / pip CLI | 两种链路升级后的公开程序均通过 `conda-cli-isolation.sh`，覆盖持久 default、原生目录修改、环境加载及配置和数据保护 |
| 真实 Conda / pip | 公开 darwin/arm64 程序通过 Conda 26.1.1 的标准及自定义 CONDARC 离线验收，以及 agent Python 3.12 / pip 26.2.1 的 default、原生缓存、证书代理、权限及 reset 保留验收 |
| Linux linux/amd64 公开包运行验收 | macOS 宿主上的 Docker amd64 仿真中，直链与加速安装、重复安装、bash 注册、真实 v0.7.0 升级、失败保护、同版跳过、拒绝降级及 Conda CLI 隔离套件全部通过；只读 root、普通 UID 10001、私有临时 HOME 与软件根 |

本次公网执行覆盖 darwin/arm64 原生与 linux/amd64 仿真，不等同于原生 Linux 或其他架构的实际上游验收。其他平台保留四平台构建、静态校验与签名核验结果。真实 Conda/pip 查询另在 macOS 临时配置中完成，不访问镜像、不安装包；验收结束后临时安装根自动清理。

发行后的记录补充与隔离入口修正仅涉及文档、测试脚本，不改变上述冻结提交的发行程序。Docker 入口向普通 UID 开放的是已公开资产的测试副本，原始验签目录保持私有权限。

公开包与签名的校验规则见 [构建与发行](go-ci-release.md#发行校验)。安装客户端核对 SHA-256，当前不验证 Sigstore；工作流签名不能代替客户端下载时的真实性验证。发行和临时目录验收均不表示已经在真实机器部署或改写其 Conda/pip 配置。

下面保留 v0.8.0 发行时的执行参数；当时 latest 指向 v0.8.0。latest 随新版改变后，使用 [v0.8.1 记录](conda-v0.8.1-release.md#验证入口)中的新旧版本参数。公开包验收须先独立核对新、旧版本的发布来源、平台、摘要及对应 tag 的 Sigstore 签名，再提供资产目录：

```sh
NIDO_ACCEPT_INPUT=/absolute/path/to/verified-v0.8.0/assets \
NIDO_ACCEPT_OLD_INPUT=/absolute/path/to/verified-v0.7.0/assets \
NIDO_ACCEPT_VERSION=0.8.0 \
NIDO_ACCEPT_OLD_VERSION=0.7.0 \
NIDO_ACCEPT_OUTPUT=/absolute/path/to/new-evidence-directory \
make check-nido-public-release
```

入口使用真实公网直链和 `gh-proxy.org`，在临时 HOME 验证 latest 安装、Shell 命令注册、重复安装、真实 v0.7.0 自更新及 Conda CLI。安装下载逐项与已验签输入比对后才执行；Go 自更新使用客户端原生 API 摘要及 SHA-256 校验，更新后再与已验签程序精确比对。`NIDO_ACCEPT_MODE=docker NIDO_ACCEPT_PLATFORM=linux/amd64` 可在已有缓存镜像中执行，容器的原生或仿真架构及网络限制需另行记录，不能推断为所有平台均已验收。
