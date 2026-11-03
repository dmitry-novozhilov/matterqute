package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNoToken — для сервера нет сохранённой сессии.
var ErrNoToken = errors.New("no saved session token")

// Tokens хранит токены сессий по URL сервера: в системном keyring (Secret
// Service на Linux), а если его нет — в tokens.json с правами 0600. Файл
// читается и при живом keyring: токен, записанный в файл, когда keyring был
// недоступен (например, коллекция была заблокирована и пользователь закрыл
// окно разблокировки), переезжает в keyring, а при выходе стирается из
// обоих — иначе сессия «терялась», а её токен оставался на диске.
type Tokens struct {
	// Backend — "keyring" или путь к файлу; для логов и UI.
	Backend string

	keyring bool   // основное хранилище — keyring; файл — запасное
	file    string // tokens.json
	mu      sync.Mutex
}

// FileTokens — токены только в файле path (0600), без keyring; для тестов и
// систем, где keyring не нужен.
func FileTokens(path string) *Tokens { return &Tokens{Backend: path, file: path} }

// OpenTokens выбирает хранилище; err — почему keyring недоступен (не фатально).
func OpenTokens(dir string) (*Tokens, error) {
	file := filepath.Join(dir, "tokens.json")
	_, err := keyring.Get(appName, "probe")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return &Tokens{Backend: "keyring", keyring: true, file: file}, nil
	}
	return &Tokens{Backend: file, file: file}, fmt.Errorf("keyring unavailable, tokens go to %s: %w", file, err)
}

func (t *Tokens) Get(serverURL string) (string, error) {
	if t.keyring {
		tok, err := keyring.Get(appName, serverURL)
		if err == nil {
			return tok, nil
		}
		if !errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("keyring get %s: %w", serverURL, err)
		}
	}
	tok, err := t.fromFile(serverURL)
	if err != nil {
		return "", err
	}
	if t.keyring {
		// Лежал в файле, пока keyring был недоступен: теперь — в keyring.
		if err := keyring.Set(appName, serverURL, tok); err == nil {
			_ = t.updateFile(func(m map[string]string) { delete(m, serverURL) })
		}
	}
	return tok, nil
}

func (t *Tokens) Set(serverURL, token string) error {
	if t.keyring {
		if err := keyring.Set(appName, serverURL, token); err != nil {
			return fmt.Errorf("keyring set %s: %w", serverURL, err)
		}
		return t.updateFile(func(m map[string]string) { delete(m, serverURL) }) // старая копия в файле, если была
	}
	return t.updateFile(func(m map[string]string) { m[serverURL] = token })
}

func (t *Tokens) Delete(serverURL string) error {
	if t.keyring {
		if err := keyring.Delete(appName, serverURL); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("keyring delete %s: %w", serverURL, err)
		}
	}
	return t.updateFile(func(m map[string]string) { delete(m, serverURL) })
}

func (t *Tokens) fromFile(serverURL string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, err := t.readFile()
	if err != nil {
		return "", err
	}
	tok, ok := m[serverURL]
	if !ok {
		return "", ErrNoToken
	}
	return tok, nil
}

func (t *Tokens) readFile() (map[string]string, error) {
	m := map[string]string{}
	data, err := os.ReadFile(t.file)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tokens: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse tokens %s: %w", t.file, err)
	}
	return m, nil
}

// updateFile меняет файл; пустой — удаляется (а не создаётся пустым).
func (t *Tokens) updateFile(change func(map[string]string)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, err := t.readFile()
	if err != nil {
		return err
	}
	change(m)
	if len(m) == 0 {
		if err := os.Remove(t.file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove tokens %s: %w", t.file, err)
		}
		return nil
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}
	return WriteFileAtomic(t.file, data, 0o600)
}
