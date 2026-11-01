package ui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mattermost/mattermost/server/public/model"

	"matterqute/internal/emoji"
)

// Окно выбора реакции: разделы, эмодзи сервера, цветные картинки, поиск,
// реакция, недавние — на сервере, как у веб-клиента.
func TestReactionPicker(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		root := openTown(t, m)
		if root == nil {
			return
		}
		m.pickReaction(root)
		pk := m.picker
		if pk == nil || !pk.w.IsVisible() {
			t.Fatal("picker not shown")
		}
		if pk.sections[0].title != "Недавние" || !slices.Equal(pk.sections[0].names, emoji.Quick) {
			t.Errorf("first section %q %v, want «Недавние» with quick reactions (no recent yet)", pk.sections[0].title, pk.sections[0].names)
		}
		if !waitFor(t, "custom emoji section", func() bool {
			last := pk.sections[len(pk.sections)-1]
			return last.title == "Эмодзи сервера" && slices.Contains(last.names, "partyparrot")
		}) {
			return
		}

		// Кнопка раздела прокручивает к нему.
		flags := pk.sections[len(pk.sections)-2]
		if flags.title != "Флаги" {
			t.Fatalf("section before custom is %q", flags.title)
		}
		pk.scroll.VerticalScrollBar().SetValue(flags.top)
		settle()
		shotWidget(t, pk.w.QWidget, "emoji-picker-flags")
		pk.scroll.VerticalScrollBar().SetValue(0)
		settle()
		shotWidget(t, pk.w.QWidget, "emoji-picker")

		// 😀 — первый в «Смайлах» — нарисован цветным: насыщенный цвет, не серый.
		smileys := pk.sections[1]
		x, y := pk.cell(smileys, 0)
		img := pk.grid.Grab().ToImage()
		c := img.PixelColor(int(float64(x+pickerCell/2)*img.DevicePixelRatio()), int(float64(y+pickerCell/2+4)*img.DevicePixelRatio()))
		if hi, lo := max(c.Red(), c.Green(), c.Blue()), min(c.Red(), c.Green(), c.Blue()); hi-lo < 80 {
			t.Errorf("😀 pixel %s: grey, not a colour emoji", c.Name())
		}

		// Поиск и Enter — реакция; окно закрывается.
		pk.search.SetText("tada")
		if got := pk.sections[0].names; len(got) == 0 || got[0] != "tada" {
			t.Errorf("search tada: %v", got)
		}
		qt.QCoreApplication_SendEvent(pk.search.QObject, qt.NewQKeyEvent(qt.QEvent__KeyPress, int(qt.Key_Return), qt.NoModifier).QEvent)
		if !waitFor(t, "tada reaction", func() bool { return HasReaction(feedPost(m.feed, root.Id), m.s.Me.Id, "tada") }) {
			return
		}
		waitFor(t, "picker closed", func() bool { return pk.closed })

		// Недавние — на сервере в формате веб-клиента, и следующее окно начинается с них.
		var saved emoji.Recent
		waitFor(t, "recent emoji saved", func() bool {
			prefs, _, err := m.s.Client.GetPreferencesByCategory(context.Background(), m.s.Me.Id, emoji.RecentCategory)
			if err != nil || len(prefs) != 1 || prefs[0].Name != m.s.Me.Id {
				return false
			}
			return json.Unmarshal([]byte(prefs[0].Value), &saved) == nil && len(saved) == 1
		})
		if len(saved) != 1 || saved[0] != (emoji.RecentEmoji{Name: "tada", UsageCount: 1}) {
			t.Errorf("saved recent %+v", saved)
		}
		m.pickReaction(root)
		if got := m.picker.sections[0].names; len(got) == 0 || got[0] != "tada" {
			t.Errorf("recent section after a reaction: %v", got)
		}
		m.picker.w.Close()
	})
}
