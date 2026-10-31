package ui

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/cache"
	"matterqute/internal/markdown"
)

// wireFeed подключает ленту к окну: ссылки, файлы, реакции.
func (m *MainWindow) wireFeed(f *Feed) {
	f.OnToggleReaction = m.toggleReaction
	f.OnLink = m.handleLink
	f.OnOpenFile = m.openFile
	f.OnSaveFile = m.saveFile
}

func (m *MainWindow) channelExists(name string) bool {
	return m.st.ChannelByName(name, "") != nil
}

func (m *MainWindow) selectChannel(id string) {
	if it := m.items[id]; it != nil {
		m.sidebar.SetCurrentItem(it)
	}
}

// handleLink: ~каналы и @упоминания открываем у себя, остальное — в системе.
func (m *MainWindow) handleLink(link string) {
	switch {
	case strings.HasPrefix(link, markdown.ChannelLinkScheme):
		name, _ := url.PathUnescape(strings.TrimPrefix(link, markdown.ChannelLinkScheme))
		team := ""
		if ch := m.st.Channel(m.current); ch != nil {
			team = ch.TeamId
		}
		if ch := m.st.ChannelByName(name, team); ch != nil {
			m.selectChannel(ch.Id)
		}
	case strings.HasPrefix(link, markdown.UserLinkScheme):
		// Личка с этим пользователем, если она есть в сайдбаре.
		name, _ := url.PathUnescape(strings.TrimPrefix(link, markdown.UserLinkScheme))
		for _, ch := range m.st.DirectChannels() {
			if ch.Type == model.ChannelTypeDirect && strings.EqualFold(m.st.Username(m.st.DMPeer(ch)), name) {
				m.selectChannel(ch.Id)
				return
			}
		}
	default:
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
			m.log.Warn("ignore link", "url", truncate(link, 200), "err", err)
			return
		}
		qt.QDesktopServices_OpenUrl(qt.NewQUrl3(link))
	}
}

// openFile скачивает вложение и открывает системным приложением; то, что
// может исполниться, только сохраняем.
func (m *MainWindow) openFile(fi *model.FileInfo) {
	if !canOpen(fi) {
		m.saveFile(fi)
		return
	}
	m.w.StatusBar().ShowMessage("Скачиваем " + fi.Name + "…")
	m.assets.Download(fi, func(path string, err error) {
		if err != nil {
			m.fail("open file", err)
			return
		}
		m.w.StatusBar().ClearMessage()
		if !qt.QDesktopServices_OpenUrl(qt.QUrl_FromLocalFile(path)) {
			m.fail("open file", fmt.Errorf("no application to open %s", path))
		}
	})
}

func (m *MainWindow) saveFile(fi *model.FileInfo) {
	target := qt.QFileDialog_GetSaveFileName3(m.w.QWidget, "Сохранить файл",
		filepath.Join(downloadsDir(), cache.SafeName(fi.Name, fi.Id)))
	if target == "" {
		return
	}
	m.w.StatusBar().ShowMessage("Скачиваем " + fi.Name + "…")
	m.assets.Download(fi, func(path string, err error) {
		if err != nil {
			m.fail("save file", err)
			return
		}
		go func() {
			err := copyFile(path, target)
			m.ui(func() {
				if err != nil {
					m.fail("save file", err)
					return
				}
				m.w.StatusBar().ShowMessage2("Сохранено: "+target, 10000)
			})
		}()
	})
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return fmt.Errorf("copy %s: %w", from, err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		return fmt.Errorf("copy to %s: %w", to, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("copy %s to %s: %w", from, to, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("copy to %s: %w", to, err)
	}
	return nil
}

// upload грузит файл из поля ввода на сервер, в канал, где его прикрепили.
func (m *MainWindow) upload(c *Composer, channelID string, id int, name, path string, data []byte) {
	limit := m.st.MaxFileSize()
	go func() {
		fileID, err := m.uploadFile(channelID, name, path, data, limit)
		m.ui(func() {
			if err != nil {
				m.fail("upload", err)
				c.UploadFailed(id, err)
				return
			}
			c.Uploaded(id, fileID)
		})
	}()
}

func (m *MainWindow) uploadFile(channelID, name, path string, data []byte, limit int64) (string, error) {
	if channelID == "" {
		return "", fmt.Errorf("upload %q: no channel", name)
	}
	if data == nil {
		st, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("upload %s: %w", path, err)
		}
		if st.IsDir() {
			return "", fmt.Errorf("upload %s: is a directory", path)
		}
		if limit > 0 && st.Size() > limit {
			return "", fmt.Errorf("upload %s: %s, server limit is %s", path, formatSize(st.Size()), formatSize(limit))
		}
		if data, err = os.ReadFile(path); err != nil {
			return "", fmt.Errorf("upload %s: %w", path, err)
		}
	}
	// Без таймаута: большие файлы грузятся долго; отменится при закрытии окна.
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	resp, _, err := m.s.Client.UploadFile(ctx, data, channelID, name)
	if err != nil {
		return "", fmt.Errorf("upload %q to channel %s: %w", name, channelID, err)
	}
	if len(resp.FileInfos) == 0 {
		return "", fmt.Errorf("upload %q to channel %s: server returned no file info", name, channelID)
	}
	return resp.FileInfos[0].Id, nil
}
