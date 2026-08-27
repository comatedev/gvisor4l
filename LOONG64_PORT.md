# gVisor LoongArch64 移植笔记

## 项目背景

- **目标**：在银河麒麟 V11 Swan25 (LoongArch64, Loongson 3A5000, 内核 6.6) 上运行 `runsc` 作为 docker OCI runtime，供 OJ 系统按需启动 4 语言 (C / Python / JS / Java) 学生作业容器。
- **范围**：仅 `ptrace` 平台。**不做** KVM、systrap、ring0 真实实现，**不做** LSX/LASX 上下文保存，**不做** 上游 PR。
- **演示门槛**：与 x86 host 上 `qemu-system-loongarch64 -accel tcg` 跑同样负载相比，性能可观察地高（预期 10×~50×）。

## 设计决策

| 决策 | 选择 | 理由 |
|---|---|---|
| 构建路径 | x86_64 上 `bazel build --config=loong64` 交叉编译 | 官方 Bazel 无 loongarch64 二进制；`//runsc:runsc` 纯 Go 免 cgo，无需 `cc_toolchain_loongarch64` |
| 页大小 | 仅支持 16K（不分 4k/64k 变体） | Linux 主线 LoongArch 默认；银河麒麟 V11 内核 6.6 默认 |
| FPU 保存 | 仅基础 32×64bit FP + FCC + FCSR | LSX/LASX 在 `cpuid.AllowedHWCap1` 中过滤掉，使 glibc/JVM 不去用 |
| KVM/systrap/ring0 | 全部 panic stub | 这些子系统在 ptrace 平台不会触发；占位让代码树能编译 |
| `atomicbitops` | 直接用 noasm fallback | 包已内置 `!amd64 && !arm64` 兜底实现，零工作量 |
| 上游策略 | fork，不提 PR | 工程压力下保留全部修改主权 |

## 阶段进度

### ✅ P1.A 已完成：Bazel 配置 + 底层 ABI/cpuid/hostarch

**Bazel 配置改动（4 处）**：

- `tools/bazeldefs/BUILD` — 新增 `config_setting(name="loong64", ...)` 约束
- `tools/bazeldefs/defs.bzl` — `select_arch()` 加 `loong64=` 参数；新增 `loong64_config()` transition
- `tools/bazeldefs/tags.bzl` — `archs` 列表加 `"_loong64"`
- `tools/bazeldefs/go.bzl` — `select_goarch()` 加 `loong64="loong64"`

**新增 Go 文件（10 个）**：

- `pkg/abi/linux/epoll_loong64.go` — 16 字节 EpollEvent（与 arm64 同）
- `pkg/abi/linux/file_loong64.go` — fcntl flags + asm-generic stat
- `pkg/abi/linux/mm_loong64.go` — 48-bit TASK_SIZE
- `pkg/abi/linux/ptrace_loong64.go` — `struct user_pt_regs` 映射：32 GPR + ERA + BADV + Reserved[10]，SP=$r3
- `pkg/abi/linux/sem_loong64.go` — asm-generic SemidDS
- `pkg/hostarch/hostarch_loong64.go` — PageShift=14 (16K)、HugePageShift=25 (32MB)、CacheLine=64B、无 TBI
- `pkg/cpuid/hwcap_loong64.go` — HWCAP_LOONGARCH_{CPUCFG,LAM,UAL,FPU,LSX,LASX,CRC32,...}
- `pkg/cpuid/features_loong64.go` — Feature 枚举 + FlagString() 拼 /proc/cpuinfo
- `pkg/cpuid/cpuid_loong64.go` — FeatureSet 主体；**AllowedHWCap1 过滤掉 LSX/LASX**
- `pkg/cpuid/native_loong64.go` — 读 /proc/cpuinfo 拿 Model/Freq

**BUILD srcs 更新（5 处）**：`pkg/abi/linux/BUILD`、`pkg/hostarch/BUILD`、`pkg/cpuid/BUILD` 加入新文件。

**确认无需改动**：`pkg/atomicbitops/atomicbitops_noasm.go` 的 build tag 是 `!amd64 && !arm64`，loong64 自动复用 Go fallback。

### 🚧 P1.B 进行中：核心架构层

详见任务 #7。预计文件：约 15 个，含汇编。最难点是 `safecopy/` 系列（sighandler 与 ucontext 布局）。

### ⏳ P1.C 待办：ptrace 平台 + runsc 适配

详见任务 #8。约 20 个文件。关键文件 `pkg/sentry/platform/ptrace/ptrace_loong64.go` 需要正确使用 `NT_PRSTATUS` regset（不是 `NT_LOONGARCH_*`，那些是补充信息）。

### ⏳ P1.D 待办：批量 panic stub + Dockerfile + 首次编译

详见任务 #9。`ring0/kvm/systrap` 约 35 个文件用最小骨架占位。

## 关键 LoongArch ABI 备忘

| 项 | 值 |
|---|---|
| 通用寄存器 | $r0=zero, $r1=ra, $r2=tp, $r3=sp, $r4..$r11=a0..a7, $r12..$r20=t0..t8, $r22=fp, $r23..$r31=s0..s8 |
| Syscall 号寄存器 | $a7 ($r11) |
| Syscall 返回值 | $a0 ($r4) |
| Syscall 指令 | `syscall 0` (4 字节定长) |
| 页大小 | 16K（CONFIG_PAGE_SIZE_16KB，主线默认） |
| 用户地址空间 | 48 位 |
| 字节序 | 小端 |
| Stack 对齐 | 16 字节 |
| FPU regset note | `NT_PRSTATUS` 通用寄存器 + `NT_PRFPREG` 浮点（gVisor 使用） |
| 补充 note | `NT_LOONGARCH_{CPUCFG,CSR,LSX,LASX,LBT}`（gVisor 不使用） |

