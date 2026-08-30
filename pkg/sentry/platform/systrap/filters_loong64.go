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
	"gvisor.dev/gvisor/pkg/seccomp"
)

// archSyscallFilters returns architecture-specific syscalls made exclusively
// by the systrap platform.
//
// There are none on LoongArch. arm64 needs PTRACE_{GET,SET}REGSET on
// NT_ARM_TLS to move the thread pointer, but LoongArch keeps it in $tp ($r2),
// an ordinary general-purpose register that comes and goes with NT_PRSTATUS,
// which the shared filters already allow.
func archSyscallFilters() seccomp.SyscallRules {
	return seccomp.MakeSyscallRules(nil)
}

// hottestSyscalls returns the hottest syscalls used by the Systrap platform.
func hottestSyscalls() []uintptr {
	return nil
}
