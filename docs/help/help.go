// Package help — справка для пользователя: статьи markdown в этом каталоге
// (их же видно в репозитории), встроенные в бинарник для меню «Справка».
package help

import (
	"embed"
	"io/fs"
	"slices"
	"strings"
)

//go:embed *.md
var files embed.FS

// Article — статья справки.
type Article struct {
	Name  string // имя файла: links.md
	Title string // первый заголовок «# …»
}

// Articles — статьи для меню, кроме оглавления (README.md), по имени файла.
func Articles() []Article {
	entries, _ := fs.ReadDir(files, ".")
	var out []Article
	for _, e := range entries {
		if e.Name() == "README.md" || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, Article{Name: e.Name(), Title: title(Text(e.Name()))})
	}
	slices.SortFunc(out, func(a, b Article) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Text — текст статьи name; нет такой — пусто.
func Text(name string) string {
	data, err := files.ReadFile(name)
	if err != nil {
		return ""
	}
	return string(data)
}

func title(md string) string {
	for _, l := range strings.Split(md, "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}