## 基础镜像选择

**官方 Docker Hub 的 debian/ubuntu/alpine 都没有 loongarch64 manifest**。必须用社区镜像：

| 用途 | 镜像 | 验证命令 |
|---|---|---|
| 编译机 Dockerfile.builder 的 base | `ghcr.io/loong64/debian:trixie-slim` | `docker run --rm --platform=linux/loong64 ghcr.io/loong64/debian:trixie-slim uname -m` → `loongarch64` |
| OJ 容器 rootfs（C/Python/JS/Java） | 同上 + `apt install` 各运行时 | 4 个语言镜像在 P2 阶段构建 |

## 汇编校对（against Vol1 r1p10 + ELF psABI v2.01）

P1.B 写完后通过两份官方 PDF 校对，结论：

| 项 | 引用页 | 结果 |
|---|---|---|
| 通用寄存器 ABI（regSP=3, regA0=4, regA7=11, regT0=12, RA=R1） | ELF psABI Table 1 | ✓ 全部匹配 |
| `MULV / MULHVU` 助记符（即 MUL.D / MULH.DU） | Vol1 §2.2.1.11 | ✓ |
| `RDTIMED R4, R0` 读 stable counter | Vol1 §2.2.10.4 | ✓ rj=R0 丢弃 ID 合规 |
| `DBAR $0` 全屏障 | Vol1 §2.2.8.1 | ✓ hint=0 是必须实现的完全功能屏障 |
| `SYSCALL` 触发系统调用例外 | Vol1 §2.2.10.1 | ✓ |
| `LL/LLV/SC/SCV` 助记符 | Vol1 §2.2.7.4 | ⚠️ 硬件是 3 操作数 `LL.W rd, rj, si14`；Go 汇编器对 `(R), R` 形式是否自动填 si14=0 需 P1.D 编译验证 |
| 信号 handler ucontext 中 `REG_PC=0xB0` 偏移 | （不在 Vol1 范围） | ⚠️ 基于 glibc `sysdeps/.../loongarch/sys/ucontext.h` 推算，运行时验证 |

附加收获：
- 3A5000 支持 AM\* 原子指令（Vol1 §2.2.10.5: CPUCFG bit22 LAM=1），未来若 LL/SC 性能不够可切到 `AMSWAP_DB.D` 等。
- e_machine = 258 (EM_LOONGARCH) 确认（ELF psABI Table 5），与 `AUDIT_ARCH_LOONGARCH64=0xc0000102` 推算一致。

## 编译指引

**在 x86_64 Linux 机器上交叉编译**。Bazel 官方不发布 linux-loongarch64 二进制，
龙芯机上跑不了 Bazel，所以构建机固定为 x86。这不付出任何代价：`//runsc:runsc` 是
`pure = True` 纯 Go，Go 工具链直接交叉编译，**全程不涉及 LoongArch C 工具链**；
vDSO 是预编译提交的 `pkg/sentry/loader/vdsodata/vdso_loong64_stub.so`，不参与构建。

```bash
./scripts/build-runsc.sh          # 产出 ./bin/runsc (linux/loong64)
MODE=opt ./scripts/build-runsc.sh # 生产版，剥调试信息
```

底层就是一条 bazel 命令：

```bash
bazel build --config=loong64 //runsc:runsc
```

`--config=loong64` 定义在 `.bazelrc`，只设 `--platforms`，**不设 `--crosstool_top`**
—— `@crosstool` 是 coral crosstool，只有 k8/aarch64，没有 loongarch64；纯 Go 也不需要它。

`--platforms` 必须显式传。`//tools/bazeldefs:loong64` 这个 config_setting 认的是
`@platforms//cpu:loongarch64` 约束，匹配不上时 `select_goarch()` 会**静默**落到默认
分支，编出错误架构的二进制而不报错。脚本因此在最后校验产物的
`e_machine == 258 (EM_LOONGARCH)`，并在 x86 上用 qemu-user 跑一次 `runsc --version`
做冒烟自检。

构建机要求：

| 项 | 要求 |
|---|---|
| 架构 | x86_64 Linux |
| Bazel | 8.3.1（`.bazelversion` → `images/default/bazelversion`） |
| Go | 1.26.3，由 `go_sdk.from_file` 按 go.mod 自动下载，无需预装 |
| 依赖补丁 | 4 个，见下（rules_go / platforms / gazelle ×2） |
| 软件包 | `build-essential git python3 zip unzip patch pkg-config libssl-dev` **外加 `crossbuild-essential-arm64 clang libc6-dev-i386 binutils-gold`**（缺一不可，见下）；可选 `qemu-user` 供冒烟自检 |
| 磁盘 / 内存 | ≥20G bazel 缓存，≥8G 内存 |
| 网络 | 需可达 BCR、GitHub releases（rules_go zip）、go.dev/dl；受限网络需配 `--registry` 镜像与 `GOPROXY` |

### 四个依赖补丁

