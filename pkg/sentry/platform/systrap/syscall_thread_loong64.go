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
	"runtime"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/hostsyscall"
	"gvisor.dev/gvisor/pkg/sentry/arch"
)

func (t *syscallThread) detach() {
	p := t.thread

	// The syscall thread can't handle any signals and doesn't expect to
	// receive anything.
	t.maskAllSignalsAttached()

	// See the register assignment comment in stub_loong64.s.
	regs := p.initRegs
	regs.Regs[3] = 0 // $sp: the loop runs without a stack.
	regs.Regs[25] = uint64(t.stubAddr)
	// Sign extended, not zero extended: the stub loads the state word with
	// ld.w and advances its copy with addi.w, both of which sign extend, so
	// the two stop comparing equal once the counter passes 2^31 unless this
	// side matches. (arm64 sidesteps this by comparing with CMPW; LoongArch
	// has no 32-bit compare-and-branch.)
	regs.Regs[26] = uint64(int64(int32(t.sentryMessage.state + 1)))
	regs.Regs[27] = uint64(t.stubAddr + syscallStubMessageOffset)
	if t.seccompNotify != nil {
		regs.Regs[24] = _RUN_SECCOMP_LOOP
	} else {
		regs.Regs[24] = _RUN_SYSCALL_LOOP
	}
	// Skip the syscall instruction.
	regs.Era += arch.SyscallWidth
	if err := p.setRegs(&regs); err != nil {
		panic(fmt.Sprintf("ptrace set regs failed: %v", err))
	}
	p.detach()
	if e := hostsyscall.RawSyscallErrno(unix.SYS_TGKILL, uintptr(p.tgid), uintptr(p.tid), uintptr(unix.SIGCONT)); e != 0 {
		panic(fmt.Sprintf("tkill failed: %v", e))
	}
	runtime.UnlockOSThread()

	if t.seccompNotify != nil {
		if err := t.waitForSeccompNotify(); err != nil {
			panic(fmt.Sprintf("%s", err))
		}
	}
}
