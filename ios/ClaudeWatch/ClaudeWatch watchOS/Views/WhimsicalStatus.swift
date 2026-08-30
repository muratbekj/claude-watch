import Foundation

// Playful "thinking" verbs, matching the flavor of Claude Code's own CLI
// status line (e.g. "Pondering…", "Noodling…").
enum WhimsicalStatus {
    private static let verbs = [
        "Pondering", "Noodling", "Marinating", "Percolating", "Ruminating",
        "Contemplating", "Cogitating", "Simmering", "Brewing", "Vibing",
        "Scheming", "Mulling", "Puzzling", "Tinkering", "Conjuring",
        "Wrangling", "Sussing", "Chewing on it", "Deliberating", "Musing",
    ]

    static func randomVerb() -> String {
        verbs.randomElement() ?? "Thinking"
    }
}
