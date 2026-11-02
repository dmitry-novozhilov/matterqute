package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"matterqute/internal/config"
	"matterqute/internal/desktop"
	"matterqute/internal/instance"
	"matterqute/internal/notify"
	"matterqute/internal/ui"
)

// Qt — в главном потоке процесса. MIQT закрепляет горутину за потоком ОС
// только в NewQApplication, а до неё (DBus в instance.Claim) Go волен
// перенести main на другой поток — и Qt: «QApplication was not created in the
// main() thread». LockOSThread в init держит main в главном потоке с начала.
func init() { runtime.LockOSThread() }

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
	// Ссылка аргументом: mattermost://… из браузера (через ярлык) или адрес
	// сервера. Уже запущены — отдаём её тому приложению и выходим.
	link := linkArg(os.Args[1:])
	var inst *instance.Instance
	if !install && os.Getenv("MATTERQUTE_SCREENSHOT") == "" {
		in, err := instance.Claim(link, os.Getenv("XDG_ACTIVATION_TOKEN"))
		switch {
		case errors.Is(err, instance.ErrRunning):
			log.Info("already running: passed the link to it", "link", link)
			os.Exit(0)
		case err != nil:
			log.Warn("single instance off", "err", err)
		default:
			inst = in
		}
	}
	app := qt.NewQApplication(os.Args)
	if level == slog.LevelDebug {
		defer ui.WatchMainThread(log, os.Stderr)()
	}
	// Имя приложения — служебное (WM_CLASS для дока, как StartupWMClass ярлыка),
	// показываемое — с большой буквы.
	qt.QCoreApplication_SetApplicationName("matterqute")
	qt.QGuiApplication_SetApplicationDisplayName(ui.AppTitle)
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
	ui.SetTyping(!cfg.Typing.Hide, !cfg.Typing.NoSend)
	ui.SetTaskbarAlert(!cfg.NoTaskbarAlert)
	ui.SetSpellcheck(!cfg.NoSpellcheck)
	ui.StartSpell(filepath.Join(dir, "dictionary.txt"), log) // словари — фоном
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
	app.OnAboutToQuit(a.BeforeQuit) // и когда закрыли последнее окно, а не «Выход»
	if inst != nil {
		inst.SetHandler(func(link, token string) { mainthread.Start(func() { a.OpenLink(link, token) }) })
	}
	if link != "" {
		a.OpenLink(link, "")
	}
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
	n, err := notify.New(ui.AppTitle, icon)
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
		registerScheme(data, log)
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

// linkArg — ссылка среди аргументов: mattermost://, http(s)://.
func linkArg(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "mattermost://") || strings.HasPrefix(a, "https://") || strings.HasPrefix(a, "http://") {
			return a
		}
	}
	return ""
}

// registerScheme — ссылки mattermost:// из браузера открывает это приложение:
// база ярлыков и обработчик по умолчанию (xdg-mime). Нет утилит — сказать,
// ярлык всё равно работает.
func registerScheme(dataDir string, log *slog.Logger) {
	// xdg-mime молча ничего не пишет, если каталога настроек ещё нет.
	if dir, err := os.UserConfigDir(); err == nil {
		os.MkdirAll(dir, 0o755)
	}
	for _, c := range desktop.SchemeCommands(dataDir) {
		if _, err := exec.LookPath(c[0]); err != nil {
			fmt.Printf("Нет %s — пропускаю\n", c[0])
			continue
		}
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			log.Warn("register mattermost:// links", "command", strings.Join(c, " "), "err", err, "output", strings.TrimSpace(string(out)))
		}
	}
	// Что вышло: кто теперь открывает ссылки. Не мы — сказать, где искать.
	out, err := exec.Command(desktop.SchemeQuery[0], desktop.SchemeQuery[1:]...).Output()
	switch handler := strings.TrimSpace(string(out)); {
	case err != nil:
		fmt.Println("Не удалось проверить, кто открывает ссылки mattermost://:", err)
	case handler == desktop.ID:
		fmt.Println("Ссылки mattermost:// открывает matterqute (проверка: xdg-open 'mattermost://сервер/команда/channels/town-square').")
		fmt.Println("Браузер, запущенный до установки, мог не заметить смены — перезапустите его.")
	default:
		fmt.Printf("Ссылки mattermost:// открывает %q, а не matterqute: проверьте ~/.config/*mimeapps.list\n", handler)
	}
}
