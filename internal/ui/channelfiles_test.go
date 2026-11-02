package ui

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// «Файлы канала»: вложения канала новыми сверху, поиск по имени и ext:,
// картинка — в просмотре, новый файл — сразу в открытом списке.
func TestChannelFiles(t *testing.T) {
	_, s := startFake(t, model.CollapsedThreadsAlwaysOn)
	onMain(func() {
		m := NewMainWindow(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "", t.TempDir())
		defer m.Close()
		if openTown(t, m) == nil {
			return
		}
		backend := channelByName(m, "dev", "backend").Id
		// Как у сервера: в поиске по всем командам имя канала не найти (оно
		// ищется в команде) — файлов ноль; id канала — работает.
		search := func(terms string) int {
			list, _, err := s.Client.SearchFilesWithParams(context.Background(), "", &model.SearchParameter{Terms: &terms})
			if err != nil {
				t.Error(err)
				return -1
			}
			return len(list.Order)
		}
		if byName, byID := search("in:backend"), search("in:"+backend); byName != 0 || byID != 4 {
			t.Errorf("search across teams: by name %d, by id %d", byName, byID)
		}
		m.selectChannel(backend)
		fp := m.files
		m.toggleFiles()
		if !waitFor(t, "backend files", func() bool { return fp.list.Count() == 4 }) {
			return
		}
		names := func() (out []string) {
			for i := range fp.list.Count() {
				out = append(out, fp.files[fp.list.Item(i).Data(fileIDRole).ToString()].Name)
			}
			return out
		}
		got := names()
		slices.Sort(got)
		if strings.Join(got, ",") != "bug.avi,deploy.gif,load-report.pdf,схема.png" || fp.info.Text() != "4 файла" {
			t.Errorf("files %v, info %q", got, fp.info.Text())
		}
		shot(t, m, "channel-files")
		fp.field.SetText("ext:pdf")
		if !waitFor(t, "ext:pdf", func() bool { return fp.list.Count() == 1 && names()[0] == "load-report.pdf" }) {
			return
		}
		fp.field.SetText("схем")
		if !waitFor(t, "by name", func() bool { return fp.list.Count() == 1 && names()[0] == "схема.png" }) {
			return
		}
		// Картинка — в просмотре.
		m.openChannelFile(fp.files[fp.list.Item(0).Data(fileIDRole).ToString()])
		if m.viewer == nil {
			t.Error("image did not open in the viewer")
		} else {
			m.viewer.w.Close()
			settle()
		}

		// Новый файл в канале — в открытом списке.
		fp.field.Clear()
		waitFor(t, "all again", func() bool { return fp.list.Count() == 4 })
		ctx := context.Background()
		up, _, err := s.Client.UploadFile(ctx, []byte("hello"), backend, "notes.txt")
		if err != nil {
			t.Error(err)
			return
		}
		if _, _, err := s.Client.CreatePost(ctx, &model.Post{ChannelId: backend, Message: "заметки", FileIds: []string{up.FileInfos[0].Id}}); err != nil {
			t.Error(err)
			return
		}
		if !waitFor(t, "new file in the list", func() bool { return fp.list.Count() == 5 && names()[0] == "notes.txt" }) {
			return
		}

		// Список следует за каналом; в личке файлов нет.
		for _, ch := range m.st.DirectChannels() {
			if ch.Type == model.ChannelTypeDirect && m.st.DMPeer(ch) == m.st.UserByName("carol").Id {
				m.selectChannel(ch.Id)
				if !waitFor(t, "DM files", func() bool { return fp.channel == ch.Id && fp.info.Text() == "В канале нет файлов" }) {
					return
				}
			}
		}
		m.toggleFiles()
		if m.filesShown() {
			t.Error("toggle did not close")
		}
	})
}
