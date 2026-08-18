#!/bin/bash

# Copyright 2026 The gVisor Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Cross-builds runsc for linux/loong64 from an x86_64 Linux host.
#
# Upstream Bazel publishes no linux-loongarch64 release binary, so the build
# always runs on x86_64. That costs nothing: //runsc:runsc is pure Go (pure =
# True, plus --@io_bazel_rules_go//go/config:pure in .bazelrc), so the Go
# toolchain cross-compiles it directly and no LoongArch C toolchain is involved
# at all. The vDSO is a prebuilt ELF committed at
# pkg/sentry/loader/vdsodata/vdso_loong64_stub.so and is not built here.
#
# Running this on a loongarch64 host also works and becomes a native build, but
# that requires a Bazel that upstream does not ship.
#
# Environment:
#   BAZEL  - bazel binary                (default: bazel)
#   MODE   - bazel compilation mode      (default: fastbuild; "opt" strips)
#   OUT    - output path for the binary  (default: <repo>/bin/runsc)
#   JOBS   - value for bazel --jobs      (default: bazel decides)
#   SMOKE  - set to 0 to skip the qemu-user smoke test
#
# Any extra arguments are passed through to `bazel build`.

set -euo pipefail

# Defined in .bazelrc; sets --platforms=@io_bazel_rules_go//go/toolchain:linux_loong64.
# That platform is only valid because of tools/rules_go_loong64.patch (registers
# the GOOS/GOARCH pair and maps loong64 to @platforms//cpu:loongarch64) and
# tools/platforms_loongarch64.patch (adds that constraint_value). The platform
# must be set explicitly even on a loongarch64 host: //tools/bazeldefs:loong64
# keys off the cpu:loongarch64 constraint, and when it fails to match,
# select_goarch() silently falls through to the default branch and yields a
# binary for the wrong architecture.
readonly BAZEL_CONFIG="loong64"
readonly TARGET="//runsc:runsc"
readonly FALLBACK_BIN="bazel-bin/runsc/runsc_/runsc"

# A missing patch does not fail the build loudly; it produces a
# wrong-architecture binary, so check up front.
readonly REQUIRED_PATCHES=(
  "tools/rules_go_loong64.patch"
  "tools/platforms_loongarch64.patch"
  "tools/gazelle_loong64_platform.patch"
  "tools/gazelle_loong64_platform_info.patch"
)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_DIR}"

BAZEL="${BAZEL:-bazel}"
MODE="${MODE:-fastbuild}"
OUT="${OUT:-${REPO_DIR}/bin/runsc}"

log() { echo "[build-runsc] $*"; }
warn() { echo "[build-runsc] warning: $*" >&2; }
die() { echo "[build-runsc] error: $*" >&2; exit 1; }

##
## Preflight.
##

command -v "${BAZEL}" >/dev/null 2>&1 || \
  die "'${BAZEL}' not found; install Bazel (or bazelisk) on this x86_64 host"

for patch in "${REQUIRED_PATCHES[@]}"; do
  [[ -f "${patch}" ]] || die "missing ${patch}; without it the build silently targets the wrong architecture"
done

grep -q "^build:${BAZEL_CONFIG} " .bazelrc || \
  die "no 'build:${BAZEL_CONFIG}' section in .bazelrc"

# .bazelversion is a symlink to images/default/bazelversion. On a checkout made
# without symlink support it is a plain file holding that path, so follow it.
expected_version=""
if [[ -f .bazelversion ]]; then
  expected_version="$(tr -d '[:space:]' < .bazelversion)"
  if [[ -f "${expected_version}" ]]; then
    expected_version="$(tr -d '[:space:]' < "${expected_version}")"
  fi
fi
actual_version="$("${BAZEL}" --version 2>/dev/null | awk '{print $2}' || true)"
if [[ -n "${expected_version}" && -n "${actual_version}" && "${expected_version}" != "${actual_version}" ]]; then
  warn "bazel ${actual_version} in use, but this tree pins ${expected_version}"
fi

command -v git >/dev/null 2>&1 || \
  warn "git not found; --stamp will fall back to version 0.0.0 (see tools/workspace_status.sh)"

host_arch="$(uname -m)"
case "${host_arch}" in
  x86_64) log "host ${host_arch}: cross-building for loong64" ;;
  loongarch64) log "host ${host_arch}: native build" ;;
  *) warn "unexpected host ${host_arch}; proceeding with the cross-build" ;;
esac
log "bazel: ${BAZEL} ${actual_version:-unknown}, mode: ${MODE}"

##
## Build.
##

bazel_args=(
  "build"
  "--config=${BAZEL_CONFIG}"
  "-c" "${MODE}"
)
[[ -n "${JOBS:-}" ]] && bazel_args+=("--jobs=${JOBS}")
bazel_args+=("$@" "${TARGET}")

log "running: ${BAZEL} ${bazel_args[*]}"
"${BAZEL}" "${bazel_args[@]}"

##
## Locate and verify.
##

bin="$("${BAZEL}" cquery \
  "--config=${BAZEL_CONFIG}" \
  -c "${MODE}" \
  --output=files \
  "${TARGET}" 2>/dev/null | head -n 1 || true)"
if [[ -z "${bin}" || ! -f "${bin}" ]]; then
  bin="${FALLBACK_BIN}"
fi
[[ -f "${bin}" ]] || die "built binary not found (looked for ${bin})"

# Verify e_machine == EM_LOONGARCH (258 == 0x0102), little-endian at offset 0x12.
# This is the real guard against a silently mis-targeted build: nothing else in
# the build fails when the loong64 patches do not apply.
machine="$(od -An -tx1 -j18 -N2 "${bin}" | tr -d '[:space:]')"
[[ "${machine}" == "0201" ]] || \
  die "${bin} is not a LoongArch ELF (e_machine=0x${machine:2:2}${machine:0:2}, expected 0x0102).
  This usually means one of the loong64 patches did not apply and select_goarch()
  fell through to the default branch. Try: ${BAZEL} clean --expunge"

mkdir -p "$(dirname "${OUT}")"
install -m 0755 "${bin}" "${OUT}"

log "built: ${OUT}"
log "  size:   $(stat -c %s "${OUT}") bytes"
log "  sha256: $(sha256sum "${OUT}" | cut -d' ' -f1)"
if command -v file >/dev/null 2>&1; then
  log "  file:   $(file -b "${OUT}")"
fi

##
## Smoke test.
##
## Catches a binary that is the right architecture but cannot actually run,
## without a round trip to the LoongArch machine. runsc --version does not need
## root, a sandbox, or any host feature.
##

if [[ "${host_arch}" != "loongarch64" && "${SMOKE:-1}" != "0" ]]; then
  qemu="$(command -v qemu-loongarch64-static 2>/dev/null || command -v qemu-loongarch64 2>/dev/null || true)"
  if [[ -z "${qemu}" ]]; then
    log "smoke test skipped: no qemu-loongarch64-static (apt install qemu-user-static)"
  else
    log "smoke test: ${qemu} ${OUT} --version"
    "${qemu}" "${OUT}" --version || die "the binary does not run under qemu-user"
  fi
fi

log "done; copy ${OUT} to the LoongArch machine"
