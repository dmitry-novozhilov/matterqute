// Package cache — файлы сервера на диске: превью картинок, вложения целиком,
// кастомные эмодзи. Качает из горутин, потокобезопасен.
//
// Имена файлов и эмодзи приходят от других пользователей, поэтому в пути на
// диске они попадают только после проверки и очистки.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
)

// ErrNotFound — на сервере этого нет (например, такого кастомного эмодзи).
var ErrNotFound = errors.New("not found on server")

type Cache struct {
	dir    string
	server string
	c      *model.Client4

	mu       sync.Mutex
	inflight map[string]*call
	noEmoji  map[string]bool // имена, которых на сервере нет
}

type call struct {
	done chan struct{}
	path string
	err  error
}

// New — кэш сервера в base/<хэш URL сервера>: у разных серверов свои эмодзи и файлы.
func New(base, serverURL string, c *model.Client4) *Cache {
	sum := sha256.Sum256([]byte(serverURL))
	return &Cache{
		dir:      filepath.Join(base, hex.EncodeToString(sum[:8])),
		server:   serverURL,
		c:        c,
		inflight: map[string]*call{},
		noEmoji:  map[string]bool{},
	}
}

// Как у сервера (model.IsValidEmojiName), плюс ограничение длины.
var emojiNameRe = regexp.MustCompile(`^[a-zA-Z0-9_+-]{1,64}$`)

// imageExts — расширения, под которыми сохраняем картинки (по содержимому).
var imageExts = []string{".png", ".gif", ".jpg", ".webp", ".bmp"}

// EmojiPath — картинка кастомного эмодзи на диске или "".
func (c *Cache) EmojiPath(name string) string {
	if !emojiNameRe.MatchString(name) {
		return ""
	}
	return existing(filepath.Join(c.dir, "emoji", name), imageExts)
}

// FetchEmoji качает картинку кастомного эмодзи; ErrNotFound — такого нет.
func (c *Cache) FetchEmoji(ctx context.Context, name string) (string, error) {
	if !emojiNameRe.MatchString(name) {
		return "", fmt.Errorf("emoji name %q: %w", truncate(name, 100), ErrNotFound)
	}
	c.mu.Lock()
	missing := c.noEmoji[name]
	c.mu.Unlock()
	if missing {
		return "", fmt.Errorf("emoji %q: %w", name, ErrNotFound)
	}
	return c.do("emoji:"+name, func() (string, error) {
		if p := c.EmojiPath(name); p != "" {
			return p, nil
		}
		e, resp, err := c.c.GetEmojiByName(ctx, name)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			c.mu.Lock()
			c.noEmoji[name] = true
			c.mu.Unlock()
			return "", fmt.Errorf("emoji %q: %w", name, ErrNotFound)
		}
		if err != nil {
			return "", fmt.Errorf("get emoji %q from %s: %w", name, c.server, err)
		}
		data, _, err := c.c.GetEmojiImage(ctx, e.Id)
		if err != nil {
			return "", fmt.Errorf("get image of emoji %q (%s) from %s: %w", name, e.Id, c.server, err)
		}
		return c.save(filepath.Join(c.dir, "emoji", name)+imageExt(data), data)
	})
}

// PreviewPath — превью картинки-вложения на диске или "".
func (c *Cache) PreviewPath(fileID string) string {
	if !model.IsValidId(fileID) {
		return ""
	}
	return existing(filepath.Join(c.dir, "previews", fileID), imageExts)
}

// FetchPreview качает превью картинки-вложения (уменьшенную сервером копию).
func (c *Cache) FetchPreview(ctx context.Context, fileID string) (string, error) {
	if !model.IsValidId(fileID) {
		return "", fmt.Errorf("file id %q: invalid", truncate(fileID, 100))
	}
	return c.do("preview:"+fileID, func() (string, error) {
		if p := c.PreviewPath(fileID); p != "" {
			return p, nil
		}
		data, _, err := c.c.GetFilePreview(ctx, fileID)
		if err != nil {
			return "", fmt.Errorf("get preview of file %s from %s: %w", fileID, c.server, err)
		}
		return c.save(filepath.Join(c.dir, "previews", fileID)+imageExt(data), data)
	})
}

