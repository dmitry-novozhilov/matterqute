package ui

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Переход к сообщению, каналу, личке по ссылке своего сервера — из текста,
// из карточки предпросмотра, из ответа команды, из браузера (mattermost://).
// Сообщение открывается в своём канале, лента прокручивается к нему (старое
// — догружается) и оно подсвечено; ответ в треде — ещё и в панели треда.

var (
	channelPathRe   = regexp.MustCompile(`^/([^/]+)/channels/([^/?#]+)`)
	messagesPathRe  = regexp.MustCompile(`^/([^/]+)/messages/@([^/?#]+)`)
	permalinkPathRe = regexp.MustCompile(`^/([^/]+)/pl/([a-z0-9]{26})`)
	threadPathRe    = regexp.MustCompile(`^/([^/]+)/threads/([a-z0-9]{26})`)
)

// revealPages — сколько старых страниц ленты догружать в поисках сообщения.
const revealPages = 50

// postLink — ссылка на сообщение, как у веб-клиента: адрес сервера/команда/pl/id.
// У личек команды нет — подходит любая, берём первую.
func (m *MainWindow) postLink(p *model.Post) string {
	team := ""
	if ch := m.st.Channel(p.ChannelId); ch != nil {
		team = ch.TeamId
	}
	if team == "" {
		team = m.teamOf("")
	}
	name := ""
	for _, t := range m.st.Teams() {
		if t.Id == team {
			name = t.Name
		}
	}
	return strings.TrimSuffix(m.s.ServerURL, "/") + "/" + name + "/pl/" + p.Id
}

// copyPostLink — «Копировать ссылку на сообщение»: в буфер обмена.
func (m *MainWindow) copyPostLink(p *model.Post) {
	qt.QGuiApplication_Clipboard().SetText(m.postLink(p))
	m.message("Ссылка на сообщение скопирована")
}

// ownPath — путь ссылки link на этом сервере (http(s) или mattermost:// с его
// адресом, или путь без адреса); false — ссылка не на него.
func (m *MainWindow) ownPath(link string) (string, bool) {
	u, err := url.Parse(link)
	if err != nil {
		return "", false
	}
	own, err := url.Parse(m.s.ServerURL)
	if err != nil {
		return "", false
	}
	base := strings.TrimSuffix(own.Path, "/")
	switch {
	case u.Scheme == "" && u.Host == "" && strings.HasPrefix(u.Path, "/"):
		return strings.TrimPrefix(u.Path, base), true
	case u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mattermost":
		return "", false
	case !strings.EqualFold(u.Host, own.Host):
		return "", false
	case base != "" && u.Path != base && !strings.HasPrefix(u.Path, base+"/"):
		return "", false
	}
	return strings.TrimPrefix(u.Path, base), true
}

// openServerPath — путь этого сервера внутри приложения: канал, личка,
// сообщение, тред. false — такого здесь нет (пусть откроет браузер).
func (m *MainWindow) openServerPath(path string) bool {
	switch {
	case channelPathRe.MatchString(path):
		g := channelPathRe.FindStringSubmatch(path)
		name, _ := url.PathUnescape(g[2])
		m.openChannelByName(g[1], name)
	case messagesPathRe.MatchString(path):
		name, _ := url.PathUnescape(messagesPathRe.FindStringSubmatch(path)[2])
		m.openDMByName(name)
	case permalinkPathRe.MatchString(path):
		m.openPermalink("", permalinkPathRe.FindStringSubmatch(path)[2])
	case threadPathRe.MatchString(path):
		m.openPermalink("", threadPathRe.FindStringSubmatch(path)[2])
	default:
		return false
	}
	return true
}

// OpenPath — путь этого сервера, пришедший снаружи (ссылка из браузера); до
// первой загрузки сайдбара — откладывается.
func (m *MainWindow) OpenPath(path string) {
	if len(m.items) == 0 {
		m.pendingPath = path
		return
	}
	if !m.openServerPath(path) {
		m.handleLink(strings.TrimSuffix(m.s.ServerURL, "/") + path)
	}
}

// gotoLocation — переход из ответа команды: свой путь — здесь, чужое — в браузере.
func (m *MainWindow) gotoLocation(loc string) {
	if path, ok := m.ownPath(loc); ok {
		if !m.openServerPath(path) {
			m.handleLink(strings.TrimSuffix(m.s.ServerURL, "/") + path)
		}
		return
	}
	m.handleLink(loc)
}

