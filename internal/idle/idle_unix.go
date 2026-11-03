//go:build !windows

// Package idle — сколько система простаивает: нет ввода с клавиатуры и
// мыши ни в одном приложении. X11 — расширение MIT-SCREEN-SAVER (libXss и
// libX11 грузим во время работы, dlopen: нет — без него), GNOME (и под
// Wayland) — org.gnome.Mutter.IdleMonitor по DBus. Ни того ни другого
// (KDE под Wayland и т. п.) — не знаем.
//
// Since можно звать из любой горутины.
package idle

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

typedef struct {
	unsigned long window;
	int state;
	int kind;
	unsigned long til_or_since;
	unsigned long idle;
	unsigned long event_mask;
} xss_info;

static void* x_open_display;
static unsigned long (*x_root)(void*);
static int (*xss_query_ext)(void*, int*, int*);
static xss_info* (*xss_alloc)(void);
static int (*xss_query)(void*, unsigned long, xss_info*);
static void* dpy;
static xss_info* info;

static int xss_load(void) {
	if (info) return 1;
	void* x11 = dlopen("libX11.so.6", RTLD_NOW | RTLD_LOCAL);
	void* xss = dlopen("libXss.so.1", RTLD_NOW | RTLD_LOCAL);
	if (!x11 || !xss) return 0;
	void* (*open)(const char*) = dlsym(x11, "XOpenDisplay");
	x_root = dlsym(x11, "XDefaultRootWindow");
	xss_query_ext = dlsym(xss, "XScreenSaverQueryExtension");
	xss_alloc = dlsym(xss, "XScreenSaverAllocInfo");
	xss_query = dlsym(xss, "XScreenSaverQueryInfo");
	if (!open || !x_root || !xss_query_ext || !xss_alloc || !xss_query) return 0;
	dpy = open(NULL);
	if (!dpy) return 0;
	int ev, err;
	if (!xss_query_ext(dpy, &ev, &err)) return 0;
	info = xss_alloc();
	return info != 0;
}

// xss_idle — миллисекунды без ввода; -1 — не узнать.
static long xss_idle(void) {
	if (!xss_query(dpy, x_root(dpy), info)) return -1;
	return (long)info->idle;
}
*/
import "C"

import (
	"os"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

var (
	mu     sync.Mutex
	x11    int // 0 — не пробовали, 1 — есть, -1 — нет
	mutter *dbus.Conn
	noDBus bool
)

// Since — сколько система без ввода; ok=false — узнать нечем.
func Since() (d time.Duration, ok bool) {
	mu.Lock()
	defer mu.Unlock()
	if os.Getenv("DISPLAY") != "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		if x11 == 0 {
			x11 = -1
			if C.xss_load() != 0 {
				x11 = 1
			}
		}
		if x11 == 1 {
			if ms := C.xss_idle(); ms >= 0 {
				return time.Duration(ms) * time.Millisecond, true
			}
		}
	}
	return mutterIdle()
}

// mutterIdle — простой по GNOME Mutter (работает и под Wayland).
func mutterIdle() (time.Duration, bool) {
	if noDBus {
		return 0, false
	}
	if mutter == nil {
		c, err := dbus.ConnectSessionBus()
		if err != nil {
			noDBus = true
			return 0, false
		}
		mutter = c
	}
	var ms uint64
	err := mutter.Object("org.gnome.Mutter.IdleMonitor", "/org/gnome/Mutter/IdleMonitor/Core").
		Call("org.gnome.Mutter.IdleMonitor.GetIdletime", 0).Store(&ms)
	if err != nil {
		// Не GNOME: сервиса нет и не появится.
		noDBus = true
		mutter.Close()
		mutter = nil
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}
