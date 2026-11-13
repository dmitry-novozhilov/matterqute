// Package memstat — сколько памяти занято: куча C (malloc — там Qt, ffmpeg и
// всё, что MIQT создаёт), RSS процесса и куча Go. Тесты на утечки объектов Qt
// меряют кучу C: сборщик мусора Go её не видит, RSS шумный.
package memstat

import (
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Log раз в every пишет в log (debug) RSS, кучу C и кучу Go — растёт ли
// память и где. Не возвращается: звать в своей горутине.
func Log(log *slog.Logger, every time.Duration) {
	for {
		time.Sleep(every)
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		malloc, _ := MallocInUse()
		log.Debug("memory", "rss_mb", RSS()>>20, "malloc_mb", malloc>>20, "go_heap_mb", ms.HeapAlloc>>20,
			"go_sys_mb", ms.Sys>>20, "gc", ms.NumGC)
	}
}

// RSS — резидентная память процесса в байтах (Linux, /proc/self/status); 0 — не узнать.
func RSS() uint64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(l, "VmRSS:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				kb, _ := strconv.ParseUint(f[0], 10, 64)
				return kb << 10
			}
		}
	}
	return 0
}
