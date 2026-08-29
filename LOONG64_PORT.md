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

### 已定性：内存压力是触发条件，且只在 runsc 下发生

先是观察到相关：把 `/tmp/rlog` 从 tmpfs 挪到磁盘（释放约 2.4Gi 物理内存）后故障消失。
但"释放内存""清空 tmpfs""时间推移"三者混在一起，只能算相关。

于是做受控操纵——用一个 2.7G 的 ballast 文件把可用内存压回原值，其余条件一律不变：

| 阶段 | MemAvailable | runsc | runc |
|---|---|---|---|
| phase1 低内存（原始状态） | ~1.4Gi | 1 / 48 | 0 / 48 |
| phase2 内存释放 | ~4.1Gi | **0 / 200** | 0 / 200 |
| phase3 人为加压 | ~1.7Gi | **2 / 120** | 0 / 120 |

**两个结论：**

1. **内存压力是因果触发条件。** 只切换这一个变量，故障随之消失和复现。
   低内存下故障率约 1.8%，若不变，phase2 的 400 轮一次不出的概率约 0.08%。
2. **只在 runsc 下发生。** 同等压力的配对样本累计 runsc 3/168、runc 0/168。
   若 runc 同为 1.8%，168 轮零失败的概率约 5%。此前 870 轮里那 16 次也全在 runsc
   （当时两臂都是 runsc），方向一致。

注意此前的保留意见——"runc 全过不能直接判 gVisor 有罪，因为 sentry 每单位 guest 工作的
缺页量远大于 runc"——那是只有观察数据时的谨慎。有了受控操纵加配对对照之后，嫌疑落在
gVisor 一侧。但**触发条件不等于根因**：真正写坏内存的那段逻辑仍未定位。

### 故障的真实形态：单个字被写错

4 次新失败全部是 mariadbd 的**线程**调 `abort()`（`statement_timer`×2、`signal_handler`、
`ib_tpool_worker`），全部是同一个 `advise_stack_range` 断言——其中两次的消息写进了
`/dev/null` 所以没进容器日志，此前被误分类为 "other"。

从失败日志里能把数算出来。victim 线程的栈由 `mprotect(0x7ffef2158000, 0x800000, 0x3)`
建立，故 `stackblock = 0x7ffef2154000`、`stackblock_size = 0x804000`、栈顶 `0x7ffef2958000`。
它崩溃时的栈地址是 `0x7ffef29576b0`，偏移 `0x8036b0`：

```
freesize = 0x8036b0 & ~0x3fff = 0x800000
0x800000 < 0x804000   ->  断言本该通过
```

**要让断言失败，`$sp` 必须 ≥ 栈顶，这对合法的栈不可能。所以被改的是 `pd` 里的
`stackblock` 或 `stackblock_size`** —— 而 `pd`（pthread 描述符）就住在线程栈顶。

加上此前 `mkdir` 那次是栈金丝雀，**两类现场都是单个字被写错**。这不是大范围踩踏，
是一次精准的错误写入。

### 已排除的假设（都是有价值的阴性结果）

| 假设 | 检验方式 | 结果 |
|---|---|---|
| 寄存器污染（原始 SIGBUS 那条路径） | 4 个探针约 250 万次，含原始复现规模 | 零污染 |
| fork/COW 页分离失败（父进程写穿透子进程） | `~/detector/det.c`，地址派生模式，9.4 万次 fork | 零命中 |
| gVisor 恢复了错误的 `$sp` | `~/detector/sp.c`，16 线程每次系统调用后校验 `$sp` 与帧金丝雀，39 万次 | 零异常 |
| 还有别的结构体 ABI 越界写（像 sigaction 那样） | `~/detector/abidiff.c`，22 个填结构体的系统调用，runc/runsc 对比写入字节数 | 无越界写 |

`abidiff` 顺带确认了 sigaction 修复生效（`rt_sigaction` 写入 24 字节，与内核一致），
并发现两个次要差异：`sysinfo` 在 runsc 下只写 108 字节而内核写 112（少写，留下陈旧数据，
不是破坏源）；`sched_rr_get_interval` 在 runsc 下返回 EPERM 而 runc 正常。

### 归因（已显著）

压力下配对样本累计 **runsc 7/318、runc 0/318**，Fisher 精确检验 **p≈0.015**。
问题在 gVisor 一侧。

