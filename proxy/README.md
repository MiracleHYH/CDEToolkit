# Proxy Dashboard

用于管理 mihomo。本地交互模式可使用 `tmux`，由 server-toolkit 安装服务后则由 supervisord 直接监管。示例 listener 只绑定 `127.0.0.1`，不启用 TUN，也不修改路由、防火墙或系统代理。

## 安装

推荐在 `server-toolkit` 根目录使用统一安装器：

```bash
cd ..
./install.sh
```

也可在本模块目录中单独安装：

```bash
./install.sh
```

普通用户默认安装到 `~/.local/bin/pm`，root 用户默认安装到 `/usr/local/bin/pm`。安装内容是指向本项目脚本的命令入口，`core/`、`workdir/` 和日志保留在项目中；GitHub Release 安装的私有配置位于 `$STK_HOME/config/proxy.yaml`，源码开发模式默认位于 `~/.config/server-toolkit/proxy.yaml`。

首次交互安装会询问是否有 Shadowsocks ISP 节点及订阅 URL。无 ISP 时只需提供订阅 URL；有 ISP 时还会询问 server、port、cipher 和 password。配置生成后也可编辑 `pm config-path` 输出的 YAML：

```yaml
proxy-providers:
  proxy_provider:
    url: "你的订阅地址"
```

然后运行：

```bash
pm
```

推荐通过统一入口重复配置和校验：

```bash
stk proxy configure
stk proxy validate
```

配置向导先写候选文件，本机已有 mihomo core 时会通过 core 校验后才原子替换；旧配置保留为 `.previous`。自动化安装使用 `--input-file` 指向禁止 group/other 访问的临时文件，内容依次为五个 NUL 分隔字段：订阅 URL、ISP server、port、cipher、password。

在管理面板中使用 `↑` / `↓` 移动选择，按 Enter 执行；按 `q` 或 Esc 退出。

首次启动会从 GitHub 下载适用于服务器架构的最新稳定版 mihomo core，并用官方发布元数据中的 SHA-256 摘要校验文件。可在统一配置的 `proxy_release_proxy` 中填写个人 HTTPS 加速前缀。

## 命令模式

```bash
pm start
pm stop
pm restart
pm update-provider
pm update-core
pm validate
pm status
pm status --quiet
pm endpoint common
pm config-path
```

`status --quiet` 和 `endpoint` 用于 `stk` 等脚本可靠查询状态和 listener，无需解析 dashboard 文字或重复读取 YAML。

tmux 会话名默认为 `proxy-mihomo`，日志位于 `workdir/mihomo.log`。由于不启用 `external-controller`，`pm update-provider` 会将 provider 缓存标记为过期并重启 mihomo，以触发刷新。

## 控制安全

- 重复执行安装脚本时，如果命令入口已指向当前项目，会直接返回而不重建链接。
- 启动、停止、重启、provider 更新和 core 更新互斥执行，异常遗留的操作锁会自动恢复。
- tmux 会话带有项目归属标记；同名外部会话不会被接管或停止。
- PID 文件会同时校验可执行文件和配置路径，支持清理崩溃残留并恢复性停止脱离 tmux 的 core。
- PID 文件缺失时会扫描 `/proc` 并以可执行文件和配置路径双重确认受管 core；supervisord 直管模式下的 stop/restart/provider 更新会转交 `stk module`。

## 让单独的程序使用代理

基础模板只有一个本机 HTTP/SOCKS5 混合 listener：

- `127.0.0.1:7890`：固定使用 `common`

选择 ISP 模式后会额外生成：

- `isp-relay`：从订阅 provider 中选择 ISP 节点的前置线路。
- `isp` listener：监听 `127.0.0.1:7891`，通过生成的 Shadowsocks ISP 节点连接，并使用 `isp-relay` 作为 `dialer-proxy`。

可以只为某一次命令设置 `common` 代理，例如：

```bash
HTTPS_PROXY=http://127.0.0.1:7890 \
HTTP_PROXY=http://127.0.0.1:7890 \
ALL_PROXY=socks5h://127.0.0.1:7890 \
codex
```

Git 也可以按命令使用，不必修改服务器的全局网络：

```bash
git -c http.proxy=http://127.0.0.1:7890 \
    -c https.proxy=http://127.0.0.1:7890 \
    clone https://github.com/OWNER/REPO.git
```

可选环境变量：

- `GH_PROXY`：临时覆盖统一配置中的 `proxy_release_proxy`
- `PROXY_TMUX_SESSION`：tmux 会话名，默认 `proxy-mihomo`

订阅和 ISP 字段不接受环境变量，避免凭据经 `/proc/<pid>/environ` 暴露；请交互输入或使用短期存在的 `--input-file`。
