// Package spell — проверка орфографии по словарям hunspell.
//
// Библиотеку грузим во время работы (dlopen), а не при сборке: нет
// libhunspell или словарей — приложение работает, просто без проверки.
// Словари — системные (пакеты hunspell-ru, hunspell-en-us): ищем в DICPATH,
// /usr/share/hunspell, /usr/share/myspell. Слово верно, если его знает
// хоть один словарь или личный (файл, по слову в строке).
//
// Checker не потокобезопасен (hunspell тоже): создать можно в горутине,
// пользоваться — из одного потока.
package spell

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// Langs — словари по умолчанию.
var Langs = []string{"ru_RU", "en_US"}

// Checker — словари и личный словарь.
type Checker struct {
	dicts    []unsafe.Pointer // Hunhandle*
	Loaded   []string         // какие словари нашлись
	personal map[string]bool
	path     string          // файл личного словаря; "" — не сохранять
	cache    map[string]bool // слово → верно
}

// dictDirs — где искать .aff/.dic.
func dictDirs() []string {
	var out []string
	if p := os.Getenv("DICPATH"); p != "" {
		out = append(out, filepath.SplitList(p)...)
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".local/share/hunspell"))
	}
	return append(out, "/usr/share/hunspell", "/usr/share/myspell", "/usr/share/myspell/dicts",
		"/opt/homebrew/share/hunspell", "/usr/local/share/hunspell", "/Library/Spelling") // последние три — macOS
}

func (c *Checker) loadPersonal() error {
	f, err := os.Open(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("spell: personal dictionary: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			c.remember(w)
		}
	}
	return sc.Err()
}

// Correct — слово верно (знает хоть один словарь или личный). Через дефис —
// верно, если верно целиком или каждая часть («кто-нибудь», «e-mail»).
func (c *Checker) Correct(word string) bool {
	if ok, seen := c.cache[word]; seen {
		return ok
	}
	ok := c.spell(word)
	if !ok && strings.Contains(word, "-") {
		ok = true
		for _, part := range strings.Split(word, "-") {
			if part != "" && !c.spell(part) {
				ok = false
				break
			}
		}
	}
	if len(c.cache) > 20000 {
		clear(c.cache)
	}
	c.cache[word] = ok
	return ok
}

// Add — слово в личный словарь (и в файл).
func (c *Checker) Add(word string) error {
	word = strings.TrimSpace(word)
	if word == "" || c.personal[strings.ToLower(word)] {
		return nil
	}
	c.remember(word)
	if c.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("spell: personal dictionary: %w", err)
	}
	f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("spell: personal dictionary: %w", err)
	}
	if _, err := f.WriteString(word + "\n"); err != nil {
		f.Close()
		return fmt.Errorf("spell: personal dictionary %s: %w", c.path, err)
	}
	return f.Close()
}

// Word — слово в тексте: байтовые границы и в единицах UTF-16 (так меряет Qt).
type Word struct {
	Text           string
	Start, End     int // байты
	Start16, End16 int // UTF-16
}

// Words — слова текста, которые стоит проверять: буквы подряд (внутри —
// дефис и апостроф), не меньше двух; мимо — @упоминания, ~каналы,
// :эмодзи:, ссылки и адреса, слова с цифрами или «_», аббревиатуры
// ЗАГЛАВНЫМИ.
func Words(text string) []Word {
	var out []Word
	i, u16 := 0, 0
	advance := func(r rune, size int) {
		i += size
		u16++
		if r >= 0x10000 {
			u16++
		}
	}
	for i < len(text) {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			advance(r, size)
			continue
		}
		// Токен: буквы, цифры, «_», а внутри — дефис, апостроф, точка, «/», «@», «:».
		start, start16 := i, u16
		for i < len(text) {
			r, size = utf8.DecodeRuneInString(text[i:])
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '\'' || r == '’' || r == '-' {
				advance(r, size)
				continue
			}
			if strings.ContainsRune(".:/@", r) && i+size < len(text) {
				// Внутри слова (адрес, путь, время) — часть токена; перед
				// пробелом («слово: дальше») — конец слова.
				next, _ := utf8.DecodeRuneInString(text[i+size:])
				if unicode.IsLetter(next) || unicode.IsDigit(next) || next == '/' {
					advance(r, size)
					continue
				}
			}
			break
		}
		end, end16 := i, u16
		tok := text[start:end]
		// Хвостовые дефисы и апострофы — не часть слова.
		for len(tok) > 0 {
			last, sz := utf8.DecodeLastRuneInString(tok)
			if !strings.ContainsRune("-'’", last) {
				break
			}
			tok = tok[:len(tok)-sz]
			end -= sz
			end16--
		}
		if skipToken(text, start, tok) {
			continue
		}
		out = append(out, Word{Text: tok, Start: start, End: end, Start16: start16, End16: end16})
	}
	return out
}

func skipToken(text string, start int, tok string) bool {
	if utf8.RuneCountInString(tok) < 2 || strings.ContainsAny(tok, ".:/@_") {
		return true // адреса, почта, время, имена файлов
	}
	if start > 0 {
		if prev, _ := utf8.DecodeLastRuneInString(text[:start]); strings.ContainsRune("@~:#$&", prev) {
			return true // @упоминание, ~канал, :эмодзи:, #хэштег
		}
	}
	upper := true
	for _, r := range tok {
		if unicode.IsDigit(r) {
			return true
		}
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			upper = false
		}
	}
	return upper // аббревиатура
}