### 根因：`.bss` 尾部的清零丢失，被文件内容顶替

线程churn 探测器（`~/detector/thr.c`）跑了约 28 分钟后**把进程挂死了**，这个活现场给出了答案。

进程只剩 2 个线程、93 分钟只用了 140ms CPU，主线程阻塞在：

```
goroutine 69 [select, 93 minutes]:
  futex(0x1200a1960, FUTEX_WAIT|FUTEX_PRIVATE, val=2)
```

`nm` 显示 `0x1200a1960` 是探测器自己的 `static pthread_mutex_t mu`，位于 `.bss`。
stub 进程映射着 guest 的地址空间，于是可以直接从 `/proc/<stub>/mem` 读出它：

```
__lock  = 0x00000002           <- 看着像"已锁且有等待者"
__count = 0x70617473 = "stap"  <- 垃圾，应为 0
__owner = 0x00746473 = "sdt\0" <- 垃圾，应为 0
```

**互斥锁被字符串 `"stapsdt"` 覆盖了**——那是 ELF `.note.stapsdt` 的节名。把这段内存与
可执行文件对应偏移逐字节比对（`.bss` 起 `0x1200a1928` ↔ 文件偏移 `0x9d928`）：

```
内存: 0000000029313179 0000003900000008 706174730000000|2| 2000eea800746473
文件: 0000000029313179 0000003900000008 706174730000000|3| 2000eea800746473
```

**48 字节里 47 字节完全相同。** 唯一的差异就是互斥锁字本身，因为 glibc 对那个垃圾值
（文件里是 3）做了 `atomic_exchange` 换成 2，然后去 futex 等待——等一个永远不会来的解锁。

也就是说：**`.bss` 里装的不是零，而是可执行文件在对应偏移处的字节。**

### 为什么这解释了全部现象

RW LOAD 段有 `p_filesz < p_memsz`；从 `p_filesz` 到该页末尾这段属于 `.bss`，加载器必须清零。
`pkg/sentry/loader/elf.go:312` 的 `ZeroOut` 逻辑本身是对的、也是 upstream 原样。
**所以不是没清，是清完之后又丢了**——那个私有脏页被丢弃，重新从可执行文件回填。

| 观测 | 由此解释 |
|---|---|
| 破坏内容是"文件字节"而非随机值 | 页面从文件重新回填 |
| 看起来像"单个字被写错" | 只有窗口里被程序用到的那几个字会暴露 |
| `advise_stack_range` 断言 | glibc 的线程栈缓存簿记在 `.bss`，被文件字节顶替后 `pd->stackblock` 就错了 |
| `stack smashing detected` | 金丝雀参考值同样落在 `.bss` |
| 死锁 | 互斥锁字被顶替成非零值，永远等不到解锁 |
| 偶发、且与内存压力强相关 | 压力下才会发生页面丢弃与重新回填 |
| 只在 runsc 下 | runc 没有这层内存管理 |

### 更正与补充 (2026-08-29)

上一节把"`.bss` 尾部清零丢失"称为根因，**这个说法超出了证据能支撑的范围**，在此更正。

那次死锁现场的逐字节比对本身没有问题：`.bss` 里的互斥锁被可执行文件对应偏移的字节顶替，
48 字节里 47 字节相同，这个观测是可靠的。但后续拿到了一个**小而快的复现器**，它反复命中的
是另一种形态，与"文件内容"对不上。

**复现器**：`~/bss/thrbss.c`，约 200 行。触发条件是**线程创建/销毁**，能复现与 MariaDB
一字不差的签名 `Fatal glibc error: allocatestack.c:194 (advise_stack_range)`。

触发条件是靠一串大样本阴性结果排出来的：

| 假设 | 规模 | 结果 |
|---|---|---|
| fork churn（COW 页分离） | 9.4 万次 fork | 零 |
| exec churn（加载时清零竞态） | **8.4 万次 exec** | 零 |
| `$sp` 恢复错误 | 39 万次校验 | 零 |
| 页面回收后从文件回填 | 3.4 小时长跑 | 零 |
| **线程 churn** | — | **三次命中** |

8.4 万次 exec 零命中尤其关键——若清零是在加载时偶尔做错，按 MariaDB 的命中率折算该出
40 余次。**所以问题不在加载路径。**

**三次命中的数据完全一致，且与"文件内容"矛盾：**

