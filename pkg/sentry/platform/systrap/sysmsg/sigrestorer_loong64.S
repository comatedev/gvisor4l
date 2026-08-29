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

#include <asm/unistd.h>

// LoongArch has no sa_restorer: struct sigaction is 24 bytes with no such
// field, and the kernel always points $ra at the vdso's rt_sigreturn. The stub
// munmaps everything above stubROMapEnd, the vdso included, so returning from
// the handler jumps into an unmapped page. Measured on a 3A5000: a
// normally-returning handler dies with SIGSEGV once the vdso is gone, while a
// handler that issues rt_sigreturn itself returns fine.
//
// So this is not reached through SA_RESTORER. __export_sighandler tail-calls it
// with the rt_sigframe base in $a0, which is the siginfo pointer the kernel
// passed in $a1 -- the frame is {siginfo, ucontext} and rt_sigreturn wants $sp
// at its base.
.global __export_restore_rt;
.type __export_restore_rt, @function;
__export_restore_rt:
  move    $sp, $a0
  li.d    $a7, __NR_rt_sigreturn
  syscall 0
  break   0                     // rt_sigreturn does not return
.size __export_restore_rt,.-__export_restore_rt
