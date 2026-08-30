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

#include "funcdata.h"
#include "textflag.h"

// LoongArch uses the asm-generic syscall table.
#define SYS_GETPID	 172 // +checkconst unix SYS_GETPID
#define SYS_EXIT	 93  // +checkconst unix SYS_EXIT
#define SYS_KILL	 129 // +checkconst unix SYS_KILL
#define SYS_GETPPID	 173 // +checkconst unix SYS_GETPPID
#define SIGKILL		 9   // +checkconst unix SIGKILL
#define SIGSTOP		 19  // +checkconst unix SIGSTOP
#define SYS_PRCTL	 167 // +checkconst unix SYS_PRCTL
#define SYS_EXIT_GROUP	 94  // +checkconst unix SYS_EXIT_GROUP
#define PR_SET_PDEATHSIG 1   // +checkconst unix PR_SET_PDEATHSIG

#define SYS_FUTEX	 98 // +checkconst unix SYS_FUTEX
#define FUTEX_WAKE	 1  // +checkconst linux FUTEX_WAKE
#define FUTEX_WAIT	 0  // +checkconst linux FUTEX_WAIT

#define NEW_STUB	 1 // +checkconst . _NEW_STUB
#define RUN_SYSCALL_LOOP 5 // +checkconst . _RUN_SYSCALL_LOOP
#define RUN_SECCOMP_LOOP 6 // +checkconst . _RUN_SECCOMP_LOOP

// syscallSentryMessage offsets.
#define SENTRY_MESSAGE_STATE 0  // +checkoffset . syscallSentryMessage.state
#define SENTRY_MESSAGE_SYSNO 8  // +checkoffset . syscallSentryMessage.sysno
#define SENTRY_MESSAGE_ARGS  16 // +checkoffset . syscallSentryMessage.args
#define SENTRY_MESSAGE_ARG0  (SENTRY_MESSAGE_ARGS + 0*8)
#define SENTRY_MESSAGE_ARG1  (SENTRY_MESSAGE_ARGS + 1*8)
#define SENTRY_MESSAGE_ARG2  (SENTRY_MESSAGE_ARGS + 2*8)
#define SENTRY_MESSAGE_ARG3  (SENTRY_MESSAGE_ARGS + 3*8)
#define SENTRY_MESSAGE_ARG4  (SENTRY_MESSAGE_ARGS + 4*8)
#define SENTRY_MESSAGE_ARG5  (SENTRY_MESSAGE_ARGS + 5*8)

#define STUB_MESSAGE_RET     0 // +checkoffset . syscallStubMessage.ret

// initStubProcess bootstraps the child and sends itself SIGSTOP to wait for
// attach.
//
// Register assignment. The LoongArch kernel clobbers $a0 (the return value)
// and, as this port measured, all of $t0-$t8 across a syscall, so everything
// that has to outlive one lives in $s0-$s4:
//
//	$a7  = R11 : syscall number
//	$a0-$a5 = R4-R9 : syscall arguments, $a0 also the return value
//	$s0  = R23 : expected PPID
//	$s1  = R24 : what to do after the SIGSTOP (_NEW_STUB, _RUN_SYSCALL_LOOP,
//	             _RUN_SECCOMP_LOOP); written by the sentry between stops
//	$s2  = R25 : &syscallSentryMessage, which is also the futex word
//	$s3  = R26 : the sentry message state this stub is waiting for
//	$s4  = R27 : &syscallStubMessage
//
// R22 is Go's g and R30 is the assembler's scratch register, so neither may
// be used to hold state; R21 is reserved by the LoongArch ABI.
//
// This should not be used outside the context of a new ptrace child (as the
// function is otherwise a bunch of nonsense).
TEXT ·initStubProcess(SB),NOSPLIT,$0
begin:
	// N.B. This loop only executes in the context of a single-threaded
	// fork child.

	// prctl(PR_SET_PDEATHSIG, SIGKILL)
	MOVV	$SYS_PRCTL, R11
	MOVV	$PR_SET_PDEATHSIG, R4
	MOVV	$SIGKILL, R5
	SYSCALL
	BLT	R4, R0, error

	// If the parent already died before we called PR_SET_PDEATHSIG then
	// we'll have an unexpected PPID.
	MOVV	$SYS_GETPPID, R11
	SYSCALL
	BNE	R4, R23, parent_dead

	// getpid(), so the kill() below can address us.
	MOVV	$SYS_GETPID, R11
	SYSCALL
	BLT	R4, R0, error

	MOVV	$0, R24

	// SIGSTOP to wait for attach.
	//
	// The SYSCALL instruction will be used for future syscall injection by
	// thread.syscall.
	MOVV	$SYS_KILL, R11
	MOVV	$SIGSTOP, R5
	SYSCALL

	// The sentry sets $s1 before resuming us to say what comes next.
	MOVV	$NEW_STUB, R12
	BEQ	R24, R12, clone

	MOVV	$RUN_SYSCALL_LOOP, R12
	BEQ	R24, R12, syscall_loop

	MOVV	$RUN_SECCOMP_LOOP, R12
	BEQ	R24, R12, seccomp_loop