| 补丁 | 作用 |
|---|---|
| `rules_go_loong64.patch` | 把 `loong64 → @platforms//cpu:loongarch64` 加进 `BAZEL_GOARCH_CONSTRAINTS`，并注册 `("linux","loong64")` |
| `platforms_loongarch64.patch` | 在 `@platforms` 里补出 `cpu:loongarch64` 这个 constraint_value |
| `gazelle_loong64_platform.patch` | 给 gazelle 的 `KnownPlatforms` 加 `{"linux","loong64"}` |
| `gazelle_loong64_platform_info.patch` | 给 gazelle 的 `IsKnownArch()` 加 `"loong64"` |

前两个决定 `--platforms` 能不能匹配上；后两个决定**外部 Go 依赖的 BUILD 文件生成**。

gazelle 这两个是 2026-08-18 才补上的，之前一直缺，症状极具误导性：编译
`com_github_creack_pty` 报 `_C_int` / `_C_uint` undefined。**gazelle 在为外部 Go 模块
生成 BUILD 时，会静默丢弃构建约束里出现未知架构的源文件** —— gazelle 0.47 的
`IsKnownArch()` 里没有 loong64，于是上游自带的 `ztypes_loong64.go` 根本没进 srcs。
同时被丢掉的还有 `ztypes_sparcx.go`、`ztypes_freebsd_ppc64.go` 等，这个规律是定位的关键。

注意 Bazel 内置的 patch 实现（非 GNU patch）**处理多文件补丁时会把 hunk 归错文件**，
所以 gazelle 的两处改动必须拆成两个补丁文件。

曾经还有一个 `creack_pty_loong64.patch` 用来补 `_C_int`，已删除：creack/pty v1.1.24
上游本来就有 `ztypes_loong64.go`，那个补丁不但多余，而且在 gazelle 修好之前也永远
不可能生效（文件照样会被过滤掉）。

### 环境踩坑

| 现象 | 根因 | 解法 |
|---|---|---|
| `/usr/bin/aarch64-linux-gnu-gcc: No such file` | `arch_genrule` 会给 amd64 **和 arm64** 都编 systrap sighandler，与目标平台无关 | `crossbuild-essential-arm64` |
| `clang: command not found` | eBPF 程序的 genrule 用 clang 编 BPF 目标码 | `clang` |
| `fatal error: 'gnu/stubs-32.h' file not found` | 同上，amd64 上编 eBPF 需要 32 位头文件 | `libc6-dev-i386` |
| `collect2: fatal error: cannot find 'ld'` | **极具迷惑性**：`ld` 明明存在。真相是 bazel 传了 `-fuse-ld=gold`，而 GNU gold 在 binutils 2.44 之后已被上游移除 | `binutils-gold`（装完确认 `/usr/bin/ld` 仍指向 bfd） |

诊断 `cannot find 'ld'` 的关键是去看 params 文件里的链接参数，而不是去查 `ld` 在不在。

注意 `.bazelversion` 在 git 里是符号链接（mode 120000），在不支持符号链接的检出
（如 Windows）上会变成内容为路径的文本文件，bazelisk 会因此失败。

## 部署到银河麒麟（占位）

```bash
scp runsc root@kylin-loong:/usr/local/bin/
# /etc/docker/daemon.json:
# { "runtimes": { "runsc": { "path": "/usr/local/bin/runsc" } } }
systemctl restart docker
docker run --rm --runtime=runsc --network=none oj-c:loong64 \
    sh -c 'echo "int main(){puts(\"ok\");}" > /t.c && cc /t.c -o /t && /t'
```

## ✅ 移植跑通里程碑 (2026-05-30)

`docker run --runtime=runsc ghcr.io/loong64/debian:trixie-slim echo hello` → **hello** (exit 0)，在银河麒麟 V11 LoongArch64 (3A5000, 内核 6.6) 上验证。

### 运行期 bug 修复链 (v3→v13)
| 版本 | 根因 | 修复 |
|---|---|---|
| v3 | LoongArch 内核忽略 mmap hint → stub 地址死循环 | stub mmap 加 `MAP_FIXED_NOREPLACE` (pkg/sentry/platform/ptrace/stub_unsafe.go) |
| v4 | TaskSize 1<<48 超出 LoongArch 用户空间上界 | mm_loong64.go: feasibleTaskSizes = 1<<47 |
| v5/v6 | vDSO Binary 为空 → 解析 EOF | 用 loongarch64-linux-gnu-gcc 编最小 vDSO ELF，独立文件名避开 arch_genrule 冲突 |
| v7 | sentry ELF loader 只认 EM_X86_64/EM_AARCH64 | pkg/sentry/loader/elf.go 加 case 258 (EM_LOONGARCH) → arch.LOONGARCH64 |
| v8 | gofer host seccomp 缺 statx → SIGSYS 杀进程 | fsgofer/filter/config_loong64.go 补 SYS_STATX (LoongArch 无 fstat) |
| v9 | 未注册 LoongArch syscall dispatch table → "no syscall table found" | 新建 syscalls/linux/linux64_loong64.go，复用 ARM64.Table (asm-generic 编号一致) |
| v12 | **syscall arg0 取错**：LoongArch 内核 entry 先存 orig_a0 再设 a0=-ENOSYS；SyscallSaveOrig 误用 a0 覆盖 orig_a0 | SyscallSaveOrig 空实现；SyscallArgs 直接读 c.Regs.OrigA0 (syscalls_loong64.go) |
| v13 | **fork 子进程漏掉 eager populate**：`TaskImage.Fork` 的注释写着 "break COW eagerly on both parent and child"，代码却只对父进程调用了 `PopulateAll`。子进程的 pma 全部 needCOW、被映射为只读，第一次写栈必然缺页，踩中内核 tlbex 不恢复 t0/t1 的缺陷 | task_image.go 补上 `newMM.PopulateAll(ctx)` |

