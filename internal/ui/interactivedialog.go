package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/markdown"
)

// Окна интеграций («Interactive dialogs»): интеграция по кнопке или
// слэш-команде просит сервер открыть окно, сервер шлёт событие open_dialog
// с описанием полей. «Отправить» — POST /actions/dialogs/submit; ответ —
// ошибки (общая и по полям — под ними), следующая форма (type=form, в том же
// окне) или ничего — окно закрывается. Закрыли без отправки, а интеграция
// просила (notify_on_cancel) — сообщаем об отмене.
//
// Поля: text (с подтипами: password скрыт, number — числом), textarea,
// select (варианты, люди, каналы; dynamic — текстом), bool, radio,
// checkbox_group, date, datetime, collapsible (раскрыто, поля подряд).
// file, checkbox_matrix, action_button — не умеем: подпись «откройте в
// веб-клиенте».

const (
	dialogSubmit = "dialog_submission"
	dialogCancel = "dialog_cancellation"
)

// interactiveDialog — открытое окно интеграции (для тестов — m.interactive).
type interactiveDialog struct {
	m      *MainWindow
	req    model.OpenDialogRequest
	d      *dialog
	scroll *qt.QScrollArea
	form   *qt.QWidget
	fields []*dialogField
	errMsg *qt.QLabel
	submit []*qt.QPushButton
	sent   bool // отправлено (или отправляется) — отмену не сообщать
}

// dialogField — поле формы: value — что отправить (ok=false — пусто).
type dialogField struct {
	el    model.DialogElement
	value func() (v any, ok bool)
	err   *qt.QLabel
}

// openInteractiveDialog — событие open_dialog: окно с полями формы.
func (m *MainWindow) openInteractiveDialog(req model.OpenDialogRequest) *interactiveDialog {
	v := &interactiveDialog{m: m, req: req}
	v.d = newDialog(m.w, req.Dialog.Title)
	m.interactive = v
	v.d.w.OnFinished(func(int) {
		if !v.sent && v.req.Dialog.NotifyOnCancel {
			v.sent = true
			v.post(model.SubmitDialogRequest{Type: dialogCancel, Cancelled: true}, nil)
		}
	})
	v.scroll = qt.NewQScrollArea2()
	v.scroll.SetWidgetResizable(true)
	v.scroll.SetFrameShape(qt.QFrame__NoFrame)
	v.scroll.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	v.d.lay.AddWidget2(v.scroll.QWidget, 1)
	v.errMsg = roleLabel("error")
	v.d.lay.AddWidget(v.errMsg.QWidget)
	v.build(req.Dialog)
	v.d.w.Show()
	return v
}

