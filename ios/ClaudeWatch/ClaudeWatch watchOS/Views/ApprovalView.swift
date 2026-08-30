import SwiftUI
import WatchKit

struct ApprovalView: View {
    @EnvironmentObject private var session: WatchViewState
    @Environment(\.dismiss) private var dismiss

    let request: ApprovalRequest
    let sessionId: String?
    @State private var hasResponded = false

    init(request: ApprovalRequest, sessionId: String? = nil) {
        self.request = request
        self.sessionId = sessionId
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 8) {
                headerStrip

                // Literal question text, when this is an AskUserQuestion
                // prompt rather than a plain tool-permission request.
                if let question = request.question {
                    Text(question)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.white)
                        .fixedSize(horizontal: false, vertical: true)
                }

                Divider().background(Theme.Text.dimmed)

                // Dynamic options from server
                ForEach(Array(request.options.enumerated()), id: \.element.id) { index, option in
                    Button {
                        respond(option: option, index: index)
                    } label: {
                        HStack(spacing: 6) {
                            Text("\(index + 1).")
                                .font(.system(size: 11, design: .monospaced))
                                .foregroundColor(Theme.Text.secondary)

                            VStack(alignment: .leading, spacing: 2) {
                                Text(option.label)
                                    .font(.system(size: 12, weight: .semibold))
                                    .foregroundColor(.white)
                                    .lineLimit(2)

                                if let desc = option.description, !desc.isEmpty {
                                    Text(desc)
                                        .font(.system(size: 10))
                                        .foregroundColor(Theme.Text.secondary)
                                        .lineLimit(2)
                                }
                            }

                            Spacer()
                        }
                        .padding(.vertical, 8)
                        .padding(.horizontal, 8)
                        .background(colorForOption(index).opacity(0.15))
                        .clipShape(RoundedRectangle(cornerRadius: 8))
                        .overlay(
                            RoundedRectangle(cornerRadius: 8)
                                .stroke(colorForOption(index).opacity(0.4), lineWidth: 1)
                        )
                    }
                    .buttonStyle(.plain)
                    .disabled(hasResponded)
                }
            }
            .padding(.horizontal, 6)
            .padding(.top, 4)
        }
        .background(Theme.Background.primary)
    }

    private var headerStrip: some View {
        let style = ToolStyle.forTool(request.toolName)
        let title = !request.actionSummary.isEmpty ? request.actionSummary : request.toolName
        return HStack(alignment: .top, spacing: 6) {
            Rectangle()
                .fill(style.color)
                .frame(width: 3)
            HStack(spacing: 6) {
                ZStack {
                    RoundedRectangle(cornerRadius: 5)
                        .fill(style.color.opacity(0.18))
                        .frame(width: 22, height: 22)
                    Image(systemName: style.symbol)
                        .font(.system(size: 10))
                        .foregroundColor(style.color)
                }
                Text(title)
                    .font(.system(size: 12, weight: .bold))
                    .foregroundColor(.white)
                    .lineLimit(2)
            }
        }
    }

    private func colorForOption(_ index: Int) -> Color {
        // First option: green, last option: red, middle: orange
        if request.options.count <= 1 { return Theme.Accent.success }
        if index == 0 { return Theme.Accent.success }
        if index == request.options.count - 1 { return Theme.Accent.error }
        return Theme.Text.primary
    }

    private func respond(option: ApprovalRequest.OptionItem, index: Int) {
        guard !hasResponded else { return }
        hasResponded = true

        let isLast = index == request.options.count - 1
        WKInterfaceDevice.current().play(isLast ? .failure : .success)

        // For AskUserQuestion: send the option label
        // For permission prompts: first = allow, last = deny
        if request.question != nil {
            session.respondToPermissionWithOption(option.label, index: index, permissionId: request.permissionId, sessionId: sessionId)
        } else {
            let approved = index != request.options.count - 1
            session.respondToPermission(approved: approved, permissionId: request.permissionId, sessionId: sessionId)
        }

        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
            dismiss()
        }
    }
}

#Preview {
    ApprovalView(
        request: ApprovalRequest(
            toolName: "AskUserQuestion",
            actionSummary: "Goal",
            question: "Before we dig in — what's your goal with this?",
            options: [
                .init(label: "Building a startup", description: "You're building a company"),
                .init(label: "Hackathon / fun", description: "Time-boxed project"),
                .init(label: "Open source", description: "Building for a community"),
            ]
        )
    )
    .environmentObject(WatchViewState.shared)
}