// openChannelByName — канал команды teamName; если его ещё нет в сайдбаре
// (только что вошли — /join), сайдбар перезагружается и канал откроется после.
func (m *MainWindow) openChannelByName(teamName, name string) {
	team := ""
	for _, t := range m.st.Teams() {
		if t.Name == teamName {
			team = t.Id
		}
	}
	if ch := m.st.ChannelByName(name, team); ch != nil && (team == "" || ch.TeamId == team) {
		if m.items[ch.Id] != nil {
			m.selectChannel(ch.Id)
			return
		}
		if ch.DeleteAt > 0 {
			m.openArchived(ch, func() { m.selectChannel(ch.Id) })
			return
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		ch, _, err := m.s.Client.GetChannelByNameForTeamNameIncludeDeleted(ctx, name, teamName, "")
		m.ui(func() {
			if err != nil {
				m.fail("open channel", fmt.Errorf("channel %s in team %s: %w", name, teamName, err))
				return
			}
			if ch.DeleteAt > 0 {
				m.openArchived(ch, func() { m.selectChannel(ch.Id) })
				return
			}
			m.selectAfterLoad = ch.Id
			m.reloadSidebar()
		})
	}()
}

// openDMByName — личка с пользователем username.
func (m *MainWindow) openDMByName(username string) {
	if u := m.st.UserByName(username); u != nil {
		m.openDM(u.Id)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		u, _, err := m.s.Client.GetUserByUsername(ctx, username, "")
		m.ui(func() {
			if err != nil {
				m.fail("open direct channel", fmt.Errorf("user %s: %w", username, err))
				return
			}
			m.st.AddUsers([]*model.User{u})
			m.openDM(u.Id)
		})
	}()
}

// openPermalink — к сообщению postID (channelID — его канал, если известен).
func (m *MainWindow) openPermalink(channelID, postID string) {
	if p := m.knownPost(channelID, postID); p != nil {
		m.goToPost(p)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		p, _, err := m.s.Client.GetPost(ctx, postID, "")
		m.ui(func() {
			if err != nil {
				m.log.Warn("open permalink", "post", postID, "err", err)
				m.message("Сообщение недоступно: удалено или в канале, где вас нет")
				return
			}
			m.goToPost(p)
		})
	}()
}

// knownPost — пост из загруженных лент канала и треда.
func (m *MainWindow) knownPost(channelID, postID string) *model.Post {
	if tl := m.st.Timeline(channelID); channelID != "" && tl != nil {
		if p := tl.Get(postID); p != nil {
			return p
		}
	}
	if t := m.thread; t != nil {
		return t.tl.Get(postID)
	}
	return nil
}

// goToPost — канал сообщения p, прокрутка к нему и подсветка; ответ в треде
// — ещё и в панели треда (при CRT в ленте канала — его корень).
func (m *MainWindow) goToPost(p *model.Post) {
	if m.items[p.ChannelId] == nil {
		m.withArchived(p.ChannelId, func() { m.goToPost(p) }) // в архиве — только читать
		return
	}
	inChannel := p.Id
	if p.RootId != "" {
		m.openThreadAt(p.RootId, p.Id)
		if m.st.CRT() {
			inChannel = p.RootId
		}
	}
	if m.current != p.ChannelId {
		m.scrollTo = inChannel // покажет showTimeline — сразу или после загрузки
		m.selectChannel(p.ChannelId)
		return
	}
	m.reveal(inChannel)
}

// reveal — пост id в открытом канале: прокрутка и подсветка; нет в
// загруженной ленте — догружаем старое, пока не найдётся.
func (m *MainWindow) reveal(id string) {
	if m.feed.Has(id) {
		m.feed.Highlight(id) // перерисовка поста — до прокрутки: она ставит курсор
		m.feed.ScrollToPost(id)
		return
	}
	m.loadOlderUntil(id, revealPages)
}

func (m *MainWindow) loadOlderUntil(id string, left int) {
	ch := m.current
	tl := m.st.Timeline(ch)
	if tl == nil || !tl.Loaded {
		m.scrollTo = id // лента ещё грузится: покажет showTimeline
		return
	}
	if !tl.HasMore || left == 0 {
		m.message("Сообщение не нашлось в истории канала")
		return
	}
	m.feed.LoadingOlder()
	m.loadOlderThen(func() {
		if m.current != ch {
			return
		}
		if m.feed.Has(id) {
			m.reveal(id)
		} else {
			m.loadOlderUntil(id, left-1)
		}
	})
}