// AvatarPath — фото пользователя на диске или "". Версия — LastPictureUpdate:
// новое фото — новый файл.
func (c *Cache) AvatarPath(userID string, version int64) string {
	if !model.IsValidId(userID) {
		return ""
	}
	return existing(filepath.Join(c.dir, "avatars", fmt.Sprintf("%s-%d", userID, version)), imageExts)
}

// FetchAvatar качает фото пользователя.
func (c *Cache) FetchAvatar(ctx context.Context, userID string, version int64) (string, error) {
	if !model.IsValidId(userID) {
		return "", fmt.Errorf("user id %q: invalid", truncate(userID, 100))
	}
	return c.do(fmt.Sprintf("avatar:%s:%d", userID, version), func() (string, error) {
		if p := c.AvatarPath(userID, version); p != "" {
			return p, nil
		}
		data, _, err := c.c.GetProfileImage(ctx, userID, "")
		if err != nil {
			return "", fmt.Errorf("get profile image of %s from %s: %w", userID, c.server, err)
		}
		return c.save(filepath.Join(c.dir, "avatars", fmt.Sprintf("%s-%d", userID, version))+imageExt(data), data)
	})
}

// Пределы для сторонних картинок: иконка интеграции — это аватар, картинка
// предпросмотра ссылки — уже побольше.
const (
	maxIconSize = 1 << 20
	maxLinkSize = 10 << 20
)

// IconPath — иконка интеграции (override_icon_url поста вебхука) на диске или "".
func (c *Cache) IconPath(rawURL string) string {
	return existing(c.urlBase("icons", rawURL), imageExts)
}

// FetchIcon качает иконку интеграции.
func (c *Cache) FetchIcon(ctx context.Context, rawURL string) (string, error) {
	return c.fetchImageURL(ctx, "icons", "integration icon", rawURL, maxIconSize)
}

// LinkImagePath — картинка предпросмотра ссылки (OpenGraph или ссылка прямо на
// картинку) на диске или "".
func (c *Cache) LinkImagePath(rawURL string) string {
	return existing(c.urlBase("links", rawURL), imageExts)
}

// FetchLinkImage качает картинку предпросмотра ссылки.
func (c *Cache) FetchLinkImage(ctx context.Context, rawURL string) (string, error) {
	return c.fetchImageURL(ctx, "links", "link preview image", rawURL, maxLinkSize)
}

func (c *Cache) urlBase(dir, rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return filepath.Join(c.dir, dir, hex.EncodeToString(sum[:16]))
}

