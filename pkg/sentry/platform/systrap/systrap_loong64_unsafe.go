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
	"gvisor.dev/gvisor/pkg/sentry/arch"
)

// getTLS gets the thread local storage register.
//
// Unlike arm64, which reaches TPIDR_EL0 through the NT_ARM_TLS regset, the
// LoongArch thread pointer is $tp ($r2) -- an ordinary general-purpose
// register that arrives with NT_PRSTATUS. So this is a plain register read
// and needs no extra ptrace request (and no extra seccomp rule; see
// filters_loong64.go).
func (t *thread) getTLS(tls *uint64) error {
	var regs arch.Registers
	if err := t.getRegs(&regs); err != nil {
		return err
	}
	*tls = regs.Regs[2]
	return nil
}

// setTLS sets the thread local storage register.
func (t *thread) setTLS(tls *uint64) error {
	var regs arch.Registers
	if err := t.getRegs(&regs); err != nil {
		return err
	}
	regs.Regs[2] = *tls
	return t.setRegs(&regs)
}
