package ui

import (
	"bytes"
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
	f.OnPickReaction = m.pickReaction
	f.OnPermalink = m.openPermalink
	f.OnRemovePreview = m.removePreview
	f.OnLink = m.handleLink
	f.OnIntegration = m.showIntegrationCard
	f.OnFullscreenVideo = m.fullscreenVideo
	f.OnOpenFile = m.openFile
	f.OnOpenImage = m.openImage
	f.OnCopyImage = m.copyImage
	f.OnSaveFile = m.saveFile
	f.OnEditPost = func(p *model.Post) { m.editPost(f, p) }
	f.OnDeletePost = m.confirmDelete
	f.OnCopyLink = m.copyPostLink
	f.OnPin = m.pinPost
	f.OnSave = m.savePost
	f.OnMarkUnread = func(p *model.Post) { m.markUnread(f, p) }
	f.OnFollowThread = m.followThread
	f.OnRemind = m.remind
	f.OnThreadWindow = m.openThreadWindow
	f.OnEditHistory = func(p *model.Post) { m.showEditHistory(p) }
	f.OnForward = func(p *model.Post) { m.showForward(p) }
	f.OnAction = func(p *model.Post, a *model.PostAction) { m.postAction(f, p, a) }
	f.OnQuote = func(p *model.Post, selected string) { m.quote(f, p, selected) }
	f.OnOutbox = m.outboxAction
}

// quote — «Цитировать»: в поле ввода колонки ленты f (тред — в поле треда,
// канал — в поле канала) выделенное в сообщении или всё сообщение.
func (m *MainWindow) quote(f *Feed, p *model.Post, selected string) {
	c := m.composer
	if f == m.panel.feed {
		c = m.panel.composer
	}
	if selected != "" {
		c.Quote(selected, false)
	} else {
		c.Quote(p.Message, true)
	}
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
		name, _ := url.PathUnescape(strings.TrimPrefix(link, markdown.UserLinkScheme))
		switch name {
		case "channel", "all", "here":
		default:
			m.showUserCard(name)
		}
	default:
		// Ссылка на свой сервер (сообщение, канал, личка) — здесь, на другой
		// добавленный — там, остальное — в браузере.
		path, own := m.ownPath(link)
		if own && m.openServerPath(path) {
			return
		}
		if m.OnOtherServerLink != nil && m.OnOtherServerLink(link) {
			return
		}
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
			m.log.Warn("ignore link", "url", truncate(link, 200), "err", err)
			return
		}
		// Ссылку на сообщение, ушедшую в браузер, по логу видно: какой адрес не узнали.
		m.log.Debug("open in browser", "url", truncate(link, 200), "own_server", own, "path", truncate(path, 200))
		qt.QDesktopServices_OpenUrl(tmp(qt.NewQUrl3(link)))
	}
}

// openFile скачивает вложение и открывает системным приложением; то, что
// может исполниться, только сохраняем.
func (m *MainWindow) openFile(fi *model.FileInfo) {
	if !canOpen(fi) {
		m.saveFile(fi)
		return
	}
	m.message("Скачиваем " + fi.Name + "…")
	m.assets.Download(fi, func(path string, err error) {
		if err != nil {
			m.fail("open file", err)
			return
		}
		m.message("")
		if !qt.QDesktopServices_OpenUrl(qt.QUrl_FromLocalFile(path)) {
			m.fail("open file", fmt.Errorf("no application to open %s", path))
		}
	})
}

func (m *MainWindow) saveFile(fi *model.FileInfo) {
	target := qt.QFileDialog_GetSaveFileName3(m.w, "Сохранить файл",
		filepath.Join(downloadsDir(), cache.SafeName(fi.Name, fi.Id)))
	if target == "" {
		return
	}
	m.message("Скачиваем " + fi.Name + "…")
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
				m.message("Сохранено: " + target)
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
func (m *MainWindow) upload(c *Composer, channelID string, id int, name, path string, data []byte, progress func(int)) {
	limit := m.st.MaxFileSize()
	go func() {
		last := -1
		fileID, err := m.uploadFile(channelID, name, path, data, limit, func(sent, total int64) {
			if total <= 0 {
				return
			}
			// Проценты, а не байты: иначе тысячи обновлений плашки на большом файле.
			if pct := int(sent * 100 / total); pct != last {
				last = pct
				m.ui(func() { progress(pct) })
			}
		})
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

func (m *MainWindow) uploadFile(channelID, name, path string, data []byte, limit int64, progress func(sent, total int64)) (string, error) {
	if channelID == "" {
		return "", fmt.Errorf("upload %q: no channel", name)
	}
	var r io.Reader
	var size int64
	if data != nil {
		r, size = bytes.NewReader(data), int64(len(data))
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("upload %s: %w", path, err)
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return "", fmt.Errorf("upload %s: %w", path, err)
		}
		if st.IsDir() {
			return "", fmt.Errorf("upload %s: is a directory", path)
		}
		r, size = f, st.Size()
	}
	if limit > 0 && size > limit {
		return "", fmt.Errorf("upload %q: %s, server limit is %s", name, formatSize(size), formatSize(limit))
	}
	// Без таймаута: большие файлы грузятся долго; отменится при закрытии окна.
	fi, err := m.s.UploadFile(m.ctx, channelID, name, r, size, progress)
	if err != nil {
		return "", err
	}
	return fi.Id, nil
}