```
at start : stack 0xa04c6c89993a7b3c  size 0x800000
at exit  : stack 0xa04c6c89993a7b3c  size 0x800000     <- 启动时就已是垃圾，运行中未变
sp now   = 0x7ffe........            <- 栈本身正常，就在 $tp 下方
```

- `pd->stackblock` 是垃圾，而 `pd->stackblock_size`（0x800000）**完全正确**——精准的单字段损坏，
  不是一片被踩，与"整页被文件回填"的图景不符。
- 三次独立运行（相隔数小时、不同进程）**值逐位相同**。在 ASLR 开启的情况下，
  这个值**不可能是指针或从指针派生**。
- 该值**不在 libc、ld.so、被测二进制、runsc 任何一个文件里**（已逐字节搜索）。
- 也不是 `AT_RANDOM`（金丝雀种子每次运行都不同）。

一个跨运行恒定、又不来自任何文件的高熵值，既不符合"随机残留"，也不符合"文件内容顶替"。
**因此上一节的机制解释无法覆盖这个签名。** 二者要么是两条不同的路径，要么"清零丢失"是更
底层某个机制的表现之一而非根因本身。目前证据不足以判定，不再强行给出统一解释。

**下一步**：`pthread_t` 在 glibc 里就是 `struct pthread *`，所以可以直接 dump `pd` 并在整个
地址空间搜索那个常量——若它在别处出现即可指认来源，若全地址空间都没有，则说明它是被"造"
出来的而不是"拷"过来的，方向要整个改。探测器已具备这两项能力（`~/bss/thrbss.c` 的
`hunt()` 与 `dump_pd()`），等下一次命中。

### 最小复现器

`~/bss/bss.c`。要点是判据不能用"`.bss` 是否为零"——glibc 启动时会合法写 `.bss`（第一版
就因此误报）。正确的指纹是**内容是否等于 `/proc/self/exe` 对应偏移的字节**：解析
`AT_PHDR` 找到 RW LOAD 段，算出窗口 `[p_filesz, 页末)` 与其文件偏移，启动稳定后取基线，
之后持续比对；一旦某个字变成文件字节即命中。所有可变状态放在堆上，避免自己落进窗口。

### 下一步该看的代码

加载器是对的，问题在私有页的生命周期。可疑处：

- `pkg/sentry/mm/pma.go` 的 `isPMACopyOnWriteLocked`——`HasUniqueRef` 快路径会直接
  **接管页面而不复制**（`pma.needCOW = false`）。若某处判断有误，被 `ZeroOut` 写过的
  私有页可能被当成干净的文件页丢弃。
- `pkg/sentry/mm` 的 `invalidateLocked(ar, invalidatePrivate, ...)`——若私有 pma 被丢弃，
  重建时会退回文件映射，文件字节就回来了。
- `pkg/sentry/pgalloc` 的 `MemoryFile.releaserMain`（在挂死现场的 goroutine 转储里可见）——
  压力下回收内存文件页。

注意 `f03b376e3` 给子进程加的 `newMM.PopulateAll(ctx)` 也走 `getPMAsLocked`，与上述快路径
相关；但故障在含该修复与不含该修复的两个二进制上等概率发生（8:8），**不是它引入的**。

### 复现配方

这是这轮调查最有价值的产出——此前只知道它"偶发"，现在可以按需复现：

1. 把 `MemAvailable` 压到约 1.4–1.7Gi（`fallocate -l 2700M /tmp/ballast`，删除即恢复）
2. 反复以 `-m 1g` 启动 MariaDB 容器跑 bootstrap（`~/soak/rc.sh`）
3. 约 1.8% 的轮次会失败，签名是 `advise_stack_range` 断言或 `stack smashing detected`

每轮约 13 秒，故障率 1.8%，平均约 12 分钟撞一次。下一步应当写一个远比 MariaDB 敏感的
探测器（fork + 金丝雀校验循环），把这个时间压到秒级。

### 运维：debug 日志不应放在 tmpfs

`daemon.json` 把 runsc 的 `--debug-log` 指到 `/tmp/rlog/`，而 `/tmp` 是 3.7G 的 tmpfs。
每次 boot 日志约 15MB，积到 3.0G 时等于把 3GB 物理内存长期锁死在一台只有 7.3Gi 的机器上——
这正是让机器进入故障触发区间的原因。**改成磁盘目录即可，不必关掉 `--strace`。**

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

