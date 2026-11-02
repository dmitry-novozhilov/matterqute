package ui

import (
	qt "github.com/mappu/miqt/qt6"
)

// Тред в отдельном окне: панель треда (она одна) отстёгивается в своё
// окно — например, на другой монитор. Кнопка в шапке треда отстёгивает и
// возвращает, «Открыть тред в отдельном окне» в меню сообщения — сразу
// туда. Закрыли окно — тред закрыт, панель вернулась на место. Пока окно
// открыто, правая колонка свободна (поиск, участники, файлы), и они треда
// не закрывают; уведомление по треду молчит, если активно окно треда.

func (m *MainWindow) threadDetached() bool { return m.threadWin != nil }

// threadOnScreen — тред перед глазами: его окно активно (отстёгнут) или
// активно главное окно (тред в правой колонке).
func (m *MainWindow) threadOnScreen() bool {
	if w := m.threadWin; w != nil {
		return w.IsActiveWindow()
	}
	return m.w.IsActiveWindow()
}

// detachThread — панель треда — в отдельное окно.
func (m *MainWindow) detachThread() {
	if w := m.threadWin; w != nil {
		w.Raise()
		w.ActivateWindow()
		return
	}
	w := qt.NewQWidget3(m.w, qt.Window) // ребёнок окна сервера: закроется вместе с ним
	w.SetAttribute(qt.WA_DeleteOnClose)
	lay := qt.NewQVBoxLayout(w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.AddWidget(m.panel.w) // из разделителя — сюда
	w.OnCloseEvent(func(super func(*qt.QCloseEvent), e *qt.QCloseEvent) {
		if m.threadWin == w { // закрыли само окно — тред закрыт
			m.attachThread()
			m.closeThread()
		}
		super(e)
	})
	m.threadWin = w
	m.panel.back.SetVisible(false)
	m.panel.w.SetVisible(true)
	m.updateThreadWindowTitle()
	m.updateDetachButton()
	w.Resize(560, max(600, m.w.Height()))
	w.Show()
	w.ActivateWindow()
	m.panel.composer.Focus()
}

// attachThread — панель треда обратно в правую колонку (окно не закрывает).
func (m *MainWindow) attachThread() {
	if m.threadWin == nil {
		return
	}
	m.threadWin = nil
	m.split.InsertWidget(2, m.panel.w)
	m.updateDetachButton()
}

// toggleDetach — кнопка в шапке треда: в отдельное окно или обратно, тред
// остаётся открытым.
func (m *MainWindow) toggleDetach() {
	w := m.threadWin
	if w == nil {
		m.detachThread()
		return
	}
	m.attachThread()
	w.Close() // threadWin уже не w: тред не закрывается
	if m.thread != nil {
		m.showRight(m.panel.w)
	}
}

func (m *MainWindow) updateDetachButton() {
	b := m.panel.detach
	if m.threadWin != nil {
		b.SetIcon(icon("arrow-left", Pal().Tx2, 18))
		b.SetToolTip("Вернуть в главное окно")
	} else {
		b.SetIcon(icon("external-link", Pal().Tx2, 18))
		b.SetToolTip("В отдельном окне")
	}
}

// updateThreadWindowTitle — «Тред · # канал» в заголовке окна.
func (m *MainWindow) updateThreadWindowTitle() {
	w := m.threadWin
	if w == nil {
		return
	}
	title := "Тред"
	if t := m.thread; t != nil {
		if ch := m.st.Channel(t.channelID); ch != nil {
			title += " · " + channelPrefix(ch) + m.st.Title(ch)
		}
	}
	w.SetWindowTitle(title)
}

// openThreadWindow — «Открыть тред в отдельном окне» в меню сообщения.
func (m *MainWindow) openThreadWindow(rootID string) {
	m.openThread(rootID)
	m.detachThread()
}