done:
	// Notify the Sentry that syscall exited.
	BREAK	$3
	JMP	done // Be paranoid.
clone:
	// subprocess.createStub clones a new stub process that is untraced,
	// thus executing this code. We setup the PDEATHSIG before SIGSTOPing
	// ourselves for attach by the tracer.
	//
	// $s0 still holds the expected PPID: createStub clones with
	// CLONE_PARENT, so the child's parent is this thread's parent.
	BEQ	R4, R0, begin

	// The clone system call returned a non-zero value.
	JMP	done

error:
	// Exit with -errno.
	SUBVU	R4, R0, R4
	MOVV	$SYS_EXIT, R11
	SYSCALL
	BREAK	$0

parent_dead:
	MOVV	$SYS_EXIT, R11
	MOVV	$1, R4
	SYSCALL
	BREAK	$0

	// syscall_loop handles requests from the Sentry to execute syscalls.
	// Look at syscall_thread for more details.
	//
	// syscall_loop is running without using the stack because it can be
	// compromised by sysmsg (guest) threads that run in the same address
	// space.
syscall_loop:
	// while (sentryMessage->state != $s3) {
	// 	futex(&sentryMessage->state, FUTEX_WAIT, state, NULL, NULL, 0);
	// }
	//
	// MOVW sign extends, and so does the ADDW that advances $s3, so the
	// two stay comparable as 64-bit values once the state passes 2^31.
	MOVW	SENTRY_MESSAGE_STATE(R25), R12
	BEQ	R12, R26, execute_syscall

	// Every argument is reloaded on each pass: $a0 comes back holding the
	// futex return value, so nothing set up before the branch survives.
	MOVV	R25, R4
	MOVV	$FUTEX_WAIT, R5
	MOVV	R12, R6
	MOVV	$0, R7
	MOVV	$0, R8
	MOVV	$0, R9
	MOVV	$SYS_FUTEX, R11
	SYSCALL
	JMP	syscall_loop

execute_syscall:
	MOVV	SENTRY_MESSAGE_SYSNO(R25), R11
	MOVV	SENTRY_MESSAGE_ARG0(R25), R4
	MOVV	SENTRY_MESSAGE_ARG1(R25), R5
	MOVV	SENTRY_MESSAGE_ARG2(R25), R6
	MOVV	SENTRY_MESSAGE_ARG3(R25), R7
	MOVV	SENTRY_MESSAGE_ARG4(R25), R8
	MOVV	SENTRY_MESSAGE_ARG5(R25), R9
	SYSCALL

	// stubMessage->ret = ret
	MOVV	R4, STUB_MESSAGE_RET(R27)

	// for {
	//   if futex(&sentryMessage->state, FUTEX_WAKE, 1) == 1 {
	//     break;
	//   }
	// }
wake_up_sentry:
	MOVV	R25, R4
	MOVV	$FUTEX_WAKE, R5
	MOVV	$1, R6
	MOVV	$0, R7
	MOVV	$0, R8
	MOVV	$0, R9
	MOVV	$SYS_FUTEX, R11
	SYSCALL

	// futex returns the number of waiters that were woken up.  If futex
	// returns 0 here, it means that the Sentry has not called futex_wait
	// yet and we need to try again. The value of sentryMessage->state
	// isn't changed, so futex_wake is the only way to wake up the Sentry.
	MOVV	$1, R12
	BNE	R4, R12, wake_up_sentry

	ADDW	$1, R26, R26
	JMP	syscall_loop
seccomp_loop:
	// SYS_EXIT_GROUP triggers seccomp notifications.
	MOVV	$SYS_EXIT_GROUP, R11
	SYSCALL

	MOVV	SENTRY_MESSAGE_SYSNO(R25), R11
	MOVV	SENTRY_MESSAGE_ARG0(R25), R4
	MOVV	SENTRY_MESSAGE_ARG1(R25), R5
	MOVV	SENTRY_MESSAGE_ARG2(R25), R6
	MOVV	SENTRY_MESSAGE_ARG3(R25), R7
	MOVV	SENTRY_MESSAGE_ARG4(R25), R8
	MOVV	SENTRY_MESSAGE_ARG5(R25), R9
	SYSCALL

	// stubMessage->ret = ret
	MOVV	R4, STUB_MESSAGE_RET(R27)
	JMP	seccomp_loop

// func addrOfInitStubProcess() uintptr
TEXT ·addrOfInitStubProcess(SB), $0-8
	MOVV	$·initStubProcess(SB), R4
	MOVV	R4, ret+0(FP)
	RET

// stubCall calls the stub function at the given address with the given PPID.
//
// This is a distinct function because stub, above, may be mapped at any
// arbitrary location, and stub has a specific binary API (see above).
TEXT ·stubCall(SB),NOSPLIT,$0-16
	MOVV	addr+0(FP), R12
	MOVV	pid+8(FP), R23
	JMP	(R12)
