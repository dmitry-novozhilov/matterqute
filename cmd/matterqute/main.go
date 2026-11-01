package main

import (
	"log/slog"
	"os"
	"path/filepath"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/config"
	"matterqute/internal/notify"
	"matterqute/internal/ui"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("MATTERQUTE_DEBUG") != "" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey && len(groups) == 0 {
				return slog.String(a.Key, a.Value.Time().Format("2006-01-02 15:04:05"))
			}
			return a
		},
	}))

	app := qt.NewQApplication(os.Args)
	qt.QCoreApplication_SetApplicationName("matterqute")
	qt.QGuiApplication_SetApplicationDisplayName("matterqute")
	qt.QGuiApplication_SetDesktopFileName("matterqute")

	dir, err := config.Dir()
	if err != nil {
		log.Error("startup", "err", err)
		os.Exit(1)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		log.Error("startup", "err", err)
		os.Exit(1)
	}
	tokens, err := config.OpenTokens(dir)
	if err != nil {
		log.Warn("startup", "err", err)
	}

	ui.InitStyle(app, ui.ThemeMode(cfg.Theme), log)
	ui.SetFonts(cfg.Fonts)
	a := ui.NewApp(cfg, tokens, log)
	if n := startNotifier(log); n != nil {
		a.SetNotifier(n)
	}
	a.Start()
	scheduleScreenshot(log)
	os.Exit(qt.QApplication_Exec())
}

// startNotifier — уведомления рабочего стола; без сессионной шины или сервера
// уведомлений работаем без них.
func startNotifier(log *slog.Logger) *notify.Notifier {
	icon := ""
	if dir, err := config.CacheDir(); err == nil {
		path := filepath.Join(dir, "app-icon.png")
		if err := ui.SaveAppIcon(path); err != nil {
			log.Warn("notifications", "err", err)
		} else {
			icon = path
		}
	}
	n, err := notify.New("matterqute", icon)
	if err != nil {
		log.Warn("desktop notifications off", "err", err)
		return nil
	}
	return n
}
