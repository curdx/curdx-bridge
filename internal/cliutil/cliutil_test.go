package cliutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	err := AtomicWriteText(path, "hello world")
	if err != nil {
		t.Fatalf("AtomicWriteText failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

func TestAtomicWriteTextCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "test.txt")

	err := AtomicWriteText(path, "nested")
	if err != nil {
		t.Fatalf("AtomicWriteText failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got %q", string(data))
	}
}

func TestAtomicWriteTextOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	AtomicWriteText(path, "first")
	AtomicWriteText(path, "second")

	data, _ := os.ReadFile(path)
	if string(data) != "second" {
		t.Errorf("expected 'second', got %q", string(data))
	}
}

func TestNormalizeMessageParts(t *testing.T) {
	tests := []struct {
		input []string
		want  string
	}{
		{[]string{"hello", "world"}, "hello world"},
		{[]string{" hello ", " world "}, "hello   world"},
		{[]string{""}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		got := NormalizeMessageParts(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeMessageParts(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
