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

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>

typedef void Hunhandle;
static void* lib;
static Hunhandle* (*hs_create)(const char*, const char*);
static void (*hs_destroy)(Hunhandle*);
static int (*hs_spell)(Hunhandle*, const char*);
static int (*hs_suggest)(Hunhandle*, char***, const char*);
static void (*hs_free_list)(Hunhandle*, char***, int);
static int (*hs_add)(Hunhandle*, const char*);

static int hs_load(const char* name) {
	if (lib) return 1;
	void* h = dlopen(name, RTLD_NOW | RTLD_LOCAL);
	if (!h) return 0;
	hs_create = dlsym(h, "Hunspell_create");
	hs_destroy = dlsym(h, "Hunspell_destroy");
	hs_spell = dlsym(h, "Hunspell_spell");
	hs_suggest = dlsym(h, "Hunspell_suggest");
	hs_free_list = dlsym(h, "Hunspell_free_list");
	hs_add = dlsym(h, "Hunspell_add");
	if (!hs_create || !hs_destroy || !hs_spell || !hs_suggest || !hs_free_list || !hs_add) {
		dlclose(h);
		return 0;
	}
	lib = h;
	return 1;
}
static Hunhandle* hs_new(const char* aff, const char* dic) { return hs_create(aff, dic); }
static void hs_del(Hunhandle* h) { hs_destroy(h); }
static int hs_ok(Hunhandle* h, const char* w) { return hs_spell(h, w); }
static int hs_sugg(Hunhandle* h, char*** list, const char* w) { return hs_suggest(h, list, w); }
static void hs_free(Hunhandle* h, char*** list, int n) { hs_free_list(h, list, n); }
static int hs_put(Hunhandle* h, const char* w) { return hs_add(h, w); }
static char* hs_at(char** list, int i) { return list[i]; }
*/
import "C"

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

// libNames — под какими именами искать библиотеку (Debian 13 — первое).
var libNames = []string{"libhunspell-1.7.so.0", "libhunspell-1.7.so", "libhunspell.so"}

// Langs — словари по умолчанию.
var Langs = []string{"ru_RU", "en_US"}

// ErrNoLibrary — libhunspell в системе нет.
var ErrNoLibrary = errors.New("spell: libhunspell not found (" + strings.Join(libNames, ", ") + ")")

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
	return append(out, "/usr/share/hunspell", "/usr/share/myspell", "/usr/share/myspell/dicts")
}

// Open загружает словари langs и личный словарь personal ("" — без него).
// Ни библиотеки, ни одного словаря — ошибка с тем, что искали.
func Open(langs []string, personal string) (*Checker, error) {
	loaded := false
	for _, name := range libNames {
		cs := C.CString(name)
		ok := C.hs_load(cs) != 0
		C.free(unsafe.Pointer(cs))
		if ok {
			loaded = true
			break
		}
	}
	if !loaded {
		return nil, ErrNoLibrary
	}
	c := &Checker{personal: map[string]bool{}, path: personal, cache: map[string]bool{}}
	dirs := dictDirs()
	for _, lang := range langs {
		for _, dir := range dirs {
			aff, dic := filepath.Join(dir, lang+".aff"), filepath.Join(dir, lang+".dic")
			if _, err := os.Stat(dic); err != nil {
				continue
			}
			ca, cd := C.CString(aff), C.CString(dic)
			h := C.hs_new(ca, cd)
			C.free(unsafe.Pointer(ca))
			C.free(unsafe.Pointer(cd))
			if h != nil {
				c.dicts = append(c.dicts, h)
				c.Loaded = append(c.Loaded, lang)
			}
			break
		}
	}
	if len(c.dicts) == 0 {
		return nil, fmt.Errorf("spell: no dictionaries %v in %v", langs, dirs)
	}
	if personal != "" {
		if err := c.loadPersonal(); err != nil {
			return c, err // словари есть — проверять можно и без личного
		}
	}
	return c, nil
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

func (c *Checker) remember(w string) {
	c.personal[strings.ToLower(w)] = true
	cw := C.CString(w)
	defer C.free(unsafe.Pointer(cw))
	for _, h := range c.dicts {
		C.hs_put(h, cw)
	}
	clear(c.cache)
}

// Close освобождает словари.
func (c *Checker) Close() {
	for _, h := range c.dicts {
		C.hs_del(h)
	}
	c.dicts = nil
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

func (c *Checker) spell(word string) bool {
	if c.personal[strings.ToLower(word)] {
		return true
	}
	cw := C.CString(word)
	defer C.free(unsafe.Pointer(cw))
	for _, h := range c.dicts {
		if C.hs_ok(h, cw) != 0 {
			return true
		}
	}
	return false
}

// Suggest — до max вариантов замены из всех словарей, без повторов.
func (c *Checker) Suggest(word string, max int) []string {
	cw := C.CString(word)
	defer C.free(unsafe.Pointer(cw))
	var out []string
	seen := map[string]bool{}
	for _, h := range c.dicts {
		var list **C.char
		n := int(C.hs_sugg(h, &list, cw))
		for i := 0; i < n && len(out) < max; i++ {
			s := C.GoString(C.hs_at(list, C.int(i)))
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		if n > 0 {
			C.hs_free(h, &list, C.int(n))
		}
	}
	return out
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
				next, _ := utf8.DecodeRuneInString(text[i+size:])
				if unicode.IsLetter(next) || unicode.IsDigit(next) || r == '/' || r == ':' {
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
