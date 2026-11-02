package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Слэш-команды («/away», «/join ~канал», «/msg @bob …»): выполняет сервер
// (/commands/execute), в треде — с root_id. Ответ «только вам» сервер шлёт
// событием ephemeral_message (applyEphemeral), а в ответе на запрос — куда
// перейти (goto_location: /join, /msg).

const commandNotFound = "api.command.execute_command.not_found.app_error"

// runCommand выполняет text из поля c в канале channelID (rootID — в треде).
func (m *MainWindow) runCommand(c *Composer, channelID, rootID, text string) {
	team := m.teamOf(channelID)
	args := &model.CommandArgs{ChannelId: channelID, TeamId: team, RootId: rootID, Command: text}
	data, _ := json.Marshal(args)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		var resp *model.CommandResponse
		r, err := m.s.Client.DoAPIPost(ctx, "/commands/execute", string(data))
		if err == nil {
			defer r.Body.Close()
			resp, err = model.CommandResponseFromJSON(r.Body)
		}
		m.ui(func() {
			if err != nil {
				c.Restore()
				var appErr *model.AppError
				if errors.As(err, &appErr) && appErr.Id == commandNotFound {
					c.OfferAsText(text)
					return
				}
				m.log.Warn("command", "command", truncate(text, 200), "channel", channelID, "err", err)
				c.ShowHint("Команда не выполнена: " + commandError(err))
				return
			}
			c.Sent()
			if resp.GotoLocation != "" {
				m.gotoLocation(resp.GotoLocation)
			}
		})
	}()
}

// commandError — что сказал сервер, без технических подробностей.
func commandError(err error) string {
	var appErr *model.AppError
	if errors.As(err, &appErr) && appErr.Message != "" {
		return appErr.Message
	}
	return err.Error()
}

// teamOf — команда канала; у личек её нет — текущая (первая) команда.
func (m *MainWindow) teamOf(channelID string) string {
	if ch := m.st.Channel(channelID); ch != nil && ch.TeamId != "" {
		return ch.TeamId
	}
	if ts := m.st.Teams(); len(ts) > 0 {
		return ts[0].Id
	}
	return ""
}

var (
	channelPathRe   = regexp.MustCompile(`^/([^/]+)/channels/([^/?#]+)`)
	messagesPathRe  = regexp.MustCompile(`^/([^/]+)/messages/@([^/?#]+)`)
	permalinkPathRe = regexp.MustCompile(`^/([^/]+)/pl/([a-z0-9]{26})`)
)

// gotoLocation — переход из ответа команды: канал, личка, сообщение своего
// сервера; остальное — в браузере.
func (m *MainWindow) gotoLocation(loc string) {
	path := loc
	if u, err := url.Parse(loc); err == nil && u.Scheme != "" {
		own, _ := url.Parse(m.s.ServerURL)
		if own == nil || !strings.EqualFold(u.Host, own.Host) {
			m.handleLink(loc)
			return
		}
		path = strings.TrimPrefix(u.Path, strings.TrimSuffix(own.Path, "/"))
	}
	switch {
	case channelPathRe.MatchString(path):
		g := channelPathRe.FindStringSubmatch(path)
		m.openChannelByName(g[1], g[2])
	case messagesPathRe.MatchString(path):
		name := messagesPathRe.FindStringSubmatch(path)[2]
		if u := m.st.UserByName(name); u != nil {
			m.openDM(u.Id)
		} else {
			m.handleLink(strings.TrimSuffix(m.s.ServerURL, "/") + path)
		}
	case permalinkPathRe.MatchString(path):
		m.openPostByID(permalinkPathRe.FindStringSubmatch(path)[2])
	default:
		m.handleLink(strings.TrimSuffix(m.s.ServerURL, "/") + path)
	}
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
	if ch := m.st.ChannelByName(name, team); ch != nil && (team == "" || ch.TeamId == team) && m.items[ch.Id] != nil {
		m.selectChannel(ch.Id)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		ch, _, err := m.s.Client.GetChannelByNameForTeamName(ctx, name, teamName, "")
		m.ui(func() {
			if err != nil {
				m.fail("open channel", fmt.Errorf("channel %s in team %s: %w", name, teamName, err))
				return
			}
			m.selectAfterLoad = ch.Id
			m.reloadSidebar()
		})
	}()
}

// openPostByID — к сообщению по id (ссылка на него): его канал и прокрутка.
func (m *MainWindow) openPostByID(id string) {
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		p, _, err := m.s.Client.GetPost(ctx, id, "")
		m.ui(func() {
			if err != nil {
				m.fail("open post", fmt.Errorf("post %s: %w", id, err))
				return
			}
			m.openPermalink(p.ChannelId, p.Id)
		})
	}()
}

// applyEphemeral — сообщение «только вам» (ответ команды): в ленту канала и
// треда, без счётчиков непрочитанного; до перезагрузки ленты.
func (m *MainWindow) applyEphemeral(p *model.Post) {
	if p.Type == "" {
		p.Type = model.PostTypeEphemeral
	}
	if tl := m.st.Timeline(p.ChannelId); tl != nil && tl.Loaded && (p.RootId == "" || !m.st.CRT()) {
		tl.Upsert(p)
		if p.ChannelId == m.current {
			m.feed.Upsert(p)
		}
	}
	if t := m.thread; t != nil && p.RootId == t.rootID && t.tl.Loaded {
		t.tl.Upsert(p)
		m.panel.feed.Upsert(p)
	}
	if p.ChannelId != m.current && (m.thread == nil || p.RootId != m.thread.rootID) {
		m.message(firstLine(p.Message, 120)) // канал не на экране — хоть в строке состояния
	}
}

// commandIcon — значок подсказки команды.
func commandIcon() *qt.QPixmap { return iconPixmap("square-code", Pal().Tx2, 20) }
