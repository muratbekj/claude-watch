import SwiftUI

// Icon + color coding shared by the action feed (SessionView) and the
// approval header (ApprovalView) so a tool type reads the same way in
// both places.
struct ToolStyle {
    let symbol: String
    let color: Color

    static func forTool(_ toolName: String) -> ToolStyle {
        switch toolName {
        case "Read":
            return ToolStyle(symbol: "doc.text", color: Theme.Accent.info)
        case "Grep":
            return ToolStyle(symbol: "magnifyingglass", color: Theme.Accent.info)
        case "Edit", "Write":
            return ToolStyle(symbol: "pencil", color: Theme.Accent.approval)
        case "Bash":
            return ToolStyle(symbol: "terminal", color: Theme.Text.secondary)
        case "AskUserQuestion":
            return ToolStyle(symbol: "questionmark.circle", color: Theme.Accent.approval)
        default:
            return ToolStyle(symbol: "circle", color: Theme.Text.secondary)
        }
    }
}
