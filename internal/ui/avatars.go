package ui

import (
	"fmt"
	"strings"
	"unicode"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/store"
)

// initials — две буквы для аватара без фото: имя и фамилия, иначе начало имени пользователя.
func initials(u *model.User, fallback string) string {
	first := func(s string) string {
		for _, r := range strings.TrimSpace(s) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return string(unicode.ToUpper(r))
			}
		}
		return ""
	}
	if u != nil {
		if f, l := first(u.FirstName), first(u.LastName); f != "" && l != "" {
			return f + l
		}
		if fallback == "" {
			fallback = u.Username
		}
	}
	var out []rune
	for _, r := range fallback {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, unicode.ToUpper(r))
			if len(out) == 2 {
				break
			}
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// displayName — «Имя Фамилия», иначе имя пользователя.
func displayName(u *model.User) string {
	if u == nil {
		return ""
	}
	if n := strings.TrimSpace(u.FirstName + " " + u.LastName); n != "" {
		return n
	}
	return u.Username
}

// avatarPixmap — круглый аватар: фото, если оно уже на диске, иначе инициалы
// на цвете из хэша имени. Фото догружается фоном; loaded зовётся, когда оно на диске.
func avatarPixmap(a *Assets, u *model.User, name string, bot bool, size int, loaded func()) *qt.QPixmap {
	photo := ""
	if a != nil && u != nil {
		if photo = a.Avatar(u); photo == "" && loaded != nil {
			a.WantAvatar(u, loaded)
		}
	}
	return roundAvatar(photo, u, name, bot, size)
}

// integrationAvatar — аватар поста интеграции: её иконка (пока не загрузилась
// или если её нет — инициалы её имени цветом ботов), а фото создателя — только
// если интеграция просит его сама (use_user_icon).
func integrationAvatar(a *Assets, in store.Integration, creator *model.User, name string, size int, loaded func()) *qt.QPixmap {
	if in.CreatorIcon {
		return avatarPixmap(a, creator, name, true, size, loaded)
	}
	photo := ""
	if a != nil && in.IconURL != "" {
		if photo = a.Icon(in.IconURL); photo == "" && loaded != nil {
			a.WantIcon(in.IconURL, loaded)
		}
	}
	return roundAvatar(photo, nil, name, true, size)
}

// roundAvatar рисует круглый аватар: картинка photo, если она есть и читается,
// иначе инициалы на цвете из хэша имени.
func roundAvatar(photo string, u *model.User, name string, bot bool, size int) *qt.QPixmap {
	key := fmt.Sprintf("%s|%s|%t|%d|%s", name, initials(u, name), bot, size, photo)
	if u != nil {
		key = u.Id + "|" + key
	}
	if pm := style.avatars[key]; pm != nil {
		return pm
	}
	px := size * resourceDPR
	img := qt.NewQImage3(px, px, qt.QImage__Format_ARGB32_Premultiplied)
	defer free(img)
	img.Fill2(qt.Transparent)
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	path := tmp(qt.NewQPainterPath())
	path.AddEllipse2(0, 0, float64(px), float64(px))
	p.SetClipPath(path)

	drawn := false
	if photo != "" {
		if src := tmp(qt.NewQPixmap4(photo)); !src.IsNull() {
			pm := tmp(src.Scaled3(px, px, qt.KeepAspectRatioByExpanding, qt.SmoothTransformation))
			p.DrawPixmap9((px-pm.Width())/2, (px-pm.Height())/2, pm)
			drawn = true
		}
	}
	if !drawn {
		if name == "" && u != nil {
			name = u.Username
		}
		bg, fg := Pal().Avatar(name, bot)
		p.FillRect5(0, 0, px, px, qcolor(bg))
		p.SetPen(qcolor(fg))
		f := pixelFont(max(8, px*36/100), qt.QFont__DemiBold)
		p.SetFont(f)
		p.DrawText7(0, 0, px, px, int(qt.AlignCenter), initials(u, name))
	}
	free(p)
	pm := qt.QPixmap_FromImage(img)
	pm.SetDevicePixelRatio(resourceDPR)
	style.avatars[key] = pm
	return pm
}

// statusColor — цвет точки присутствия.
func statusColor(status string) string {
	p := Pal()
	switch status {
	case model.StatusOnline:
		return p.Good
	case model.StatusAway:
		return p.Warn
	case model.StatusDnd:
		return p.Dang
	}
	return p.Tx3
}

// statusText — подпись присутствия для карточки.
func statusText(status string) string {
	switch status {
	case model.StatusOnline:
		return "В сети"
	case model.StatusAway:
		return "Отошёл"
	case model.StatusDnd:
		return "Не беспокоить"
	}
	return "Не в сети"
}

// drawStatusDot рисует точку статуса size px с кольцом цвета ring в (x, y) — левый верхний угол.
func drawStatusDot(p *qt.QPainter, x, y, size float64, status, ring string) {
	p.Save()
	p.SetRenderHint(qt.QPainter__Antialiasing)
	p.SetPenWithStyle(qt.NoPen)
	p.SetBrush(qbrush(ring))
	p.DrawEllipse(tmp(qt.NewQRectF4(x-1.5, y-1.5, size+3, size+3)))
	p.SetBrush(qbrush(statusColor(status)))
	p.DrawEllipse(tmp(qt.NewQRectF4(x, y, size, size)))
	p.Restore()
}
