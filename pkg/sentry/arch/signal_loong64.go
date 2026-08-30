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

package arch

import (
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/cpuid"
	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/marshal"
	"gvisor.dev/gvisor/pkg/sentry/arch/fpu"
)

// LoongArch extended-context magic numbers and sizes, from
// arch/loongarch/include/uapi/asm/sigcontext.h.
const (
	_FPU_CTX_MAGIC  = 0x46505501
	_LSX_CTX_MAGIC  = 0x53580001
	_LASX_CTX_MAGIC = 0x41535801
	_END_CTX_MAGIC  = 0

	// _LASX_CTX_ALIGN is the alignment the kernel gives an LASX context
	// body. LSX gets 16 and FPU 8, both of which any 16-byte aligned frame
	// already satisfies, so only this one constrains where the frame goes.
	_LASX_CTX_ALIGN = 32

	// _sctxInfoSize is sizeof(struct sctx_info).
	_sctxInfoSize = 16

	// _maxExtContextRecords bounds the walk over a chain that came back
	// from the application on rt_sigreturn.
	_maxExtContextRecords = 8
)

// SctxInfo is the header preceding each entry in sigcontext.sc_extcontext.
// Layout matches `struct sctx_info`: { __u32 magic; __u32 size; __u64 _pad; }.
//
// Size counts from this header to the start of the next one, so it absorbs any
// alignment padding the producer left after the body.
//
// +marshal
type SctxInfo struct {
	Magic   uint32
	Size    uint32
	Padding uint64
}

// FpuContext is the base floating-point save area, identified by
// FPU_CTX_MAGIC. Matches `struct fpu_context`.
//
// The layout is byte-for-byte `struct user_fp_state`, which is what the ptrace
// NT_PRFPREG regset carries and therefore what the first 272 bytes of
// fpu.State hold; see fpu.LoongFPRegsOffset. That is why the FPU case below is
// a straight copy.
//
// +marshal
type FpuContext struct {
	Regs [32]uint64
	Fcc  uint64
	Fcsr uint32
	_    uint32 // explicit pad to align next ext-context
}

// LsxContext is `struct lsx_context`, identified by LSX_CTX_MAGIC: the 32
// vector registers at 128 bits each, plus its own copy of fcc and fcsr.
//
// +marshal
type LsxContext struct {
	Regs [2 * 32]uint64
	Fcc  uint64
	Fcsr uint32
	_    uint32
}

// LasxContext is `struct lasx_context`, identified by LASX_CTX_MAGIC: the same
// registers at 256 bits.
//
// +marshal
type LasxContext struct {
	Regs [4 * 32]uint64
	Fcc  uint64
	Fcsr uint32
	_    uint32
}

// SignalContext64 is the fixed part of `struct sigcontext` for LoongArch64:
// { pc, regs[32], flags, pad[4] }, 272 bytes and 16-byte aligned.
//
// The extended context follows it in memory but is not a field here, because
// which records appear and how long they are depends on the CPU. SignalSetup
// writes them separately and SignalRestore walks them.
//
// +marshal
type SignalContext64 struct {
	Pc    uint64
	Regs  [32]uint64
	Flags uint32
	_     [4]byte // pad so the extended-context area is 16-byte aligned
}

// UContext64 is `struct ucontext` for LoongArch64. Layout:
//
//	uc_flags : 8 bytes
//	uc_link  : 8 bytes
//	uc_stack : 24 bytes (linux.SignalStack)
//	uc_sigmask : 8 bytes (linux.SignalSet, sigset_t)
//	unused  : (1024/8) - 8 = 120 bytes
//	pad     : 8 bytes (so uc_mcontext is 16-byte aligned)
//	uc_mcontext : SignalContext64, followed by the extended context
//
// +marshal
type UContext64 struct {
	Flags    uint64
	Link     uint64
	Stack    linux.SignalStack
	Sigset   linux.SignalSet
	_        [120]byte
	_        [8]byte
	MContext SignalContext64
}

// extContextKind is which of the three mutually exclusive floating-point
// records a frame carries. The kernel emits exactly one, for the widest unit
// the thread has used; gVisor emits one for the widest unit it tells the guest
// it has.
type extContextKind int

