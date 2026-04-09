package compat

import (
	"os"
	"testing"
	"unicode/utf8"
)

// Source: claude_code_bridge/test/test_compat_stdin_decode.py

func TestDecodeStdinBytesPreferUTF8WhenValid(t *testing.T) {
	raw := []byte("你好")
	result := DecodeStdinBytes(raw)
	if result != "你好" {
		t.Errorf("expected '你好', got %q", result)
	}
}

func TestDecodeStdinBytesNeverEmitsSurrogates(t *testing.T) {
	// Invalid UTF-8 byte 0x80 should not end up as a lone surrogate.
	out := DecodeStdinBytes([]byte("abc\x80def"))
	if !utf8.ValidString(out) {
		t.Errorf("output contains invalid UTF-8: %q", out)
	}
	// Should not contain \udc80
	for _, r := range out {
		if r >= 0xD800 && r <= 0xDFFF {
			t.Errorf("output contains surrogate: %q", out)
			break
		}
	}
}

func TestDecodeStdinBytesHonorsUTF16LEBOM(t *testing.T) {
	// Python: b"\xff\xfe" + "你好".encode("utf-16le")
	bom := []byte{0xff, 0xfe}
	// "你好" in UTF-16LE: 0x60 0x4f 0x7d 0x59
	payload := []byte{0x60, 0x4f, 0x7d, 0x59}
	raw := append(bom, payload...)
	result := DecodeStdinBytes(raw)
	if result != "你好" {
		t.Errorf("expected '你好', got %q", result)
	}
}

func TestDecodeStdinBytesEmpty(t *testing.T) {
	if DecodeStdinBytes(nil) != "" {
		t.Error("nil should return empty string")
	}
	if DecodeStdinBytes([]byte{}) != "" {
		t.Error("empty should return empty string")
	}
}

func TestDecodeStdinBytesUTF8BOM(t *testing.T) {
	raw := append([]byte{0xef, 0xbb, 0xbf}, []byte("hello")...)
	result := DecodeStdinBytes(raw)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestDecodeStdinBytesOverrideEncoding(t *testing.T) {
	os.Setenv("CCB_STDIN_ENCODING", "utf-8")
	defer os.Unsetenv("CCB_STDIN_ENCODING")

	raw := []byte("hello")
	result := DecodeStdinBytes(raw)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}
