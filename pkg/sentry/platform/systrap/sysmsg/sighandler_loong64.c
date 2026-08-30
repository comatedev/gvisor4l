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

#define _GNU_SOURCE
#include <asm/sigcontext.h>
#include <asm/unistd.h>
#include <errno.h>
#include <linux/audit.h>
#include <linux/futex.h>
#include <linux/unistd.h>
#include <signal.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <sys/prctl.h>
#include <sys/ucontext.h>

#include "atomic.h"
#include "sysmsg.h"
#include "sysmsg_offsets.h"

struct arch_state __export_arch_state;
uint64_t __export_stub_start;
// Syscall patching is amd64 only; this exists so the shared Go side has a
// symbol to write.
uint64_t __export_disable_syscall_patching;

// Bits of thread_context.err, which on LoongArch the stub synthesises rather
// than copying out of the signal frame; see __export_sighandler.
//
// LINT.IfChange
#define LOONG_FAULT_WRITE (1 << 0)
#define LOONG_FAULT_INSTR (1 << 1)
// LINT.ThenChange(../subprocess_loong64.go)

// LoongArch has no sa_restorer, so the handler cannot return -- see
// sigrestorer_loong64.S. It tail-calls this with the rt_sigframe base instead.
extern void __export_restore_rt(void *frame) __attribute__((noreturn));

long __syscall(long n, long a1, long a2, long a3, long a4, long a5, long a6) {
  // LoongArch passes the syscall number in $a7 and arguments in $a0-$a5, with
  // the result in $a0.
  register long a7 __asm__("$a7") = n;
  register long r0 __asm__("$a0") = a1;
  register long r1 __asm__("$a1") = a2;
  register long r2 __asm__("$a2") = a3;
  register long r3 __asm__("$a3") = a4;
  register long r4 __asm__("$a4") = a5;
  register long r5 __asm__("$a5") = a6;
  __asm__ __volatile__("syscall 0"
                       : "+r"(r0)
                       : "r"(a7), "r"(r1), "r"(r2), "r"(r3), "r"(r4), "r"(r5)
                       : "memory");
  return r0;
}

// The thread pointer is an ordinary register on LoongArch ($r2), not a system
// register as on arm64.
static __inline void set_tls(uint64_t tls) {
  __asm__ __volatile__("move $tp, %0" : : "r"(tls));
}

static __inline uint64_t get_tls(void) {
  uint64_t tls;
  __asm__ __volatile__("move %0, $tp" : "=r"(tls));
  return tls;
}

long sys_futex(uint32_t *addr, int op, int val, struct __kernel_timespec *tv,
               uint32_t *addr2, int val3) {
  return __syscall(__NR_futex, (long)addr, (long)op, (long)val, (long)tv,
                   (long)addr2, (long)val3);
}

static void gregs_to_ptregs(ucontext_t *ucontext,
                            struct user_regs_struct *ptregs) {
  for (int i = 0; i < 32; i++) {
    ptregs->regs[i] = ucontext->uc_mcontext.__gregs[i];
  }
  ptregs->csr_era = ucontext->uc_mcontext.__pc;
}

static void ptregs_to_gregs(ucontext_t *ucontext,
                            struct user_regs_struct *ptregs) {
  for (int i = 0; i < 32; i++) {
    ucontext->uc_mcontext.__gregs[i] = ptregs->regs[i];
  }
  ucontext->uc_mcontext.__pc = ptregs->csr_era;
}

// Floating point and vector state reaches the handler as a chain of sctx_info
// records in sc_extcontext -- FPU, then LSX or LASX, then LBT, terminated by a
// record with magic zero. Measured on a 3A5000 the first record is
// LASX_CTX_MAGIC at 1056 bytes, so the whole chain is well under
// MAX_FPSTATE_LEN.
//
// Carrying the chain verbatim is what lets systrap preserve vector state at
// all: the ptrace platform moves FP state with PTRACE_GETREGSET and loses the
// upper 192 bits of every LASX register, whereas rewriting these records was
// measured to take effect on all four lanes.
//
// Returns the total length including the terminating record, or 0 if the chain
// is malformed.
static uint32_t extctx_len(const uint8_t *base) {
  uint32_t off = 0;
  for (;;) {
    const struct sctx_info *h = (const struct sctx_info *)(base + off);
    if (off + sizeof(*h) > MAX_FPSTATE_LEN) return 0;
    if (h->magic == 0) return off + (uint32_t)sizeof(*h);
    if (h->size < sizeof(*h)) return 0;
    if (off + h->size > MAX_FPSTATE_LEN) return 0;
    off += h->size;
  }
}

void __export_start(struct sysmsg *sysmsg, void *_ucontext) {
  panic(0x11111111, 0);
}

