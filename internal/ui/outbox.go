package ui

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// Отправка без сети: сообщение, которое не ушло из-за связи (её нет, запрос
// не дошёл или прокси ответил, что сервер недоступен), встаёт в очередь — в
// ленте оно сразу, со строкой «Не отправлено — уйдёт, когда появится связь»,
// поле ввода чистое. Связь вернулась (OnConnected) — очередь уходит по
// порядку. У каждого — pending_post_id, как у веб-клиента: пришедший пост с
// тем же pending_post_id (ответ или WebSocket) снимает его из очереди, а
// повтор того, что дошло до сервера перед обрывом, сервер не задваивает (по
// памяти о сервере — если повтор в пределах ~30 с; не проверено). Отказ
// сервера — текст ошибки у сообщения, «Повторить» и «Удалить». Очередь — в
// снимке окна (state.json): переживает перезапуск. В store её нет: ленты
// сверяются с сервером, и сообщения из очереди там пропали бы.

const (
	outboxRetryLink  = "mm-outbox-retry:"  // mm-outbox-retry:<id> — отправить снова
	outboxDeleteLink = "mm-outbox-delete:" // mm-outbox-delete:<id> — убрать из очереди
)

type outbox struct {
	items   []*queued
	sending bool  // идёт отправка очереди: по одному, по порядку
	last    int64 // время в последнем pending_post_id: два в одну миллисекунду совпали бы
}

type queued struct {
	post *model.Post // Id = PendingPostId: под ним сообщение в лентах до ответа сервера
	err  string      // отказ сервера: ждёт «Повторить» или «Удалить»
}

// isQueued — сообщение из очереди отправки (ещё не на сервере).
func isQueued(p *model.Post) bool { return p.PendingPostId != "" && p.Id == p.PendingPostId }

func (o *outbox) find(id string) *queued {
	if i := slices.IndexFunc(o.items, func(q *queued) bool { return q.post.Id == id }); i >= 0 {
		return o.items[i]
	}
	return nil
}

func (o *outbox) remove(id string) {
	o.items = slices.DeleteFunc(o.items, func(q *queued) bool { return q.post.Id == id })
}

// posts — очередь для снимка.
func (o *outbox) posts() []*model.Post {
	var out []*model.Post
	for _, q := range o.items {
		out = append(out, q.post)
	}
	return out
}

// newPost — сообщение к отправке: с pending_post_id, как у веб-клиента
// («пользователь:время», время — строго растёт); в ленте до ответа сервера
// оно под этим id.
func (m *MainWindow) newPost(p *model.Post) *model.Post {
	now := max(model.GetMillis(), m.outbox.last+1)
	m.outbox.last = now
	p.PendingPostId = fmt.Sprintf("%s:%d", m.s.Me.Id, now)
	p.UserId, p.CreateAt = m.s.Me.Id, now
	return p
}