// build — форма dlg в окне (и заново — следующая форма от интеграции).
func (v *interactiveDialog) build(dlg model.Dialog) {
	v.req.Dialog = dlg
	v.d.w.SetWindowTitle(dlg.Title)
	switch dlg.Size {
	case model.DialogSizeLarge:
		v.d.w.SetMinimumWidth(800)
	case model.DialogSizeMedium:
		v.d.w.SetMinimumWidth(600)
	default:
		v.d.w.SetMinimumWidth(480)
	}
	if v.form != nil {
		v.form.DeleteLater()
	}
	v.form = qt.NewQWidget2()
	lay := qt.NewQVBoxLayout(v.form)
	lay.SetContentsMargins(0, 0, 8, 0)
	lay.SetSpacing(4)
	v.fields = nil
	if dlg.IntroductionText != "" {
		intro := qt.NewQLabel2()
		intro.SetTextFormat(qt.RichText)
		intro.SetWordWrap(true)
		intro.SetOpenExternalLinks(false)
		intro.OnLinkActivated(v.m.handleLink)
		intro.SetText(scaleFonts(markdown.New(markdown.Options{Me: v.m.s.Me.Username, Palette: Pal()}).Render(dlg.IntroductionText)))
		lay.AddWidget(intro.QWidget)
		lay.AddSpacing(8)
	}
	var buttons *model.DialogElement // варианты — кнопками отправки
	for i := range dlg.Elements {
		if el := &dlg.Elements[i]; dlg.SubmitButtons != "" && el.Name == dlg.SubmitButtons {
			buttons = el
			continue
		}
		v.addElement(lay, dlg.Elements[i])
	}
	lay.AddStretch()
	v.scroll.SetWidget(v.form)
	// Высота — по форме, но не выше 70 % экрана: дальше прокрутка.
	h := v.form.SizeHint().Height() + 4
	if sc := v.d.w.Screen(); sc != nil {
		h = min(h, sc.AvailableGeometry().Height()*7/10)
	}
	v.scroll.SetMinimumHeight(min(h, 560))
	v.errMsg.SetVisible(false)

	// Кнопки внизу — заново.
	for v.d.bottom.Count() > 0 {
		if it := v.d.bottom.TakeAt(0); it != nil && it.Widget() != nil {
			it.Widget().DeleteLater()
		}
	}
	v.submit = nil
	v.d.bottom.AddStretch()
	cancel := qt.NewQPushButton3("Отмена")
	cancel.OnClicked(func() { v.d.w.Close() })
	v.d.bottom.AddWidget(cancel.QWidget)
	add := func(label string, extra func(map[string]any)) {
		b := qt.NewQPushButton3(label)
		b.SetProperty("primary", tmp(qt.NewQVariant11("true")))
		b.OnClicked(func() { v.send(extra) })
		v.d.bottom.AddWidget(b.QWidget)
		v.submit = append(v.submit, b)
	}
	if buttons != nil && len(buttons.Options) > 0 {
		for _, o := range buttons.Options {
			if o == nil {
				continue
			}
			name, value := buttons.Name, o.Value
			add(o.Text, func(sub map[string]any) { sub[name] = value })
		}
	} else {
		label := dlg.SubmitLabel
		if label == "" {
			label = "Отправить"
		}
		add(label, nil)
		v.submit[0].SetDefault(true)
	}
}

// addElement — подпись, поле, подсказка и место для ошибки.
func (v *interactiveDialog) addElement(lay *qt.QVBoxLayout, el model.DialogElement) {
	if el.Type == "collapsible" {
		if el.DisplayName != "" {
			head := qt.NewQLabel3(el.DisplayName)
			head.SetProperty("role", tmp(qt.NewQVariant11("fieldLabel")))
			lay.AddSpacing(6)
			lay.AddWidget(head.QWidget)
		}
		if el.CollapsibleConfig != nil {
			for _, child := range el.CollapsibleConfig.Elements {
				v.addElement(lay, child)
			}
		}
		return
	}
	label := el.DisplayName
	if el.Optional {
		label += " (необязательно)"
	}
	lay.AddSpacing(6)
	lay.AddWidget(fieldLabel(label).QWidget)
	f := &dialogField{el: el, err: roleLabel("error")}
	w := v.widget(f)
	lay.AddWidget(w)
	if el.HelpText != "" {
		help := qt.NewQLabel3(el.HelpText)
		help.SetProperty("role", tmp(qt.NewQVariant11("hint")))
		help.SetWordWrap(true)
		lay.AddWidget(help.QWidget)
	}
	lay.AddWidget(f.err.QWidget)
	if f.value != nil {
		v.fields = append(v.fields, f)
	}
}