const (
	extContextFPU extContextKind = iota
	extContextLSX
	extContextLASX
)

// extContextKindFor picks the record to hand the application.
//
// This follows the advertised HWCAP rather than the hardware, so the frame
// agrees with what the guest was told at exec time: a platform that does not
// carry vector state does not advertise LSX or LASX (see
// cpuid.SetVectorStateSaved), and a guest there gets an FPU record, which is
// the only part of the state that platform keeps.
func extContextKindFor(featureSet cpuid.FeatureSet) extContextKind {
	allowed := featureSet.AllowedHWCap1()
	switch {
	case allowed&cpuid.HWCAP_LOONGARCH_LASX != 0:
		return extContextLASX
	case allowed&cpuid.HWCAP_LOONGARCH_LSX != 0:
		return extContextLSX
	default:
		return extContextFPU
	}
}

// bodySize returns the size of the record body, excluding its SctxInfo.
func (k extContextKind) bodySize() int {
	switch k {
	case extContextLASX:
		return (*LasxContext)(nil).SizeBytes()
	case extContextLSX:
		return (*LsxContext)(nil).SizeBytes()
	default:
		return (*FpuContext)(nil).SizeBytes()
	}
}

// magic returns the record's sctx_info magic.
func (k extContextKind) magic() uint32 {
	switch k {
	case extContextLASX:
		return _LASX_CTX_MAGIC
	case extContextLSX:
		return _LSX_CTX_MAGIC
	default:
		return _FPU_CTX_MAGIC
	}
}

// fcc and fcsr live in the base FP regset in fpu.State even when the state
// came from a vector record, because the LSX and LASX ptrace regsets carry
// only the register file. These are their offsets there.
const (
	_fpStateFccOffset  = fpu.LoongFPRegsOffset + 32*8
	_fpStateFcsrOffset = _fpStateFccOffset + 8
)

// marshalExtContext returns the record body describing fpState.
func (k extContextKind) marshalExtContext(fpState []byte) marshal.Marshallable {
	fcc := hostarch.ByteOrder.Uint64(fpState[_fpStateFccOffset:])
	fcsr := hostarch.ByteOrder.Uint32(fpState[_fpStateFcsrOffset:])
	switch k {
	case extContextLASX:
		c := &LasxContext{Fcc: fcc, Fcsr: fcsr}
		regsToUint64(fpState[fpu.LoongLASXOffset:][:fpu.LoongLASXSize], c.Regs[:])
		return c
	case extContextLSX:
		c := &LsxContext{Fcc: fcc, Fcsr: fcsr}
		regsToUint64(fpState[fpu.LoongLSXOffset:][:fpu.LoongLSXSize], c.Regs[:])
		return c
	default:
		// Identical layouts; see FpuContext.
		c := &FpuContext{}
		c.UnmarshalUnsafe(fpState[fpu.LoongFPRegsOffset:][:fpu.LoongFPRegsSize])
		return c
	}
}

func regsToUint64(src []byte, dst []uint64) {
	for i := range dst {
		dst[i] = hostarch.ByteOrder.Uint64(src[i*8:])
	}
}

func uint64ToRegs(src []uint64, dst []byte) {
	for i := range src {
		hostarch.ByteOrder.PutUint64(dst[i*8:], src[i])
	}
}

// validRegs vets a set of registers loaded from userspace via PtraceSetRegs
// or sigreturn. LoongArch does not surface privilege-level bits to userspace
// the way arm64 does (PSTATE), so the policy here is very permissive: we
// only ensure $r0 stays zero. Anything else could be deliberate.
func (regs *Registers) validRegs() bool {
	regs.Regs[0] = 0
	return true
}

