package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
)

// «Файлы канала» (кнопка в шапке канала): вложения открытого канала в
// правой колонке, новые сверху; строка поиска — по имени (и всему, что
// понимает поиск файлов сервера: ext:pdf, from:имя, after:…). Ищет сервер
// с in:<id канала>: имя канала сервер ищет в команде поиска, а поиск — по
// всем командам (команды нет — имени не найти, файлов ноль); id он
// пропускает как есть (convertChannelNamesToChannelIds в app/post.go). Щелчок — открыть
// (картинку — в просмотре), правый — «Сохранить…», «Перейти к сообщению».

const fileIDRole = int(qt.UserRole) + 10

type filesPanel struct {
	w     *qt.QWidget
	head  *header
	field *qt.QLineEdit
	info  *qt.QLabel
	list  *qt.QListWidget
	timer *qt.QTimer // поиск — после паузы в наборе

	channel string
	files   map[string]*model.FileInfo
	thumbs  map[string]*qt.QPixmap // превью картинок 32×32, по id файла
	gen     int

	unsubscribe func()
}

func (m *MainWindow) newFilesPanel() *filesPanel {
	fp := &filesPanel{w: qt.NewQWidget2(), field: qt.NewQLineEdit2(), info: qt.NewQLabel2(), list: qt.NewQListWidget2(),
		files: map[string]*model.FileInfo{}, thumbs: map[string]*qt.QPixmap{}}
	fp.head = newHeader("threadHeader", true, nil)
	fp.head.close.OnClicked(func() { m.showRight(nil) })
	fp.field.SetPlaceholderText("Имя файла, ext:pdf, from:имя")
	fp.field.SetClearButtonEnabled(true)
	fp.timer = qt.NewQTimer2(fp.w.QObject)
	fp.timer.SetSingleShot(true)
	fp.timer.SetInterval(300)
	fp.timer.OnTimeout(func() { m.loadFiles() })
	fp.field.OnTextChanged(func(string) { fp.timer.Start2() })
	fp.info.SetProperty("role", qt.NewQVariant11("hint"))
	fp.info.SetWordWrap(true)

	l := fp.list
	setName(l.QObject, "members") // тот же вид, что у списка участников
	l.SetFrameShape(qt.QFrame__NoFrame)
	l.SetUniformItemSizes(true)
	l.SetHorizontalScrollBarPolicy(qt.ScrollBarAlwaysOff)
	d := qt.NewQStyledItemDelegate2(l.QObject)
	d.OnPaint(func(super func(*qt.QPainter, *qt.QStyleOptionViewItem, *qt.QModelIndex), p *qt.QPainter, opt *qt.QStyleOptionViewItem, idx *qt.QModelIndex) {
		noFocusFrame(opt)
		super(p, opt, idx)
		if it := l.ItemFromIndex(idx); it != nil {
			if fi := fp.files[it.Data(fileIDRole).ToString()]; fi != nil {
				m.paintFileRow(p, opt, fi)
			}
		}
	})
	d.OnSizeHint(func(super func(*qt.QStyleOptionViewItem, *qt.QModelIndex) *qt.QSize, opt *qt.QStyleOptionViewItem, idx *qt.QModelIndex) *qt.QSize {
		return qt.NewQSize2(opt.Rect().Width(), 52)
	})
	l.SetItemDelegate(d.QAbstractItemDelegate)
	l.OnItemClicked(func(it *qt.QListWidgetItem) {
		if fi := fp.files[it.Data(fileIDRole).ToString()]; fi != nil {
			m.openChannelFile(fi)
		}
	})
	l.SetContextMenuPolicy(qt.CustomContextMenu)
	l.OnCustomContextMenuRequested(func(pos *qt.QPoint) {
		it := l.ItemAt(pos)
		if it == nil {
			return
		}
		fi := fp.files[it.Data(fileIDRole).ToString()]
		if fi == nil {
			return
		}
		menu := qt.NewQMenu(l.QWidget)
		defer menu.DeleteLater()
		fillMenu(menu, []menuItem{
			{title: "Открыть", do: func() { m.openChannelFile(fi) }},
			{title: "Сохранить…", do: func() { m.saveFile(fi) }},
			{},
			{title: "Перейти к сообщению", do: func() { m.openPermalink(fi.ChannelId, fi.PostId) }},
		})
		menu.ExecWithPos(l.Viewport().MapToGlobalWithQPoint(pos))
	})

	top := qt.NewQVBoxLayout2()
	top.SetContentsMargins(16, 8, 16, 6)
	top.SetSpacing(4)
	top.AddWidget(fp.field.QWidget)
	top.AddWidget(fp.info.QWidget)
	lay := qt.NewQVBoxLayout(fp.w)
	lay.SetContentsMargins(0, 0, 0, 0)
	lay.SetSpacing(0)
	lay.AddWidget(fp.head.f.QWidget)
	lay.AddLayout(top.QLayout)
	lay.AddWidget2(l.QWidget, 1)
	fp.w.SetVisible(false)
	fp.unsubscribe = OnThemeChanged(fp.restyle)
	fp.restyle()
	return fp
}

