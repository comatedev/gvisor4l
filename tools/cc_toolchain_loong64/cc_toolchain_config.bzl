"""A minimal CC toolchain for cross-compiling to loongarch64.

Only the systrap sysmsg blob needs a C compiler for loong64: a handful of
freestanding .c and .S files, linked with a linker script and turned into a raw
binary with objcopy. So this configures tool paths and nothing else -- no sysroot,
no libc, no dynamic linking.

Note that LoongArch gcc has no -mgeneral-regs-only; -msoft-float is the
equivalent and is applied in sysmsg/build.bzl rather than here, since it belongs
with the other blob-specific flags.
"""

load("@rules_cc//cc:action_names.bzl", "ACTION_NAMES")
load("@rules_cc//cc:cc_toolchain_config_lib.bzl", "feature", "flag_group", "flag_set", "tool_path")

_PREFIX = "/usr/bin/loongarch64-linux-gnu-"

def _impl(ctx):
    tool_paths = [
        tool_path(name = "gcc", path = _PREFIX + "gcc"),
        tool_path(name = "ld", path = _PREFIX + "ld"),
        tool_path(name = "ar", path = _PREFIX + "ar"),
        tool_path(name = "cpp", path = _PREFIX + "cpp"),
        tool_path(name = "gcov", path = "/bin/false"),
        tool_path(name = "nm", path = _PREFIX + "nm"),
        tool_path(name = "objdump", path = _PREFIX + "objdump"),
        tool_path(name = "objcopy", path = _PREFIX + "objcopy"),
        tool_path(name = "strip", path = _PREFIX + "strip"),
    ]

    # The blob is freestanding and position independent; keep the defaults
    # minimal so sysmsg/build.bzl stays the single place that decides flags.
    default_flags = feature(
        name = "default_flags",
        enabled = True,
        flag_sets = [
            flag_set(
                actions = [
                    ACTION_NAMES.c_compile,
                    ACTION_NAMES.assemble,
                    ACTION_NAMES.preprocess_assemble,
                ],
                flag_groups = [flag_group(flags = ["-no-canonical-prefixes"])],
            ),
        ],
    )

    return cc_common.create_cc_toolchain_config_info(
        ctx = ctx,
        toolchain_identifier = "loong64-linux-gnu",
        host_system_name = "local",
        target_system_name = "loongarch64-linux-gnu",
        target_cpu = "loongarch64",
        target_libc = "glibc",
        compiler = "gcc",
        abi_version = "lp64d",
        abi_libc_version = "glibc",
        tool_paths = tool_paths,
        features = [default_flags],
        cxx_builtin_include_directories = [
            "/usr/lib/gcc-cross/loongarch64-linux-gnu",
            "/usr/loongarch64-linux-gnu/include",
        ],
    )

cc_toolchain_config = rule(
    implementation = _impl,
    attrs = {},
    provides = [CcToolchainConfigInfo],
)