// SignalSetup implements Context.SignalSetup. It pushes a siginfo_t followed
// by a ucontext_t onto the signal stack, then redirects execution to the
// user-installed handler with the LoongArch-mandated argument convention:
//
//	$a0 = signo
//	$a1 = &siginfo
//	$a2 = &ucontext
//	$ra = restorer (rt_sigreturn trampoline)
//	$sp = signal frame top
func (c *Context64) SignalSetup(st *Stack, act *linux.SigAction, info *linux.SignalInfo, alt *linux.SignalStack, sigset linux.SignalSet, featureSet cpuid.FeatureSet) error {
	sp := st.Bottom

	uc := &UContext64{
		Flags: 0,
		Stack: *alt,
		MContext: SignalContext64{
			Pc:   c.Regs.Era,
			Regs: c.Regs.Regs,
		},
		Sigset: sigset,
	}

	// The extended context is one floating-point record followed by the
	// terminator. Both are written separately from uc, because the record's
	// length depends on which one it is.
	kind := extContextKindFor(featureSet)
	recInfo := SctxInfo{
		Magic: kind.magic(),
		Size:  uint32(_sctxInfoSize + kind.bodySize()),
	}
	endInfo := SctxInfo{Magic: _END_CTX_MAGIC, Size: 0}
	body := kind.marshalExtContext(c.fpState)

	ucPrefixSize := uc.SizeBytes()
	ucSize := ucPrefixSize + int(recInfo.Size) + _sctxInfoSize

	// Stack frame layout (low to high): [ucontext | extended context | siginfo].
	// sizeof(siginfo) == 128.
	frameSize := ucSize + 128
	frameBottom := (sp - hostarch.Addr(frameSize)) & ^hostarch.Addr(15)

	// The kernel aligns an LASX context body to 32 bytes. The body sits
	// _sctxInfoSize past the start of the extended context, which is
	// ucPrefixSize past the start of the ucontext, so aligning the frame to
	// 32 would leave the body at 16 mod 32. Shift the frame down to the
	// nearest address that puts the body where the kernel would.
	if kind == extContextLASX {
		bodyOff := hostarch.Addr(ucPrefixSize + _sctxInfoSize)
		if rem := (frameBottom + bodyOff) % _LASX_CTX_ALIGN; rem != 0 {
			frameBottom -= rem
		}
	}
	sp = frameBottom + hostarch.Addr(frameSize)
	st.Bottom = sp

	if act.Flags&linux.SA_ONSTACK != 0 && alt.IsEnabled() && !alt.Contains(frameBottom) {
		return unix.EFAULT
	}

	info.FixSignalCodeForUser()

	// The stack is written downwards, so this runs from the top of the
	// frame to the bottom: siginfo, then the extended context back to
	// front, then the ucontext.
	if _, err := info.CopyOut(st, StackBottomMagic); err != nil {
		return err
	}
	infoAddr := st.Bottom
	if _, err := endInfo.CopyOut(st, StackBottomMagic); err != nil {
		return err
	}
	if _, err := body.CopyOut(st, StackBottomMagic); err != nil {
		return err
	}
	if _, err := recInfo.CopyOut(st, StackBottomMagic); err != nil {
		return err
	}
	if _, err := uc.CopyOut(st, StackBottomMagic); err != nil {
		return err
	}
	ucAddr := st.Bottom

	// Redirect execution to the handler.
	c.Regs.Regs[regSP] = uint64(st.Bottom)
	c.Regs.Era = act.Handler
	c.Regs.Regs[regA0] = uint64(info.Signo)
	c.Regs.Regs[regA1] = uint64(infoAddr)
	c.Regs.Regs[regA2] = uint64(ucAddr)
	c.Regs.Regs[regRA] = act.Restorer

	// Save and reset FP state for the handler.
	c.sigFPState = append(c.sigFPState, c.fpState)
	c.fpState = fpu.NewState()
	return nil
}

