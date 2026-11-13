package ui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// «Профиль и уведомления» (меню «Файл», панель статуса): вкладка «Профиль» —
// имя, фамилия, псевдоним, должность, фото (сразу на сервер, как веб-клиент);
// «Уведомления» — по умолчанию для всех каналов (на них ссылается «Как в
// профиле» в меню канала): на рабочий стол, ответы в тредах, что считать
// упоминанием, письма. Сохранение — PatchUser с изменённым; настройки
// уведомлений сервер заменяет целиком — шлём их все.

// maxAvatar — больше не грузим: сервер всё равно уменьшит до 128 px.
const maxAvatar = 10 << 20

// profileForm — поля окна (для тестов).
type profileForm struct {
	d                                  *dialog
	first, last, nick, position, keys  *qt.QLineEdit
	all, mention, none                 *qt.QRadioButton
	threads, firstName, channel, email *qt.QCheckBox
	hint                               *qt.QLabel
	save                               func()
}

// notifyProp — действующая настройка уведомлений профиля: своя или, если
// её нет, умолчание сервера (User.SetDefaultNotifications).
func notifyProp(me *model.User, key string) string {
	if v, ok := me.NotifyProps[key]; ok {
		return v
	}
	def := &model.User{}
	def.SetDefaultNotifications()
	return def.NotifyProps[key]
}

