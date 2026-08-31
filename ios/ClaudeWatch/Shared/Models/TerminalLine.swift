import Foundation

struct TerminalLine: Identifiable, Codable, Equatable {
    let id: UUID
    let text: String
    let timestamp: Date
    let type: LineType
    let sessionId: String?

    // Only used when type == .action: which tool this card represents
    // (drives icon/color via ToolStyle) and an optional one-line result
    // shown under the title.
    let toolName: String?
    let detail: String?

    enum LineType: String, Codable {
        case output      // Claude's output
        case command     // User's command (prefixed with >)
        case system      // System messages (connected, disconnected, etc.)
        case thinking    // Pulsing cursor indicator
        case error       // Error messages
        case action      // A single tool call: icon + title + optional detail
        case notification // Claude is waiting on you — needs a highlighted, hard-to-miss line
    }

    init(text: String, type: LineType = .output, sessionId: String? = nil, toolName: String? = nil, detail: String? = nil) {
        self.id = UUID()
        self.text = text
        self.timestamp = Date()
        self.type = type
        self.sessionId = sessionId
        self.toolName = toolName
        self.detail = detail
    }
}