// restoreExtContext walks the extended context the application handed back and
// applies it to fpState.
//
// Precondition: st.Bottom is at the start of the extended context, that is,
// just past the ucontext prefix.
//
// The values are not sanitised. Nothing here can be reached only through this
// path: an application can already write any of these registers, fcsr and fcc
// included, with ordinary instructions, and the hardware ignores the reserved
// bits of both.
func restoreExtContext(st *Stack, fpState []byte) error {
	for i := 0; i < _maxExtContextRecords; i++ {
		start := st.Bottom
		var info SctxInfo
		if _, err := info.CopyIn(st, StackBottomMagic); err != nil {
			return err
		}
		if info.Magic == _END_CTX_MAGIC {
			return nil
		}
		if info.Size < _sctxInfoSize {
			return unix.EFAULT
		}

		switch info.Magic {
		case _FPU_CTX_MAGIC:
			var c FpuContext
			if _, err := c.CopyIn(st, StackBottomMagic); err != nil {
				return err
			}
			// Identical layouts; see FpuContext.
			c.MarshalUnsafe(fpState[fpu.LoongFPRegsOffset:][:fpu.LoongFPRegsSize])
		case _LSX_CTX_MAGIC:
			var c LsxContext
			if _, err := c.CopyIn(st, StackBottomMagic); err != nil {
				return err
			}
			uint64ToRegs(c.Regs[:], fpState[fpu.LoongLSXOffset:][:fpu.LoongLSXSize])
			hostarch.ByteOrder.PutUint64(fpState[_fpStateFccOffset:], c.Fcc)
			hostarch.ByteOrder.PutUint32(fpState[_fpStateFcsrOffset:], c.Fcsr)
		case _LASX_CTX_MAGIC:
			var c LasxContext
			if _, err := c.CopyIn(st, StackBottomMagic); err != nil {
				return err
			}
			uint64ToRegs(c.Regs[:], fpState[fpu.LoongLASXOffset:][:fpu.LoongLASXSize])
			hostarch.ByteOrder.PutUint64(fpState[_fpStateFccOffset:], c.Fcc)
			hostarch.ByteOrder.PutUint32(fpState[_fpStateFcsrOffset:], c.Fcsr)
		default:
			// Some extension this build does not know about, LBT for
			// one. Skipping it is what the size field is for.
		}

		// Step over the whole record, including whatever alignment
		// padding the producer left after the body.
		next := start + hostarch.Addr(info.Size)
		if next <= start {
			return unix.EFAULT
		}
		st.Bottom = next
	}
	return unix.EFAULT
}

// SignalRestore implements Context.SignalRestore (rt_sigreturn).
func (c *Context64) SignalRestore(st *Stack, rt bool, featureSet cpuid.FeatureSet) (linux.SignalSet, linux.SignalStack, error) {
	var uc UContext64
	if _, err := uc.CopyIn(st, StackBottomMagic); err != nil {
		return 0, linux.SignalStack{}, err
	}

	// Restore integer registers from the saved sigcontext.
	c.Regs.Regs = uc.MContext.Regs
	c.Regs.Era = uc.MContext.Pc

	if !c.Regs.validRegs() {
		return 0, linux.SignalStack{}, unix.EFAULT
	}

	// Pop the state the handler interrupted. This is the base: a handler
	// that left the extended context alone gets exactly what it had.
	l := len(c.sigFPState)
	if l > 0 {
		c.fpState = c.sigFPState[l-1]
		c.sigFPState[l-1] = nil
		c.sigFPState = c.sigFPState[0 : l-1]
	} else {
		// Unbalanced sigreturn -- leave FP state alone, but warn.
		log.Warningf("sigreturn unable to restore application fpstate")
		return 0, linux.SignalStack{}, unix.EFAULT
	}

	// Then apply whatever the handler left in the frame, so that changing
	// uc_mcontext takes effect the way it does on Linux. A frame that does
	// not parse leaves the popped state in place rather than failing the
	// sigreturn, because that state is the correct one to resume on; the
	// walk stops at the first record it cannot make sense of, so st.Bottom
	// is then somewhere arbitrary and the siginfo behind the chain cannot
	// be located any more.
	if err := restoreExtContext(st, c.fpState); err != nil {
		log.Warningf("sigreturn: ignoring malformed extended context: %v", err)
		return uc.Sigset, uc.Stack, nil
	}

	// Read the siginfo that follows the chain, purely to fault it in the
	// way Linux would; its contents are not used.
	var info linux.SignalInfo
	if _, err := info.CopyIn(st, StackBottomMagic); err != nil {
		return 0, linux.SignalStack{}, err
	}

	return uc.Sigset, uc.Stack, nil
}