func (m *MainWindow) showProfile() *profileForm {
	me := m.me()
	props := func(key string) string { return notifyProp(me, key) }
	d := newDialog(m.w, "Профиль и уведомления")
	m.channelDialog = d
	d.w.SetMinimumWidth(480)
	f := &profileForm{d: d, hint: qt.NewQLabel2()}
	tabs := qt.NewQTabWidget2()

	// Профиль.
	page := qt.NewQWidget2()
	pl := qt.NewQVBoxLayout(page)
	pl.SetSpacing(6)
	avatar := qt.NewQLabel2()
	drawAvatar := func() {
		u := m.me()
		avatar.SetPixmap(avatarPixmap(m.assets, u, u.Username, u.IsBot, 64, nil))
	}
	drawAvatar()
	upload := qt.NewQPushButton3("Загрузить фото…")
	reset := qt.NewQPushButton3("Убрать фото")
	reset.SetEnabled(me.LastPictureUpdate > 0)
	photo := qt.NewQVBoxLayout2()
	photo.AddWidget(upload.QWidget)
	photo.AddWidget(reset.QWidget)
	photo.AddStretch()
	top := qt.NewQHBoxLayout2()
	top.SetSpacing(12)
	top.AddWidget(avatar.QWidget)
	name := qt.NewQLabel3("@" + me.Username)
	name.SetProperty("role", tmp(qt.NewQVariant11("hint")))
	top.AddWidget2(name.QWidget, 1)
	top.AddLayout(photo.QLayout)
	pl.AddLayout(top.QLayout)
	field := func(label, value string) *qt.QLineEdit {
		e := qt.NewQLineEdit3(value)
		pl.AddWidget(fieldLabel(label).QWidget)
		pl.AddWidget(e.QWidget)
		return e
	}
	f.first = field("Имя", me.FirstName)
	f.last = field("Фамилия", me.LastName)
	f.nick = field("Псевдоним", me.Nickname)
	f.position = field("Должность", me.Position)
	pl.AddStretch()
	tabs.AddTab(page, "Профиль")

	// Уведомления.
	page = qt.NewQWidget2()
	nl := qt.NewQVBoxLayout(page)
	nl.SetSpacing(6)
	nl.AddWidget(fieldLabel("Уведомления на рабочий стол").QWidget)
	f.all = qt.NewQRadioButton3("Обо всех сообщениях")
	f.mention = qt.NewQRadioButton3("Об упоминаниях и личных сообщениях")
	f.none = qt.NewQRadioButton3("Не присылать")
	switch props(model.DesktopNotifyProp) {
	case model.UserNotifyMention:
		f.mention.SetChecked(true)
	case model.UserNotifyNone:
		f.none.SetChecked(true)
	default:
		f.all.SetChecked(true)
	}
	for _, b := range []*qt.QRadioButton{f.all, f.mention, f.none} {
		nl.AddWidget(b.QWidget)
	}
	f.threads = qt.NewQCheckBox3("Об ответах в тредах, на которые я подписан")
	f.threads.SetChecked(props(model.DesktopThreadsNotifyProp) != model.UserNotifyMention)
	f.threads.SetVisible(m.st.CRT())
	nl.AddWidget(f.threads.QWidget)
	nl.AddSpacing(8)
	nl.AddWidget(fieldLabel("Упоминанием считать — кроме @" + me.Username).QWidget)
	f.firstName = qt.NewQCheckBox3("Моё имя «" + me.FirstName + "»")
	f.firstName.SetChecked(props(model.FirstNameNotifyProp) == "true")
	f.firstName.SetVisible(me.FirstName != "")
	f.channel = qt.NewQCheckBox3("@channel, @here и @all")
	f.channel.SetChecked(props(model.ChannelMentionsNotifyProp) != "false")
	f.keys = qt.NewQLineEdit3(strings.Join(customKeys(me), ", "))
	f.keys.SetPlaceholderText("Другие слова через запятую")
	nl.AddWidget(f.firstName.QWidget)
	nl.AddWidget(f.channel.QWidget)
	nl.AddWidget(f.keys.QWidget)
	nl.AddSpacing(8)
	f.email = qt.NewQCheckBox3("Письма об упоминаниях и личных сообщениях, пока меня нет")
	f.email.SetChecked(props(model.EmailNotifyProp) != "false")
	nl.AddWidget(f.email.QWidget)
	nl.AddStretch()
	tabs.AddTab(page, "Уведомления")

	d.lay.AddWidget(tabs.QWidget)
	f.hint.SetProperty("role", tmp(qt.NewQVariant11("hint")))
	f.hint.SetWordWrap(true)
	d.lay.AddWidget(f.hint.QWidget)

	refreshMe := func(done func()) {
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			u, _, err := m.s.Client.GetMe(ctx, "")
			m.ui(func() {
				if err == nil {
					m.setMe(u)
				}
				done()
			})
		}()
	}
	upload.OnClicked(func() {
		path := qt.QFileDialog_GetOpenFileName4(d.w.QWidget, "Фото профиля", "", "Картинки (*.png *.jpg *.jpeg *.gif *.bmp *.webp)")
		if path == "" {
			return
		}
		data, err := os.ReadFile(path)
		switch {
		case err != nil:
			f.hint.SetText("Не прочитать файл: " + err.Error())
			return
		case len(data) > maxAvatar:
			f.hint.SetText(fmt.Sprintf("Файл больше %d МБ", maxAvatar>>20))
			return
		case len(data) == 0 || !tmp(qt.NewQImage()).LoadFromData2(&data[0], len(data)):
			f.hint.SetText("Это не картинка: " + path)
			return
		}
		f.hint.SetText("Загружаем фото…")
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			_, err := m.s.Client.SetProfileImage(ctx, m.s.Me.Id, data)
			m.ui(func() {
				if err != nil {
					m.log.Warn("set profile image", "err", err)
					if !d.closed {
						f.hint.SetText("Фото не загрузилось: " + commandError(err))
					}
					return
				}
				refreshMe(func() {
					if !d.closed {
						f.hint.SetText("Фото обновлено")
						drawAvatar()
						reset.SetEnabled(true)
					}
				})
			})
		}()
	})
	reset.OnClicked(func() {
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			_, err := m.s.Client.SetDefaultProfileImage(ctx, m.s.Me.Id)
			m.ui(func() {
				if err != nil {
					m.log.Warn("reset profile image", "err", err)
					if !d.closed {
						f.hint.SetText("Не вышло: " + commandError(err))
					}
					return
				}
				refreshMe(func() {
					if !d.closed {
						drawAvatar()
						reset.SetEnabled(false)
					}
				})
			})
		}()
	})

	d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { d.w.Close() })
	ok := qt.NewQPushButton3("Сохранить")
	ok.SetProperty("primary", tmp(qt.NewQVariant11("true")))
	ok.SetDefault(true)
	f.save = func() {
		patch, changed := f.patch(m.me(), m.st.CRT())
		if !changed {
			d.w.Close()
			return
		}
		ok.SetEnabled(false)
		f.hint.SetText("Сохраняем…")
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
			defer cancel()
			u, _, err := m.s.Client.PatchUser(ctx, m.s.Me.Id, patch)
			m.ui(func() {
				if err != nil {
					m.log.Warn("patch profile", "err", err)
					if !d.closed {
						ok.SetEnabled(true)
						f.hint.SetText("Не сохранено: " + commandError(err))
					}
					return
				}
				m.setMe(u)
				if !d.closed {
					d.w.Close()
				}
				m.message("Профиль сохранён")
			})
		}()
	}
	ok.OnClicked(f.save)
	d.bottom.AddWidget(cancel.QWidget)
	d.bottom.AddWidget(ok.QWidget)
	d.w.Show()
	return f
}

