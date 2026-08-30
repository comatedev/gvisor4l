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

package cpuid

import (
	"fmt"
	"io"
	"sync/atomic"
)

// FeatureSet for LoongArch64. Like arm64, there's no in-CPU CPUID-equivalent
// discoverable from userspace; the kernel exposes capabilities via HWCAP bits
// in the auxiliary vector (see arch/loongarch/include/uapi/asm/hwcap.h).
//
// +stateify savable
type FeatureSet struct {
	hwCap      hwCap
	cpuFreqMHz float64
	cpuModel   string
}

// CPUModel returns the model name from /proc/cpuinfo, e.g. "Loongson-3A5000".
func (fs FeatureSet) CPUModel() string {
	return fs.cpuModel
}

// ExtendedStateSize returns the size and alignment of the extended FPU state
// area. It covers the base FP file (NT_PRFPREG) plus the vector and binary
// translation register files, which live in their own ptrace regsets; see
// fpu.LoongFPRegsOffset and friends for the layout.
//
// This does not depend on SetVectorStateSaved: the buffer is the same size
// either way, and a platform that cannot fill all of it simply leaves part of
// it alone.
//
// Saving only NT_PRFPREG is not enough: it holds the low 64 bits of each
// vector register, so the upper 192 bits of every LASX register are dropped
// across a context switch. Guests reach the vector unit whether or not HWCAP
// advertises it, because cpucfg reports the real hardware.
func (fs FeatureSet) ExtendedStateSize() (size, align uint) {
	return 1856, 16
}

// HasFeature checks for the presence of a feature.
func (fs FeatureSet) HasFeature(feature Feature) bool {
	return fs.hwCap.hwCap1&(1<<feature) != 0
}

// WriteCPUInfoTo generates a single CPU entry for /proc/cpuinfo. This is a
// minimal version; BogoMIPS is bogus by design.
func (fs FeatureSet) WriteCPUInfoTo(cpu, numCPU uint, w io.Writer) {
	fmt.Fprintf(w, "processor\t\t: %d\n", cpu)
	fmt.Fprintf(w, "package\t\t\t: 0\n")
	fmt.Fprintf(w, "core\t\t\t: %d\n", cpu)
	if fs.cpuModel != "" {
		fmt.Fprintf(w, "CPU Family\t\t: Loongson-64bit\n")
		fmt.Fprintf(w, "Model Name\t\t: %s\n", fs.cpuModel)
	}
	fmt.Fprintf(w, "CPU Revision\t\t: 0x00\n")
	fmt.Fprintf(w, "FPU Revision\t\t: 0x00\n")
	fmt.Fprintf(w, "CPU MHz\t\t\t: %.02f\n", fs.cpuFreqMHz)
	fmt.Fprintf(w, "BogoMIPS\t\t: %.02f\n", fs.cpuFreqMHz*2)
	fmt.Fprintf(w, "TLB Entries\t\t: 2112\n")
	fmt.Fprintf(w, "Address Sizes\t\t: 48 bits physical, 48 bits virtual\n")
	fmt.Fprintf(w, "ISA\t\t\t: loongarch32 loongarch64\n")
	fmt.Fprintf(w, "Features\t\t: %s\n", fs.FlagString())
	fmt.Fprintf(w, "Hardware Watchpoints\t: iwatch count: 0, dwatch count: 0\n")
	fmt.Fprintf(w, "\n")
}

// Fixed returns the same feature set.
func (fs FeatureSet) Fixed() FeatureSet {
	return fs
}

// Intersect is not supported on LoongArch64.
func (fs FeatureSet) Intersect(allowedFeatures map[Feature]struct{}) (FeatureSet, error) {
	return FeatureSet{}, fmt.Errorf("FeatureSet intersection is not supported on LoongArch64")
}

// archCheckHostCompatible is a noop on LoongArch64.
func (FeatureSet) archCheckHostCompatible(FeatureSet) error {
	return nil
}

// vectorStateSaved records whether the platform in use saves and restores the
// vector register file across context switches. It is false until a platform
// says otherwise, because that is the conservative answer.
//
// This is a package-level variable for the same reason arch.ConfigureAddressSpace
// is a package-level function: the FeatureSet is built long before a platform
// exists, and the property being described belongs to the platform, not the CPU.
var vectorStateSaved atomic.Bool

// SetVectorStateSaved records that the platform in use carries the LSX/LASX
// register file across context switches, which makes it safe to advertise
// those extensions to the guest.
//
// A platform MUST call this during initialization, before any guest runs.
func SetVectorStateSaved(saved bool) {
	vectorStateSaved.Store(saved)
}

// AllowedHWCap1 returns the HWCAP1 bits the guest may rely on.
//
// The vector extensions are advertised only when the platform preserves their
// registers; see SetVectorStateSaved. Filtering them out is not a defence --
// glibc's ifunc resolvers on LoongArch dispatch on cpucfg, not HWCAP, so a
// guest reaches the vector unit either way -- it just stops programs that do
// check HWCAP from relying on registers that will not survive.
//
// COMPLEX and CRYPTO are gated with them because they are LSX/LASX
// instructions and use the same register file. LBT is not advertised at all:
// its state is carried, but nothing here has exercised it.
func (fs FeatureSet) AllowedHWCap1() uint64 {
	allowed := uint64(HWCAP_LOONGARCH_CPUCFG |
		HWCAP_LOONGARCH_LAM |
		HWCAP_LOONGARCH_UAL |
		HWCAP_LOONGARCH_FPU |
		HWCAP_LOONGARCH_CRC32)
	if vectorStateSaved.Load() {
		allowed |= HWCAP_LOONGARCH_LSX |
			HWCAP_LOONGARCH_LASX |
			HWCAP_LOONGARCH_COMPLEX |
			HWCAP_LOONGARCH_CRYPTO
	}
	return fs.hwCap.hwCap1 & allowed
}

// AllowedHWCap2 returns the HWCAP2 bits the guest may rely on. LoongArch
// currently does not define HWCAP2 in mainline Linux.
func (fs FeatureSet) AllowedHWCap2() uint64 {
	return 0
}
