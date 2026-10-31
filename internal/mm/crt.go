package mm

import (
	"context"
	"fmt"

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

// FetchCRT спрашивает у сервера конфиг и настройки пользователя и решает про CRT.
func (s *Session) FetchCRT(ctx context.Context) (bool, error) {
	cfg, resp, err := s.Client.GetClientConfig(ctx, "")
	if err != nil {
		return false, describe(s.ServerURL, "load client config", resp, err)
	}
	prefs, resp, err := s.Client.GetPreferencesByCategory(ctx, s.Me.Id, model.PreferenceCategoryDisplaySettings)
	if err != nil {
		return false, describe(s.ServerURL, fmt.Sprintf("load %s preferences", model.PreferenceCategoryDisplaySettings), resp, err)
	}
	return CRTEnabled(cfg, prefs), nil
}
