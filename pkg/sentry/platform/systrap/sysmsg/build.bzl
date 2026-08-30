"""Sysmsg rules."""

load("//tools:arch.bzl", "select_arch")
load("//tools:defs.bzl", "cc_toolchain")

def cc_pie_obj(name, srcs, outs):
    native.genrule(
        name = name,
        srcs = srcs,
        outs = outs,
        cmd = "$(CC)  $(CC_FLAGS)  " +
              select({
                  "//tools/bazeldefs:pagesize_64k": " -DPAGE_SIZE=65536 ",
                  "//tools/bazeldefs:loong64": " -DPAGE_SIZE=16384 ",
                  "//conditions:default": " -DPAGE_SIZE=4096 ",
              }) +
              "-Wall -Werror -Wno-unused-command-line-argument " +
              "-fpie " +
              # -01 is required for clang to avoid making use of memcpy when
              # building for ARM64. For some reason when no optimization is turned
              # on clang makes use of memcpy to copy structures and when combined
              # with -ffreestanding it means we need to provide our own version of
              # memcpy. Using -01 causes clang to not make use of memcpy avoiding
              # the need to supply our own memcpy version.
              select_arch(
                  amd64 = "-O2",
                  arm64 = "-O1 -mno-outline-atomics ",
                  loong64 = "-O2 ",
              ) +
              " -fno-builtin " +
              "-ffreestanding " +
              # Keep the handler off the FP and vector registers: it runs in a
              # signal context and must not disturb the guest's.
              #
              # LoongArch has no -mgeneral-regs-only, and its nearest
              # equivalents switch ABI: -msoft-float and -mfpu=none both select
              # lp64s, which the hard-float cross-libc has no headers for, and
              # -mabi=lp64d -mfpu=none is rejected outright as contradictory.
              #
              # It matters less here than the flag name suggests. The kernel
              # saves the full FP and vector state into the signal frame on
              # entry and restores it on rt_sigreturn, so the guest's registers
              # live in memory for as long as the handler runs; the handler
              # touching FP registers cannot lose them. What must not appear is
              # vector instructions, hence -mno-lsx -mno-lasx. The build then
              # checks the emitted blob really contains no FP or vector
              # instructions -- see the objdump assertion below.
              select_arch(
                  amd64 = "-mgeneral-regs-only ",
                  arm64 = "-mgeneral-regs-only ",
                  loong64 = "-mno-lsx -mno-lasx ",
              ) +
              # Set -g0 to omit debugging information because it contains
              # absolute paths, which are volatile build information and results
              # in Bazel being unable to properly cache the output. If debugging
              # information is desired, the flags -fdebug-compilation-dir or
              # -fdebug-prefix-map can be used.
              "-g0 " +
              "-Wa,--noexecstack " +
              "-fno-asynchronous-unwind-tables " +
              "-fno-stack-protector " +
              "-c $$(echo $(SRCS) | tr ' ' '\n' | grep -v -E '.h$$') -o $@" +
              # loong64 cannot express -mgeneral-regs-only, so assert the
              # outcome instead of trusting the flag: the emitted object must
              # contain no vector instruction, and no floating point one
              # either. Fails the build rather than shipping a handler that
              # quietly disturbs the guest's registers.
              #
              # touch_vector_unit is the one exception -- it exists to execute
              # a vector instruction, so that the kernel gives this thread's
              # signal frames a vector record to save and restore through. It
              # is compiled noinline so the assertion can skip its body and
              # still cover everything else.
              select_arch(
                  # Bazel's cc_toolchain exposes $(CC), $(LD) and $(OBJCOPY)
                  # as make variables but not objdump, so name the tool the way
                  # cc_toolchain_config.bzl already names the rest of them.
                  loong64 = " && ! /usr/bin/loongarch64-linux-gnu-objdump -d $@" +
                            # Compiler-generated .L blocks belong to whatever
                            # function precedes them, so they must not reset
                            # the skip.
                            " | awk '/^[0-9a-f]+ <.*>:/ && $$0 !~ /<[.]L/" +
                            " { skip = ($$0 ~ /<touch_vector_unit>:/) } !skip'" +
                            " | grep -qE" +
                            " '\\b(x?v(ld|st|or|xor|repli|insgr|pickve)|" +
                            "f(add|sub|mul|div|ld|st|mov|cmp|int))'",
                  default = "",
              ),
        toolchains = [
            ":no_pie_cc_flags",
            cc_toolchain,
        ],
    )