// patch — что поменялось в окне относительно профиля me; уведомления — все
// настройки целиком (сервер заменяет их, а не дополняет).
func (f *profileForm) patch(me *model.User, crt bool) (*model.UserPatch, bool) {
	p := &model.UserPatch{}
	changed := false
	text := func(dst **string, e *qt.QLineEdit, was string) {
		if v := strings.TrimSpace(e.Text()); v != was {
			*dst = &v
			changed = true
		}
	}
	text(&p.FirstName, f.first, me.FirstName)
	text(&p.LastName, f.last, me.LastName)
	text(&p.Nickname, f.nick, me.Nickname)
	text(&p.Position, f.position, me.Position)

	props := model.StringMap{}
	for k, v := range me.NotifyProps {
		props[k] = v
	}
	notify := false
	set := func(key, value string) {
		if notifyProp(me, key) != value { // «нет» — то же, что умолчание сервера
			notify = true
		}
		// Явно: пустую настройку приложение (ShouldNotify, меню канала) и
		// сервер понимают по-разному.
		props[key] = value
	}
	switch {
	case f.mention.IsChecked():
		set(model.DesktopNotifyProp, model.UserNotifyMention)
	case f.none.IsChecked():
		set(model.DesktopNotifyProp, model.UserNotifyNone)
	default:
		set(model.DesktopNotifyProp, model.UserNotifyAll)
	}
	if crt {
		set(model.DesktopThreadsNotifyProp, map[bool]string{true: model.UserNotifyAll, false: model.UserNotifyMention}[f.threads.IsChecked()])
	}
	bools := map[bool]string{true: "true", false: "false"}
	if me.FirstName != "" {
		set(model.FirstNameNotifyProp, bools[f.firstName.IsChecked()])
	}
	set(model.ChannelMentionsNotifyProp, bools[f.channel.IsChecked()])
	set(model.EmailNotifyProp, bools[f.email.IsChecked()])
	var keys []string
	for _, k := range strings.Split(f.keys.Text(), ",") {
		if k = strings.TrimSpace(k); k != "" && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	if !slices.Equal(keys, customKeys(me)) {
		notify = true
	}
	props[model.MentionKeysNotifyProp] = strings.Join(keys, ",")
	if notify {
		p.NotifyProps = props
		changed = true
	}
	return p, changed
}

// customKeys — свои слова-упоминания (без имени пользователя: оно — всегда).
func customKeys(me *model.User) []string {
	var out []string
	for _, k := range strings.Split(me.NotifyProps[model.MentionKeysNotifyProp], ",") {
		k = strings.TrimSpace(k)
		if k != "" && !strings.EqualFold(k, me.Username) && !strings.EqualFold(k, "@"+me.Username) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}
