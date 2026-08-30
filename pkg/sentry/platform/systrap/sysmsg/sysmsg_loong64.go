// Copyright 2024 The gVisor Authors.
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

package sysmsg

import (
	_ "embed"
	"fmt"
	"strings"

	"gvisor.dev/gvisor/pkg/cpuid"
)

// maxFPStateLen is smaller here than on the other architectures: ThreadContext
// has to fit in AllocatedSizeofThreadContextStruct, and loong64's
// user_regs_struct is 360 bytes against arm64's 272, which pushes the struct
// past 4096 at 3584. See MAX_FPSTATE_LEN in sysmsg_offsets.h, which must agree.
//
// The room is ample: what lives here is the ptrace regset layout the sentry
// keeps (fpu.State, 1856 bytes), which the stub converts to and from the
// signal frame's sc_extcontext record chain.
const maxFPStateLen uint32 = 2048

// SighandlerBlob contains the compiled code of the sysmsg signal handler.
//
//go:embed sighandler.built-in.loong64.bin
var SighandlerBlob []byte

// ArchState defines variables specific to the architecture being used.
type ArchState struct {
	fpLen uint32
}

// Init initializes the arch specific state.
func (s *ArchState) Init() {
	fs := cpuid.HostFeatureSet()
	fpLenUint, _ := fs.ExtendedStateSize()
	s.fpLen = uint32(fpLenUint)
}

// FpLen returns the FP state length for LoongArch.
func (s *ArchState) FpLen() int {
	return int(s.fpLen)
}

func (s *ArchState) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "sysmsg.ArchState{")
	fmt.Fprintf(&b, " fpLen %d", s.fpLen)
	b.WriteString(" }")

	return b.String()
}
