// Copyright 2026 The gVisor Authors.
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

package systrap

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/seccomp"
	"gvisor.dev/gvisor/pkg/sentry/arch"
	"gvisor.dev/gvisor/pkg/sentry/platform/systrap/sysmsg"
)

const (
	// initRegsRipAdjustment is the size of the `syscall 0` instruction.
	initRegsRipAdjustment = 4
)

// LoongArch reports no fault status register in the signal frame, so there is
// nothing for the stub to hand over the way arm64 hands over an esr_context.
// It synthesises these bits instead; see sighandler_loong64.c.
//
// LINT.IfChange
const (
	// faultWrite is set when the kernel reported SEGV_ACCERR, that is, the
	// mapping was present and the access was not permitted. Every host
	// mapping the sentry installs is at least readable, so in practice this
	// only happens for a store to a page mapped read-only.
	faultWrite = 1 << 0

	// faultInstr is set when the faulting address is the address of the
	// instruction that faulted.
	faultInstr = 1 << 1
)

// LINT.ThenChange(sysmsg/sighandler_loong64.c)

// resetSysemuRegs sets up emulation registers.
//
// This should be called prior to calling sysemu.
func (s *subprocess) resetSysemuRegs(regs *arch.Registers) {
}

// createSyscallRegs sets up syscall registers.
//
// This should be called to generate registers for a system call.
//
// The LoongArch Linux ABI puts the syscall number in $a7 ($r11) and arguments
// in $a0-$a5 ($r4-$r9); $a0 also receives the return value.
func createSyscallRegs(initRegs *arch.Registers, sysno uintptr, args ...arch.SyscallArgument) arch.Registers {
	// Copy initial registers (Era, $sp, callee-saved).
	regs := *initRegs

	regs.Regs[11] = uint64(sysno)
	if len(args) >= 1 {
		regs.Regs[4] = args[0].Uint64()
	}
	if len(args) >= 2 {
		regs.Regs[5] = args[1].Uint64()
	}
	if len(args) >= 3 {
		regs.Regs[6] = args[2].Uint64()
	}
	if len(args) >= 4 {
		regs.Regs[7] = args[3].Uint64()
	}
	if len(args) >= 5 {
		regs.Regs[8] = args[4].Uint64()
	}
	if len(args) >= 6 {
		regs.Regs[9] = args[5].Uint64()
	}

	return regs
}

// updateSyscallRegs updates registers after finishing sysemu.
func updateSyscallRegs(regs *arch.Registers) {
	// No special work is necessary.
}

// syscallReturnValue extracts a sensible return from registers.
func syscallReturnValue(regs *arch.Registers) (uintptr, error) {
	rval := int64(regs.Regs[4])
	if rval < 0 {
		return 0, unix.Errno(-rval)
	}
	return uintptr(rval), nil
}

func dumpRegs(regs *arch.Registers) string {
	var m strings.Builder

	fmt.Fprintf(&m, "Registers:\n")

	for i := 0; i < 32; i++ {
		fmt.Fprintf(&m, "\tRegs[%d]\t = %016x\n", i, regs.Regs[i])
	}
	fmt.Fprintf(&m, "\tOrigA0\t = %016x\n", regs.OrigA0)
	fmt.Fprintf(&m, "\tEra\t = %016x\n", regs.Era)
	fmt.Fprintf(&m, "\tBadv\t = %016x\n", regs.Badv)

	return m.String()
}

// adjustInitRegsRip rewinds Era to the start of the `syscall 0` instruction,
// so the next resume re-executes it with the rewritten number and arguments.
func (t *thread) adjustInitRegsRip() {
	t.initRegs.Era -= initRegsRipAdjustment
}

// initChildProcessPPID tells a freshly cloned stub what to do when it wakes.
//
// The expected PPID is not written here: createStub clones with CLONE_PARENT,
// so the child's parent is this thread's parent, and the value already in $s0
// is still the right one.
func initChildProcessPPID(initregs *arch.Registers, ppid int32) {
	initregs.Regs[24] = _NEW_STUB // $s1
}

func maybePatchSignalInfo(regs *arch.Registers, signalInfo *linux.SignalInfo) (patched bool) {
	// vsyscall emulation is not supported on LoongArch. No need to patch
	// anything.
	return false
}

// Noop on loong64.
//
//go:nosplit
func enableCpuidFault() {
}

// appendArchSeccompRules append architecture specific seccomp rules when creating BPF program.
// Ref attachedThread() for more detail.
func appendArchSeccompRules(rules []seccomp.RuleSet) []seccomp.RuleSet {
	return rules
}

// probeSeccomp returns true if seccomp is run after ptrace notifications,
// which is generally the case for kernel version >= 4.8.
//
// LoongArch support landed in Linux 5.19, well after that, so this is always
// true.
func probeSeccomp() bool {
	return true
}

func restoreArchSpecificState(ctx *sysmsg.ThreadContext, ac *arch.Context64) {
	ctx.TLS = uint64(ac.TLS())
}

// setArchSpecificRegs is a noop on loong64. The stub finds its own sysmsg from
// the stack pointer (sysmsg_sp() in sysmsg.h), so no register has to be seeded
// the way amd64 seeds gs_base.
func setArchSpecificRegs(sysThread *sysmsgThread, regs *arch.Registers) {
}

func retrieveArchSpecificState(ctx *sysmsg.ThreadContext, ac *arch.Context64) {
	if !ac.SetTLS(uintptr(ctx.TLS)) {
		panic(fmt.Sprintf("ac.SetTLS(%+v) failed", ctx.TLS))
	}
}

func sigErrorToAccessType(sigError uint64) hostarch.AccessType {
	at := hostarch.Read
	if sigError&faultWrite != 0 {
		at.Write = true
	}
	if sigError&faultInstr != 0 {
		at.Execute = true
	}
	return at
}