func (fp *filesPanel) restyle() {
	fp.head.close.SetIcon(icon("x", Pal().Tx2, 18))
	fp.head.icon.SetPixmap(iconPixmap("file-text", Pal().Tx2, 16))
	fp.list.Viewport().Update()
}

// paintFileRow — превью картинки или значок файла, имя, ниже — кто, когда, размер.
func (m *MainWindow) paintFileRow(p *qt.QPainter, opt *qt.QStyleOptionViewItem, fi *model.FileInfo) {
	pal := Pal()
	r := opt.Rect()
	x, y, h := r.X()+16, r.Y(), r.Height()
	cy := y + h/2
	pm := m.files.thumbs[fi.Id]
	if pm == nil && isImage(fi) && !isVideo(fi) && fi.HasPreviewImage {
		if path := m.assets.Preview(fi.Id); path != "" {
			if img := qt.NewQPixmap4(path); !img.IsNull() {
				pm = img.Scaled3(32, 32, qt.KeepAspectRatioByExpanding, qt.SmoothTransformation).Copy(0, 0, 32, 32)
				m.files.thumbs[fi.Id] = pm
			}
		} else {
			m.assets.WantPreview(fi.Id, m.files.list.Viewport().Update)
		}
	}
	if pm == nil {
		name := "file-text"
		if isImage(fi) {
			name = "image"
		}
		pm = iconPixmap(name, pal.Tx2, 24)
		p.DrawPixmap9(x+4, cy-12, pm)
	} else {
		p.DrawPixmap9(x, cy-16, pm)
	}
	tx := x + 32 + 10
	w := r.X() + r.Width() - 16 - tx
	f := sidebarFont(0, qt.QFont__DemiBold)
	p.SetFont(f)
	p.SetPen(qt.NewQColor6(pal.Tx))
	p.DrawText7(tx, cy-20, w, 20, int(qt.AlignVCenter|qt.AlignLeft), qt.NewQFontMetrics(f).ElidedText(fi.Name, qt.ElideMiddle, w))
	sub := m.st.Username(fi.CreatorId) + " · " + dayLabel(fi.CreateAt, time.Now()) + " · " + formatSize(fi.Size)
	f = sidebarFont(-2, qt.QFont__Normal)
	p.SetFont(f)
	p.SetPen(qt.NewQColor6(pal.Tx2))
	p.DrawText7(tx, cy, w, 18, int(qt.AlignVCenter|qt.AlignLeft), qt.NewQFontMetrics(f).ElidedText(sub, qt.ElideRight, w))
}

// filesShown — открыт ли список файлов.
func (m *MainWindow) filesShown() bool { return !m.files.w.IsHidden() }

// toggleFiles — кнопка в шапке: файлы открытого канала или закрыть.
func (m *MainWindow) toggleFiles() {
	if m.filesShown() && m.files.channel == m.current {
		m.showRight(nil)
		return
	}
	m.showFiles(m.current)
}

