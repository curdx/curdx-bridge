package formatguard

import (
	"strings"
	"testing"
)

// Source: claude_code_bridge/test/test_format_guardrails.py

func TestApplyGuardrailsAddsFenceWhenRequested(t *testing.T) {
	message := "请用代码块输出，多行代码"
	reply := strings.Join([]string{
		"Title",
		"def main():",
		"    print('ok')",
		"    return 0",
		"",
		"if __name__ == '__main__':",
		"    main()",
	}, "\n")
	fixed := ApplyGuardrails(message, reply)
	if !strings.Contains(fixed, "```") {
		t.Error("should contain code fences")
	}
	if !strings.Contains(fixed, "def main()") {
		t.Error("should contain original code")
	}
}

func TestApplyGuardrailsRepairsUnbalancedFence(t *testing.T) {
	message := "请用代码块输出，多行代码"
	reply := strings.Join([]string{
		"Title",
		"```python",
		"def main():",
		"    print('ok')",
		"    return 0",
		"",
		"if __name__ == '__main__':",
		"    main()",
	}, "\n")
	fixed := ApplyGuardrails(message, reply)
	count := strings.Count(fixed, "```")
	if count%2 != 0 {
		t.Errorf("fence count should be even, got %d", count)
	}
}

func TestApplyGuardrailsAddsMissingFencesOutsideBlocks(t *testing.T) {
	message := "请用代码块输出，多行代码"
	reply := strings.Join([]string{
		"Title A",
		"def one():",
		"    a = 1",
		"    b = 2",
		"    c = a + b",
		"    return c",
		"print(one())",
		"```js",
		"function two() {",
		"  return 2;",
		"}",
		"```",
		"",
		"Title B",
		"class Three:",
		"    def __init__(self):",
		"        self.value = 3",
		"    def get(self):",
		"        return self.value",
		"print(Three().get())",
	}, "\n")
	fixed := ApplyGuardrails(message, reply)
	count := strings.Count(fixed, "```")
	if count < 4 {
		t.Errorf("should have at least 4 fence markers, got %d", count)
	}
}

func TestWantsCodeFences(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"show me code", false},
		{"use ``` to format", true},
		{"give me a code block", true},
		{"use fenced output", true},
		{"请用代码块", true},
		{"请用多行代码", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := WantsCodeFences(tt.msg); got != tt.want {
			t.Errorf("WantsCodeFences(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestApplyGuardrailsEmptyReply(t *testing.T) {
	result := ApplyGuardrails("code block please", "")
	if result != "" {
		t.Errorf("empty reply should stay empty, got %q", result)
	}
	result = ApplyGuardrails("code block please", "   ")
	if result != "   " {
		t.Errorf("whitespace reply should stay unchanged, got %q", result)
	}
}
