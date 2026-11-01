package main

import (
	"log/slog"
	"os"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/config"
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
	ui.NewApp(cfg, tokens, log).Start()
	scheduleScreenshot(log)
	os.Exit(qt.QApplication_Exec())
}
