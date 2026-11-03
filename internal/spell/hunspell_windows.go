//go:build windows

package spell

import (
	"errors"
	"strings"
)

// ErrNoLibrary — на Windows hunspell не подключён: проверки орфографии нет,
// личный словарь работает (слова из него считаются верными).
var ErrNoLibrary = errors.New("spell: hunspell is not available on Windows")

// Open — библиотеки нет: ошибка, приложение работает без проверки.
func Open(langs []string, personal string) (*Checker, error) {
	return nil, ErrNoLibrary
}

func (c *Checker) remember(w string) {
	c.personal[strings.ToLower(w)] = true
	clear(c.cache)
}

// Close освобождает словари.
func (c *Checker) Close() { c.dicts = nil }

func (c *Checker) spell(word string) bool { return c.personal[strings.ToLower(word)] }

// Suggest — вариантов замены без словарей нет.
func (c *Checker) Suggest(word string, max int) []string { return nil }
