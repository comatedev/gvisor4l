// Copyright 2018 The gVisor Authors.
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

// System call support for the VDSO.
//
// Provides fallback system call interfaces for getcpu()
// and clock_gettime().

#ifndef VDSO_SYSCALLS_H_
#define VDSO_SYSCALLS_H_

#include <asm/unistd.h>
#include <errno.h>
#include <fcntl.h>
#include <stddef.h>
#include <sys/types.h>

#define __stringify_1(x...) #x
#define __stringify(x...) __stringify_1(x)

namespace vdso {

#if __x86_64__

struct getcpu_cache;

static inline int sys_clock_gettime(clockid_t clock, struct timespec* ts) {
  int num = __NR_clock_gettime;
  asm volatile("syscall\n"
               : "+a"(num)
               : "D"(clock), "S"(ts)
               : "rcx", "r11", "memory");
  return num;
}

static inline int sys_getcpu(unsigned* cpu, unsigned* node,
                             struct getcpu_cache* cache) {
  int num = __NR_getcpu;
  asm volatile("syscall\n"
               : "+a"(num)
               : "D"(cpu), "S"(node), "d"(cache)
               : "rcx", "r11", "memory");
  return num;
}

static inline void sys_rt_sigreturn(void) {
  asm volatile("movl $" __stringify(__NR_rt_sigreturn)", %eax \n"
               "syscall \n");
}

#elif __aarch64__

static inline int sys_clock_gettime(clockid_t _clkid, struct timespec* _ts) {
  register struct timespec* ts asm("x1") = _ts;
  register clockid_t clkid asm("x0") = _clkid;
  register long ret asm("x0");
  register long nr asm("x8") = __NR_clock_gettime;

  asm volatile("svc #0\n"
               : "=r"(ret)
               : "r"(clkid), "r"(ts), "r"(nr)
               : "memory");
  return ret;
}

static inline int sys_clock_getres(clockid_t _clkid, struct timespec* _ts) {
  register struct timespec* ts asm("x1") = _ts;
  register clockid_t clkid asm("x0") = _clkid;
  register long ret asm("x0");
  register long nr asm("x8") = __NR_clock_getres;

  asm volatile("svc #0\n"
               : "=r"(ret)
               : "r"(clkid), "r"(ts), "r"(nr)
               : "memory");
  return ret;
}

static inline void sys_rt_sigreturn(void) {
  asm volatile("mov x8, #" __stringify(__NR_rt_sigreturn)" \n"
               "svc #0 \n");
}

#elif __loongarch64

struct getcpu_cache;

// LoongArch takes the syscall number in $a7 and arguments in $a0..$a5, and
// returns in $a0 (ELF psABI v2.01, Table 1). `syscall 0` is the trap.

static inline int sys_clock_gettime(clockid_t _clock, struct timespec* _ts) {
  register clockid_t clock asm("$a0") = _clock;
  register struct timespec* ts asm("$a1") = _ts;
  register long nr asm("$a7") = __NR_clock_gettime;
  register long ret asm("$a0");

  asm volatile("syscall 0\n"
               : "=r"(ret)
               : "r"(clock), "r"(ts), "r"(nr)
               : "memory");
  return ret;
}

static inline int sys_clock_getres(clockid_t _clock, struct timespec* _ts) {
  register clockid_t clock asm("$a0") = _clock;
  register struct timespec* ts asm("$a1") = _ts;
  register long nr asm("$a7") = __NR_clock_getres;
  register long ret asm("$a0");

  asm volatile("syscall 0\n"
               : "=r"(ret)
               : "r"(clock), "r"(ts), "r"(nr)
               : "memory");
  return ret;
}

static inline int sys_getcpu(unsigned* _cpu, unsigned* _node,
                             struct getcpu_cache* _cache) {
  register unsigned* cpu asm("$a0") = _cpu;
  register unsigned* node asm("$a1") = _node;
  register struct getcpu_cache* cache asm("$a2") = _cache;
  register long nr asm("$a7") = __NR_getcpu;
  register long ret asm("$a0");

  asm volatile("syscall 0\n"
               : "=r"(ret)
               : "r"(cpu), "r"(node), "r"(cache), "r"(nr)
               : "memory");
  return ret;
}

static inline void sys_rt_sigreturn(void) {
  asm volatile("li.d $a7, " __stringify(__NR_rt_sigreturn) "\n"
               "syscall 0\n");
}

#else
#error "unsupported architecture"
#endif
}  // namespace vdso

#endif  // VDSO_SYSCALLS_H_
