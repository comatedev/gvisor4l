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

# Regenerates pkg/sentry/loader/vdsodata/vdso_loong64.so.
#
# //vdso:vdso cannot do this for us: it compiles C++ with the Bazel C++
# toolchain of the *target* platform, and this port has no cc_toolchain for
# loongarch64 on purpose (runsc is pure Go and cross-builds without one). So the
# VDSO is built here instead and the result is committed.
#
# Run this on a LoongArch64 machine, or set CXX to a loongarch64 cross g++.
# The flags below mirror the genrule in vdso/BUILD exactly; keep them in sync.
#
# Environment:
#   CXX - C++ compiler (default: g++)
#   OUT - output path (default: pkg/sentry/loader/vdsodata/vdso_loong64.so)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_DIR}"

CXX="${CXX:-g++}"
OUT="${OUT:-pkg/sentry/loader/vdsodata/vdso_loong64.so}"

log() { echo "[build-vdso] $*"; }
die() { echo "[build-vdso] error: $*" >&2; exit 1; }

command -v "${CXX}" >/dev/null 2>&1 || \
  die "'${CXX}' not found. Run this on a LoongArch64 host, or set CXX to a cross g++.
  A container works too, e.g.:
    docker run --rm -v \"\$PWD:/w\" -w /w ghcr.io/loong64/gcc:15 scripts/build-vdso-loong64.sh"

log "compiling with ${CXX}"
"${CXX}" -I. -O2 -std=c++11 -fPIC \
    -fno-sanitize=all \
    -fno-stack-protector \
    -fno-pie \
    -shared \
    -nostdlib \
    -Wl,-soname=linux-vdso.so.1 \
    -Wl,--hash-style=sysv \
    -Wl,--no-undefined \
    -Wl,-Bsymbolic \
    -Wl,-z,max-page-size=4096 \
    -Wl,-z,common-page-size=4096 \
    -Wl,-T,vdso/vdso_loong64.lds \
    -o "${OUT}" \
    vdso/vdso.cc \
    vdso/vdso_time.cc

# The sentry maps the VDSO directly, with no dynamic linker to fix anything up,
# so a single relocation makes it unusable. check_vdso.py is locale-sensitive
# (it looks for empty readelf output), hence LC_ALL=C.
log "validating"
LC_ALL=C python3 vdso/check_vdso.py --vdso "${OUT}" || die "check_vdso.py rejected ${OUT}"

log "built: ${OUT}"
log "  size:   $(stat -c %s "${OUT}") bytes"
log "  sha256: $(sha256sum "${OUT}" | cut -d' ' -f1)"
LC_ALL=C readelf --dyn-syms -W "${OUT}" | grep -E "__vdso_|__kernel_" | \
    sed 's/^/[build-vdso]   /'
