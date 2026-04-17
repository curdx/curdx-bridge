// Package formatguard provides code fence detection and auto-wrapping guardrails.
// Source: claude_code_bridge/lib/format_guardrails.py
package formatguard

import (
	"regexp"
	"strings"
)

// codeStarts are line prefixes that suggest the line is code.
var codeStarts = []string{
	"def ",
	"class ",
	"async def ",
	"func ",
	"package ",
	"import ",
	"from ",
	"const ",
	"let ",
	"var ",
	"public ",
	"private ",
	"#include",
	"using ",
	"select ",
	"insert ",
	"update ",
	"delete ",
}

var keyValueRe = regexp.MustCompile(`^\s*[A-Za-z0-9_.-]+\s*:\s*.+$`)
var indentRe = regexp.MustCompile(`^\s{4,}\S`)

// codeySymbols that suggest a line is code.
var codeySymbols = []string{"{", "}", ";", "=>", "==", "!=", "::", "<-", "->"}

// WantsCodeFences checks if the message is asking for code fences.
func WantsCodeFences(message string) bool {
	if message == "" {
		return false
	}
	if strings.Contains(message, "```") {
		return true
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "code block") || strings.Contains(lower, "fenced") || strings.Contains(lower, "fence") {
		return true
	}
	if strings.Contains(message, "代码块") {
		return true
	}
	if strings.Contains(message, "多行代码") || strings.Contains(lower, "multi-line code") {
		return true
	}
	return false
}

// ApplyGuardrails applies code fence guardrails to a reply.
func ApplyGuardrails(message, reply string) string {
	if strings.TrimSpace(reply) == "" {
		return reply
	}
	if WantsCodeFences(message) {
		if hasUnbalancedFences(reply) {
			stripped := stripFences(reply)
			return ensureCodeFences(stripped)
		}
		return ensureCodeFences(reply)
	}
	return reply
}

func looksLikeKeyValue(line string) bool {
	return keyValueRe.MatchString(line)
}

func looksLikeCodeLine(line string, prevIsCode bool) bool {
	stripped := strings.TrimRight(line, "\n")
	trimmed := strings.TrimSpace(stripped)
	if trimmed == "" {
		return prevIsCode
	}
	lower := strings.ToLower(strings.TrimLeft(stripped, " \t"))

	for _, prefix := range codeStarts {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	lstripped := strings.TrimLeft(stripped, " \t")
	for _, prefix := range []string{"#!/bin/", "apiVersion:", "kind:", "metadata:", "spec:"} {
		if strings.HasPrefix(lstripped, prefix) {
			return true
		}
	}

	if looksLikeKeyValue(stripped) && !strings.HasPrefix(lstripped, "-") && !strings.HasPrefix(lstripped, "*") {
		return true
	}

	if indentRe.MatchString(stripped) {
		return true
	}

	for _, sym := range codeySymbols {
		if strings.Contains(stripped, sym) {
			return true
		}
	}

	return false
}

func guessLanguage(blockLines []string) string {
	first := ""
	for _, ln := range blockLines {
		if strings.TrimSpace(ln) != "" {
			first = strings.TrimSpace(ln)
			break
		}
	}
	if first == "" {
		return "text"
	}
	lower := strings.ToLower(first)

	if strings.HasPrefix(lower, "package ") || strings.HasPrefix(lower, "func ") {
		return "go"
	}
	if strings.HasPrefix(lower, "def ") || strings.HasPrefix(lower, "async def ") || strings.HasPrefix(lower, "import ") || strings.HasPrefix(lower, "from ") {
		return "python"
	}
	if strings.HasPrefix(lower, "#!/bin/bash") || strings.HasPrefix(lower, "#!/usr/bin/env bash") {
		return "bash"
	}
	if strings.HasPrefix(lower, "#!/usr/bin/env pwsh") || strings.HasPrefix(lower, "#!/usr/bin/pwsh") ||
		strings.HasPrefix(lower, "#requires ") || strings.HasPrefix(lower, "param(") ||
		strings.HasPrefix(lower, "[cmdletbinding(") {
		return "powershell"
	}
	if strings.HasPrefix(first, "{") || strings.HasPrefix(first, "[") {
		return "json"
	}
	if strings.Contains(first, ":") && !strings.ContainsAny(first, ";{}") {
		return "yaml"
	}
	if strings.HasPrefix(lower, "class ") && strings.Contains(first, "{") {
		return "ts"
	}
	if strings.HasPrefix(lower, "select ") || strings.HasPrefix(lower, "insert ") || strings.HasPrefix(lower, "update ") || strings.HasPrefix(lower, "delete ") {
		return "sql"
	}
	return "text"
}

func ensureCodeFences(reply string) string {
	lines := strings.Split(reply, "\n")
	var out []string
	i := 0
	n := len(lines)
	minLines := 4
	inFence := false

	for i < n {
		line := lines[i]
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			inFence = !inFence
			out = append(out, line)
			i++
			continue
		}
		if inFence {
			out = append(out, line)
			i++
			continue
		}
		if !looksLikeCodeLine(line, false) {
			out = append(out, line)
			i++
			continue
		}
		// expand possible code block
		j := i
		codeLineCount := 0
		prevIsCode := false
		for j < n {
			ln := lines[j]
			isCode := looksLikeCodeLine(ln, prevIsCode)
			if !isCode && strings.TrimSpace(ln) != "" {
				break
			}
			if isCode && strings.TrimSpace(ln) != "" {
				codeLineCount++
			}
			prevIsCode = isCode
			j++
		}
		block := lines[i:j]
		if len(block) >= minLines && codeLineCount >= 3 {
			lang := guessLanguage(block)
			out = append(out, "```"+lang)
			out = append(out, block...)
			out = append(out, "```")
			i = j
			continue
		}
		out = append(out, line)
		i++
	}
	return strings.TrimRight(strings.Join(out, "\n"), " \t\n")
}

func hasUnbalancedFences(text string) bool {
	count := 0
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			count++
		}
	}
	return (count % 2) == 1
}

func stripFences(text string) string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\n")
}
