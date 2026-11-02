package ui

import (
	"context"
	"log/slog"

	qt "github.com/mappu/miqt/qt6"
)

// Отладка ленты (MATTERQUTE_DEBUG=1, пункты в меню ленты): структура
// документа по постам — в лог, без текста сообщений; и «перерисовать всё»,
// чтобы отличить испорченную вставку поста от раскладки Qt.

func (f *Feed) debugEnabled() bool {
	return f.d.Log != nil && f.d.Log.Enabled(context.Background(), slog.LevelDebug)
}

// debugDump — по посту: место во фрейме документа, высота текста, абзацы
// (сколько ужато до 0–1 px и сколько из них с текстом), длина HTML; и
// сверка фреймов документа с постами ленты.
func (f *Feed) debugDump() {
	f.doc.Size()
	lay := f.doc.DocumentLayout()
	root := f.doc.RootFrame()
	children := root.ChildFrames()
	inDoc := map[uintptr]bool{}
	for _, fr := range children {
		inDoc[uintptr(fr.UnsafePointer())] = true
	}
	f.d.Log.Debug("feed dump", "items", len(f.items), "pending", len(f.pending), "stale", len(f.stale),
		"root_frames", len(children), "doc_chars", f.doc.CharacterCount(), "shown", f.shown, "marker", f.newMarker,
		"width", f.view.Viewport().Width())
	for i, it := range f.items {
		attrs := []any{"i", i, "post", it.post.Id, "prev", it.prev, "type", it.post.Type, "edited", it.post.EditAt > 0,
			"html", len(it.html), "avatar", it.avatar != "", "bg", it.bg != "",
			"frame_in_doc", inDoc[uintptr(it.frame.UnsafePointer())], "first", it.frame.FirstPosition(), "last", it.frame.LastPosition()}
		if it.content != nil {
			r := lay.FrameBoundingRect(it.content)
			blocks, squashed, squashedText, chars := 0, 0, 0, 0
			for b := f.doc.FindBlock(it.content.FirstPosition()); b.IsValid() && b.Position() <= it.content.LastPosition(); b = b.Next() {
				blocks++
				chars += b.Length() - 1
				bf := b.BlockFormat()
				if bf.LineHeightType() == int(qt.QTextBlockFormat__FixedHeight) && bf.LineHeight2() <= 1 {
					squashed++
					if b.Length() > 1 {
						squashedText++
					}
				}
			}
			attrs = append(attrs, "nested", it.content.UnsafePointer() != it.frame.UnsafePointer(),
				"y", int(r.Top()), "h", int(r.Height()), "blocks", blocks, "chars", chars,
				"squashed", squashed, "squashed_with_text", squashedText)
		}
		f.d.Log.Debug("feed post", attrs...)
	}
}

// debugItems — пункты отладки для меню ленты; пусто — отладка выключена.
func (f *Feed) debugItems() []menuItem {
	if !f.debugEnabled() {
		return nil
	}
	return []menuItem{
		{title: "Отладка: структура ленты в лог", do: f.debugDump},
		{title: "Отладка: перерисовать ленту", do: f.renderAll},
	}
}
