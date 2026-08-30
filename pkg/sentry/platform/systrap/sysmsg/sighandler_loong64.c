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
#include <asm/ptrace.h>
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

// The sentry keeps floating-point state in the layout of the ptrace regsets
// laid end to end -- see pkg/sentry/arch/fpu/fpu_loong64.go -- because that is
// what the ptrace platform moves with PTRACE_GETREGSET and what the rest of
// the sentry reads. The signal frame carries the same state in a different
// shape: a chain of sc_extcontext records. The stub converts between the two,
// so a thread_context describes FP state identically no matter which platform
// filled it.
//
// LINT.IfChange
#define FPSTATE_FPREGS_OFFSET 0
#define FPSTATE_LSX_OFFSET    272
#define FPSTATE_LASX_OFFSET   784
#define FPSTATE_LBT_OFFSET    1808
#define FPSTATE_LEN           1856
// LINT.ThenChange(../../../arch/fpu/fpu_loong64.go)

// The regset and sigcontext forms of the base FP file are the same bytes in
// the same order, which is what makes the FPU case below a single memcpy.
_Static_assert(sizeof(struct user_fp_state) == sizeof(struct fpu_context),
               "user_fp_state and fpu_context must agree");
_Static_assert(sizeof(struct user_fp_state) == 272, "");
_Static_assert(sizeof(struct user_lsx_state) == 512, "");
_Static_assert(sizeof(struct user_lasx_state) == 1024, "");
_Static_assert(sizeof(struct user_lbt_state) == sizeof(struct lbt_context),
               "user_lbt_state and lbt_context must agree");
_Static_assert(sizeof(struct user_lbt_state) == 40, "");
_Static_assert(FPSTATE_LSX_OFFSET == sizeof(struct user_fp_state), "");
_Static_assert(FPSTATE_LASX_OFFSET ==
                   FPSTATE_LSX_OFFSET + sizeof(struct user_lsx_state),
               "");
_Static_assert(FPSTATE_LBT_OFFSET ==
                   FPSTATE_LASX_OFFSET + sizeof(struct user_lasx_state),
               "");
_Static_assert(FPSTATE_LEN >= FPSTATE_LBT_OFFSET + sizeof(struct user_lbt_state),
               "");
_Static_assert(FPSTATE_LEN <= MAX_FPSTATE_LEN, "");

// Offsets of fcc and fcsr inside the base FP regset. LSX and LASX carry their
// own copies of both, but the regsets for them do not, so they land here.
#define FPSTATE_FCC_OFFSET  offsetof(struct user_fp_state, fcc)
#define FPSTATE_FCSR_OFFSET offsetof(struct user_fp_state, fcsr)

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
// records in sc_extcontext -- FPU, or LSX, or LASX depending on the widest
// unit the thread has touched, optionally followed by LBT, terminated by a
// record with magic zero.
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

// extctx_to_fpstate converts the signal frame's record chain into the regset
// layout the sentry keeps.
//
// The wider units are supersets of the narrower ones in the hardware but not
// in the regsets, which are separate buffers, so an LASX record fills the LASX,
// LSX and base FP areas rather than only its own. That way the sentry's view is
// complete whichever record the kernel happened to emit, which matters because
// the kernel emits only the widest one the thread has used.
//
// Precondition: the chain has been validated by extctx_len.
static void extctx_to_fpstate(uint8_t *frame, uint8_t *fp) {
  uint32_t off = 0;
  for (;;) {
    struct sctx_info *h = (struct sctx_info *)(frame + off);
    if (h->magic == 0) return;
    uint8_t *body = frame + off + sizeof(*h);

    switch (h->magic) {
      case FPU_CTX_MAGIC: {
        // Identical layouts; see the static assert above.
        memcpy(fp + FPSTATE_FPREGS_OFFSET, body, sizeof(struct fpu_context));
        break;
      }
      case LSX_CTX_MAGIC: {
        struct lsx_context *c = (struct lsx_context *)body;
        memcpy(fp + FPSTATE_LSX_OFFSET, (uint8_t *)c->regs, sizeof(c->regs));
        for (int i = 0; i < 32; i++)
          memcpy(fp + FPSTATE_FPREGS_OFFSET + i * 8, (uint8_t *)&c->regs[i * 2], 8);
        memcpy(fp + FPSTATE_FCC_OFFSET, (uint8_t *)&c->fcc, sizeof(c->fcc));
        memcpy(fp + FPSTATE_FCSR_OFFSET, (uint8_t *)&c->fcsr, sizeof(c->fcsr));
        break;
      }
      case LASX_CTX_MAGIC: {
        struct lasx_context *c = (struct lasx_context *)body;
        memcpy(fp + FPSTATE_LASX_OFFSET, (uint8_t *)c->regs, sizeof(c->regs));
        for (int i = 0; i < 32; i++) {
          memcpy(fp + FPSTATE_LSX_OFFSET + i * 16, (uint8_t *)&c->regs[i * 4], 16);
          memcpy(fp + FPSTATE_FPREGS_OFFSET + i * 8, (uint8_t *)&c->regs[i * 4], 8);
        }
        memcpy(fp + FPSTATE_FCC_OFFSET, (uint8_t *)&c->fcc, sizeof(c->fcc));
        memcpy(fp + FPSTATE_FCSR_OFFSET, (uint8_t *)&c->fcsr, sizeof(c->fcsr));
        break;
      }
      case LBT_CTX_MAGIC: {
        memcpy(fp + FPSTATE_LBT_OFFSET, body, sizeof(struct lbt_context));
        break;
      }
      default:
        // An extension this build does not know about. Leaving it alone is
        // the only safe choice: it stays whatever the kernel put there for
        // this thread.
        break;
    }
    off += h->size;
  }
}

