package mmfake

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

type storedFile struct {
	info *model.FileInfo
	data []byte
}

// addFile регистрирует файл (под s.mu); превью у картинок — сами байты.
func (s *Server) addFile(userID, channelID, name string, data []byte) *model.FileInfo {
	mime := http.DetectContentType(data)
	fi := &model.FileInfo{
		Id: model.NewId(), CreatorId: userID, ChannelId: channelID, CreateAt: model.GetMillis(),
		Name: name, Extension: strings.TrimPrefix(filepath.Ext(name), "."), Size: int64(len(data)), MimeType: mime,
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		fi.Width, fi.Height, fi.HasPreviewImage = cfg.Width, cfg.Height, true
	}
	s.files[fi.Id] = &storedFile{info: fi, data: data}
	return fi
}

// attachFiles кладёт FileInfo загруженных файлов в метаданные поста (под s.mu).
func (s *Server) attachFiles(p *model.Post) {
	if len(p.FileIds) == 0 {
		return
	}
	if p.Metadata == nil {
		p.Metadata = &model.PostMetadata{}
	}
	for _, id := range p.FileIds {
		if f := s.files[id]; f != nil {
			f.info.PostId = p.Id
			p.Metadata.Files = append(p.Metadata.Files, f.info)
		}
	}
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request, me *model.User) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		s.fail(w, http.StatusBadRequest, "api.file.upload_file.read_request.app_error")
		return
	}
	channelID := r.FormValue("channel_id")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.members[me.Id][channelID] == nil {
		s.fail(w, http.StatusForbidden, "api.context.permissions.app_error")
		return
	}
	resp := &model.FileUploadResponse{}
	for _, fh := range r.MultipartForm.File["files"] {
		f, err := fh.Open()
		if err != nil {
			s.fail(w, http.StatusBadRequest, "api.file.upload_file.read_request.app_error")
			return
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			s.fail(w, http.StatusBadRequest, "api.file.upload_file.read_request.app_error")
			return
		}
		resp.FileInfos = append(resp.FileInfos, s.addFile(me.Id, channelID, fh.Filename, data))
	}
	w.WriteHeader(http.StatusCreated)
	s.json(w, resp)
}

// getFile отдаёт файл; для preview и thumbnail — те же байты.
func (s *Server) getFile(w http.ResponseWriter, r *http.Request, _ *model.User) {
	s.mu.Lock()
	f := s.files[r.PathValue("fid")]
	s.mu.Unlock()
	if f == nil {
		s.fail(w, http.StatusNotFound, "app.file_info.get.app_error")
		return
	}
	w.Header().Set("Content-Type", f.info.MimeType)
	w.Write(f.data)
}

func (s *Server) emojiRoute(w http.ResponseWriter, r *http.Request, me *model.User) {
	switch {
	case r.PathValue("a") == "name":
		s.emojiByName(w, r.PathValue("b"))
	case r.PathValue("b") == "image":
		s.emojiImage(w, r.PathValue("a"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) emojiByName(w http.ResponseWriter, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.emoji {
		if e.Name == name {
			s.json(w, e)
			return
		}
	}
	s.fail(w, http.StatusNotFound, "app.emoji.get_by_name.no_result")
}

func (s *Server) emojiImage(w http.ResponseWriter, id string) {
	s.mu.Lock()
	data := s.emojiData[id]
	s.mu.Unlock()
	if data == nil {
		s.fail(w, http.StatusNotFound, "app.emoji.get.no_result")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(data)
}

// AddEmoji заводит кастомный эмодзи с картинкой-кружком цвета c.
func (s *Server) AddEmoji(name string, c color.Color) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := &model.Emoji{Id: model.NewId(), Name: name}
	s.emoji = append(s.emoji, e)
	s.emojiData[e.Id] = circlePNG(32, c)
}

// gradientPNG — картинка для сидов: диагональный градиент.
func gradientPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(40 + 180*x/w), uint8(90 + 120*y/h), 200, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func circlePNG(size int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	r := size / 2
	for y := range size {
		for x := range size {
			if dx, dy := x-r, y-r; dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}