## 复现器定型 + 损坏点精确定位 (2026-08-29)

### 复现器（已用 runc 对照验证）

`~/bss/thrbss.c`，约 260 行。关键改进是**由父线程在 `pthread_create` 返回的瞬间检查子线程
的描述符**——glibc 的 `pthread_t` 就是 `struct pthread *`，所以父线程能直接读
`pd+1168`。此前只有跑进 worker 函数的线程会被检查，漏掉了大量样本，这也是早期看起来
"几百万线程才中一次"的原因。

| | 40 秒内创建线程数 | 命中 |
|---|---|---|
| **runc** ×3 轮 | **3,606,984** | **0** |
| **runsc** ×3 轮 | 100,000 上下 | **1**（第 252 个线程） |

runc 三百六十万个线程零命中，runsc 几百个线程即中。**探测器判据可靠。**
顺带：同样 40 秒 runc 能创建 120 万线程而 runsc 只有 3.3 万，gVisor 的线程创建慢约 36 倍。

### 损坏点

```
pd+1168  stackblock      = 0xa04c6c89993a3b3c   <- 坏
pd+1176  stackblock_size = 0x804000             ok
pd+1184  guardsize       = 0x4000               ok
pd+1192  reported_guard  = 0x4000               ok
```

**单个 8 字节字段被改，相邻三个字段完好。** 不是一片被踩。

**写入发生在 `clone` 期间或之前**——父线程在 `pthread_create` 刚返回时读到的就已经是坏值，
线程还没开始跑自己的函数。这把搜索范围切到了 gVisor 的 clone / TLS 建立路径。

值 `0xa04c6c89993a3b3c` 跨全部命中逐位恒定，且遍寻不获：libc、ld.so、libm、libpthread、
被测二进制、runsc 本体、运行中的 sentry 进程（1239MB 全扫）、gofer 进程、以及健康 guest
的整个地址空间——**都没有**。它是被某段逻辑"造"出来的，不是从别处拷来的。

### 两个探测器自身的错误（已更正，记录以免重犯）

调查中有两条结论是**探测器自己造成的假象**，都已推翻：

1. **"内存没坏，是 glibc 算错了"** —— 当时拿字段原值去比 `pthread_getattr_np` 的返回值，
   而后者是 `stackblock + guardsize`，天然差 `0x4000`。把 `pd` dump 从 768 字节扩到
   2560 字节后字段直接可见，**内存确实是坏的**。
2. **"健康进程里本来就有这个常量"** —— `hunt()` 把常量作为函数参数接收，编译器将其溢出到
   自己的栈帧，扫描时找到的就是这份副本。排除自身栈帧后健康进程里 `NOT FOUND`。
   早期几次命中报告的"出错线程栈上有 8–10 处"同样是这个假象。

教训：**探测器本身也是被测系统的一部分**，它留下的痕迹会混进证据里。

### 下一步

复现已进入秒级，可以负担 sentry 侧插桩迭代了。目标是找出 clone 路径上是谁写了那 8 字节：
`kernel.Task.Clone` 的 TLS / `CLONE_CHILD_SETTID` / `CLONE_PARENT_SETTID` 写入点，
以及 `mm` 在新建地址空间时对 guest 内存的写操作。

### sentry 侧哨兵：那 8 字节不是 sentry 写的 (2026-08-29)

在 `pkg/sentry/mm/io.go` 的 `CopyOut` 和 `SwapUint32` 上加了针对该常量的哨兵，命中即打印
Go 调用栈。哨兵确实会触发（说明它工作正常），但**每一条触发的调用栈都是
`proc.(*memFD).PRead`**——那是探测器自己通过 `/proc/self/mem` 读内存时，sentry 把含该常量
的数据拷回 guest。**没有任何一条是真正的写入。**

结论：**sentry 从未通过 `mm.CopyOut` 往 guest 写过这个值。** 写入不在 sentry 的这条路径上。

（又一次被自己的探测器干扰：第一版探测器每轮启动都做全地址空间扫描，于是每轮都触发哨兵。
去掉启动扫描后才看清。)

### 常量与被测程序无关

同一份源码用 `-O1` 和 `-O2` 编出两个 MD5 不同的二进制，各跑 6 轮 40 秒：

```
-O1  1/6 命中   stackblock = 0xa04c6c89993a3b3c
-O2  1/6 命中   stackblock = 0xa04c6c89993a3b3c
```