void __export_sighandler(int signo, siginfo_t *siginfo, void *_ucontext) {
  ucontext_t *ucontext = _ucontext;
  void *sp = sysmsg_sp();
  struct sysmsg *sysmsg = sysmsg_addr(sp);

  if (sysmsg != sysmsg->self) panic(STUB_ERROR_BAD_SYSMSG, 0);
  int32_t thread_state = atomic_load(&sysmsg->state);

  uint32_t ctx_state = CONTEXT_STATE_INVALID;
  struct thread_context *ctx = NULL, *old_ctx = NULL;
  if (thread_state == THREAD_STATE_INITIALIZING) {
    init_new_thread();
    goto init;
  }

  ctx = sysmsg->context;
  old_ctx = sysmsg->context;

  ctx->signo = signo;
  gregs_to_ptregs(ucontext, &ctx->ptregs);

  {
    uint8_t *extctx = (uint8_t *)ucontext->uc_mcontext.__extcontext;
    uint32_t len = extctx_len(extctx);
    if (len == 0) {
      // Read the magic through memcpy: __extcontext is declared as an array of
      // unsigned long long, and casting it to uint32_t* trips strict aliasing.
      uint32_t magic;
      memcpy((uint8_t *)&magic, extctx, sizeof(magic));
      panic(STUB_ERROR_FPSTATE_BAD_HEADER, magic);
    }
    memcpy(ctx->fpstate, extctx, len);
  }

  ctx->tls = get_tls();
  ctx->siginfo = *siginfo;

  // LoongArch reports no fault status register in the signal frame, so there
  // is no equivalent of arm64's esr_context to read an access type from.
  // Synthesise the two bits the sentry needs from what the frame does carry.
  //
  // The write bit is not a guess about the instruction: SEGV_ACCERR says the
  // mapping was present and the access was refused, and every host mapping
  // the sentry installs is at least readable, so a refused access is a store
  // to a page mapped read-only. A store to an address with no host mapping
  // yet reports SEGV_MAPERR and is handled as a read; the sentry then maps
  // the page read-only and the retried store takes the SEGV_ACCERR path, so
  // it costs one extra fault rather than looping.
  ctx->err = 0;
  if (signo == SIGSEGV || signo == SIGBUS) {
    if (siginfo->si_code == SEGV_ACCERR) ctx->err |= LOONG_FAULT_WRITE;
    if ((uint64_t)siginfo->si_addr == ucontext->uc_mcontext.__pc)
      ctx->err |= LOONG_FAULT_INSTR;
  }

  switch (signo) {
    case SIGSYS: {
      ctx_state = CONTEXT_STATE_SYSCALL;
      if (siginfo->si_arch != AUDIT_ARCH_LOONGARCH64) {
        // Push the syscall number out of range so it returns ENOSYS, the same
        // trick the other architectures use for foreign personalities.
        ctx->ptregs.regs[11] += 0x86000000;
      }
      break;
    }
    case SIGSEGV:
    case SIGBUS:
    case SIGCHLD:
    case SIGFPE:
    case SIGTRAP:
    case SIGILL:
      ctx_state = CONTEXT_STATE_FAULT;
      break;
    default:
      // Nothing to report; hand the thread straight back.
      __export_restore_rt(siginfo);
  }

init:
  for (;;) {
    ctx = switch_context(sysmsg, ctx, ctx_state);

    if (atomic_load(&ctx->interrupt) != 0) {
      atomic_store(&ctx->interrupt, 0);
      ctx_state = CONTEXT_STATE_FAULT;
      ctx->signo = SIGCHLD;
      ctx->siginfo.si_signo = SIGCHLD;
    } else {
      break;
    }
  }

  if (old_ctx != ctx || ctx->last_thread_id != sysmsg->thread_id) {
    ctx->fpstate_changed = 1;
  }
  restore_state(sysmsg, ctx, _ucontext);
  __export_restore_rt(siginfo);
}

void restore_state(struct sysmsg *sysmsg, struct thread_context *ctx,
                   void *_ucontext) {
  ucontext_t *ucontext = _ucontext;

  if (atomic_load(&ctx->fpstate_changed)) {
    uint8_t *extctx = (uint8_t *)ucontext->uc_mcontext.__extcontext;
    uint32_t saved = extctx_len(ctx->fpstate);
    uint32_t frame = extctx_len(extctx);
    // The kernel emits the same chain shape for every frame on a given CPU, so
    // a mismatch means the saved state does not belong to this frame. Writing
    // it anyway would either overrun the frame or leave a malformed chain, so
    // say so instead of guessing.
    if (saved == 0 || saved != frame) {
      panic(STUB_ERROR_FPSTATE_BAD_HEADER, saved);
    }
    memcpy(extctx, ctx->fpstate, saved);
  }
  ptregs_to_gregs(ucontext, &ctx->ptregs);
  set_tls(ctx->tls);
  atomic_store(&sysmsg->state, THREAD_STATE_NONE);
}
