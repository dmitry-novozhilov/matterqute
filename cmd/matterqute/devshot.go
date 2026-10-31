package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
)

// Для разработки без дисплея: MATTERQUTE_SCREENSHOT=/path/shot.png (вместе с
// QT_QPA_PLATFORM=offscreen) через MATTERQUTE_SCREENSHOT_DELAY (по умолчанию 3s)
// сохраняет видимые окна в /path/shot-N.png и завершает программу.
func scheduleScreenshot(log *slog.Logger) {
	path := os.Getenv("MATTERQUTE_SCREENSHOT")
	if path == "" {
		return
	}
	delay := 3 * time.Second
	if v := os.Getenv("MATTERQUTE_SCREENSHOT_DELAY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Error("bad MATTERQUTE_SCREENSHOT_DELAY", "value", v, "err", err)
			os.Exit(2)
		}
		delay = d
	}
	time.AfterFunc(delay, func() {
		mainthread.Start(func() {
			base := strings.TrimSuffix(path, ".png")
			n := 0
			for _, w := range qt.QApplication_TopLevelWidgets() {
				if !w.IsVisible() {
					continue
				}
				n++
				out := fmt.Sprintf("%s-%d.png", base, n)
				if !w.Grab().Save(out) {
					log.Error("screenshot", "window", w.WindowTitle(), "path", out)
					continue
				}
				log.Info("screenshot", "window", w.WindowTitle(), "path", out)
			}
			qt.QCoreApplication_Quit()
		})
	})
}
