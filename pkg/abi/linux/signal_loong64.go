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

package linux

import (
	"structs"

	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/marshal"
)

// SigActionABISize is the size of struct sigaction in application memory.
const SigActionABISize = 24

// SigActionABI is struct sigaction as LoongArch64 applications see it.
//
// LoongArch does not define __ARCH_HAS_SA_RESTORER, so the generic
// include/uapi/asm-generic/signal.h layout applies without the sa_restorer
// field: sa_mask sits at offset 16 and the whole structure is 24 bytes.
// Signal handlers always return through the vdso's rt_sigreturn trampoline.
//
// SigAction cannot be marshalled to an application directly, then. Doing so
// reads sa_mask from offset 24 -- so the application's mask is silently
// ignored -- and writes 8 bytes past the end of an oldact buffer.
//
// +marshal
type SigActionABI struct {
	_       structs.HostLayout
	Handler uint64
	Flags   uint64
	Mask    SignalSet
}

// CopyInABI reads a struct sigaction from an application's address space.
//
// SA_RESTORER is cleared: since the field does not exist, the LoongArch kernel
// does not keep the flag either, and an application that sets it reads back a
// value without it. Keeping it would be worse than cosmetic, because
// deliverSignalToHandler only substitutes the vdso trampoline when SA_RESTORER
// is clear -- a set bit leaves the handler's return address at zero.
func (s *SigAction) CopyInABI(cc marshal.CopyContext, addr hostarch.Addr) (int, error) {
	var abi SigActionABI
	n, err := abi.CopyIn(cc, addr)
	if err != nil {
		return n, err
	}
	s.Handler = abi.Handler
	s.Flags = abi.Flags &^ SA_RESTORER
	s.Restorer = 0
	s.Mask = abi.Mask
	return n, nil
}

// CopyOutABI writes a struct sigaction to an application's address space.
func (s *SigAction) CopyOutABI(cc marshal.CopyContext, addr hostarch.Addr) (int, error) {
	abi := SigActionABI{
		Handler: s.Handler,
		Flags:   s.Flags,
		Mask:    s.Mask,
	}
	return abi.CopyOut(cc, addr)
}
