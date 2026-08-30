// Copyright 2024 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build loong64
// +build loong64

package ptrace

import (
	"gvisor.dev/gvisor/pkg/abi/linux"
	pkgcontext "gvisor.dev/gvisor/pkg/context"
	"gvisor.dev/gvisor/pkg/cpuid"
	"gvisor.dev/gvisor/pkg/sentry/arch"
	"gvisor.dev/gvisor/pkg/sentry/arch/fpu"
)

// archContext is architecture-specific context.
type archContext struct {
	// fpLen is the size of the whole floating-point save area.
	fpLen int
}

// init initializes the archContext.
func (a *archContext) init(ctx pkgcontext.Context) {
	fs := cpuid.FromContext(ctx)
	fpLen, _ := fs.ExtendedStateSize()
	a.fpLen = int(fpLen)
}

// floatingPointLength returns the length of the FP state.
func (a *archContext) floatingPointLength() uint64 {
	return uint64(a.fpLen)
}

// floatingPointRegSet returns the ptrace regset note used to read/write the
// base FP register file.
func (a *archContext) floatingPointRegSet() uintptr {
	return linux.NT_PRFPREG
}

// fpRegSets returns the regsets making up the saved FP state.
//
// NT_PRFPREG holds only the low 64 bits of each vector register, so on its own
// it drops the upper 192 bits of every LASX register whenever a thread is
// descheduled. A guest reaches the vector unit whether or not HWCAP advertises
// it, since cpucfg reports the real hardware, so the state has to be carried
// even though this platform does not declare it (see cpuid.SetVectorStateSaved,
// which only systrap calls) and so leaves LSX and LASX out of HWCAP.
//
// Carrying it here is still not enough -- the upper lanes are lost anyway, and
// that is why the port recommends systrap. See LOONG64_PORT.md.
//
// The vector and LBT regsets are optional: a CPU or kernel without them makes
// PTRACE_GETREGSET fail, which is not an error for us.
func (a *archContext) fpRegSets() []fpRegSetSpec {
	return []fpRegSetSpec{
		{note: linux.NT_PRFPREG, offset: fpu.LoongFPRegsOffset, length: fpu.LoongFPRegsSize},
		// LASX carries the full 256 bits and supersedes LSX; transferring
		// both regressed the upper lanes, so carry only the wider one.
		{note: linux.NT_LOONGARCH_LASX, offset: fpu.LoongLASXOffset, length: fpu.LoongLASXSize, optional: true},
		{note: linux.NT_LOONGARCH_LBT, offset: fpu.LoongLBTOffset, length: fpu.LoongLBTSize, optional: true},
	}
}

// stackPointer returns the user-mode stack pointer. On LoongArch SP is just
// $r3 in the GPR file (no separate Sp field like arm64).
func stackPointer(r *arch.Registers) uintptr {
	return uintptr(r.Regs[3])
}
