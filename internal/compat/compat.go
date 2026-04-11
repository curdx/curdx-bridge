// Package compat provides cross-platform compatibility utilities.
// Source: claude_code_bridge/lib/compat.py
package compat

import (
	"io"
	"os"
	"runtime"
	"strings"
	"unicode/utf8"
)

// SetupWindowsEncoding configures UTF-8 encoding for Windows console.
// On non-Windows platforms, this is a no-op.
// Go handles encoding differently from Python — os.Stdout/Stderr write raw bytes.
func SetupWindowsEncoding() {
	// No-op in Go. Kept for API compatibility with Python version.
}

// DecodeStdinBytes decodes raw stdin bytes robustly (especially on Windows).
//
// Strategy (matches Python exactly):
//  1. Honor BOMs (UTF-8/UTF-16).
//  2. CURDX_STDIN_ENCODING override.
//  3. Try UTF-8 strictly.
//  4. Fallback to locale preferred encoding (best effort).
//  5. Windows fallback: treat as Latin-1 (approximate mbcs).
//  6. Last resort: UTF-8 with replacement.
func DecodeStdinBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// BOM detection first.
	if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
		// UTF-8 BOM — strip BOM and return
		return string(data[3:])
	}
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		// UTF-16LE BOM
		if s, ok := decodeUTF16LE(data[2:]); ok {
			return s
		}
	}
	if len(data) >= 2 && data[0] == 0xfe && data[1] == 0xff {
		// UTF-16BE BOM
		if s, ok := decodeUTF16BE(data[2:]); ok {
			return s
		}
	}

	// CURDX_STDIN_ENCODING override
	forced := strings.TrimSpace(os.Getenv("CURDX_STDIN_ENCODING"))
	if forced != "" {
		lower := strings.ToLower(forced)
		if lower == "utf-8" || lower == "utf8" {
			if utf8.Valid(data) {
				return string(data)
			}
			return replaceInvalidUTF8(data)
		}
		// For other encodings, try as Latin-1 (lossless byte-to-rune)
		if lower == "latin-1" || lower == "iso-8859-1" || lower == "latin1" ||
			lower == "mbcs" || lower == "windows-1252" || lower == "cp1252" {
			return decodeLatin1(data)
		}
		// Unknown encoding: try UTF-8, fall back to replacement
		if utf8.Valid(data) {
			return string(data)
		}
		return replaceInvalidUTF8(data)
	}

	// Try UTF-8 strictly
	if utf8.Valid(data) {
		return string(data)
	}

	// Windows fallback: approximate mbcs with Latin-1
	if runtime.GOOS == "windows" {
		return decodeLatin1(data)
	}

	// Last resort: UTF-8 with replacement
	return replaceInvalidUTF8(data)
}

// ReadStdinText reads all text from stdin using robust decoding.
func ReadStdinText() string {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}
	return DecodeStdinBytes(data)
}

// decodeLatin1 does a lossless byte-to-rune conversion (ISO 8859-1).
func decodeLatin1(data []byte) string {
	runes := make([]rune, len(data))
	for i, b := range data {
		runes[i] = rune(b)
	}
	return string(runes)
}

// decodeUTF16LE decodes UTF-16 Little Endian bytes to string, handling surrogate pairs.
func decodeUTF16LE(data []byte) (string, bool) {
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	var buf strings.Builder
	for i := 0; i+1 < len(data); i += 2 {
		u := uint16(data[i]) | uint16(data[i+1])<<8
		if u >= 0xD800 && u <= 0xDBFF && i+3 < len(data) {
			// High surrogate — read the low surrogate.
			u2 := uint16(data[i+2]) | uint16(data[i+3])<<8
			if u2 >= 0xDC00 && u2 <= 0xDFFF {
				r := rune((uint32(u)-0xD800)*0x400 + (uint32(u2) - 0xDC00) + 0x10000)
				buf.WriteRune(r)
				i += 2
				continue
			}
		}
		buf.WriteRune(rune(u))
	}
	return buf.String(), true
}

// decodeUTF16BE decodes UTF-16 Big Endian bytes to string, handling surrogate pairs.
func decodeUTF16BE(data []byte) (string, bool) {
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	var buf strings.Builder
	for i := 0; i+1 < len(data); i += 2 {
		u := uint16(data[i])<<8 | uint16(data[i+1])
		if u >= 0xD800 && u <= 0xDBFF && i+3 < len(data) {
			u2 := uint16(data[i+2])<<8 | uint16(data[i+3])
			if u2 >= 0xDC00 && u2 <= 0xDFFF {
				r := rune((uint32(u)-0xD800)*0x400 + (uint32(u2) - 0xDC00) + 0x10000)
				buf.WriteRune(r)
				i += 2
				continue
			}
		}
		buf.WriteRune(rune(u))
	}
	return buf.String(), true
}

// replaceInvalidUTF8 replaces invalid UTF-8 sequences with U+FFFD.
func replaceInvalidUTF8(data []byte) string {
	var buf strings.Builder
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 {
			buf.WriteRune('\uFFFD')
			i++
		} else {
			buf.WriteRune(r)
			i += size
		}
	}
	return buf.String()
}