命中率相同、常量逐位相同。**该值来自 gVisor 或运行环境，不是被测程序派生的。**

### "写丢失"假设：合成负载复现不出来

glibc 的 `allocate_stack()` 写入顺序是 `stackblock` → `stackblock_size` → `guardsize`，
而每次命中都是第一个字段陈旧、后面两个正确——这正是"两次相邻写之间那一页被换掉"会产生的
形态。于是做了两个合成测试：

| 测试 | 序列 | 规模 | 结果 |
|---|---|---|---|
| `~/bss/lostwrite.c` | mmap → 写 → 立刻回读 | 6.5 万线程 | 零 |
| `~/bss/lostwrite2.c` | mmap → 写 → **clone** → 回读 | 8.2 万线程 | 零 |

两者都干净。所以要么真实路径里还有合成负载没模拟到的要素（glibc 的栈缓存、
`_dl_allocate_tls` 对该区域的写、`CLONE_SETTLS`），要么机制并非"写丢失"。**假设未被证实。**

### 当前状态小结

| 项 | 状态 |
|---|---|
| 归因 | 确凿：MariaDB runsc 24/1318 vs runc 0/1317；探测器 runc 360 万线程零命中 |
| 复现器 | `~/bss/thrbss.c`，40 秒一轮，约 1/6 轮命中 |
| 损坏点 | `pd+1168`（`stackblock`）单字段，相邻字段完好 |
| 写入时刻 | `pthread_create` 返回时已存在，即在 clone 期间或之前 |
| 写入者 | **不是 sentry 的 `mm.CopyOut`**（哨兵已验证） |
| 常量来源 | 未知；与程序无关，不在任何文件 / sentry / gofer / 健康 guest 中 |
| 机制 | **未定**。寄存器污染、fork/COW、`$sp` 恢复、ABI 越界写、加载时清零、页面回收、写丢失——全部已用大样本排除 |

调试用的 runsc 在龙芯机 `~/runsc-watch`（`b16d2470`）；生产机已恢复 `cadfa5a5`。
那台机器上跑着的几个服务（comate-ai-backend、mysql、redis、caddy、byteCourt）**都用 runc**，
不受本调查影响。

下一步可走的方向：把哨兵扩到 `CopyOutFrom`（marshal 路径）与 `ZeroOut`；或反过来在
guest 侧用 `userfaultfd`/`mprotect` 把 `pd` 所在页设为只读来捕获写入者。

### 三条路都走完了 (2026-08-29)

**① 哨兵扩到全部 sentry 写路径。** 除 `CopyOut` 外，又在 `withInternalMappings` 上加了
写后扫描——`imCopyOut`、`CopyOutFrom`、`ZeroOut` 全部经过它。复现命中的那一轮里只有 4 条
告警，**全部来自 `proc.(*memFD).PRead`**（探测器自己 `dump_pd()` 读 `/proc/self/mem`，
每次读产生 CopyOut + imCopyOut 两条）。

**结论：sentry 的任何写路径都没有写过这个值。那 8 字节是 guest 自己写进去的。**

**② 寄存器是否跨系统调用保留。** 既然是 guest 写的，那 glibc 执行
`pd->stackblock = mem` 时寄存器里就已经是垃圾了——这能解释"常量"（寄存器被恢复成固定错值）。
`~/bss/regcheck.c` 给每个通用寄存器灌入可辨识模式，执行 `syscall 0`，逐个校验。

先测出真内核的基线（很有用的副产品）：**LoongArch Linux 不保留 `$t0`–`$t8`（`$r12`–`$r20`）**，
返回后它们含内核值，实测见到 `0x9000000113df1418`、`0x900000000253cbc0` 这类直映射地址。
其余 `$ra`、`$a1`–`$a6`、`$s0`–`$s8` 保留。

对这组保留寄存器实测：

| | 迭代次数 | 失败 |
|---|---|---|
| runc 单线程 | 7,764 万 | 0 |
| runsc 单线程 | 约 500 万 | 0 |
| runc 4 线程 | 2.75 亿 | 0 |
| runsc 4 线程 | 约 600 万 | 0 |

**寄存器跨系统调用的保存/恢复没有问题。**

**③ 上游是否有同类问题。** CRIU 有一个 thread-bomb 触发同一条断言的 issue
（checkpoint-restore/criu#1317），但根因是 parasite 注入遇上延迟绑定，加 `-Wl,-z,now` 解决，
**与本问题机制不同**。gVisor 上游未见对应报告。

