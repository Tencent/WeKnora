//go:build linux && !musl

package anydoc

/*
#cgo amd64 LDFLAGS: -L${SRCDIR}/lib/linux_amd64_gnu -lanydoc_go -lm -lstdc++ -ldl -lpthread
#cgo arm64 LDFLAGS: -L${SRCDIR}/lib/linux_arm64_gnu -lanydoc_go -lm -lstdc++ -ldl -lpthread
*/
import "C"
