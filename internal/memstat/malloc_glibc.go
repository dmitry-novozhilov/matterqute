//go:build linux && cgo

package memstat

/*
#include <malloc.h>
*/
import "C"

// MallocInUse — байты, выданные malloc и ещё не освобождённые (и крупные
// блоки через mmap); ok=false — узнать нельзя. glibc 2.33+ (mallinfo2).
func MallocInUse() (bytes uint64, ok bool) {
	mi := C.mallinfo2()
	return uint64(mi.uordblks) + uint64(mi.hblkhd), true
}
