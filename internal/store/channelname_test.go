package store

import "testing"

func TestChannelURLName(t *testing.T) {
	for in, want := range map[string]string{
		"Релизы 2.0":       "relizy-2-0",
		"Backend API":      "backend-api",
		"  Щука & Ёжик!  ": "shchuka-ezhik",
		"dev_ops":          "dev_ops",
		"Объявления":       "obyavleniya",
		"🎉":                "",
		"Очень длинное название канала, которое никак не влезет в шестьдесят четыре символа адреса": "ochen-dlinnoe-nazvanie-kanala-kotoroe-nikak-ne-vlezet-v-shestdes",
	} {
		if got := ChannelURLName(in); got != want {
			t.Errorf("ChannelURLName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidChannelName(t *testing.T) {
	for name, ok := range map[string]bool{"relizy": true, "dev_ops-2": true, "": false, "-x": false, "Релизы": false, "a b": false} {
		if got := ValidChannelName(name) == ""; got != ok {
			t.Errorf("ValidChannelName(%q) ok=%v, want %v", name, got, ok)
		}
	}
}
