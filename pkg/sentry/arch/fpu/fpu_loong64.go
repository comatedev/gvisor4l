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

package fpu

const (
	// loongFPUMagic is the magic number identifying the basic FPU context
	// inside LoongArch sigcontext.sc_extcontext, matching FPU_CTX_MAGIC in
	// arch/loongarch/include/uapi/asm/sigcontext.h.
	loongFPUMagic = 0x46505501

	// The per-task save area is a concatenation of the ptrace regsets that
	// together make up a thread's floating-point and vector state. NT_PRFPREG
	// only covers the low 64 bits of each vector register, so saving it alone
	// silently drops the upper 192 bits of every LASX register across a
	// context switch.
	//
	// Sizes are the kernel's, confirmed by PTRACE_GETREGSET on a 3A5000:
	//
	//	NT_PRFPREG         272   struct user_fp_state   fpr[32], fcc, fcsr
	//	NT_LOONGARCH_LSX   512   struct user_lsx_state  32 x 128 bits
	//	NT_LOONGARCH_LASX 1024   struct user_lasx_state 32 x 256 bits
	//	NT_LOONGARCH_LBT    40   struct user_lbt_state  scr[4], eflags, ftop
	//
	// LSX and LASX are the same register file at different widths, so only
	// the wider one the CPU supports is transferred; LoongVecSize reserves
	// room for the larger.
	LoongFPRegsOffset = 0
	LoongFPRegsSize   = 272

	// LSX and LASX are the same registers at different widths, but their
	// regsets use different layouts (32x128 vs 32x256), so they cannot share
	// a buffer. Both are carried and both are optional; on restore LSX is
	// applied first and LASX, where the CPU has it, overwrites at full width.
	//
	// Which of them a given host implements is discovered by attempting each
	// and letting the kernel reject what it does not have, rather than from
	// a feature bit: the ptrace platform moves this state before any guest
	// exists to ask about.
	//
	// LINT.IfChange
	LoongLSXOffset = LoongFPRegsOffset + LoongFPRegsSize
	LoongLSXSize   = 512

	LoongLASXOffset = LoongLSXOffset + LoongLSXSize
	LoongLASXSize   = 1024

	LoongLBTOffset = LoongLASXOffset + LoongLASXSize
	LoongLBTSize   = 40

	// loongFPUStateSize is the total, rounded up to 16-byte alignment.
	loongFPUStateSize = (LoongLBTOffset + LoongLBTSize + 15) &^ 15 // 1856
	// LINT.ThenChange(../../platform/systrap/sysmsg/sighandler_loong64.c)
)

// initLoongFPState resets the state to the canonical "clean" values.
// fcsr / fcc default to zero; floating-point registers are don't-care, so an
// all-zero buffer is already the clean state and there is nothing to do.
func initLoongFPState(data *State) {
}

// newLoongFPStateSlice returns an over-allocated, 16-byte aligned backing
// buffer of which the first loongFPUStateSize bytes are usable. The over
// allocation mirrors fpu_arm64.go and lets us keep alignment without
// special-casing the slice header.
func newLoongFPStateSlice() []byte {
	return alignedBytes(4096, 16)[:loongFPUStateSize]
}

// NewState returns an initialized floating-point state.
func NewState() State {
	f := State(newLoongFPStateSlice())
	initLoongFPState(&f)
	return f
}

// Fork creates and returns an identical copy of the LoongArch FPU state.
func (s *State) Fork() State {
	n := State(newLoongFPStateSlice())
	copy(n, *s)
	return n
}

// BytePointer returns a pointer to the first byte of the state.
//
//go:nosplit
func (s *State) BytePointer() *byte {
	return &(*s)[0]
}