### 探测器自身的错误（本轮又两个）

- `regcheck` 的 `out[]` 是**全局数组，4 个线程并发读写**，导致 runc 基线也"失败"。改成
  `__thread` + `la.tls.ie` 寻址（`$tp` 能活过系统调用，`$t8` 不能）后才干净。
- 检查列表里含 `$r22`，但汇编里从未初始化它。

加上此前两个（比较对象错位、`hunt()` 找到自己溢出的参数），**这次调查一共有四个结论是被
探测器自己污染的**。每一个都在纠正后改变了判断方向。

### 排除清单（全部大样本）

| 机制 | 规模 | 结果 |
|---|---|---|
| 寄存器污染（缺页路径 t0/t1） | 250 万次探测 | 排除 |
| fork/COW 页分离 | 9.4 万次 fork | 排除 |
| `$sp` 恢复错误 | 39 万次 | 排除 |
| 结构体 ABI 越界写 | 22 个系统调用逐字节比对 | 排除 |
| 加载时清零竞态 | 8.4 万次 exec | 排除 |
| 页面回收后从文件回填 | 3.4 小时长跑 | 排除 |
| 写丢失（写→回读 / 写→clone→回读） | 14.7 万线程 | 排除 |
| sentry 写入（全部 mm 写路径） | 哨兵覆盖，命中轮次零真实告警 | 排除 |
| 寄存器跨系统调用不保留 | 2.8 亿次迭代 | 排除 |

**现象仍在，机制未定。** 但归因确凿、复现器可用（40 秒一轮、约 1/6 命中）、损坏点精确到
`pd+1168` 单字段、写入时刻锁定在 clone 期间、写入者确定不是 sentry。

## 确凿缺陷：LSX/LASX 向量寄存器不跨上下文切换保存 (2026-08-29)

**这是一个独立的、可稳定复现的严重缺陷，与前面那个未破的 `pd` 损坏是两回事（关联未证实）。**

线索来自上游三个 issue：#12741（systrap 在 Intel 上的静默内存损坏）+ #12994（其修复：
AMX 扩展状态范围算错导致多写）+ #13542（ARM64 的 PAC 密钥未随 checkpoint 保存）。
三者主题一致：**架构特有的扩展状态没被正确处理**。LoongArch 上对应的就是 LSX/LASX——
而本移植明确不保存它们（`fpu_loong64.go`、`signal_loong64.go`、`ptrace_loong64.go`、
`features_loong64.go` 均有注释说明）。

### 实测

`~/bss/veccheck.c`：给 `$xr0`–`$xr7` 灌入可辨识值，执行 `getpid` + `sched_yield`
强制往返 sentry 与重新调度，再校验。

| | 迭代次数 | 失败 |
|---|---|---|
| runc | **9605 万** | 0 |
| runsc | **128** | 是 |

失败形态是决定性的：

```
$xr5 = 0000000000000066 ffffffffffffffff ffffffffffffffff ffffffffffffffff
       ^^^^ lane0 正确   ^^^^^^^^^^^^^^^^ 高 192 位全丢
```

**低 64 位（传统 FPU 的 `$f` 寄存器）保住了，高 192 位没有。** 与
`ptrace_loong64.go` 里只走 `NT_PRFPREG` 的实现完全对应——内核另有
`NT_LOONGARCH_LSX` / `NT_LOONGARCH_LASX` 两个 regset，gVisor 没有使用。

### 危险之处：`cpucfg` 没有被屏蔽

```
             AT_HWCAP            cpucfg(2) 报告的真实硬件
runc         LSX=1 LASX=1        LSX=1 LASX=1
runsc        LSX=0 LASX=0        LSX=1 LASX=1
```

gVisor 在 HWCAP 里把 LSX/LASX 屏蔽掉了，所以 glibc 的 ifunc 会选标量实现
（实测容器内 memcpy 1MB 后向量寄存器完好，走的是标量路径）。**但 `cpucfg` 是非特权指令，
gVisor 没有拦截它，guest 直接执行看到的是真实硬件能力。**

于是任何**不查 HWCAP、而用 `cpucfg` 自行探测**的代码都会使用向量指令，
然后在一个不保存这些寄存器的运行时上静默出错。这类代码包括：

