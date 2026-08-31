package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test transcript: %v", err)
	}
	return path
}

func TestLatestAssistantText_FindsLastTextEntry(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"first reply"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second reply"}]}}`,
	})
	got := LatestAssistantText(path)
	if got != "second reply" {
		t.Errorf("got %q, want %q", got, "second reply")
	}
}

func TestLatestAssistantText_SkipsToolUseAndThinkingOnlyEntries(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"the real answer"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","text":"pondering"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","text":""}]}}`,
	})
	got := LatestAssistantText(path)
	if got != "the real answer" {
		t.Errorf("got %q, want %q", got, "the real answer")
	}
}

func TestLatestAssistantText_JoinsMultipleTextBlocks(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]}}`,
	})
	got := LatestAssistantText(path)
	want := "part one\npart two"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLatestAssistantText_SkipsMalformedLines(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"good one"}]}}`,
		`not valid json at all {{{`,
	})
	got := LatestAssistantText(path)
	if got != "good one" {
		t.Errorf("got %q, want %q", got, "good one")
	}
}

func TestLatestAssistantText_MissingFileReturnsEmpty(t *testing.T) {
	got := LatestAssistantText("/no/such/file/exists.jsonl")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestLatestAssistantText_EmptyPathReturnsEmpty(t *testing.T) {
	got := LatestAssistantText("")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestLatestAssistantText_NoQualifyingEntryReturnsEmpty(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","text":""}]}}`,
	})
	got := LatestAssistantText(path)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

// Regression test for a real production bug: a transcript line larger than
// bufio.Scanner's buffer cap (e.g. a tool result embedding a large file's
// contents) used to make LatestAssistantText silently stop reading right
// there, returning the earliest message in the file instead of the latest
// one from a turn that completed afterward.
func TestLatestAssistantText_HandlesLinesLargerThanOldScannerLimit(t *testing.T) {
	huge := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","text":"` +
		strings.Repeat("x", 2*1024*1024) + `"}]}}`
	path := writeTranscript(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"earliest message"}]}}`,
		huge,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"latest message"}]}}`,
	})
	got := LatestAssistantText(path)
	if got != "latest message" {
		t.Errorf("got %q, want %q", got, "latest message")
	}
}

func TestLatestAssistantText_HandlesFinalLineWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	// No trailing "\n" after the last line, simulating a transcript file
	// caught mid-write when a hook fires.
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"no newline at eof"}]}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test transcript: %v", err)
	}
	got := LatestAssistantText(path)
	if got != "no newline at eof" {
		t.Errorf("got %q, want %q", got, "no newline at eof")
	}
}
