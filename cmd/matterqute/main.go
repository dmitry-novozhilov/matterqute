package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"matterqute/internal/config"
	"matterqute/internal/desktop"
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

	install := len(os.Args) > 1 && (os.Args[1] == "--install" || os.Args[1] == "--uninstall")
	if install {
		os.Setenv("QT_QPA_PLATFORM", "offscreen") // иконки рисуем без окон и дисплея
	}
	app := qt.NewQApplication(os.Args)
	qt.QCoreApplication_SetApplicationName("matterqute")
	qt.QGuiApplication_SetApplicationDisplayName("matterqute")
	qt.QGuiApplication_SetDesktopFileName("matterqute")

	if install {
		os.Exit(installDesktop(os.Args[1] == "--install", log))
	}

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
	ui.SetFormatBar(cfg.FormatBar)
	a := ui.NewApp(cfg, tokens, log)
	if n := startNotifier(log); n != nil {
		a.SetNotifier(n)
	}
	if l, err := notify.NewLauncher(desktop.ID); err != nil {
		log.Warn("dock badge off", "err", err)
	} else {
		a.SetLauncher(l)
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
		if err := ui.SaveAppIcon(path, 256); err != nil {
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

// installDesktop — «matterqute --install»: ярлык в меню приложений и иконки
// (для дока со счётчиком); «--uninstall» — убрать. Код выхода.
func installDesktop(install bool, log *slog.Logger) int {
	data, err := desktop.DataDir()
	if err != nil {
		log.Error("install", "err", err)
		return 1
	}
	var paths []string
	if install {
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			log.Error("install", "err", fmt.Errorf("path to the binary: %w", err))
			return 1
		}
		if strings.Contains(exe, "go-build") {
			log.Warn("install: binary is a temporary go run build, the shortcut will break", "exe", exe)
		}
		paths, err = desktop.Install(data, exe, ui.SaveAppIcon)
		if err != nil {
			log.Error("install", "err", err)
			return 1
		}
		fmt.Println("Ярлык установлен, запускает", exe)
	} else {
		if paths, err = desktop.Uninstall(data); err != nil {
			log.Error("uninstall", "err", err)
			return 1
		}
		fmt.Println("Ярлык удалён")
	}
	for _, p := range paths {
		fmt.Println("  " + p)
	}
	return 0
}
