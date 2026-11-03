//go:build windows

// Package idle — сколько система простаивает. Windows: GetLastInputInfo —
// время последнего ввода в миллисекундах с запуска системы (32 бита, как и
// GetTickCount: разница верна и после переполнения).
package idle

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	getLastInputInfo = user32.NewProc("GetLastInputInfo")
	getTickCount     = kernel32.NewProc("GetTickCount")
)

type lastInputInfo struct {
	size uint32
	time uint32
}

// Since — сколько система без ввода; ok=false — узнать нечем.
func Since() (d time.Duration, ok bool) {
	info := lastInputInfo{size: uint32(unsafe.Sizeof(lastInputInfo{}))}
	if r, _, _ := getLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, false
	}
	now, _, _ := getTickCount.Call()
	return time.Duration(uint32(now)-info.time) * time.Millisecond, true
}
