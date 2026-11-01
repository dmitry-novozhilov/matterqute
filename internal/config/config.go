// Package config — список серверов и токены сессий.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

const appName = "matterqute"

// Server — сервер, на который пользователь уже входил.
type Server struct {
	URL      string `json:"url"`
	LoginID  string `json:"login_id"` // что вводили в поле логина, чтобы подставить при повторном входе
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	// Канал, открытый последним: с него начинаем при следующем запуске.
	LastChannel string `json:"last_channel,omitempty"`
}

type Config struct {
	Servers []Server `json:"servers"`
	Active  string   `json:"active"`          // URL сервера, открытого последним
	Theme   string   `json:"theme,omitempty"` // system, light, dark; пусто — system
	Window  Window   `json:"window,omitzero"` // окно и колонки, как их оставили
	Fonts   Fonts    `json:"fonts,omitzero"`  // шрифты сайдбара, ленты, моноширинного; пусто — по дизайну

	path string
}

// Fonts — шрифты, выбранные в «Вид → Шрифты…».
type Fonts struct {
	Sidebar Font `json:"sidebar,omitzero"`
	Feed    Font `json:"feed,omitzero"` // текст сообщений и поле ввода
	Mono    Font `json:"mono,omitzero"` // код, время сообщений
}

// Font — семейство и размер в пикселях; пусто или 0 — по дизайну.
type Font struct {
	Family string `json:"family,omitempty"`
	Size   int    `json:"size,omitempty"`
}

// Window — окно и колонки с прошлого запуска; нули — как по умолчанию.
type Window struct {
	Geometry []byte `json:"geometry,omitempty"` // QWidget::saveGeometry: место, размер, развёрнуто ли
	Sidebar  int    `json:"sidebar,omitempty"`  // ширина сайдбара, px
	Thread   int    `json:"thread,omitempty"`   // ширина панели треда, px
}

// Dir — каталог настроек, ~/.config/matterqute на Linux.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	return filepath.Join(base, appName), nil
}

// CacheDir — каталог кэша (картинки, вложения), ~/.cache/matterqute на Linux.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}
	return filepath.Join(base, appName), nil
}

// Load читает config.json из dir; если файла нет — пустой конфиг.
func Load(dir string) (*Config, error) {
	c := &Config{path: filepath.Join(dir, "config.json")}
	data, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", c.path, err)
	}
	return c, nil
}

func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return writeFileAtomic(c.path, append(data, '\n'), 0o644)
}

// Server возвращает сервер по URL или nil.
func (c *Config) Server(url string) *Server {
	for i := range c.Servers {
		if c.Servers[i].URL == url {
			return &c.Servers[i]
		}
	}
	return nil
}

// ActiveServer — последний открытый сервер или nil.
func (c *Config) ActiveServer() *Server {
	return c.Server(c.Active)
}

// Upsert добавляет сервер или обновляет запись с тем же URL и делает его активным.
func (c *Config) Upsert(s Server) {
	c.Active = s.URL
	if old := c.Server(s.URL); old != nil {
		*old = s
		return
	}
	c.Servers = append(c.Servers, s)
}

// Remove убирает сервер из списка; если он был активным — активен первый
// оставшийся (или никакой).
func (c *Config) Remove(url string) {
	c.Servers = slices.DeleteFunc(c.Servers, func(s Server) bool { return s.URL == url })
	if c.Active == url {
		c.Active = ""
		if len(c.Servers) > 0 {
			c.Active = c.Servers[0].URL
		}
	}
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