关键诊断手段：x86 host 上 qemu-user + bazel 交叉编译迭代；龙芯机上 strace gofer + sentry --strace + 在 SyscallArgs 临时 dump 寄存器，定位真实 arg0 在 orig_a0。

### v13 的验证方法 (2026-08-18)

这个 bug 是概率性的，`echo hello`、fork+写栈、3000 次 fork+execve 都压不出来。**能稳定复现的
配置是：父进程弄脏 256MB 私有匿名内存，子进程 fork 后遍历全部 16384 个页**——关键变量是
子进程必须触发足量 COW 缺页，因为 t0/t1 是调用者保存寄存器，只有恰好持有活跃值时才致命。

与其等 SIGBUS，不如直接测寄存器有没有被写坏：内联汇编把已知值放进 $t0/$t1，通过 $t0 做一次
触发 COW 缺页的存储，再回读比对（见 `/tmp/forktest/cowprobe.c` 的思路）。

A/B 对照结果（`runsc do`，各 100 轮）：

| | 失败轮次 | 观察到的信号 |
|---|---|---|
| 修复前 | **12 / 100** | SIGSEGV ×11，SIGBUS ×1 |
| 修复后 | **0 / 100** | — |

按 12% 发生率算，修复后连过 100 轮的概率约 3×10⁻⁶。其中那次 SIGBUS 与最初 MariaDB 容器里
`Bus error  sleep 1` 的签名一致；多数情况表现为 SIGSEGV——垃圾值落在合法但未映射的地址就是
SIGSEGV，落在非规范地址才触发 LoongArch 的 ADE 例外变成 SIGBUS。这解释了为什么当初只是
"偶尔"看到 Bus error。

代价：fork 变成 O(堆大小)。192MB 堆的 JVM 上，fork 从 2ms 涨到约 76ms。

## 性能优化

### ① vDSO：clock_gettime 37500ns → 34ns (2026-08-18)

移植初期的 `vdso_loong64_stub.so` 只是个让 `vdso.PrepareVDSO()` 能解析下去的占位——
导出符号一律返回 -1，glibc 于是每次 `clock_gettime` 都回落到真实系统调用，被 ptrace
拦截后约 37.5µs。JVM 对 nanoTime 的调用极其频繁，这是 Java 负载上最大的单项开销。

现在是一个真正的 vDSO，由 `vdso/vdso.cc` + `vdso_time.cc` 编译而来，直接从 sentry
维护的参数页算时间，不进内核。新增的架构分支：

| 文件 | 内容 |
|---|---|
| `cycle_clock.h` | `rdtime.d %0, $zero` 读稳定计数器——必须与 sentry 的 `tsc_loong64.s` 同源，否则参数页里的 cycle 值对我们没有意义 |
| `barrier.h` | `dbar 0`。Vol1 §2.2.8.1 只保证 hint=0 是完整屏障，细粒度 hint 是可选的 |
| `syscalls.h` | `syscall 0` 回退桩；$a7 传号，$a0-$a5 传参，$a0 返回 |
| `vdso_time.cc` | `la.pcrel` 定位参数页，链接期解析，零动态重定位 |
| `vdso.cc` | 六个 `__vdso_*` 导出。LoongArch 用 `__vdso_` 拼写（同 x86_64），不是 arm64 的 `__kernel_` |

**最大的坑是页大小。** `vdso_loong64.lds` 原本照抄了 amd64/arm64 的
`_params = VDSO_PRELINK - 0x1000`，但 `pkg/sentry/loader/vdso.go:216` 是按
`hostarch.PageSize` 分配参数页的，LoongArch 上等于 **16K**。于是 vDSO 跑去
`vdso_base - 0x1000`（页中间的空白）读参数，`ready` 恒为 0，**静默回退到系统调用且
不报任何错**——现象是"vDSO 装上了但一点没变快"。改成 `- 0x4000` 才对。

定位它的关键线索是应用侧 `/proc/self/maps` 里 `[vvar]` 段的长度为 `0x4000`。

实测（`vdsoprobe`，3A5000）：

| | clock_gettime |
|---|---|
| 宿主裸跑 | 38 ns |
| 修复前（桩） | 37500 ns |
| 修复后（冷，沙箱刚启动） | 9400 ns |
| 修复后（热） | **34 ns** |

冷启动那段是 gVisor 时钟校准的固有热身窗口：单调钟要等几个更新周期才发布参数，其间
vDSO 正确地回退到系统调用；amd64/arm64 同理。Java 端到端 `nanoTime` 从 37497ns 降到
507ns（该均值含热身窗口，稳态即上表的 34ns）。

构建走 `scripts/build-vdso-loong64.sh`——`//vdso:vdso` 那个 genrule 需要**目标架构**的
C++ 工具链，而本移植刻意没有（runsc 是纯 Go，交叉编译用不着）。所以 vDSO 在龙芯机上
（或用 loongarch 交叉 g++）单独编好后把产物提交。注意 `check_vdso.py` 对 locale 敏感
（它判断 `readelf -r` 输出是否为空），必须 `LC_ALL=C` 运行。

### ② futex 屏障：已验证有 25× 空间，但保守起见未采用 (2026-08-18)

