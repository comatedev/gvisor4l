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

// spinloop pauses the pipeline inside a busy wait. LoongArch has no dedicated
// hint instruction, so use the same barrier the stub's C code uses (dbar 0).
TEXT ·spinloop(SB),NOSPLIT,$0
	DBAR	$0
	RET

// cputicks reads the constant frequency timer, which is what the stub reads
// too (rdtime.d in sysmsg_lib.c), so the two are directly comparable.
TEXT ·cputicks(SB),NOSPLIT,$0-8
	RDTIMED	R0, R4
	MOVV	R4, ret+0(FP)
	RET
