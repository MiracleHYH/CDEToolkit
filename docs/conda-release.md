# Nido v0.8.0：Conda / pip 配置管理

Conda / pip 外部配置集成自 Nido v0.8.0 提供，包含在 Linux/macOS amd64、arm64 的 Nido 包中。Nido 不安装 Conda、初始化 Shell、安装包或迁移环境与缓存。日常命令见 [使用指南](usage.md#conda--pip)，配置格式与保护边界见 [设计说明](conda-design.md)。

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
NIDO_TEST_REAL_CONDA=/absolute/path/to/conda go test ./internal/integrations/conda -run '^TestFinalAuditRuntime' -count=1
```

这两项将写入限制在临时 HOME、软件根和配置夹具；来源保护脚本另建临时 Conda root、prefix 及证书夹具。它们只读查询已有 Conda，不联网、不安装包、不修改真实机器配置。标准及自定义 CONDARC 的优先级、证书和代理保护、default 完整恢复等历史通过记录见 [组件审计](conda-audit.md)。这些结果不证明镜像下载、包完整性或所有平台的实际上游行为。

## 本次发行记录

本节用于记录 v0.8.0 最终冻结提交及其产物。发布前源码审计结果保留在上面的历史记录中；公开资产验收结果须在实际执行后补入。

| 项目 | 记录 |
| --- | --- |
| 冻结提交、v0.8.0 标签与 VERSION 一致性 | 待最终发布步骤记录 |
| 四平台包、摘要、安装器及 Sigstore 签名 | 待发布工作流完成后记录 |
| 最终源码检查与临时 HOME 验证 | 待最终冻结提交复核后记录 |
| 公开发行包的临时目录下载、校验与 CLI 验收 | 待实际执行后记录 |

公开包与签名的校验规则见 [构建与发行](go-ci-release.md#发行校验)。安装客户端核对 SHA-256，当前不验证 Sigstore；工作流签名不能代替客户端下载时的真实性验证。发行和临时目录验收均不表示已经在真实机器部署或改写其 Conda/pip 配置。

公开包验收须先独立核对新、旧版本的发布来源、平台、摘要及对应 tag 的 Sigstore 签名，再提供资产目录：

```sh
NIDO_ACCEPT_INPUT=/absolute/path/to/verified-v0.8.0/assets \
NIDO_ACCEPT_OLD_INPUT=/absolute/path/to/verified-v0.7.0/assets \
NIDO_ACCEPT_OUTPUT=/absolute/path/to/new-evidence-directory \
make check-nido-public-release
```

入口使用真实公网直链和 `gh-proxy.org`，在临时 HOME 验证 latest 安装、Shell 命令注册、重复安装、真实 v0.7.0 自更新及 Conda CLI。安装下载逐项与已验签输入比对后才执行；Go 自更新使用客户端原生 API 摘要及 SHA-256 校验，更新后再与已验签程序精确比对。`NIDO_ACCEPT_MODE=docker NIDO_ACCEPT_PLATFORM=linux/amd64` 可在已有缓存镜像中执行，容器的原生或仿真架构及网络限制需另行记录，不能推断为所有平台均已验收。
