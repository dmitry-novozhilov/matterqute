//go:build !windows

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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// libNames — под какими именами искать библиотеку (Debian 13 — первое, dylib — macOS с Homebrew).
var libNames = []string{"libhunspell-1.7.so.0", "libhunspell-1.7.so", "libhunspell.so", "libhunspell-1.7.dylib", "libhunspell.dylib"}

// ErrNoLibrary — libhunspell в системе нет.
var ErrNoLibrary = errors.New("spell: libhunspell not found (" + strings.Join(libNames, ", ") + ")")

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
