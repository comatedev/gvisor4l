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

package ptrace

import "gvisor.dev/gvisor/pkg/sentry/mm"

// declarePlatformQuirks tells the sentry that a lazy guest page fault is not
// safe here, so memory has to be populated when it is created instead.
//
// Measured with cowprobe, which forks and walks 256MB of copy-on-write pages in
// the child: one SIGBUS per ~1.6M faults under this platform, none at all under
// systrap over the same 1.6M. systrap handles the fault in the stub's own
// signal handler and rebuilds the register set from the ucontext, rather than
// letting the kernel retry the faulting instruction.
//
// It is expensive -- see mm.SetEagerFaultWorkaround -- which is one more reason
// to prefer systrap on this architecture.
func declarePlatformQuirks() {
	mm.SetEagerFaultWorkaround(true)
}
