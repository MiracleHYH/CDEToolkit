## GPUDynamicScheduler and local GPU use

- The server-toolkit GPUDynamicScheduler (`gds`) may intentionally reserve an idle local NVIDIA GPU with its managed CUDA worker, whose executable is `gds/core/scheduler`.
- When selecting or waiting for a local GPU, ignore GPU processes and VRAM only when the PID matches `gds/workdir/scheduler.pid` and the live executable resolves to that same server-toolkit installation's `gds/core/scheduler`. Never infer GDS ownership from the process name `scheduler` alone.
- Do not wait for that verified worker's GPU usage to disappear, and do not stop, kill, or restart GDS merely to acquire the GPU. Exclude the verified worker from zero-process or free-memory admission checks and start the intended local GPU workload normally.
- The GDS monitor treats every other compute process as external and terminates its worker on the next successful probe, releasing its reserved VRAM. The default probe interval is 10 seconds; use the configured `probe_interval_seconds` when it differs.
- Continue to respect every non-GDS GPU process and its memory. If the verified worker remains after the new workload is visible to `nvidia-smi` for more than two successful probe intervals, inspect GDS status and logs instead of waiting indefinitely.
