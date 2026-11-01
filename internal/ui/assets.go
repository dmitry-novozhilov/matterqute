package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/cache"
)

// Assets — картинки и файлы сервера для UI. Отдаёт то, что уже на диске, и
// догружает недостающее в фоне; done вызывается в главном потоке. Все методы —
// из главного потока.
type Assets struct {
	c   *cache.Cache
	ctx context.Context
	ui  func(func()) // MainWindow.ui: главный поток, если окно живо
	log *slog.Logger

	waiting map[string][]func()
	failed  map[string]error // не грузим повторно до Retry

	anims   map[string]*emojiAnim // анимированные кастомные эмодзи
	nextSub int
}

func NewAssets(c *cache.Cache, ctx context.Context, ui func(func()), log *slog.Logger) *Assets {
	return &Assets{c: c, ctx: ctx, ui: ui, log: log, waiting: map[string][]func(){}, failed: map[string]error{},
		anims: map[string]*emojiAnim{}}
}

// Emoji — картинка кастомного эмодзи на диске или "".
func (a *Assets) Emoji(name string) string {
	if a.failed["emoji:"+name] != nil {
		return ""
	}
	return a.c.EmojiPath(name)
}

// WantEmoji догружает кастомный эмодзи; done — когда картинка на диске.
func (a *Assets) WantEmoji(name string, done func()) {
	a.want("emoji:"+name, done, func(ctx context.Context) error {
		_, err := a.c.FetchEmoji(ctx, name)
		return err
	})
}

// Preview — превью картинки-вложения на диске или "".
func (a *Assets) Preview(fileID string) string { return a.c.PreviewPath(fileID) }

func (a *Assets) WantPreview(fileID string, done func()) {
	a.want("preview:"+fileID, done, func(ctx context.Context) error {
		_, err := a.c.FetchPreview(ctx, fileID)
		return err
	})
}

// Avatar — фото пользователя на диске или "" (нет фото или ещё не загружено).
func (a *Assets) Avatar(u *model.User) string {
	if u == nil || u.LastPictureUpdate <= 0 {
		return ""
	}
	return a.c.AvatarPath(u.Id, u.LastPictureUpdate)
}

// WantAvatar догружает фото пользователя, если оно у него есть.
func (a *Assets) WantAvatar(u *model.User, done func()) {
	if u == nil || u.LastPictureUpdate <= 0 {
		return
	}
	id, version := u.Id, u.LastPictureUpdate
	a.want(fmt.Sprintf("avatar:%s:%d", id, version), done, func(ctx context.Context) error {
		_, err := a.c.FetchAvatar(ctx, id, version)
		return err
	})
}

// Icon — иконка интеграции (override_icon_url) на диске или "".
func (a *Assets) Icon(rawURL string) string { return a.c.IconPath(rawURL) }

// WantIcon догружает иконку интеграции; done — когда она на диске.
func (a *Assets) WantIcon(rawURL string, done func()) {
	a.want("icon:"+rawURL, done, func(ctx context.Context) error {
		_, err := a.c.FetchIcon(ctx, rawURL)
		return err
	})
}

// Download качает вложение целиком; done получает путь на диске.
func (a *Assets) Download(fi *model.FileInfo, done func(path string, err error)) {
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
		defer cancel()
		path, err := a.c.FetchFile(ctx, fi)
		a.ui(func() { done(path, err) })
	}()
}

// Later выполняет f в главном потоке после текущих событий.
func (a *Assets) Later(f func()) { a.ui(f) }

// Retry разрешает повторить неудавшиеся загрузки (после переподключения);
// то, чего на сервере нет, повторно не спрашиваем.
func (a *Assets) Retry() {
	for k, err := range a.failed {
		if !errors.Is(err, cache.ErrNotFound) {
			delete(a.failed, k)
		}
	}
}

func (a *Assets) want(key string, done func(), fetch func(context.Context) error) {
	if a.failed[key] != nil {
		return
	}
	if ws, ok := a.waiting[key]; ok {
		a.waiting[key] = append(ws, done)
		return
	}
	a.waiting[key] = []func(){done}
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		err := fetch(ctx)
		a.ui(func() {
			ws := a.waiting[key]
			delete(a.waiting, key)
			if err != nil {
				a.failed[key] = err
				if !errors.Is(err, cache.ErrNotFound) {
					a.log.Warn("load asset", "key", key, "err", err)
				}
				return
			}
			for _, f := range ws {
				f()
			}
		})
	}()
}

// isImage — вложение показываем картинкой (превью есть у растровых картинок).
func isImage(fi *model.FileInfo) bool {
	return fi.HasPreviewImage || strings.HasPrefix(fi.MimeType, "image/") && fi.MimeType != "image/svg+xml"
}

// Открывать системным приложением можно только то, что не исполняется:
// .desktop, скрипты, HTML и SVG (со скриптами внутри) — только «Сохранить».
var openableExts = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "bmp": true, "tif": true, "tiff": true,
	"pdf": true, "txt": true, "log": true, "md": true, "csv": true, "json": true, "yaml": true, "yml": true,
	"doc": true, "docx": true, "xls": true, "xlsx": true, "ppt": true, "pptx": true, "odt": true, "ods": true, "odp": true, "rtf": true,
	"mp3": true, "ogg": true, "wav": true, "flac": true, "m4a": true, "mp4": true, "webm": true, "mkv": true, "mov": true, "avi": true,
	"zip": true, "tar": true, "gz": true, "tgz": true, "7z": true, "rar": true, "xz": true, "bz2": true,
}

func canOpen(fi *model.FileInfo) bool {
	return openableExts[strings.ToLower(strings.TrimPrefix(filepath.Ext(fi.Name), "."))]
}

// fitSize вписывает картинку w×h в maxW×maxH с сохранением пропорций.
func fitSize(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return maxW, maxH * 2 / 3
	}
	if w > maxW {
		h, w = h*maxW/w, maxW
	}
	if h > maxH {
		w, h = w*maxH/h, maxH
	}
	return max(w, 1), max(h, 1)
}

// downloadsDir — куда предлагать сохранять: ~/Downloads, если есть, иначе домашний.
func downloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, d := range []string{"Downloads", "Загрузки"} {
		if st, err := os.Stat(filepath.Join(home, d)); err == nil && st.IsDir() {
			return filepath.Join(home, d)
		}
	}
	return home
}