// widget — поле ввода по типу элемента; f.value — его значение.
func (v *interactiveDialog) widget(f *dialogField) *qt.QWidget {
	el := f.el
	switch el.Type {
	case "text":
		e := qt.NewQLineEdit3(el.Default)
		e.SetPlaceholderText(el.Placeholder)
		if el.MaxLength > 0 {
			e.SetMaxLength(el.MaxLength)
		}
		switch el.SubType {
		case "password":
			e.SetEchoMode(qt.QLineEdit__Password)
		case "number":
			e.SetValidator(qt.NewQDoubleValidator3(e.QObject).QValidator)
		}
		f.value = func() (any, bool) {
			s := e.Text()
			if s == "" {
				return nil, false
			}
			if el.SubType == "number" {
				if n, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64); err == nil {
					return n, true
				}
			}
			return s, true
		}
		return e.QWidget
	case "textarea":
		e := qt.NewQPlainTextEdit3(el.Default)
		e.SetPlaceholderText(el.Placeholder)
		e.SetTabChangesFocus(true)
		e.SetFixedHeight(e.FontMetrics().LineSpacing()*5 + 12)
		f.value = func() (any, bool) {
			s := e.ToPlainText()
			return s, s != ""
		}
		return e.QWidget
	case "bool":
		c := qt.NewQCheckBox3(el.Placeholder)
		c.SetChecked(el.Default == "true")
		f.value = func() (any, bool) { return c.IsChecked(), true }
		return c.QWidget
	case "radio":
		box := qt.NewQWidget2()
		bl := qt.NewQVBoxLayout(box)
		bl.SetContentsMargins(0, 0, 0, 0)
		group := qt.NewQButtonGroup2(box.QObject)
		var values []string
		for _, o := range el.Options {
			if o == nil {
				continue
			}
			r := qt.NewQRadioButton3(o.Text)
			r.SetChecked(o.Value == el.Default)
			group.AddButton2(r.QAbstractButton, len(values))
			values = append(values, o.Value)
			bl.AddWidget(r.QWidget)
		}
		f.value = func() (any, bool) {
			if i := group.CheckedId(); i >= 0 && i < len(values) {
				return values[i], true
			}
			return nil, false
		}
		return box
	case "checkbox_group":
		box := qt.NewQWidget2()
		bl := qt.NewQVBoxLayout(box)
		bl.SetContentsMargins(0, 0, 0, 0)
		on := map[string]bool{}
		for _, s := range strings.Split(el.Default, ",") {
			on[strings.TrimSpace(s)] = true
		}
		type opt struct {
			c     *qt.QCheckBox
			value string
		}
		var opts []opt
		for _, o := range el.Options {
			if o == nil {
				continue
			}
			c := qt.NewQCheckBox3(o.Text)
			c.SetChecked(on[o.Value])
			opts = append(opts, opt{c, o.Value})
			bl.AddWidget(c.QWidget)
		}
		f.value = func() (any, bool) {
			var out []string
			for _, o := range opts {
				if o.c.IsChecked() {
					out = append(out, o.value)
				}
			}
			return out, len(out) > 0
		}
		return box
	case "select":
		return v.selectWidget(f)
	case "date", "datetime":
		return dateWidget(f)
	}
	note := qt.NewQLabel3("Такое поле приложение не показывает — откройте это окно в веб-клиенте.")
	note.SetProperty("role", tmp(qt.NewQVariant11("hint")))
	note.SetWordWrap(true)
	return note.QWidget
}

