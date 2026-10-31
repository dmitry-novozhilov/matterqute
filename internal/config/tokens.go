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
// Service на Linux), а если его нет — в tokens.json с правами 0600.
type Tokens struct {
	// Backend — "keyring" или путь к файлу; для логов и UI.
	Backend string

	file string // пусто, если работаем через keyring
	mu   sync.Mutex
}

// OpenTokens выбирает хранилище; err — почему keyring недоступен (не фатально).
func OpenTokens(dir string) (*Tokens, error) {
	_, err := keyring.Get(appName, "probe")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return &Tokens{Backend: "keyring"}, nil
	}
	file := filepath.Join(dir, "tokens.json")
	return &Tokens{Backend: file, file: file}, fmt.Errorf("keyring unavailable, tokens go to %s: %w", file, err)
}

func (t *Tokens) Get(serverURL string) (string, error) {
	if t.file == "" {
		tok, err := keyring.Get(appName, serverURL)
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNoToken
		}
		if err != nil {
			return "", fmt.Errorf("keyring get %s: %w", serverURL, err)
		}
		return tok, nil
	}
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

func (t *Tokens) Set(serverURL, token string) error {
	if t.file == "" {
		if err := keyring.Set(appName, serverURL, token); err != nil {
			return fmt.Errorf("keyring set %s: %w", serverURL, err)
		}
		return nil
	}
	return t.updateFile(func(m map[string]string) { m[serverURL] = token })
}

func (t *Tokens) Delete(serverURL string) error {
	if t.file == "" {
		err := keyring.Delete(appName, serverURL)
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("keyring delete %s: %w", serverURL, err)
		}
		return nil
	}
	return t.updateFile(func(m map[string]string) { delete(m, serverURL) })
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

func (t *Tokens) updateFile(change func(map[string]string)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, err := t.readFile()
	if err != nil {
		return err
	}
	change(m)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode tokens: %w", err)
	}
	return writeFileAtomic(t.file, data, 0o600)
}
