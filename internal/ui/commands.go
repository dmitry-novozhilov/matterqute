package ui

import (
	"context"
	"encoding/json"
	"errors"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Слэш-команды («/away», «/join ~канал», «/msg @bob …»): выполняет сервер
// (/commands/execute), в треде — с root_id. Ответ «только вам» сервер шлёт
// событием ephemeral_message (applyEphemeral), а в ответе на запрос — куда
// перейти (goto_location: /join, /msg).

const commandNotFound = "api.command.execute_command.not_found.app_error"

// runCommand выполняет text из поля c в канале channelID (rootID — в треде).
func (m *MainWindow) runCommand(c *Composer, channelID, rootID, text string, d *draft) {
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
				c.Restore(d)
				var appErr *model.AppError
				if errors.As(err, &appErr) && appErr.Id == commandNotFound {
					c.OfferAsText(text)
					return
				}
				m.log.Warn("command", "command", truncate(text, 200), "channel", channelID, "err", err)
				c.ShowHint("Команда не выполнена: " + commandError(err))
				return
			}
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

// applyEphemeral — сообщение «только вам» (ответ команды): в ленту канала и
// треда, без счётчиков непрочитанного; до перезагрузки ленты.
func (m *MainWindow) applyEphemeral(p *model.Post) {
	if p.Type == "" {
		p.Type = model.PostTypeEphemeral
	}
	if tl := m.st.Timeline(p.ChannelId); tl != nil && tl.Loaded && (p.RootId == "" || !m.st.CRT()) {
		tl.Upsert(p)
		if f := m.feeds[p.ChannelId]; f != nil {
			f.Upsert(p)
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