- **JIT**：HotSpot 在 LoongArch 上正是用 `cpucfg` 探测 CPU 特性的。这很可能是当初
  "被 Java 折腾惨了"的一部分原因。
- 用 `-mlsx` / `-mlasx` 编译的第三方库（本机 gcc 默认就支持这两个选项）
- 手写向量汇编

### 两条可选的修法

1. **拦截 `cpucfg`**，把 LSX/LASX 位清掉，让 guest 的探测与 HWCAP 一致。
   代价小，但只是"让 guest 别用"，用了仍然坏。
2. **真正保存/恢复向量状态**：在 ptrace 平台用 `NT_LOONGARCH_LSX`/`NT_LOONGARCH_LASX`
   两个 regset，并把扩展上下文纳入信号帧（`signal_loong64.go` 现在写死
   `FpuInfo: SctxInfo{Magic: _FPU_CTX_MAGIC, Size: 16+272}`，而真实硬件的第一条记录
   实测是 `LASX_CTX_MAGIC`、1056 字节）。代价大但根治。

**在做到 2 之前，1 是必须的**——否则 guest 会以为自己能用向量指令而实际上不能。

### 与 `pd` 损坏的关系：未证实

被测探测器 `thrbss` 本身有 0 条向量指令，容器内 libc 的 memcpy 也走标量路径，
所以那个 `pd->stackblock` 常量损坏**不能**直接归因到本缺陷。两者都真实存在，
但因果关系没有证据，不硬凑。

### 修法 2 的尝试：基础设施就位，但没修好

按"真正保存/恢复向量状态"的路子改了一版，**没有成功**，如实记录以免下一个人重走。

改了什么（在 `47be24383..` 之后的工作区，未合入生产）：

- `pkg/abi/linux/elf.go` 加 `NT_LOONGARCH_LSX/LASX/LBT` 常量
- `fpu_loong64.go` 把每任务 FP 保存区从 272 字节扩到 1856，按 regset 分段布局
- `cpuid_loong64.go` 的 `ExtendedStateSize` 相应改为 1856/16
- `ptrace_unsafe.go` 把 FP 传输抽象成 regset 列表（`fpRegSetSpec`），各架构给自己的组成；
  amd64/arm64 保持单个 regset，行为不变

结果：

| 版本 | veccheck 表现 |
|---|---|
| 修复前 | 只有 lane0 保留（128 次内失败）|
| 传 LSX（按 feature bit 选） | lane0+1 保留（12–287 次失败）|
| 传 LSX + LASX | **退回只有 lane0** |
| 只传 LASX | 只有 lane0（偶尔 lane1）|

**关键：加了 errno 日志后，四个 regset 的传输全部成功，零失败。** 传输是通的，
缓冲区大小和偏移都对（`fpu.NewState()` 确实分配 1856 字节，`FloatingPointData()`
返回的就是它），顺序也验证过是对的——

用户态直接测两种恢复顺序（`~/bss/ordertest.c`）：

```
PRFPREG,LSX,LASX   xr0: aaaa0000 aaaa0001 aaaa0002 aaaa0003   全宽保留
LASX,LSX,PRFPREG   xr0: 0000005a 0000005a aaaa0002 aaaa0003   高位丢失
```

gVisor 用的正是前者。而 `~/bss/regsettest.c` 也证明这三个 regset 在宿主上
GET/SET 都完全正常。

**所以 ptrace 的存取不是 guest 向量状态的唯一去处，还有别的地方在丢它，我没找到。**
丢失时填充值恒为 `0xffffffffffffffff`，与 LoongArch 内核 `init_fp_ctx()` 的
`memset(fpr, ~0, ...)` 一致——即某处触发了"该任务尚未用过 FP"的初始化路径。

下一个人可以从这里接着查：
- `switchToApp` 之外是否还有进入 guest 的路径没有恢复 FP
- 内核的 `TIF_USEDSIMD` / `tsk_used_math` 标志在 PTRACE_SETREGSET 序列中如何变化
- 信号投递路径（`signal_loong64.go` 的 `SignalSetup` 会 `c.fpState = fpu.NewState()`，
  新状态全零，而扩展上下文仍写死 `FPU_CTX` 288 字节）

**代价提醒**：这版把每任务 FP 保存区放大了 6.8 倍（272 → 1856 字节）。在没修好之前
这个代价不值得，所以未合入生产；生产机仍是 `cadfa5a5`。

