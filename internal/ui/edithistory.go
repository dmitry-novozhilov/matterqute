package ui

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
)

// История правок своего сообщения — щелчок по «(изменено)» (как в
// веб-клиенте; чужую историю сервер не отдаёт): окно с нынешним текстом и
// прежними версиями, новые сверху, у каждой — когда; прежнюю можно
// восстановить (PatchPost её текстом — это тоже правка).

const historyRestore = "mm-restore:" // mm-restore:<N> — восстановить N-ю прежнюю версию

// editHistoryView — окно истории (для тестов).
type editHistoryView struct {
	d        *dialog
	text     *qt.QTextBrowser
	versions []*model.Post // прежние, новые первыми
	restore  func(i int)
}

func (m *MainWindow) showEditHistory(p *model.Post) *editHistoryView {
	d := newDialog(m.w, "История правок")
	m.channelDialog = d
	d.w.SetMinimumSize2(520, 420)
	v := &editHistoryView{d: d, text: qt.NewQTextBrowser2()}
	v.text.SetOpenLinks(false)
	v.text.SetHtml(html.EscapeString("Загружаем…"))
	d.lay.AddWidget2(v.text.QWidget, 1)
	d.bottom.AddStretch()
	closeBtn := qt.NewQPushButton3("Закрыть")
	closeBtn.OnClicked(func() { d.w.Close() })
	d.bottom.AddWidget(closeBtn.QWidget)
	v.restore = func(i int) {
		if i < 0 || i >= len(v.versions) {
			return
		}
		text := v.versions[i].Message
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			edited, _, err := m.s.Client.PatchPost(ctx, p.Id, &model.PostPatch{Message: &text})
			m.ui(func() {
				if err != nil {
					m.fail("restore post version", fmt.Errorf("restore version of post %s: %w", p.Id, err))
					return
				}
				m.applyEdited(edited)
				if !d.closed {
					d.w.Close()
				}
				m.message("Прежняя версия восстановлена")
			})
		}()
	}
	v.text.OnAnchorClicked(func(u *qt.QUrl) {
		if s := u.ToString(); strings.HasPrefix(s, historyRestore) {
			var i int
			fmt.Sscan(strings.TrimPrefix(s, historyRestore), &i)
			v.restore(i)
		}
	})
	d.w.Show()

	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		list, resp, err := m.s.Client.GetEditHistoryForPost(ctx, p.Id)
		m.ui(func() {
			if d.closed {
				return
			}
			switch {
			case err != nil && resp != nil && resp.StatusCode == http.StatusNotFound:
				list = nil // правок, о которых знает сервер, нет
			case err != nil:
				m.log.Warn("edit history", "post", p.Id, "err", err)
				v.text.SetHtml(html.EscapeString("Не загрузилось: " + commandError(err)))
				return
			}
			v.versions = list
			v.text.SetHtml(m.editHistoryHTML(p, list))
		})
	}()
	return v
}

// editHistoryHTML — нынешний текст и прежние версии, у каждой — когда.
func (m *MainWindow) editHistoryHTML(p *model.Post, old []*model.Post) string {
	pal := Pal()
	md := markdown.New(markdown.Options{Me: m.s.Me.Username, Palette: pal})
	now := time.Now()
	var b strings.Builder
	head := func(label string, at int64, extra string) {
		fmt.Fprintf(&b, `<p style="margin-top:12px;margin-bottom:2px"><span style="font-size:12px;font-weight:600;color:%s">%s</span>`, pal.Tx2, html.EscapeString(label))
		if at > 0 {
			fmt.Fprintf(&b, `<span style="font-size:12px;color:%s"> · %s</span>`, pal.Tx2, html.EscapeString(shortWhen(time.UnixMilli(at), now)))
		}
		b.WriteString(extra + "</p>")
	}
	head("Сейчас", p.EditAt, "")
	b.WriteString(md.Render(p.Message))
	if len(old) == 0 {
		fmt.Fprintf(&b, `<p style="color:%s">Прежних версий сервер не хранит.</p>`, pal.Tx2)
	}
	for i, o := range old {
		at := o.EditAt
		if at == 0 {
			at = o.CreateAt // самая первая версия — когда написано
		}
		restore := fmt.Sprintf(` · <a href="%s%d" style="font-size:12px;color:%s;text-decoration:none">Восстановить эту версию</a>`, historyRestore, i, pal.Acc)
		head("Было", at, restore)
		b.WriteString(md.Render(o.Message))
	}
	return b.String()
}
