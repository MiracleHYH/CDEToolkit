# P0 测试运行时输入

仅用于隔离验收，不是 Nido 安装或部署脚本。固定 Linux amd64 Conda 24.1.2、Python 3.12.1、pip 23.3.1；不得挂载真实 HOME、Conda 安装、SSH、Docker socket 或服务器配置。

先在临时构建上下文准备这些**已经独立核验**的官方输入，再复制本目录的 Dockerfile、Dockerfile.trace、nido-p0-strace 和 .dockerignore。不要将下载程序或 .deb 提交到仓库。

| 输入 | 官方来源 | SHA256 |
| --- | --- | --- |
| Miniconda3-py312_24.1.2-0-Linux-x86_64.sh | [Miniconda 索引](https://repo.anaconda.com/miniconda/) | `b978856ec3c826eb495b60e3fffe621f670c101150ebcbdeede4f961f22dc438` |
| strace_6.1-0.1_amd64.deb | [Debian strace 包池](https://deb.debian.org/debian/pool/main/s/strace/) | `1942d086a6244a1a9643489d3b0aa604ac44b88991d6c69217a63a193671bd4f` |
| libunwind8_1.6.2-3_amd64.deb | [Debian libunwind 包池](https://deb.debian.org/debian/pool/main/libu/libunwind/) | `7b297868682836e4c87be349f17e4a56bc287586e3576503e84a5cb5485ce925` |
| strace_6.1-0.1_arm64.deb | [Debian strace 包池](https://deb.debian.org/debian/pool/main/s/strace/) | `751817724af91cb95df566a21a154f9a7dcdd938e700e5cb9a5a47eb19752ea2` |
| libc6_2.36-9+deb12u14_arm64.deb | [Debian glibc 包池](https://deb.debian.org/debian/pool/main/g/glibc/) | `01f4330719fd4f65580e16ea5a0527f372fca750e8f588d26deaf09f2d3b1cf4` |

Miniconda 安装器应与官方索引摘要及 144041912 字节大小匹配。Debian 输入应通过其 archive keyring 验证 bookworm `InRelease` 签名，验证 `main/binary-{amd64,arm64}/Packages.xz` 摘要，再从包索引核对 .deb 的 SHA256/大小；Dockerfile 中的摘要断言不能替代来源验证。

基础镜像固定为 `public.ecr.aws/docker/library/golang@sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db`。仅在已验证输入齐备后构建，两个 Docker build 的执行步骤均可断网：

```sh
docker build --network none --platform linux/amd64 -t nido-conda-p0:24.1.2-amd64 CONTEXT
docker build --network none --platform linux/amd64 -f CONTEXT/Dockerfile.trace \
  -t nido-conda-p0:24.1.2-amd64-final CONTEXT
```

`Dockerfile.trace` 加入独立 arm64 strace 及私有 loader，并将 prefix owner 固定为 UID 1000。两份文件合并了本次准备运行时的最终 ownership 归一化步骤，镜像摘要应以每次实际构建结果为准。本次本地输入链证据、构建日志与镜像身份保存于忽略的 `build/conda-p0-24/evidence/`。

固定 prefix 是 `/runtime/miniconda`；完整种子归档 `/opt/miniconda-seed.tar` 约 630 MiB。normal 模式在 2 GiB 私有 tmpfs `/runtime` 中以 UID 1000 执行 `tar --no-same-owner -C /runtime -xf /opt/miniconda-seed.tar`；strict 模式直接读取只读镜像的同一 prefix。两模式都用 `--read-only --network none --cap-drop ALL --security-opt no-new-privileges --user 1000:1000`，HOME 和配置在 `/work` 私有 tmpfs，`PYTHONDONTWRITEBYTECODE=1`。不能仅改 HOME 而继续调用宿主 Conda base。

在原生 x86_64 Linux 使用 `/usr/bin/strace`。本次 Apple arm64 宿主的 Linux VM 通过 amd64 仿真运行，guest x86 ptrace 不可用；`/usr/local/bin/nido-p0-strace` 使用 arm64 原生 strace 追踪仿真器在 VM 中发出的实际文件及网络系统调用，语法与 strace 相同。它仅适用于 arm64 Linux VM，不能冒充 x86 guest ptrace 或原生 Linux amd64 验收。trace 需结合上游基线及文件内容/元数据前后对比解读。

每个模式需先记录自己的完整 prefix manifest，再验证该模式前后保持；同时比较同模式下发行程序与修复源码的结果。非 root tar 会应用 umask：本次固定输入和 umask 022 下，normal 仅将 `pkgs/.constructor-build.info`、`pkgs/urls`、`pkgs/urls.txt` 从 0664 解包为 0644，其余 26,738 个条目的类型、mode、UID/GID、内容/链接与 strict 相同。两个模式不能直接要求 manifest hash 完全相同；该初始差异不来自 Nido。