// selectWidget — список: варианты (несколько — галочками), люди, каналы;
// dynamic (варианты у интеграции по запросу) — значением вручную.
func (v *interactiveDialog) selectWidget(f *dialogField) *qt.QWidget {
	el := f.el
	switch el.DataSource {
	case "users", "channels":
		value, text := el.Default, el.Default
		if el.DataSource == "users" {
			if u := v.m.st.User(value); u != nil {
				text = "@" + u.Username
			}
		} else if ch := v.m.st.Channel(value); ch != nil {
			text = v.m.st.Title(ch)
		}
		b := qt.NewQPushButton3(text)
		empty := el.Placeholder
		if empty == "" {
			empty = "Выбрать…"
		}
		if text == "" {
			b.SetText(empty)
		}
		b.OnClicked(func() {
			if el.DataSource == "users" {
				v.m.chooseUser(el.DisplayName, func(u *model.User) { value = u.Id; b.SetText("@" + u.Username) })
			} else {
				team := v.m.teamOf(v.channel())
				v.m.chooseChannel(el.DisplayName, team, func(it switchItem) { value = it.id; b.SetText(it.title) })
			}
		})
		f.value = func() (any, bool) { return value, value != "" }
		return b.QWidget
	case "dynamic":
		e := qt.NewQLineEdit3(el.Default)
		e.SetPlaceholderText(el.Placeholder)
		f.value = func() (any, bool) {
			s := e.Text()
			return s, s != ""
		}
		return e.QWidget
	}
	if el.MultiSelect {
		list := qt.NewQListWidget2()
		on := map[string]bool{}
		for _, s := range strings.Split(el.Default, ",") {
			on[strings.TrimSpace(s)] = true
		}
		var values []string
		for _, o := range el.Options {
			if o == nil {
				continue
			}
			it := qt.NewQListWidgetItem7(o.Text, list)
			it.SetFlags(it.Flags() | qt.ItemIsUserCheckable)
			it.SetCheckState(qt.Unchecked)
			if on[o.Value] {
				it.SetCheckState(qt.Checked)
			}
			values = append(values, o.Value)
		}
		list.SetFixedHeight(min(len(values), 6)*list.FontMetrics().LineSpacing()*3/2 + 12)
		f.value = func() (any, bool) {
			var out []string
			for i, val := range values {
				if list.Item(i).CheckState() == qt.Checked {
					out = append(out, val)
				}
			}
			return out, len(out) > 0
		}
		return list.QWidget
	}
	c := qt.NewQComboBox2()
	var values []string
	if el.Default == "" {
		c.AddItem(el.Placeholder) // ничего не выбрано
		values = append(values, "")
	}
	for _, o := range el.Options {
		if o == nil {
			continue
		}
		c.AddItem(o.Text)
		values = append(values, o.Value)
		if o.Value == el.Default {
			c.SetCurrentIndex(len(values) - 1)
		}
	}
	f.value = func() (any, bool) {
		if i := c.CurrentIndex(); i >= 0 && i < len(values) && values[i] != "" {
			return values[i], true
		}
		return nil, false
	}
	return c.QWidget
}

// dateWidget — дата (YYYY-MM-DD) или дата со временем (ISO 8601, UTC).
// Пустое значение — минимальная дата с пустой подписью (как у QDateEdit
// принято): необязательное поле можно оставить пустым.
func dateWidget(f *dialogField) *qt.QWidget {
	el := f.el
	withTime := el.Type == "datetime"
	e := qt.NewQDateTimeEdit2()
	e.SetCalendarPopup(true)
	if withTime {
		e.SetDisplayFormat("dd.MM.yyyy HH:mm")
	} else {
		e.SetDisplayFormat("dd.MM.yyyy")
	}
	floor := time.Date(2000, 1, 1, 0, 0, 0, 0, time.Local)
	e.SetMinimumDateTime(qt.QDateTime_FromMSecsSinceEpoch(floor.UnixMilli()))
	e.SetSpecialValueText(" ")
	e.SetDateTime(qt.QDateTime_FromMSecsSinceEpoch(floor.UnixMilli()))
	if t, ok := dialogDate(el.Default, time.Now()); ok {
		e.SetDateTime(qt.QDateTime_FromMSecsSinceEpoch(t.UnixMilli()))
	}
	f.value = func() (any, bool) {
		t := time.UnixMilli(e.DateTime().ToMSecsSinceEpoch())
		if !t.After(floor) {
			return nil, false
		}
		if withTime {
			return t.UTC().Format("2006-01-02T15:04:05Z"), true
		}
		return t.Format("2006-01-02"), true
	}
	return e.QWidget
}

