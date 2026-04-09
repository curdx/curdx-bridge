package i18n

import (
	"os"
	"testing"
)

func TestDetectLanguageZh(t *testing.T) {
	os.Setenv("CCB_LANG", "zh")
	defer os.Unsetenv("CCB_LANG")
	if DetectLanguage() != "zh" {
		t.Error("CCB_LANG=zh should detect zh")
	}
}

func TestDetectLanguageEn(t *testing.T) {
	os.Setenv("CCB_LANG", "en")
	defer os.Unsetenv("CCB_LANG")
	if DetectLanguage() != "en" {
		t.Error("CCB_LANG=en should detect en")
	}
}

func TestDetectLanguageAutoFromLANG(t *testing.T) {
	os.Unsetenv("CCB_LANG")
	os.Setenv("LANG", "zh_CN.UTF-8")
	defer os.Unsetenv("LANG")
	if DetectLanguage() != "zh" {
		t.Error("LANG=zh_CN should detect zh")
	}
}

func TestDetectLanguageDefaultEn(t *testing.T) {
	os.Unsetenv("CCB_LANG")
	os.Unsetenv("LANG")
	os.Unsetenv("LC_ALL")
	os.Unsetenv("LC_MESSAGES")
	if DetectLanguage() != "en" {
		t.Error("no locale should default to en")
	}
}

func TestTFormatting(t *testing.T) {
	// Reset cached lang
	langMu.Lock()
	currentLang = "en"
	langMu.Unlock()

	result := T("banner_title", map[string]string{"version": "1.0"})
	if result != "Claude Code Bridge 1.0" {
		t.Errorf("expected 'Claude Code Bridge 1.0', got %q", result)
	}
}

func TestTFallbackToEnglish(t *testing.T) {
	langMu.Lock()
	currentLang = "zh"
	langMu.Unlock()

	// All keys exist in both languages, test unknown key falls back
	result := T("nonexistent_key_xyz", nil)
	if result != "nonexistent_key_xyz" {
		t.Errorf("missing key should return key itself, got %q", result)
	}
}

func TestSetLang(t *testing.T) {
	SetLang("zh")
	if GetLang() != "zh" {
		t.Error("SetLang(zh) should set zh")
	}
	SetLang("en")
	if GetLang() != "en" {
		t.Error("SetLang(en) should set en")
	}
	SetLang("invalid")
	if GetLang() != "en" {
		t.Error("invalid lang should not change current")
	}
}