// fetchImageURL качает картинку по адресу из поста. Чужой сервер — без токена
// Mattermost, как веб-клиент без прокси картинок; сам сервер (его прокси
// картинок /api/v4/image) — с токеном. Ответ не картинку, больше max или 4xx —
// ErrNotFound: повторять без толку.
func (c *Cache) fetchImageURL(ctx context.Context, dir, what, rawURL string, max int) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%s url %q: want http(s): %w", what, truncate(rawURL, 200), ErrNotFound)
	}
	return c.do(dir+":"+rawURL, func() (string, error) {
		if p := existing(c.urlBase(dir, rawURL), imageExts); p != "" {
			return p, nil
		}
		fail := func(format string, args ...any) (string, error) {
			return "", fmt.Errorf("get %s %s: "+format, append([]any{what, truncate(rawURL, 200)}, args...)...)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return fail("%w", err)
		}
		if c.c != nil && c.c.AuthToken != "" && strings.HasPrefix(rawURL, c.server+"/") {
			req.Header.Set(model.HeaderAuth, model.HeaderBearer+" "+c.c.AuthToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fail("%w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return fail("HTTP %d: %w", resp.StatusCode, ErrNotFound)
		}
		if resp.StatusCode != http.StatusOK {
			return fail("HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, int64(max)+1))
		if err != nil {
			return fail("%w", err)
		}
		if len(data) > max {
			return fail("larger than %d bytes: %w", max, ErrNotFound)
		}
		if ct := http.DetectContentType(data); !strings.HasPrefix(ct, "image/") {
			return fail("content is %s, not an image: %w", ct, ErrNotFound)
		}
		return c.save(c.urlBase(dir, rawURL)+imageExt(data), data)
	})
}

// VideoInfo — что узнали о ролике, разобрав его: кадр-превью, длительность, размер.
type VideoInfo struct {
	Thumb    string // PNG первого кадра
	Duration int64  // мс
	Width    int
	Height   int
}

// VideoThumbPath — куда сохранить кадр-превью ролика; длительность и размер — в имени файла.
func (c *Cache) VideoThumbPath(fileID string, durationMs int64, w, h int) string {
	if !model.IsValidId(fileID) {
		return ""
	}
	return filepath.Join(c.dir, "videos", fmt.Sprintf("%s-%d-%dx%d.png", fileID, durationMs, w, h))
}

// Video — разобранный ранее ролик; ok=false — ещё не разбирали.
func (c *Cache) Video(fileID string) (VideoInfo, bool) {
	if !model.IsValidId(fileID) {
		return VideoInfo{}, false
	}
	matches, _ := filepath.Glob(filepath.Join(c.dir, "videos", fileID+"-*.png"))
	for _, m := range matches {
		var v VideoInfo
		if _, err := fmt.Sscanf(strings.TrimPrefix(filepath.Base(m), fileID+"-"), "%d-%dx%d.png", &v.Duration, &v.Width, &v.Height); err == nil {
			v.Thumb = m
			return v, true
		}
	}
	return VideoInfo{}, false
}

// FilePath — где лежит (или будет лежать) вложение целиком.
func (c *Cache) FilePath(fi *model.FileInfo) string {
	return filepath.Join(c.dir, "files", fi.Id, SafeName(fi.Name, fi.Id))
}

// FetchFile качает вложение целиком (если ещё не скачано).
func (c *Cache) FetchFile(ctx context.Context, fi *model.FileInfo) (string, error) {
	if !model.IsValidId(fi.Id) {
		return "", fmt.Errorf("file id %q: invalid", truncate(fi.Id, 100))
	}
	path := c.FilePath(fi)
	return c.do("file:"+fi.Id, func() (string, error) {
		if st, err := os.Stat(path); err == nil && st.Size() == fi.Size {
			return path, nil
		}
		data, _, err := c.c.GetFile(ctx, fi.Id)
		if err != nil {
			return "", fmt.Errorf("download file %s (%q) from %s: %w", fi.Id, fi.Name, c.server, err)
		}
		return c.save(path, data)
	})
}

// SafeName — имя файла от сервера, пригодное для диска: без каталогов,
// управляющих символов и ведущих точек; пустое заменяется на fallback.
func SafeName(name, fallback string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' || r == ':' {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		name = strings.ToValidUTF8(name[:200-len(ext)], "") + ext
	}
	if name == "" {
		return fallback
	}
	return name
}

// do выполняет загрузку с ключом один раз, даже если её ждут несколько горутин.
func (c *Cache) do(key string, f func() (string, error)) (string, error) {
	c.mu.Lock()
	if cl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-cl.done
		return cl.path, cl.err
	}
	cl := &call{done: make(chan struct{})}
	c.inflight[key] = cl
	c.mu.Unlock()

	cl.path, cl.err = f()
	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	close(cl.done)
	return cl.path, cl.err
}

func (c *Cache) save(path string, data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("cache %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("cache %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return "", fmt.Errorf("cache %s: %w", path, err)
	}
	return path, nil
}

func existing(base string, exts []string) string {
	for _, ext := range exts {
		if _, err := os.Stat(base + ext); err == nil {
			return base + ext
		}
	}
	return ""
}

// imageExt — расширение по содержимому: Qt надёжнее определяет формат по нему.
func imageExt(data []byte) string {
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	}
	return ".jpg"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
