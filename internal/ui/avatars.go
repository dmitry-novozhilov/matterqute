package ui

import (
	"fmt"
	"strings"
	"unicode"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"
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
	key := fmt.Sprintf("%s|%s|%t|%d|%s", name, initials(u, name), bot, size, photo)
	if u != nil {
		key = u.Id + "|" + key
	}
	if pm := style.avatars[key]; pm != nil {
		return pm
	}
	px := size * resourceDPR
	img := qt.NewQImage3(px, px, qt.QImage__Format_ARGB32_Premultiplied)
	img.Fill2(qt.Transparent)
	p := qt.NewQPainter2(img.QPaintDevice)
	p.SetRenderHint(qt.QPainter__Antialiasing)
	path := qt.NewQPainterPath()
	path.AddEllipse2(0, 0, float64(px), float64(px))
	p.SetClipPath(path)

	drawn := false
	if photo != "" {
		if pm := qt.NewQPixmap4(photo); !pm.IsNull() {
			pm = pm.Scaled3(px, px, qt.KeepAspectRatioByExpanding, qt.SmoothTransformation)
			p.DrawPixmap9((px-pm.Width())/2, (px-pm.Height())/2, pm)
			drawn = true
		}
	}
	if !drawn {
		if name == "" && u != nil {
			name = u.Username
		}
		bg, fg := Pal().Avatar(name, bot)
		p.FillRect5(0, 0, px, px, qt.NewQColor6(bg))
		p.SetPen(qt.NewQColor6(fg))
		f := pixelFont(max(8, px*36/100), qt.QFont__DemiBold)
		p.SetFont(f)
		p.DrawText7(0, 0, px, px, int(qt.AlignCenter), initials(u, name))
	}
	p.End()
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
	p.SetBrush(qt.NewQBrush3(qt.NewQColor6(ring)))
	p.DrawEllipse(qt.NewQRectF4(x-1.5, y-1.5, size+3, size+3))
	p.SetBrush(qt.NewQBrush3(qt.NewQColor6(statusColor(status))))
	p.DrawEllipse(qt.NewQRectF4(x, y, size, size))
	p.Restore()
}
