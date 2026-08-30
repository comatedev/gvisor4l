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

package mm

import "sync/atomic"

// eagerFaultWorkaround makes the MemoryManager commit and map memory when it is
// created rather than faulting it in on first access.
//
// It exists for one platform: on LoongArch under ptrace, a guest page fault
// comes back having damaged the running program. cowprobe, which forks and then
// walks 256MB of copy-on-write pages in the child, kills the child with SIGBUS
// about once per 1.6M faults there. The same binary and the same probe under
// systrap take none, because systrap handles the fault in the stub's own signal
// handler and rebuilds the register set from the ucontext rather than letting
// the kernel retry the faulting instruction.
//
// This is off by default, and should stay off wherever the platform's fault
// path is sound: it defeats copy-on-write, commits every mapping at mmap(2)
// time whatever its size, and costs about 12x on fork(2) and 7x on
// fork+execve.
var eagerFaultWorkaround atomic.Bool

// SetEagerFaultWorkaround records that this platform cannot take a lazy guest
// page fault without corrupting the application.
//
// A platform that needs it MUST call this during initialization, before any
// guest runs.
func SetEagerFaultWorkaround(v bool) {
	eagerFaultWorkaround.Store(v)
}

// EagerFaultWorkaround reports whether the platform in use needs memory to be
// populated eagerly. See SetEagerFaultWorkaround.
func EagerFaultWorkaround() bool {
	return eagerFaultWorkaround.Load()
}

// futexGlobalBarrier makes MemoryManager.MemoryBarrier issue a host global
// memory barrier, which futex.WaitPrepare asks for before it puts a task to
// sleep.
//
// It exists for the same platform as eagerFaultWorkaround and for a related
// reason: the value a futex sleeps on was written by a peer stub process, and
// under ptrace the sentry reads that memory without the ordering a native
// futex_wake would have provided on the writing CPU.
//
// It is off by default. The barrier is membarrier(MEMBARRIER_CMD_GLOBAL),
// which the kernel implements as synchronize_rcu(), so it waits out a
// system-wide RCU grace period -- 15.6ms on an idle machine, measured. That is
// per futex sleep.
var futexGlobalBarrier atomic.Bool

// SetFutexGlobalBarrier records that this platform needs a global memory
// barrier before a task sleeps on a futex.
//
// A platform that needs it MUST call this during initialization, before any
// guest runs.
func SetFutexGlobalBarrier(v bool) {
	futexGlobalBarrier.Store(v)
}