// offline — запрос не дошёл до сервера: ответа нет (нет связи, таймаут) или
// прокси говорит, что сервер недоступен.
func offline(resp *model.Response, err error) bool {
	if err == nil {
		return false
	}
	if resp == nil || resp.StatusCode == 0 {
		return true
	}
	switch resp.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// createPost — отправить p (из поля ввода): связи нет — сразу в очередь;
// не дошло — в очередь; ушло — sent и пост в ленты; отказ сервера — failed.
func (m *MainWindow) createPost(p *model.Post, sent func(), failed func(err error)) {
	if !m.online {
		m.enqueue(p)
		sent()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		created, resp, err := m.s.Client.CreatePost(ctx, p)
		m.ui(func() {
			switch {
			case offline(resp, err):
				m.log.Warn("send: no connection, queued", "channel", p.ChannelId, "err", err)
				m.enqueue(p)
				sent()
			case err != nil:
				failed(err)
			default:
				sent()
				m.applyPosted(store.NewPost{Post: created}) // тот же придёт и по WebSocket — не задублируется
			}
		})
	}()
}

// enqueue — p в очередь и в ленты.
func (m *MainWindow) enqueue(p *model.Post) {
	p.Id = p.PendingPostId
	m.outbox.items = append(m.outbox.items, &queued{post: p})
	m.showQueued(p)
}

// showQueued — сообщение из очереди в ленту канала (ответ — если треды не
// свёрнуты) и в панель его треда.
func (m *MainWindow) showQueued(p *model.Post) {
	if f := m.feeds[p.ChannelId]; f != nil && f.ready && (p.RootId == "" || !m.st.CRT()) {
		f.Upsert(p)
	}
	if t := m.thread; t != nil && p.RootId == t.rootID && t.tl.Loaded {
		m.panel.feed.Upsert(p)
	}
}

// hideQueued — сообщение id из очереди — прочь из лент.
func (m *MainWindow) hideQueued(id string) {
	for _, f := range m.feeds {
		if f.Has(id) {
			f.Remove(id)
		}
	}
	if m.panel.feed.Has(id) {
		m.panel.feed.Remove(id)
	}
}

// requeueIn — лента канала (rootID пусто) или треда построена заново: её
// сообщения из очереди — снова в ней.
func (m *MainWindow) requeueIn(f *Feed, channelID, rootID string) {
	for _, q := range m.outbox.items {
		p := q.post
		if p.ChannelId != channelID {
			continue
		}
		if rootID != "" && p.RootId == rootID || rootID == "" && (p.RootId == "" || !m.st.CRT()) {
			f.Upsert(p)
		}
	}
}

// queuedPosted — пришёл пост с pending_post_id из очереди: он на сервере.
func (m *MainWindow) queuedPosted(p *model.Post) {
	if p.PendingPostId == "" || m.outbox.find(p.PendingPostId) == nil {
		return
	}
	m.outbox.remove(p.PendingPostId)
	m.hideQueued(p.PendingPostId)
}

// queuedState — для ленты: сообщение id в очереди и отказ сервера ("" — ждёт связи).
func (m *MainWindow) queuedState(id string) (failed string, ok bool) {
	if q := m.outbox.find(id); q != nil {
		return q.err, true
	}
	return "", false
}

// flushOutbox — связь есть: очередь — на сервер, по одному, по порядку;
// отказавшие ждут «Повторить».
func (m *MainWindow) flushOutbox() {
	o := m.outbox
	if o.sending || !m.online {
		return
	}
	i := slices.IndexFunc(o.items, func(q *queued) bool { return q.err == "" })
	if i < 0 {
		return
	}
	q := o.items[i]
	o.sending = true
	send := q.post.Clone()
	send.Id = "" // новый пост: id даст сервер, pending_post_id — тот же
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		created, resp, err := m.s.Client.CreatePost(ctx, send)
		m.ui(func() {
			o.sending = false
			switch {
			case offline(resp, err):
				m.log.Warn("send queued: still no connection", "err", err)
				return // ждём OnConnected
			case err != nil:
				if o.find(q.post.Id) != nil {
					m.log.Warn("send queued", "channel", q.post.ChannelId, "err", err)
					q.err = commandError(err)
					m.refreshQueued(q.post)
				}
			default:
				created.PendingPostId = q.post.PendingPostId
				m.queuedPosted(created)
				m.applyPosted(store.NewPost{Post: created})
			}
			m.flushOutbox()
		})
	}()
}

// refreshQueued — состояние сообщения из очереди сменилось: перерисовать.
func (m *MainWindow) refreshQueued(p *model.Post) {
	for _, f := range m.feeds {
		if f.Has(p.Id) {
			f.Upsert(p)
		}
	}
	if m.panel.feed.Has(p.Id) {
		m.panel.feed.Upsert(p)
	}
}

// outboxAction — «Повторить» (retry) или «Удалить» у сообщения из очереди.
func (m *MainWindow) outboxAction(id string, retry bool) {
	q := m.outbox.find(id)
	if q == nil {
		return
	}
	if !retry {
		m.outbox.remove(id)
		m.hideQueued(id)
		return
	}
	q.err = ""
	m.refreshQueued(q.post)
	m.flushOutbox()
}

// restoreOutbox — очередь из снимка: в ленты (когда построятся) и на сервер,
// когда будет связь.
func (m *MainWindow) restoreOutbox(posts []*model.Post) {
	for _, p := range posts {
		if p != nil && isQueued(p) && m.outbox.find(p.Id) == nil {
			m.outbox.items = append(m.outbox.items, &queued{post: p})
			m.showQueued(p)
		}
	}
}
