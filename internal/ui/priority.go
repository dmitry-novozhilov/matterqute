package ui

import (
	"fmt"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// Метки «Важно» и «Срочно» (post.metadata.priority, как в веб-клиенте):
// у сообщения — плашкой в строке автора (сообщение с меткой не
// склеивается с предыдущим — строка автора у него всегда), в уведомлении —
// перед заголовком. Ставит поле ввода канала кнопкой с восклицательным
// знаком, если сервер их разрешает (PostPriority); в тредах сервер метки не
// принимает — там кнопки нет. Подтверждение прочтения и настойчивые
// уведомления (с лицензией) — нет.

const priorityImportant = "important"

// postPriority — метка поста: "", important или urgent.
func postPriority(p *model.Post) string {
	if pr := p.GetPriority(); pr != nil && pr.Priority != nil {
		switch *pr.Priority {
		case priorityImportant, model.PostPriorityUrgent:
			return *pr.Priority
		}
	}
	return ""
}

// priorityLook — подпись метки и её цвета: текст и фон.
func priorityLook(pr string) (label, fg, bg string) {
	pal := Pal()
	switch pr {
	case priorityImportant:
		return "Важно", pal.Warn, pal.WarnBg
	case model.PostPriorityUrgent:
		return "Срочно", pal.Dang, pal.DangBg
	}
	return "", "", ""
}

// priorityHTML — плашка метки для строки автора; "" — метки нет.
func priorityHTML(pr string) string {
	label, fg, bg := priorityLook(pr)
	if label == "" {
		return ""
	}
	return fmt.Sprintf(`&nbsp;<span style="font-size:11px;font-weight:600;background-color:%s;color:%s">&nbsp;%s&nbsp;</span>`,
		bg, fg, strings.ToUpper(label))
}

// withPriority — метка pr в новый пост.
func withPriority(p *model.Post, pr string) *model.Post {
	if pr != "" {
		if p.Metadata == nil {
			p.Metadata = &model.PostMetadata{}
		}
		p.Metadata.Priority = &model.PostPriority{Priority: model.NewPointer(pr)}
	}
	return p
}

// buildPriority — кнопка метки (справа в поле) и строка с меткой над текстом.
func (c *Composer) buildPriority() {
	c.prioBtn = qt.NewQToolButton2()
	setName(c.prioBtn.QObject, "priority")
	c.prioBtn.SetFixedSize2(28, 28)
	c.prioBtn.SetIconSize(tmp(qt.NewQSize2(18, 18)))
	c.prioBtn.SetFocusPolicy(qt.NoFocus)
	c.prioBtn.SetToolTip("Метка сообщения: «Важно», «Срочно»")
	c.prioBtn.SetVisible(false)
	c.prioBtn.OnClicked(func() {
		menu := c.priorityMenu()
		menu.SetAttribute(qt.WA_DeleteOnClose)
		menu.Popup(c.prioBtn.MapToGlobalWithQPoint(tmp(qt.NewQPoint2(0, -menu.SizeHint().Height())))) // над полем
	})
	c.prioNote = qt.NewQLabel2()
	c.prioNote.SetTextFormat(qt.RichText)
	c.prioNote.SetVisible(false)
	c.prioNote.OnLinkActivated(func(string) {
		c.setPriority("")
		c.edit.SetFocus()
	})
}

// priorityMenu — «Обычное», «Важное», «Срочное»; текущее отмечено.
func (c *Composer) priorityMenu() *qt.QMenu {
	menu := qt.NewQMenu(c.w)
	var items []menuItem
	for _, x := range []struct{ pr, title string }{{"", "Обычное"}, {priorityImportant, "Важное"}, {model.PostPriorityUrgent, "Срочное"}} {
		items = append(items, menuItem{title: x.title, check: checkIf(c.priority == x.pr), do: func() { c.setPriority(x.pr) }})
	}
	fillMenu(menu, items)
	return menu
}

// SetPriorities — можно ли ставить метки (сервер разрешает).
func (c *Composer) SetPriorities(on bool) {
	c.priorities = on
	if !on {
		c.setPriority("")
	}
	c.updatePriority()
}

// setPriority — метка следующего сообщения.
func (c *Composer) setPriority(pr string) {
	c.priority = pr
	c.updatePriority()
}

// updatePriority — кнопка (видна, если метки можно ставить и это не правка)
// и строка с меткой над текстом.
func (c *Composer) updatePriority() {
	c.prioBtn.SetVisible(c.priorities && c.editID == "")
	label, fg, bg := priorityLook(c.priority)
	color := Pal().Tx2
	if label != "" {
		color = fg
		c.prioNote.SetText(fmt.Sprintf(`<span style="font-size:11px;font-weight:600;background-color:%s;color:%s">&nbsp;%s&nbsp;</span>`+
			`&nbsp;&nbsp;<a href="clear" style="font-size:12px;color:%s;text-decoration:none">убрать</a>`,
			bg, fg, strings.ToUpper(label), Pal().Tx2))
	}
	c.prioNote.SetVisible(label != "" && c.editID == "")
	c.prioBtn.SetIcon(icon("circle-alert", color, 18))
}
