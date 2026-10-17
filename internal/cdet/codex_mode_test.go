package cdet

import (
	"bytes"
	"strings"
	"testing"
)

func TestCodexImportDoesNotExposeConversationModes(t *testing.T) {
	for _, args := range [][]string{
		{"install", "codex", "--codex-mode", "home"},
		{"install", "codex", "--codex-mode", "conversations"},
		{"scan", "--codex-mode", "conversations"},
		{"install", "codex", "--codex-mode=home"},
	} {
		f := newFixture(t)
		var out, err bytes.Buffer
		args = append([]string{"--prefix", f.prefix, "--store", f.store}, args...)
		if (CLI{Out: &out, Err: &err}).Run(args) == 0 || !strings.Contains(err.String(), "未知选项") {
			t.Fatal("removed migration mode was accepted", args, err.String())
		}
		if !absent(f.prefix) || !absent(f.store) {
			t.Fatal("invalid mode option wrote managed directories")
		}
	}
	if strings.Contains(help, "--codex-mode") || strings.Contains(help, "仅迁入对话") {
		t.Fatal("help still advertises unsupported partial import")
	}
}
