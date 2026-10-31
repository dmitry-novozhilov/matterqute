package mm

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestCRTEnabled(t *testing.T) {
	pref := func(v string) model.Preferences {
		return model.Preferences{{Category: model.PreferenceCategoryDisplaySettings, Name: model.PreferenceNameCollapsedThreadsEnabled, Value: v}}
	}
	for _, c := range []struct {
		mode  string
		prefs model.Preferences
		want  bool
	}{
		{"", nil, false}, // старый сервер или ограниченный конфиг
		{model.CollapsedThreadsDisabled, pref("on"), false},
		{model.CollapsedThreadsAlwaysOn, pref("off"), true},
		{model.CollapsedThreadsDefaultOn, nil, true},
		{model.CollapsedThreadsDefaultOn, pref("off"), false},
		{model.CollapsedThreadsDefaultOff, nil, false},
		{model.CollapsedThreadsDefaultOff, pref("on"), true},
	} {
		if got := CRTEnabled(map[string]string{"CollapsedThreads": c.mode}, c.prefs); got != c.want {
			t.Errorf("CRTEnabled(%q, %v) = %v, want %v", c.mode, c.prefs, got, c.want)
		}
	}
}