`futex.WaitPrepare` 在每次真正阻塞前调用 `GlobalMemoryBarrier()`，它落到
`membarrier(MEMBARRIER_CMD_GLOBAL)`——内核里就是 `synchronize_rcu()`，等一个系统级
RCU 宽限期，毫秒量级。上游选这个慢命令是合理的：它只在实现 `membarrier(2)` 时偶尔调一次。
本移植把它放到了 futex 热路径上，代价极大。

试过改用 `MEMBARRIER_CMD_GLOBAL_EXPEDITED`（IPI 实现，微秒级），实测：

| 指标 | GLOBAL | GLOBAL_EXPEDITED | runc |
|---|---|---|---|
| park/unpark | 15.34 ms/hop | 0.28 ms/hop | 0.01 ms |
| wait/notify | 9.90 ms | 1.39 ms | 1.26 ms |
| fork/exec | 72 ms | 18 ms | 2 ms |
| Java 全套总耗时 | 156 s | 4.5 s | 2.3 s |

浸泡 50 轮 JVM 启动 + 15000 次 park/unpark，零失败零丢失唤醒。

**但已回退，未采用。** 原因是一个无法自证的正确性问题：`GLOBAL_EXPEDITED` 只向注册过
`REGISTER_GLOBAL_EXPEDITED` 的进程所在 CPU 发 IPI，而这个屏障需要命中的是 **stub 进程**
（让 stub 的写对 sentry 可见），stub 从未注册——改动只在 sentry 里注册。是否有效完全取决于
Linux 在 fork 时会不会继承 `mm->membarrier_state`，未能确认。若不继承，等于退回无屏障状态，
当初的 JVM 启动死锁可能以极低概率复发，这种偶发故障在生产上代价太高。

将来若要拿这 25×，正确做法是**让 stub 自己注册**（ptrace 平台本就具备向 stub 注入系统调用的
能力），而不是只在 sentry 注册。

两个配套注意事项：

- gVisor 自己的 seccomp 只放行 `MEMBARRIER_CMD_GLOBAL`
  （`runsc/boot/filter/config/config_main.go`）。换命令必须同步放行，否则 sentry 一发出新命令
  就被 SIGSYS 打死，容器退出码 159、无任何输出。
- 那段代码是"屏障 + 重新检查"，而两次检查**都在 `b.mu` 锁内**。bucket 锁加上 stub 陷入内核的
  上下文切换本身可能已经提供了足够的顺序保证——屏障当初之所以奏效，也可能只是因为它引入了
  毫秒级延迟让写落地。若真如此，任何缩短该延迟的改动都在缩小安全边际。这是保守选择的另一个理由。

## systrap 平台可行性调研 (2026-08-27)

结论：**核心机制可行，但代价是构建体系。** 已在龙芯机器（内核 `6.6.0-32.22.v2505.ky11`）
上用四个探针把每个环节实测过，程序留在 `~/systrap-probes/`（见其中的 `README`）。

### 实测结论

| 事实 | 值 | 影响 |
|---|---|---|
| `sizeof(struct sigaction)` | **24 字节，无 `sa_restorer`** | `linux.SigAction` 的 `Mask` 偏移在 loong64 上是错的，见下 |
| `sa_mask` 偏移 | 16（不是 24） | 同上 |
| `SA_RESTORER` | **被内核忽略**，`$ra` 恒为 vdso 的 `__vdso_rt_sigreturn` | systrap 的 restorer 方案不成立 |
| 自发 `rt_sigreturn` | **可行**，且 vdso 已 munmap 时依然可行 | 这是出路 |
| rt_sigframe 基址 | `== siginfo_t *`（handler 的 `$a1`）；`ucontext - siginfo == 128` | 不需要依赖结构体偏移 |
| 缺页时 `sc_pc` | 指向**出错指令本身**（delta 0） | 修好映射后 sigreturn 原地重试 |
| seccomp SIGSYS 时 `sc_pc` | 已**越过** `syscall 0`（delta +4） | 只需塞 `$a0`，不必自己推进 pc |
| 系统调用号 | `sc_regs[11]`（`$a7`） | |
| `sc_extcontext` | 本机首条即 `LASX_CTX_MAGIC` (0x41535801)，1056 字节 | 必须遍历整条链保存/还原 |

对照实验（vdso 已 munmap）：普通返回的 handler → **SIGSEGV**；自发 `rt_sigreturn` 的 handler
→ 正常返回。这正是 stub 进程的处境，因为
`subprocess_linux.go` 会 munmap `stubROMapEnd` 到 `maximumUserAddress` 的全部区间，含 vdso。

核心循环已整体验证：从 handler 改写 `uc_mcontext.__pc` 与 `sc_regs[]` 生效；seccomp
`SECCOMP_RET_TRAP` → SIGSYS → 塞 `$a0` 伪造返回值 → 自发 sigreturn，应用侧拿到伪造值。
FPU/LASX 上下文经"整块拷出 → 抹成 0xa5 → 拷回"的往返后，`$f0` 完好。

写 systrap 的自发 sigreturn 时要注意一处 ABI 差异（**不是**本移植引入的）：gVisor 的信号帧是
`{ucontext, siginfo}`（`$sp` 指向 ucontext），真内核是 `{siginfo, ucontext}`（`$sp` 指向
siginfo，`uc - si = +128`）。upstream 的 arm64 也是如此（`signal_arm64.go` 同样先
`info.CopyOut` 再 `uc.CopyOut`，`Sp = ucAddr`），sentry 自己的 `SignalRestore` 与之自洽，
所以普通程序无感。探针 `02` 里的 `do_sigreturn(si)` 就是因为假设了内核布局而在 runsc 下 SIGBUS。