// fpstate_to_extctx writes the sentry's regset layout back into the signal
// frame, filling only the records the frame already has. The frame's chain is
// the one the kernel built for this thread and its length is fixed, so records
// are never added or removed -- a context that has state for a unit this frame
// does not carry simply does not get it restored, and vice versa.
//
// Where a record's contents overlap another regset -- an LASX record covers
// what the base FP regset also holds -- the wider regset wins, matching the
// order in which the ptrace platform writes them.
//
// Precondition: both chains have been validated by extctx_len.
static void fpstate_to_extctx(uint8_t *fp, uint8_t *frame) {
  uint32_t off = 0;
  for (;;) {
    struct sctx_info *h = (struct sctx_info *)(frame + off);
    if (h->magic == 0) return;
    uint8_t *body = frame + off + sizeof(*h);

    switch (h->magic) {
      case FPU_CTX_MAGIC: {
        memcpy(body, fp + FPSTATE_FPREGS_OFFSET, sizeof(struct fpu_context));
        break;
      }
      case LSX_CTX_MAGIC: {
        struct lsx_context *c = (struct lsx_context *)body;
        memcpy((uint8_t *)c->regs, fp + FPSTATE_LSX_OFFSET, sizeof(c->regs));
        memcpy((uint8_t *)&c->fcc, fp + FPSTATE_FCC_OFFSET, sizeof(c->fcc));
        memcpy((uint8_t *)&c->fcsr, fp + FPSTATE_FCSR_OFFSET, sizeof(c->fcsr));
        break;
      }
      case LASX_CTX_MAGIC: {
        struct lasx_context *c = (struct lasx_context *)body;
        memcpy((uint8_t *)c->regs, fp + FPSTATE_LASX_OFFSET, sizeof(c->regs));
        memcpy((uint8_t *)&c->fcc, fp + FPSTATE_FCC_OFFSET, sizeof(c->fcc));
        memcpy((uint8_t *)&c->fcsr, fp + FPSTATE_FCSR_OFFSET, sizeof(c->fcsr));
        break;
      }
      case LBT_CTX_MAGIC: {
        memcpy(body, fp + FPSTATE_LBT_OFFSET, sizeof(struct lbt_context));
        break;
      }
      default:
        break;
    }
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
    if (extctx_len(extctx) == 0) {
      // Read the magic through memcpy: __extcontext is declared as an array of
      // unsigned long long, and casting it to uint32_t* trips strict aliasing.
      uint32_t magic;
      memcpy((uint8_t *)&magic, extctx, sizeof(magic));
      panic(STUB_ERROR_FPSTATE_BAD_HEADER, magic);
    }
    extctx_to_fpstate(extctx, ctx->fpstate);
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
    if (extctx_len(extctx) == 0) {
      uint32_t magic;
      memcpy((uint8_t *)&magic, extctx, sizeof(magic));
      panic(STUB_ERROR_FPSTATE_BAD_HEADER, magic);
    }
    fpstate_to_extctx(ctx->fpstate, extctx);
  }
  ptregs_to_gregs(ucontext, &ctx->ptregs);
  set_tls(ctx->tls);
  atomic_store(&sysmsg->state, THREAD_STATE_NONE);
}
