# GPU Dynamic Scheduler

`gds` 是一个低开销的 NVIDIA GPU 动态保留模块。它通过 `tmux` 在后台运行，每 10 秒读取一次 `nvidia-smi`：

- 检测到其他 GPU 计算任务时，只监听，不占用 GPU。
- GPU 连续空闲 30 分钟后，启动名称透明的 `scheduler` CUDA worker。
- `scheduler` 默认占目标 GPU 25% 显存，并保持可观测的 GPU 活动。
- 发现其他 GPU 计算任务后，在下一次探测时停止 `scheduler`、释放显存，但 monitor 继续监听；再次连续空闲 30 分钟后重新启动。

worker 是仓库内的原生 CUDA 程序 `src/scheduler.cu`，编译结果保存为 `core/scheduler`，不使用伪装的任务名。它只运行一个线程的有界 GPU kernel，主机线程使用 CUDA 阻塞同步，因此额外 CPU 和系统内存开销较低。源码或编译参数指纹变化时会自动重编译。

## 安装

推荐在 `server-toolkit` 根目录使用统一安装器：

```bash
./install.sh --module gds
```

也可在本目录单独安装：

```bash
./install.sh
```

普通用户默认安装到 `~/.local/bin/gds`，root 用户默认安装到 `/usr/local/bin/gds`。

安装/更新还会把 `global-agents.md` 作为受管段落合并到 Codex 当前实际生效的全局
`AGENTS.md`（存在非空 `AGENTS.override.md` 时写入后者）。该规则要求 Codex 在使用本地
GPU 时只忽略 PID 和可执行文件路径均验证为本模块所有的 `scheduler`，直接启动实际任务，
不等待保留显存先归零；monitor 会在下一次成功探测时自动停止 worker。

依赖：

- `tmux`
- NVIDIA 驱动提供的 `nvidia-smi`
- CUDA Toolkit 提供的 `nvcc`（安装时编译 `scheduler`）

## 使用

```bash
gds                 # 打开 dashboard
gds start
gds stop
gds restart
gds logs
gds status
gds status --quiet
```

dashboard 会直接显示监听服务、空闲计时、计算进程数，以及每张 GPU 的利用率和显存摘要，并每 10 秒自动刷新。状态保存在 `workdir/state`，事件日志保存在 `workdir/gds.log`。

## 配置

编辑 `config.conf`：

```ini
probe_interval_seconds=10
idle_threshold_seconds=1800
gpu_device=0
process_scope=target
memory_percent=25
```

`probe_interval_seconds`、`idle_threshold_seconds` 和 `memory_percent` 必须是正整数；`gpu_device` 从 0 开始。`process_scope=target` 只监测所选 GPU，适合多卡服务器；设为 `all` 可恢复任意 GPU 忙碌即暂停的旧行为。显存比例允许 1–90，默认 25。修改后执行 `gds restart` 生效。

可用 `GDS_TMUX_SESSION` 环境变量覆盖默认 tmux 会话名 `gpu-dynamic-scheduler`。

## 控制安全

- start/stop/restart 使用互斥锁，避免并发管理操作互相干扰。
- tmux 会话带项目归属标记；同名外部会话不会被接管或停止。
- PID 校验同时检查脚本路径和内部启动标记，支持清理崩溃残留。
- monitor 只会忽略自己记录且可执行文件路径匹配的 `scheduler` PID；同名外部任务仍会被视为其他 GPU 任务。
- Codex 全局规则也使用相同的 PID 与可执行文件双重校验；不会按 `scheduler` 进程名宽泛忽略 GPU 占用。
- monitor 停止、探针失败或出现其他 GPU 任务时都会终止 `scheduler` 并清理显存。
- worker 启动阶段允许短暂的 fork/exec 窗口；启动失败使用有界终止和回收，不会无限等待一个已经运行的 worker。
- 健康状态同时校验 monitor 状态文件的新鲜度；仅有 supervisord 进程存活不会被判为健康。
- 状态文件使用原子替换，dashboard 不会读到半写入内容。
