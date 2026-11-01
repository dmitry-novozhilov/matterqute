package mm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// UploadFile грузит файл в канал потоком, сообщая прогресс (отправлено байт
// из total). Client4.UploadFile держит файл в памяти целиком и прогресса не даёт.
func (s *Session) UploadFile(ctx context.Context, channelID, name string, r io.Reader, total int64, progress func(sent, total int64)) (*model.FileInfo, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			if err := mw.WriteField("channel_id", channelID); err != nil {
				return err
			}
			part, err := mw.CreateFormFile("files", name)
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, &countingReader{r: r, total: total, progress: progress}); err != nil {
				return err
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ServerURL+model.APIURLSuffix+"/files", pr)
	if err != nil {
		pr.Close()
		return nil, fmt.Errorf("upload %q to %s: %w", name, s.ServerURL, err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(model.HeaderAuth, model.HeaderBearer+" "+s.Token())
	resp, err := s.Client.HTTPClient.Do(req)
	if err != nil {
		pr.Close()
		return nil, &ServerError{ServerURL: s.ServerURL, Op: fmt.Sprintf("upload %q", name), Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		se := &ServerError{ServerURL: s.ServerURL, Op: fmt.Sprintf("upload %q", name), Status: resp.StatusCode}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		var appErr model.AppError
		if json.Unmarshal(body, &appErr) == nil && appErr.Id != "" {
			se.ID, se.Err = appErr.Id, &appErr
		} else {
			se.Err = fmt.Errorf("%s", truncate(strings.TrimSpace(string(body)), 200))
		}
		return nil, se
	}
	var out model.FileUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("upload %q to %s: decode response: %w", name, s.ServerURL, err)
	}
	if len(out.FileInfos) == 0 {
		return nil, fmt.Errorf("upload %q to %s: server returned no file info", name, s.ServerURL)
	}
	return out.FileInfos[0], nil
}

type countingReader struct {
	r        io.Reader
	sent     int64
	total    int64
	progress func(sent, total int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.sent += int64(n)
	if c.progress != nil && n > 0 {
		c.progress(c.sent, c.total)
	}
	return n, err
}