### 继续排查：损坏在保存端，但差异点未找到

在 gVisor 里加了"写后立刻回读比对"的诊断，得到几条硬数据。

**内核确实接受了写入。** 40 次 LASX SET 中 38 次回读完全一致；另外加了"内核实际搬运字节数"
的检查（`iovec.Len` 会被内核改写），**没有任何一次短传输**——1024 字节每次都全部搬到。

**损坏在保存端，不在恢复端。** 看我们写进去的内容本身：

```
wrote = 0000000000000000 48be5bcafe7f0000 ffffffffffffffff ffffffffffffffff
                                          ^^^^^^^^^^^^^^^^ 高位本来就是 0xff
```

也就是说 `getFPRegs` 读回来的就已经是 0xff。而最早那一次（`match=false`）暴露了源头：

```
第一次 SET:  wrote = 全零 ×4
             back  = 0  ffffffff  ffffffff  ffffffff
```

**我们写了 1024 字节全零，立刻回读高三个 lane 变成 0xff。** 此后就一直忠实地把这个填充值
转来转去，guest 的真实值每次恢复都被覆盖。

**但用户态复现不出来。** 三个独立的探针都表明这个序列在宿主上是正确的：

| 探针 | 场景 | 结果 |
|---|---|---|
| `~/bss/firstset.c` | 子进程**用过** LASX：PRFPREG SET(0) → LASX SET(0) → GET | 全零，正确 |
| `~/bss/firstset.c` | 子进程**没用过** LASX，同样序列 | 全零，正确 |
| `~/bss/shortget.c` | SET 1024 零后分别按 64 / 1024 字节回读 | 两者都是全零 |

所以"回读长度不同导致误判"和"任务没用过向量单元"这两个解释都被排除了。

**gVisor 与用户态的差异点没有找到。** 已排除：传输失败、短传输、缓冲区大小与偏移、
恢复顺序、回读长度、任务是否用过向量单元。剩下的可疑点是 stub 线程所处的停止类型
（gVisor 是 `PTRACE_SYSEMU` 的 syscall-enter stop，用户态探针是 SIGSTOP），
以及是否有别的路径在两者之间改动了 FP 上下文。

诊断版 runsc 在龙芯机 `~/runsc-vec6`；生产机仍是 `cadfa5a5`。

### 结构性差异全部排除，怀疑转向诊断本身

把 gVisor 与用户态探针之间**最后两个**不同点也测了，都正确：

| 探针 | 差异点 | 结果 |
|---|---|---|
| `~/bss/stoptype.c` | `PTRACE_SYSEMU` 的 syscall-enter stop（gVisor 用的）vs SIGSTOP | 两者都正确 |
| `~/bss/threadtrace.c` | 被 trace 的是**非线程组长的线程**（gVisor 的 stub 就是）vs 独立进程 | 正确 |

至此已排除：传输失败、短传输、缓冲区大小与偏移、恢复顺序、回读长度、任务是否用过向量
单元、停止类型、被 trace 对象是进程还是线程。**用户态怎么组合都复现不出 gVisor 里看到的
那个 `[0, ff, ff, ff]`。**

**因此最可疑的已经变成 sentry 里那段诊断代码本身。** 这次调查中已经有四个结论被仪器污染
过（比较对象错位、`hunt()` 找到自己溢出的参数、`regcheck` 的全局数组竞争、未初始化的
`$r22`），而 `debugVerifyRegSet` 是目前唯一没有被独立手段交叉验证的环节。下一个人接手时，
应当先怀疑它，而不是继续沿着它的输出往下推。

**但现象本身不受影响**：`veccheck` 在 runc 下 9605 万次迭代零失败、在 runsc 下 128 次内
必失败，这是两个独立二进制、多次重复测得的，与诊断代码无关。缺陷成立，机制未明。

### 建议的止血措施

在把向量状态真正修好之前，应当先做**拦截 `cpucfg`、清掉 LSX/LASX 位**：

- 改动小、独立、可单独验证
- 消除的是"gVisor 告诉 guest 有向量单元，实际却不保存它"这个组合——这才是危险所在
- 受影响的是用 `cpucfg` 而非 HWCAP 做特性探测的代码，**HotSpot 在 LoongArch 上正是如此**

在能正确保存之前，让 guest 知道自己没有向量单元，比让它以为自己有要安全得多。