// showFiles — файлы канала в правой колонке.
func (m *MainWindow) showFiles(channelID string) {
	fp := m.files
	ch := m.st.Channel(channelID)
	if ch == nil {
		return
	}
	if m.thread != nil && !m.threadDetached() { // отстёгнутый тред правой колонке не мешает
		m.closeThread()
	}
	if fp.channel != channelID {
		fp.field.Clear()
	}
	fp.channel = channelID
	fp.head.set(iconPixmap("file-text", Pal().Tx2, 16), "Файлы", channelPrefix(ch)+m.st.Title(ch))
	fp.timer.Stop()
	m.showRight(fp.w)
	m.loadFiles()
}

// loadFiles — файлы канала с сервера под запрос из строки поиска.
func (m *MainWindow) loadFiles() {
	fp := m.files
	ch := m.st.Channel(fp.channel)
	if ch == nil {
		return
	}
	fp.gen++
	gen, channel := fp.gen, ch.Id
	terms := strings.TrimSpace("in:" + ch.Id + " " + strings.TrimSpace(fp.field.Text()))
	_, offset := time.Now().Zone()
	params := &model.SearchParameter{Terms: &terms, IsOrSearch: new(false), TimeZoneOffset: &offset, Page: new(0), PerPage: new(searchLimit)}
	team := m.teamOf(channel)
	fp.info.SetText("Ищем…")
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, requestTimeout)
		defer cancel()
		list, _, err := m.s.Client.SearchFilesWithParams(ctx, "", params)
		if err != nil && team != "" {
			list, _, err = m.s.Client.SearchFilesWithParams(ctx, team, params) // сервер постарше — по команде
		}
		var authors []string
		var files []*model.FileInfo
		if err == nil {
			for _, id := range list.Order {
				if fi := list.FileInfos[id]; fi != nil && fi.DeleteAt == 0 && (fi.ChannelId == "" || fi.ChannelId == channel) {
					files = append(files, fi)
					authors = append(authors, fi.CreatorId)
				}
			}
		}
		m.ui(func() {
			if fp.gen != gen {
				return
			}
			if err != nil {
				m.log.Warn("search files", "channel", channel, "err", err)
				fp.info.SetText("Не загрузилось: " + commandError(err))
				return
			}
			m.withUsers(authors, func() {
				if fp.gen != gen {
					return
				}
				slices.SortStableFunc(files, func(a, b *model.FileInfo) int { return int(b.CreateAt - a.CreateAt) })
				fp.fill(files)
			})
		})
	}()
}

func (fp *filesPanel) fill(files []*model.FileInfo) {
	fp.list.Clear()
	clear(fp.files)
	for _, fi := range files {
		fp.files[fi.Id] = fi
		it := qt.NewQListWidgetItem5(fp.list)
		it.SetData(fileIDRole, qt.NewQVariant11(fi.Id))
	}
	switch q := strings.TrimSpace(fp.field.Text()); {
	case len(files) == 0 && q != "":
		fp.info.SetText("Ничего не нашлось")
	case len(files) == 0:
		fp.info.SetText("В канале нет файлов")
	case len(files) >= searchLimit:
		fp.info.SetText(fmt.Sprintf("Последние %d — уточните поиск, чтобы найти остальные", len(files)))
	default:
		fp.info.SetText(plural(int64(len(files)), "файл", "файла", "файлов"))
	}
}

// openChannelFile — картинку — в просмотре, остальное — как вложение в ленте.
func (m *MainWindow) openChannelFile(fi *model.FileInfo) {
	if isImage(fi) && !isVideo(fi) {
		m.openImage(&model.Post{Id: fi.PostId, ChannelId: fi.ChannelId, Metadata: &model.PostMetadata{Files: []*model.FileInfo{fi}}}, fi.Id)
		return
	}
	m.openFile(fi)
}

// filesChanged — в канал пришло сообщение с файлами: открытый список — заново.
func (m *MainWindow) filesChanged(channelID string) {
	if m.filesShown() && m.files.channel == channelID {
		m.files.timer.Start2()
	}
}