调研过程中还发现了一个与 systrap 无关的既有 bug（guest 侧 sigaction 结构错位），已单独修复，
见下一节。

### 实现路线（未动工）

按风险从高到低：

1. **构建体系**（最大代价）。`tools/arch.bzl` 的 `arch_transition` 只有 amd64/arm64 两档，
   而 `arch_genrule` 会给转换里的**每个**架构都编一遍 sighandler。加 loong64 意味着
   `loong64_config()` 必须补回 `cpu` 和 `crosstool_top`，而 `@crosstool` 没有 loongarch64，
   得自行注册 CC toolchain；14 处 `select_arch()` 全部要补分支；x86 构建机要装 LoongArch
   C 交叉工具链。**`.bazelrc` 里"`//runsc:runsc` 是纯 Go 所以不需要 crosstool"从此不成立。**
2. **`sysmsg/sighandler_loong64.c`**（参照 arm64 版 222 行）。难点是 `sc_extcontext` 链的
   完整保存还原；写错不会崩，只会让浮点/向量结果静默出错。
   `sysmsg/build.bzl` 的 `-DPAGE_SIZE` 只有 4096/65536 两档，要加 **16384**。
3. **约 800 行 Go/汇编**，照抄 arm64 骨架：`filters` / `lib`（`cputicks` 用 `RDTIMED`）/
   `stub` / `subprocess` / `syscall_thread` / `sysmsg_thread` / `systrap` / `systrap_unsafe` /
   `sysmsg/sysmsg` / `usertrap/usertrap`（空实现）。

有利因素：arm64 的 systrap 本身就是退化版（无 usertrap、无 syscall patching，`syshandler`
只是一条陷阱指令），loong64 照抄即可；TLS 比 arm64 简单（`$tp` 就是 `sc_regs[2]`，在通用
寄存器组里，不需要 `PTRACE_GETREGSET`）；`pkg/seccomp` 已就绪；`sigErrorToAccessType()`
可先返回 `NoAccess`（ptrace 平台现在就是这么做的）。

另：现有的占位 `pkg/sentry/platform/systrap/systrap_loong64.go` 是**截断的**，
末尾停在一句注释上；它能过编译只是因为模板生成的代码恰好没被引用。

## loong64 sigaction ABI 修复 (2026-08-27)

做 systrap 调研时发现的既有 bug，已修复。**与 systrap 无关，影响的是当前的 ptrace 平台。**

`pkg/abi/linux/SigAction` 是 32 字节、带 `Restorer`（x86/arm64 布局），而 loong64 内核的
`struct sigaction` 是 **24 字节、无 `sa_restorer`、`sa_mask` 在偏移 16**——LoongArch 不定义
`__ARCH_HAS_SA_RESTORER`，信号处理器一律经 vdso 的 `rt_sigreturn` 蹦床返回。sentry 却用
32 字节布局 CopyIn/CopyOut guest 的 `rt_sigaction` 参数。

三个后果（同一个静态二进制分别跑 runc 与 runsc 实测，探针在龙芯机 `~/sigabi/`）：

| | 修复前 (runsc) | 修复后 (runsc) | 真内核 / runc |
|---|---|---|---|
| `sa_mask`@16（真实 ABI） | **被忽略** | 生效 | 生效 |
| `sa_mask`@24 | 生效 | 被忽略 | 被忽略 |
| `oldact` 写回字节数 | **32**（越界 8 字节） | 24 | 24 |
| `SA_RESTORER` 位回读 | 保留 | 丢弃 | 丢弃 |
| 置 `SA_RESTORER` 时 `$ra` | **0x0**（一返回就崩） | vdso 地址 | vdso 地址 |

1. **guest 的 `sa_mask` 被完全忽略。** 任何用非空 `sa_mask` 的程序（HotSpot、MariaDB 都算），
   handler 执行期间本该阻塞的信号不会被阻塞。不崩，只是行为不对。
2. **`sigaction(sig, act, oldact)` 往 guest 的 24 字节缓冲区写 32 字节。** glibc 的
   `__libc_sigaction` 把 `koact` 放在栈上，越界的 8 字节覆盖相邻栈空间。
3. **guest 若置了 `SA_RESTORER` 位，`$ra` 会是 0。** `kernel/task_signals.go:277` 只在该位
   未置时才用 `mm.VDSOSigReturn()` 兜底，而置位时 `Restorer` 读到的是偏移 16 上的 `sa_mask`。
   真内核会直接丢弃这个位（实测确认），所以正确的模拟是在 copy-in 时抹掉它。

改法：`SigAction` 本身不动（`Restorer` 字段被 sentry 内部和 amd64/arm64 大量引用），只在
**guest ABI 边界**上转换。新增 `CopyInABI` / `CopyOutABI`：amd64/arm64 直接转发；loong64 走
一个 24 字节的 `SigActionABI`，并在 copy-in 时清掉 `SA_RESTORER`。调用点只有
`syscalls/linux/sys_signal.go` 的 `RtSigaction` 和 `strace/signal.go`。

### 一个 Bazel 坑

第一版把 amd64/arm64 的转发放在一个 `signal_restorer.go` 里，用 `//go:build amd64 || arm64`。
结果 **loong64 下整个 `pkg/abi/linux` 的 marshal 方法全部消失**，报错是几十个
`missing method CopyIn` / `SizeBytes undefined`。

