package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

type transcriptContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type transcriptMessage struct {
	Role    string                   `json:"role"`
	Content []transcriptContentBlock `json:"content"`
}

type transcriptEntry struct {
	Type    string            `json:"type"`
	Message transcriptMessage `json:"message"`
}

// maxTranscriptScanLines caps how many trailing lines LatestAssistantText
// reads, since a long-running session's transcript can grow large and only
// the most recent turn matters here.
const maxTranscriptScanLines = 200

// LatestAssistantText scans transcriptPath backward for the most recent
// "assistant" entry that has at least one "text" content block, and
// returns that entry's text blocks joined by "\n". Returns "" (never an
// error) if the path is empty, the file is missing/unreadable, or no
// qualifying entry exists within the scan window — this is a best-effort
// read used to surface chat content on the watch, never a hard dependency.
func LatestAssistantText(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	// bufio.Scanner was tried here first but silently truncates the whole
	// read on any single line over its buffer cap (bufio.ErrTooLong) —
	// transcript lines can legitimately exceed that (a large tool result,
	// e.g. reading a big file), which made this function return the
	// earliest message in the file instead of the latest whenever that
	// happened. bufio.Reader.ReadString has no such limit.
	lines := make([]string, 0, maxTranscriptScanLines)
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		if line != "" {
			lines = append(lines, line)
			if len(lines) > maxTranscriptScanLines {
				lines = lines[1:]
			}
		}
		if err != nil {
			break
		}
	}

	for i := len(lines) - 1; i >= 0; i-- {
		if text := textFromTranscriptLine(lines[i]); text != "" {
			return text
		}
	}
	return ""
}

// textFromTranscriptLine parses one JSONL line and, if it's an assistant
// entry with at least one non-empty text content block, returns those
// blocks joined by "\n". Returns "" for any other line — wrong type, no
// text blocks (pure tool_use/thinking), or malformed JSON (the transcript
// file can be mid-write when a hook fires).
func textFromTranscriptLine(line string) string {
	var entry transcriptEntry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return ""
	}
	if entry.Type != "assistant" {
		return ""
	}
	var texts []string
	for _, block := range entry.Message.Content {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	if len(texts) == 0 {
		return ""
	}
	return strings.Join(texts, "\n")
}
