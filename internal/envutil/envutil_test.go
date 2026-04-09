package envutil

import (
	"os"
	"testing"
)

// Source: claude_code_bridge/test/test_env_utils.py

func TestEnvBoolTruthyAndFalsy(t *testing.T) {
	// Unset → returns default
	os.Unsetenv("X")
	if !EnvBool("X", true) {
		t.Error("unset with default=true should return true")
	}
	if EnvBool("X", false) {
		t.Error("unset with default=false should return false")
	}

	// Truthy values
	for _, v := range []string{"1", "true", "yes", "on", " TRUE ", "Yes"} {
		os.Setenv("X", v)
		if !EnvBool("X", false) {
			t.Errorf("EnvBool(%q, false) should be true", v)
		}
	}

	// Falsy values
	for _, v := range []string{"0", "false", "no", "off", " 0 ", "False"} {
		os.Setenv("X", v)
		if EnvBool("X", true) {
			t.Errorf("EnvBool(%q, true) should be false", v)
		}
	}

	// Unrecognized → returns default
	os.Setenv("X", "maybe")
	if !EnvBool("X", true) {
		t.Error("unrecognized with default=true should return true")
	}
	if EnvBool("X", false) {
		t.Error("unrecognized with default=false should return false")
	}

	os.Unsetenv("X")
}

func TestEnvInt(t *testing.T) {
	os.Unsetenv("Y")
	if EnvInt("Y", 42) != 42 {
		t.Error("unset should return default")
	}

	os.Setenv("Y", "123")
	if EnvInt("Y", 0) != 123 {
		t.Error("valid int should parse")
	}

	os.Setenv("Y", " 456 ")
	if EnvInt("Y", 0) != 456 {
		t.Error("should trim whitespace")
	}

	os.Setenv("Y", "abc")
	if EnvInt("Y", 99) != 99 {
		t.Error("invalid should return default")
	}

	os.Setenv("Y", "")
	if EnvInt("Y", 77) != 77 {
		t.Error("empty should return default")
	}

	os.Unsetenv("Y")
}