// dialogDate — значение по умолчанию поля даты: ISO-дата или дата со
// временем, today, tomorrow, yesterday, +Nd/+NH/+NM («через N дней…»).
func dialogDate(s string, now time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch strings.ToLower(s) {
	case "":
		return time.Time{}, false
	case "today":
		return day, true
	case "tomorrow":
		return day.AddDate(0, 0, 1), true
	case "yesterday":
		return day.AddDate(0, 0, -1), true
	}
	if len(s) > 2 && (s[0] == '+' || s[0] == '-') {
		if n, err := strconv.Atoi(s[1 : len(s)-1]); err == nil {
			if s[0] == '-' {
				n = -n
			}
			switch s[len(s)-1] {
			case 'd':
				return day.AddDate(0, 0, n), true
			case 'H', 'h':
				return now.Add(time.Duration(n) * time.Hour), true
			case 'M', 'm':
				return now.Add(time.Duration(n) * time.Minute), true
			}
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Local(), true
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// channel — канал, к которому относится окно: его назвал сервер, иначе открытый.
func (v *interactiveDialog) channel() string {
	if v.req.ChannelId != "" {
		return v.req.ChannelId
	}
	return v.m.current
}

// send — проверить поля и отправить; extra — значение кнопки отправки.
func (v *interactiveDialog) send(extra func(map[string]any)) {
	sub := map[string]any{}
	bad := false
	for _, f := range v.fields {
		val, ok := f.value()
		problem := ""
		switch {
		case !ok && !f.el.Optional:
			problem = "Обязательное поле"
		case ok && f.el.MinLength > 0:
			if s, isText := val.(string); isText && len([]rune(s)) < f.el.MinLength {
				problem = fmt.Sprintf("Не короче %d символов", f.el.MinLength)
			}
		}
		f.err.SetText(problem)
		f.err.SetVisible(problem != "")
		if problem != "" {
			bad = true
			continue
		}
		if ok {
			sub[f.el.Name] = val
		}
	}
	if extra != nil {
		extra(sub)
	}
	if bad {
		return
	}
	v.sent = true
	v.errMsg.SetVisible(false)
	for _, b := range v.submit {
		b.SetEnabled(false)
	}
	v.post(model.SubmitDialogRequest{Type: dialogSubmit, Submission: sub}, func(resp *model.SubmitDialogResponse, err error) {
		for _, b := range v.submit {
			b.SetEnabled(true)
		}
		switch {
		case err != nil:
			v.sent = false
			v.errMsg.SetText("Не отправлено: " + commandError(err))
			v.errMsg.SetVisible(true)
		case resp != nil && (resp.Error != "" || len(resp.Errors) > 0):
			v.sent = false
			v.showErrors(resp)
		case resp != nil && resp.Type == string(model.SubmitDialogResponseTypeForm) && resp.Form != nil:
			v.sent = false
			v.build(*resp.Form)
		default:
			v.d.w.Close()
		}
	})
}

// showErrors — ошибки интеграции: общая — над кнопками, по полям — под ними
// (поле, которого нет в форме, — к общей).
func (v *interactiveDialog) showErrors(resp *model.SubmitDialogResponse) {
	general := []string{}
	if resp.Error != "" {
		general = append(general, resp.Error)
	}
	for name, text := range resp.Errors {
		found := false
		for _, f := range v.fields {
			if f.el.Name == name {
				f.err.SetText(text)
				f.err.SetVisible(true)
				found = true
			}
		}
		if !found {
			general = append(general, name+": "+text)
		}
	}
	v.errMsg.SetText(strings.Join(general, "\n"))
	v.errMsg.SetVisible(len(general) > 0)
}

// post — запрос на сервер (адрес, callback, состояние — из окна); done
// (если есть) — в главном потоке, пока окно живо.
func (v *interactiveDialog) post(req model.SubmitDialogRequest, done func(*model.SubmitDialogResponse, error)) {
	m := v.m
	ch := v.channel()
	req.URL, req.CallbackId, req.State = v.req.URL, v.req.Dialog.CallbackId, v.req.Dialog.State
	req.UserId, req.ChannelId, req.TeamId = m.s.Me.Id, ch, m.teamOf(ch)
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		resp, _, err := m.s.Client.SubmitInteractiveDialog(ctx, req)
		m.ui(func() {
			if err != nil {
				m.log.Warn("interactive dialog", "type", req.Type, "callback", truncate(req.CallbackId, 200), "err", err)
			}
			if done != nil && !v.d.closed {
				done(resp, err)
			}
		})
	}()
}
