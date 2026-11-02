package store

import (
	"regexp"
	"strings"
)

// Адрес (name) канала Mattermost: строчные латинские буквы, цифры, «-» и «_»,
// до 64 символов. Из русского названия — транслитом, как привычно в адресах.

var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh", 'з': "z", 'и': "i",
	'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t",
	'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "",
	'э': "e", 'ю': "yu", 'я': "ya",
}

var channelNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ChannelURLName — адрес канала из названия: «Релизы 2.0» → «relizy-2-0».
func ChannelURLName(display string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(display) {
		var part string
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			part = string(r)
		default:
			t, ok := translit[r]
			if !ok {
				dash = b.Len() > 0
				continue
			}
			part = t
		}
		if part == "" {
			continue
		}
		if dash {
			b.WriteByte('-')
			dash = false
		}
		b.WriteString(part)
	}
	name := b.String()
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "-_")
	}
	return name
}

// ValidChannelName — годится ли адрес канала; пусто — годится.
func ValidChannelName(name string) string {
	switch {
	case name == "":
		return "Нужен адрес канала"
	case len(name) > 64:
		return "Адрес длиннее 64 символов"
	case !channelNameRe.MatchString(name):
		return "В адресе — только строчные латинские буквы, цифры, «-» и «_», в начале — буква или цифра"
	}
	return ""
}