原因：`tools/defs.bzl:calculate_sets()` 纯按**文件名后缀**给源文件分桶，`signal_restorer.go`
没有架构后缀就落进通用桶，go_marshal 于是把它的 `//go:build amd64 || arm64` 聚合到了整个通用
autogen 文件的头部——loong64 下该文件被整体排除。生成文件开头那段注释正是在警告这件事。

**结论：`pkg/abi/linux` 里任何带架构约束的文件都必须用 `_amd64.go` / `_arm64.go` /
`_loong64.go` 后缀命名，不能只靠 `//go:build`。** 拆成两个文件即可。

### 验证

- 三项 ABI 行为全部与真内核对齐，`oldact` 原始字节逐字节相同。
- JStress 全套新旧二进制同机 A/B：均为 `futex/park-unpark` 一项失败、耗时 155s 上下，
  无差异。该失败是既有的 futex 慢速问题（见《② futex 屏障》），`JFutex` 判定
  `RESULT PASS (no lost wakeups)`、`lost_wakeups=0`、15.41ms/hop，属超时而非丢唤醒。

## 未结：容器内偶发的 guest 内存破坏 (2026-08-27)

**这不是 MariaDB 的 bug，也不是 sigaction 修复引入的。是 guest 内存被静默写坏。**

### 与 sigaction 修复无关（已结案）

逐次交替切换二进制的 A/B soak，435 轮 ×2：

```
新（含 sigaction 修复）: PASS=430 FAIL=8
旧                    : PASS=429 FAIL=8
```

**8 比 8。** 加上此前 250×2 全过的一轮，该修复可以排除。

### 两种故障签名，两个二进制都有

| 签名 | 次数 | 含义 |
|---|---|---|
| `advise_stack_range: freesize < size` | 11 | glibc 线程退出时 `$sp` 不在自己的 stackblock 里 |
| `*** stack smashing detected ***` | 5 | 栈金丝雀被覆写 |

其中一次受害者是 **`mkdir`**。strace 显示得很清楚：

```
[1:1] E clone(CLONE_CHILD_CLEARTID|CLONE_CHILD_SETTID|0x11, ...)
[2:2] E set_robust_list(...)
[2:2] E rt_sigprocmask(SIG_SETMASK, [SIGINT SIGTERM SIGCHLD], ...)
[1:1] X clone(...) = 2   (8.862042ms)      <- 正常 fork 约 100µs
[2:2] E writev(2, "*** ", "stack smashing detected", ...)
```

bash fork 出子进程，子进程做完两个系统调用、**还没走到 `execve`**，金丝雀就已经坏了。
两种签名指向同一件事：**fork 后子进程的内存被写坏**。

### 已排除：寄存器污染（原始 SIGBUS 那条路径）

原始 bug 的机理是内核 TLB 例外路径不恢复 t0/t1，sentry 用垃圾基址重跑 store。
垃圾地址非规范时是 SIGBUS，规范且已映射时就是**静默写错地址**——形态上完全吻合。
但实测不成立，四个探针合计约 **250 万次**，全部零污染：

| 探针 | 路径 | 规模 | 结果 |
|---|---|---|---|
| `cowprobe` | fork COW 缺页 | 163 万次（原始复现规模 256MB/16384 页，`-m 1g`） | t0=0 t1=0 |
| `fp` A | 栈自动增长 | 宿主 + runsc | 0 |
| `fp` B | `MADV_DONTNEED` 重缺页 | 宿主 + runsc | 0 |
| `fp` C | 匿名页首次触碰 | 宿主 + runsc | 0 |
| `fp2` D | SIGSEGV 投递 + 修复 + 重试 | 宿主 + runsc，20 万次真实投递 | 0 |

探针在龙芯机 `~/faultprobe/`（`fp.c`、`fp2.c`）和 `/tmp/forktest/cowprobe.c`。
**`f03b376e3` 的 COW 修复守得很稳**；文档里此前点名的另外两条懒缺页路径也没问题。

### 未决：是不是 gVisor 的问题

runsc vs runc 逐次交替对照，因 `/tmp` 触发保护阈值只跑到 48 轮就停了：

```
runsc: PASS=47 FAIL=1     runc: PASS=48 FAIL=0
```

**样本远不够。** 需要注意：即使 runc 最终全过，也**不能**直接断定 gVisor 有 bug——
这台机器是旧世界固件 + 新世界系统，正是当初 SIGBUS 唯一发作的组合；gVisor 的 sentry
每单位 guest 工作要做的缺页/mmap/madvise 远多于 runc，可能只是把某条有缺陷的路径踩得更频繁。

### 一条强线索：时间累积 + 内存压力

- 第一轮 250×2（15:41–17:31）：**0 失败**
- 第二轮前 115 轮（20:19–21:10）：**0 失败**
- 21:10–23:22 之间：**16 次失败**

有东西是累积的。失败那次的 fork 耗时 **8.9ms**（正常 ~100µs）。机器 `MemAvailable` 长期
只有约 1.4Gi（总 7.3Gi），其中 **2.6–2.8Gi 是 `/tmp` 这个 tmpfs**——`daemon.json` 给 runsc
配了 `--debug --strace --debug-log=/tmp/rlog/`，每次 boot 日志约 15MB，已积到 3.0G。

**把 `/tmp/rlog` 挪到磁盘可释放约 3GB 物理内存**，既是继续跑 soak 的前提，
本身也是对"内存压力是触发条件"这一假设的直接检验：若挪走后故障消失，假设成立。

### 数据位置（龙芯机）

