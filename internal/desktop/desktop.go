// Package desktop — ярлык приложения для меню и дока: matterqute.desktop и
// иконки в $XDG_DATA_HOME (обычно ~/.local/share), без root. Нужен, чтобы
// окружение показывало имя и значок, а доки — счётчик на нём (LauncherEntry
// находит значок по имени .desktop-файла).
package desktop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ID — имя .desktop-файла; его же Qt сообщает окружению (setDesktopFileName).
const ID = "matterqute.desktop"

// IconSizes — размеры иконки в hicolor.
var IconSizes = []int{48, 64, 128, 256}

// DataDir — $XDG_DATA_HOME, без него — ~/.local/share.
func DataDir() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w", err)
	}
	return filepath.Join(home, ".local", "share"), nil
}

// Entry — содержимое .desktop для бинарника exe.
func Entry(exe string) string {
	return "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Matterqute\n" +
		"GenericName=Mattermost\n" +
		"Comment=Клиент Mattermost\n" +
		"Exec=" + quoteExec(exe) + "\n" +
		"Icon=matterqute\n" +
		"Terminal=false\n" +
		"Categories=Network;InstantMessaging;Chat;\n" +
		"StartupWMClass=matterqute\n" +
		"StartupNotify=true\n"
}

// quoteExec — путь для Exec по спецификации Desktop Entry: в кавычках, если в
// нём есть пробелы или спецсимволы; \ " ` $ внутри экранируются.
func quoteExec(path string) string {
	if !strings.ContainsAny(path, " \t\n\"'\\><~|&;$*?#()`") {
		return path
	}
	r := strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", `$`, `\\$`)
	return `"` + r.Replace(path) + `"`
}

func entryPath(dataDir string) string { return filepath.Join(dataDir, "applications", ID) }

func iconPath(dataDir string, px int) string {
	return filepath.Join(dataDir, "icons", "hicolor", fmt.Sprintf("%dx%d", px, px), "apps", "matterqute.png")
}

// Install пишет .desktop для exe и иконки (saveIcon рисует PNG px×px в путь);
// возвращает, что записано.
func Install(dataDir, exe string, saveIcon func(path string, px int) error) ([]string, error) {
	var written []string
	for _, px := range IconSizes {
		p := iconPath(dataDir, px)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return written, fmt.Errorf("install icon %s: %w", p, err)
		}
		if err := saveIcon(p, px); err != nil {
			return written, fmt.Errorf("install icon %s: %w", p, err)
		}
		written = append(written, p)
	}
	p := entryPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return written, fmt.Errorf("install %s: %w", p, err)
	}
	if err := os.WriteFile(p, []byte(Entry(exe)), 0o644); err != nil {
		return written, fmt.Errorf("install %s: %w", p, err)
	}
	return append(written, p), nil
}

// Uninstall убирает то, что поставил Install; возвращает удалённое.
func Uninstall(dataDir string) ([]string, error) {
	paths := []string{entryPath(dataDir)}
	for _, px := range IconSizes {
		paths = append(paths, iconPath(dataDir, px))
	}
	var removed []string
	for _, p := range paths {
		err := os.Remove(p)
		switch {
		case err == nil:
			removed = append(removed, p)
		case !errors.Is(err, fs.ErrNotExist):
			return removed, fmt.Errorf("uninstall %s: %w", p, err)
		}
	}
	return removed, nil
}
