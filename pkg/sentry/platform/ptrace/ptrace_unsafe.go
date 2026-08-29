// Copyright 2018 The gVisor Authors.
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

package ptrace

import (
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/sentry/arch"
	"gvisor.dev/gvisor/pkg/sentry/arch/fpu"
)

// getRegs gets the general purpose register set.
func (t *thread) getRegs(regs *arch.Registers) error {
	iovec := unix.Iovec{
		Base: (*byte)(unsafe.Pointer(regs)),
		Len:  uint64(unsafe.Sizeof(*regs)),
	}
	_, _, errno := unix.RawSyscall6(
		unix.SYS_PTRACE,
		unix.PTRACE_GETREGSET,
		uintptr(t.tid),
		linux.NT_PRSTATUS,
		uintptr(unsafe.Pointer(&iovec)),
		0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// setRegs sets the general purpose register set.
func (t *thread) setRegs(regs *arch.Registers) error {
	iovec := unix.Iovec{
		Base: (*byte)(unsafe.Pointer(regs)),
		Len:  uint64(unsafe.Sizeof(*regs)),
	}
	_, _, errno := unix.RawSyscall6(
		unix.SYS_PTRACE,
		unix.PTRACE_SETREGSET,
		uintptr(t.tid),
		linux.NT_PRSTATUS,
		uintptr(unsafe.Pointer(&iovec)),
		0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// fpRegSetSpec describes one ptrace regset making up a thread's saved
// floating-point state, and where it sits in the fpu.State buffer.
//
// Most architectures expose the whole thing as a single regset. LoongArch
// splits it: NT_PRFPREG carries only the low 64 bits of each vector register,
// and the rest lives in NT_LOONGARCH_LSX/LASX with NT_LOONGARCH_LBT alongside.
type fpRegSetSpec struct {
	note   uintptr
	offset int
	length int

	// optional regsets are skipped when the host does not implement them,
	// rather than failing the transfer.
	optional bool
}

func (t *thread) transferFPRegs(fpState *fpu.State, ac *archContext, req uintptr) error {
	for _, rs := range ac.fpRegSets() {
		if rs.length == 0 {
			continue
		}
		iovec := unix.Iovec{
			Base: &(*fpState)[rs.offset],
			Len:  uint64(rs.length),
		}
		_, _, errno := unix.RawSyscall6(
			unix.SYS_PTRACE,
			req,
			uintptr(t.tid),
			rs.note,
			uintptr(unsafe.Pointer(&iovec)),
			0, 0)
		// The kernel writes back how much it actually transferred; a short
		// count is the difference between "succeeded" and "did what we asked".
		if uint64(rs.length) != iovec.Len && debugShortCount.Add(1) <= 20 {
			log.Warningf("ptrace regset %#x req %#x: asked %d bytes, kernel moved %d",
				rs.note, req, rs.length, iovec.Len)
		}
		if errno != 0 {
			if rs.optional {
				log.Warningf("ptrace regset %#x (%d bytes at +%d) req %#x failed: %v",
					rs.note, rs.length, rs.offset, req, errno)
				continue
			}
			return errno
		}
		debugVerifyRegSet(t, fpState, rs, req)
	}
	return nil
}

// debugVerifyRegSet reads a regset straight back after writing it and reports
// any difference. The vector state is still being lost even though every
// transfer reports success, so this distinguishes 'the kernel did not take our
// write' from 'something else drops it later'. Debug aid; rate limited.
var debugVerifyCount atomic.Int64
var debugShortCount atomic.Int64

func debugVerifyRegSet(t *thread, fpState *fpu.State, rs fpRegSetSpec, req uintptr) {
	if req != unix.PTRACE_SETREGSET || rs.note != linux.NT_LOONGARCH_LASX {
		return
	}
	if n := debugVerifyCount.Add(1); n > 40 {
		return
	}
	var back [64]byte
	iovec := unix.Iovec{Base: &back[0], Len: uint64(len(back))}
	if _, _, errno := unix.RawSyscall6(
		unix.SYS_PTRACE, unix.PTRACE_GETREGSET, uintptr(t.tid), rs.note,
		uintptr(unsafe.Pointer(&iovec)), 0, 0); errno != 0 {
		log.Warningf("LASX readback failed: %v", errno)
		return
	}
	wrote := (*fpState)[rs.offset : rs.offset+len(back)]
	same := true
	for i := range back {
		if back[i] != wrote[i] {
			same = false
			break
		}
	}
	log.Warningf("LASX set/readback tid=%d match=%v wrote=%x back=%x",
		t.tid, same, wrote[:32], back[:32])
}

// getFPRegs gets the floating-point data via the GETREGSET ptrace unix.
func (t *thread) getFPRegs(fpState *fpu.State, ac *archContext) error {
	return t.transferFPRegs(fpState, ac, unix.PTRACE_GETREGSET)
}

// setFPRegs sets the floating-point data via the SETREGSET ptrace unix.
func (t *thread) setFPRegs(fpState *fpu.State, ac *archContext) error {
	return t.transferFPRegs(fpState, ac, unix.PTRACE_SETREGSET)
}

// getSignalInfo retrieves information about the signal that caused the stop.
func (t *thread) getSignalInfo(si *linux.SignalInfo) error {
	_, _, errno := unix.RawSyscall6(
		unix.SYS_PTRACE,
		unix.PTRACE_GETSIGINFO,
		uintptr(t.tid),
		0,
		uintptr(unsafe.Pointer(si)),
		0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// clone creates a new thread from this one.
//
// The returned thread will be stopped and available for any system thread to
// call attach on it.
//
// Precondition: the OS thread must be locked and own t.
func (t *thread) clone() (*thread, error) {
	r, ok := hostarch.Addr(stackPointer(&t.initRegs)).RoundUp()
	if !ok {
		return nil, unix.EINVAL
	}
	rval, err := t.syscallIgnoreInterrupt(
		&t.initRegs,
		unix.SYS_CLONE,
		arch.SyscallArgument{Value: uintptr(
			unix.CLONE_FILES |
				unix.CLONE_FS |
				unix.CLONE_SIGHAND |
				unix.CLONE_THREAD |
				unix.CLONE_PTRACE |
				unix.CLONE_VM)},
		// The stack pointer is just made up, but we have it be
		// something sensible so the kernel doesn't think we're
		// up to no good. Which we are.
		arch.SyscallArgument{Value: uintptr(r)},
		arch.SyscallArgument{},
		arch.SyscallArgument{},
		// We use these registers initially, but really they
		// could be anything. We're going to stop immediately.
		arch.SyscallArgument{Value: uintptr(unsafe.Pointer(&t.initRegs))})
	if err != nil {
		return nil, err
	}

	return &thread{
		tgid: t.tgid,
		tid:  int32(rval),
		cpu:  ^uint32(0),
	}, nil
}

// getEventMessage retrieves a message about the ptrace event that just happened.
func (t *thread) getEventMessage() (uintptr, error) {
	var msg uintptr
	_, _, errno := unix.RawSyscall6(
		unix.SYS_PTRACE,
		unix.PTRACE_GETEVENTMSG,
		uintptr(t.tid),
		0,
		uintptr(unsafe.Pointer(&msg)),
		0, 0)
	if errno != 0 {
		return msg, errno
	}
	return msg, nil
}
