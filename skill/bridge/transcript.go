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

// ChatMessage is one past turn of plain-text conversation, as backfilled
// for a client opening a session it doesn't have live history for.
type ChatMessage struct {
	Role string `json:"role"` // "user" or "assistant"
	Text string `json:"text"`
}

// chatMessageFromTranscriptLine parses one JSONL line and, if it's a
// "user" or "assistant" entry with at least one non-empty text content
// block, returns it as a ChatMessage. A "user"-typed entry whose only
// content is a tool_result (the CLI feeding a tool's output back, not
// something a human typed) has no "text" blocks and is correctly skipped
// by the same filter used for assistant entries.
func chatMessageFromTranscriptLine(line string) (ChatMessage, bool) {
	var entry transcriptEntry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return ChatMessage{}, false
	}
	if entry.Type != "user" && entry.Type != "assistant" {
		return ChatMessage{}, false
	}
	var texts []string
	for _, block := range entry.Message.Content {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	if len(texts) == 0 {
		return ChatMessage{}, false
	}
	return ChatMessage{Role: entry.Type, Text: strings.Join(texts, "\n")}, true
}

// RecentChatHistory scans transcriptPath backward for up to maxMessages
// past user/assistant text messages (tool calls and thinking are not
// "chat" and are skipped, same as LatestAssistantText), and returns them
// in chronological order — oldest first — ready to backfill a session's
// feed. Returns an empty slice (never an error) if the path is empty, the
// file is missing/unreadable, or nothing qualifies within the scan window.
func RecentChatHistory(transcriptPath string, maxMessages int) []ChatMessage {
	if transcriptPath == "" || maxMessages <= 0 {
		return nil
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		return nil
	}
	defer f.Close()

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

	var messages []ChatMessage
	for i := len(lines) - 1; i >= 0 && len(messages) < maxMessages; i-- {
		if msg, ok := chatMessageFromTranscriptLine(lines[i]); ok {
			messages = append(messages, msg)
		}
	}

	// messages was built newest-first; reverse it to chronological order.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages
}
