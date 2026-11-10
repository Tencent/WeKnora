//go:build linux && musl

package anydoc

/*
#cgo amd64 LDFLAGS: -L${SRCDIR}/lib/linux_amd64_musl -lanydoc_go -lm -lstdc++ -ldl -lpthread
#cgo arm64 LDFLAGS: -L${SRCDIR}/lib/linux_arm64_musl -lanydoc_go -lm -lstdc++ -ldl -lpthread
*/
import "C"
