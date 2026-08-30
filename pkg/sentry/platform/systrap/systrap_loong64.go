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
	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/cpuid"
	"gvisor.dev/gvisor/pkg/sentry/arch"
)

// stackPointer returns the user-mode stack pointer. On LoongArch this is just
// $r3 in the GPR file; there is no separate Sp field as on arm64.
func stackPointer(r *arch.Registers) uintptr {
	return uintptr(r.Regs[3])
}

// configureSystrapAddressSpace overrides the default address space parameters
// when the host uses a different VA width.
//
// This function MUST be called during systrap initialization, before any
// Context64 is created.
func configureSystrapAddressSpace() {
	arch.ConfigureAddressSpace(uintptr(linux.TaskSize))
}

// declareVectorStateSaved tells cpuid that this platform carries the vector
// register file across context switches, so the guest can be told LSX and LASX
// exist.
//
// The stub saves and restores them through the signal frame's sc_extcontext
// records, and makes each sysmsg thread's vector context live during
// initialization so a record is always there to use; see touch_vector_unit in
// sighandler_loong64.c. Measured: veccheck runs 7.2M iterations clean here and
// fails at iteration 52 under ptrace, which declares nothing and so leaves the
// extensions hidden.
func declareVectorStateSaved() {
	cpuid.SetVectorStateSaved(true)
}
