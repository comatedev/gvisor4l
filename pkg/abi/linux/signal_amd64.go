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

//go:build amd64
// +build amd64

package linux

import (
	"gvisor.dev/gvisor/pkg/hostarch"
	"gvisor.dev/gvisor/pkg/marshal"
)

// SigActionABISize is the size of struct sigaction in application memory.
const SigActionABISize = 32

// CopyInABI reads a struct sigaction from an application's address space.
//
// amd64 defines __ARCH_HAS_SA_RESTORER, so struct sigaction carries an
// sa_restorer field and its layout is exactly SigAction's.
func (s *SigAction) CopyInABI(cc marshal.CopyContext, addr hostarch.Addr) (int, error) {
	return s.CopyIn(cc, addr)
}

// CopyOutABI writes a struct sigaction to an application's address space.
func (s *SigAction) CopyOutABI(cc marshal.CopyContext, addr hostarch.Addr) (int, error) {
	return s.CopyOut(cc, addr)
}
