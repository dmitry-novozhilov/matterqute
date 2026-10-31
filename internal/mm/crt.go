package mm

import (
	"context"
	"fmt"
	"strconv"

	"github.com/mattermost/mattermost/server/public/model"
)

// CRTEnabled — включены ли у пользователя свёрнутые треды (Collapsed Reply
// Threads). Повторяет isCollapsedThreadsEnabled веб-клиента
// (mattermost-redux/selectors/entities/preferences.ts): сервер разрешает их,
// и либо они обязательны, либо их не выключил сам пользователь.
func CRTEnabled(clientConfig map[string]string, displayPrefs model.Preferences) bool {
	mode := clientConfig["CollapsedThreads"]
	if mode == "" || mode == model.CollapsedThreadsDisabled {
		return false
	}
	if mode == model.CollapsedThreadsAlwaysOn {
		return true
	}
	pref := "off"
	if mode == model.CollapsedThreadsDefaultOn {
		pref = "on"
	}
	for _, p := range displayPrefs {
		if p.Category == model.PreferenceCategoryDisplaySettings && p.Name == model.PreferenceNameCollapsedThreadsEnabled {
			pref = p.Value
		}
	}
	return pref == "on"
}

// Settings — настройки сервера и пользователя, от которых зависит поведение клиента.
type Settings struct {
	CRT         bool
	MaxFileSize int64 // байт; 0 — сервер не сказал
}

// FetchSettings читает клиентский конфиг сервера и настройки отображения пользователя.
func (s *Session) FetchSettings(ctx context.Context) (Settings, error) {
	cfg, resp, err := s.Client.GetClientConfig(ctx, "")
	if err != nil {
		return Settings{}, describe(s.ServerURL, "load client config", resp, err)
	}
	prefs, resp, err := s.Client.GetPreferencesByCategory(ctx, s.Me.Id, model.PreferenceCategoryDisplaySettings)
	if err != nil {
		return Settings{}, describe(s.ServerURL, fmt.Sprintf("load %s preferences", model.PreferenceCategoryDisplaySettings), resp, err)
	}
	st := Settings{CRT: CRTEnabled(cfg, prefs)}
	if v := cfg["MaxFileSize"]; v != "" {
		if st.MaxFileSize, err = strconv.ParseInt(v, 10, 64); err != nil {
			return st, fmt.Errorf("client config of %s: MaxFileSize %q: %w", s.ServerURL, truncate(v, 50), err)
		}
	}
	return st, nil
}
