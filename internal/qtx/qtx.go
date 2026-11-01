// Package qtx — то, чего нет в MIQT: обращения к Qt новее 6.4.
package qtx

/*
#cgo CXXFLAGS: -std=c++17
#cgo pkg-config: Qt6Gui
#include "colorscheme.h"
*/
import "C"

var onSchemeChange func(dark bool)

//export mqColorSchemeChanged
func mqColorSchemeChanged(scheme C.int) {
	if onSchemeChange != nil {
		onSchemeChange(scheme == 2)
	}
}

// SystemDark — тёмная ли системная схема; known=false — система не сказала.
// Только из главного потока Qt, после создания QApplication.
func SystemDark() (dark, known bool) {
	s := C.mq_color_scheme()
	return s == 2, s != 0
}

// WatchSystemScheme вызывает f (в главном потоке) при смене системной схемы.
func WatchSystemScheme(f func(dark bool)) {
	onSchemeChange = f
	C.mq_watch_color_scheme()
}
