package i18n

import "testing"

func TestDetect(t *testing.T) {
	for _, env := range []string{"KOMAR_LANG", "LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		t.Setenv(env, "")
	}
	t.Setenv("LANG", "ru_RU.UTF-8")
	if Detect() != RU {
		t.Error("ru_RU.UTF-8 should be Russian")
	}
	t.Setenv("LC_ALL", "en_US.UTF-8")
	if Detect() != EN {
		t.Error("LC_ALL wins over LANG")
	}
	t.Setenv("LC_ALL", "C")
	if Detect() != RU {
		t.Error("LC_ALL=C should be skipped")
	}
	t.Setenv("KOMAR_LANG", "en")
	if Detect() != EN {
		t.Error("KOMAR_LANG overrides")
	}
	t.Setenv("KOMAR_LANG", "")
	t.Setenv("LANGUAGE", "ru:en")
	if Detect() != RU {
		t.Error("LANGUAGE list")
	}
}

func TestEveryKeyTranslated(t *testing.T) {
	for k := range en {
		if _, ok := ru[k]; !ok {
			t.Errorf("missing Russian string for %q", k)
		}
	}
	for k := range ru {
		if _, ok := en[k]; !ok {
			t.Errorf("Russian string %q has no English original", k)
		}
	}
}
