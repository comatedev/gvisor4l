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
	"gvisor.dev/gvisor/pkg/seccomp"
)

func appendSysThreadArchSeccompRules(rules []seccomp.RuleSet) []seccomp.RuleSet {
	return rules
}

// hostSigAction is struct sigaction in the shape the host kernel expects.
//
// LoongArch does not define __ARCH_HAS_SA_RESTORER, so struct sigaction has no
// sa_restorer field: sa_mask sits at offset 16 and the whole structure is 24
// bytes. Handing the kernel a linux.SigAction instead would make it read
// sa_mask out of the restorer field, blocking an arbitrary set of signals in
// every stub thread.
//
// LINT.IfChange
type hostSigAction = linux.SigActionABI

// newHostSigAction builds the sigaction that sysmsgSigactions installs.
//
// restorer is ignored, and SA_RESTORER is not set: the LoongArch handler never
// returns to the kernel's restorer. sighandler_loong64.c tail-calls
// __export_restore_rt, which issues rt_sigreturn itself, because the stub has
// no vdso to return through.
//
//go:nosplit
func newHostSigAction(handler, restorer uint64, mask linux.SignalSet) hostSigAction {
	return linux.SigActionABI{
		Handler: handler,
		Flags:   linux.SA_ONSTACK | linux.SA_SIGINFO,
		Mask:    mask,
	}
}

// LINT.ThenChange(sysmsg_thread_amd64.go, sysmsg_thread_arm64.go)
