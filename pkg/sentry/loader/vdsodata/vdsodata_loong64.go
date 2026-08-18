//go:build loong64
// +build loong64

package vdsodata

import _ "embed"

// Binary is the LoongArch64 VDSO: a real one, serving clock_gettime,
// gettimeofday, clock_getres and getcpu out of the sentry's parameter page.
//
// It is built out of tree by scripts/build-vdso-loong64.sh rather than by
// //vdso:vdso, because that genrule needs a C++ toolchain for the target
// architecture and this port deliberately has none -- runsc itself is pure Go
// and cross-builds from x86_64 without one. Rerun that script and commit the
// result whenever anything under vdso/ changes.
//
//go:embed vdso_loong64.so
var Binary []byte