- `~/soak/results-250.txt`、`~/soak/results.txt`：new/old A/B 原始数据
- `~/soak/failures/`：16 次失败的完整 rlog + 容器日志
- `~/soak/results-runc.txt`、`~/soak/failures-runc/`：runsc vs runc 对照
- `~/faultprobe/`：四个寄存器污染探针

## 附：该故障的首次观测记录 (2026-08-27)

> 下节是最初只有一次观测时的分析，结论已被上一节取代（当时怀疑与 sigaction 修复有关，
> 后经 435×2 轮 A/B 证明无关）。保留以记录排查过程。

### 首次观测

**一次观测，未能归因，未能排除。** 记录在此以免下次重新摸索。

bootstrap 版 mariadbd 关闭时，`ib_tpool_worker` 线程在 glibc 的线程退出路径上崩溃：

```
Fatal glibc error: allocatestack.c:194 (advise_stack_range): assertion failed: freesize < size
```

容器退出码 1，`mariadb-install-db` 报 "Installation of system tables failed"。

`advise_stack_range()` 在线程退出时把栈的未使用部分 `madvise(MADV_DONTNEED)` 还给内核，
`freesize = (sp - pd->stackblock) & ~(pagesize-1)`。断言失败意味着退出时的 `$sp` 落在
`pd->stackblock` 之外。

### 已排除的两条

1. **不是 sigaction 修复引起的**（就现有证据）。该修复唯一的行为变化是"handler 执行期间
   应用 `sa_mask`"，而 strace 日志显示**崩溃前后该进程没有任何信号投递**——15:11:41 的信号
   事件只有 task 1 的 SIGCHLD 和拆除阶段的 SIGKILL，全部在崩溃（`.144`）之后。日志里那条
   `rt_sigaction(SIGABRT, SIG_DFL)` 是 `abort()` 自己在重置 disposition。mariadbd 也从未
   调用 `sigaltstack`。
2. **不是 `io_destroy` 打断 `io_getevents`。** 崩溃前主线程 `io_destroy()` 使 worker 的
   `io_getevents()` 返回 EINVAL，看着可疑，但 `/tmp/rlog` 里 **76/76 次运行都有这个
   EINVAL**，是 MariaDB 正常关闭行为。

### 统计

`/tmp/rlog` 覆盖 2026-08-09 至 08-27，76 次跑到关闭路径的 mariadbd（全部开着 `--strace`）：

| | 失败 / 总数 |
|---|---|
| 含 sigaction 修复 | **1 / 281** |
| 修复之前 | **0 / 295** |

明细：调查当时是 1/31 对 0/45（Fisher p≈0.41）；随后 30 轮逐次交替切换二进制的对照 0 失败；
再之后 250×2 轮同样交替的 soak，两边各 250 次**全部通过**。至此单次故障率若真为 1/31，
在 250 次里一次不出的概率约 0.03%——**倾向于认为这次故障与修复无关**，但仍是单次事件，
未能定位根因。

故障特征：容器**不是立刻崩**，而是启动后约 **4.8 秒**（`07:11:37.07` → `07:11:41.92`），
正是 bootstrap 版 mariadbd 跑完 install-db 进入关闭阶段的时刻。

### 推不下去的地方

从 `mprotect(0x7ffec0830000, 0x800000, PROT_READ|PROT_WRITE)` 可反推线程 92 的
`stackblock = 0x7ffec082c000`、`stackblock_size = 0x804000`（含 16K guard），而崩溃时观察到
的栈地址 `0x7ffec102e3e8` **在范围内**。所以要么 `pd` 里的 stackblock 字段被写坏，要么线程
当时真的在另一个栈上——两者都需要 glibc 的内部状态才能判定，strace 日志给不出。

### 运维发现：`/tmp` 是内存

`/etc/docker/daemon.json` 给 runsc 配了 `--debug --strace --debug-log=/tmp/rlog/`，
每次 boot 日志约 **15MB**。而 `/tmp` 是 **3.7G 的 tmpfs**，即占用物理内存，机器总共只有
7.3Gi。调查时 `/tmp/rlog` 已积到 **3.0G / 82%**。

也就是说**这台机器长期有 3GB 内存被 strace 日志占着**，且几十次容器运行就会把 `/tmp` 撑爆。
这未必是本 bug 的成因，但它是一个真实的、会影响一切偶发问题复现条件的环境因素。跑批量实验
前务必先清理或关掉 `--strace`。

### 一个未验证的怀疑：内存压力

故障当时机器的 `MemAvailable` 只有约 **1.5Gi**（总 7.3Gi，其中 2.8Gi 是 tmpfs 里的调试日志），
而容器本身要 `-m 1g`。`advise_stack_range` 的断言失败等价于"线程退出时 `$sp` 不在自己的
stackblock 里"，这类现象与内存/换页压力下的时序变化并非全无关系。目前只是怀疑，没有证据链。

因此 soak 脚本每轮都记录 `dur=` 和 `avail=`，一旦复现可以直接看相关性。

### 进行中

`~/soak/soak.sh` 在龙芯机后台跑 **2500×2 轮**逐次交替的 A/B（约 18 小时）。
结果见 `~/soak/results.txt`，失败的完整 rlog 落在 `~/soak/failures/<时间戳>-<arm>-<轮次>/`；
已完成的 250×2 轮存档在 `~/soak/results-250.txt`。
脚本边跑边删自己产生的成功日志（用户原有日志一个不动），并在 `/tmp` 剩余不足 400M 时自动停止。
