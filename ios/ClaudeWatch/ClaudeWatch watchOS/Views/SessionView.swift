import SwiftUI

struct SessionView: View {
    let sessionIndex: Int
    @EnvironmentObject private var session: WatchViewState

    @State private var showVoiceInput = false

    private var agentSession: AgentSession {
        guard session.sessions.indices.contains(sessionIndex) else {
            return AgentSession(id: "", agent: .claude, cwd: "", folderName: "", activity: .idle)
        }
        return session.sessions[sessionIndex]
    }

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                // Top bar — agent icon + folder name
                HStack(spacing: 4) {
                    AgentIcon(agent: agentSession.agent, size: 14)
                    Text(agentSession.folderName.isEmpty ? agentSession.agent.rawValue.capitalized : agentSession.folderName)
                        .font(.system(size: 10, weight: .bold))
                        .foregroundColor(Theme.Text.primary)
                        .lineLimit(1)
                    Spacer()
                    if let otherIdx = session.sessionIndexWithPendingApproval(excluding: sessionIndex) {
                        Button {
                            session.activeSessionIndex = otherIdx
                        } label: {
                            HStack(spacing: 2) {
                                Image(systemName: "exclamationmark.circle.fill")
                                    .font(.system(size: 9))
                                Text("1")
                                    .font(.system(size: 9, weight: .bold))
                            }
                            .foregroundColor(Theme.Accent.approval)
                        }
                        .buttonStyle(.plain)
                    }
                    Circle()
                        .fill(statusColor)
                        .frame(width: 5, height: 5)
                }
                .padding(.horizontal, 4)
                .padding(.bottom, 2)

                // Terminal
                ScrollViewReader { proxy in
                    ScrollView(.vertical, showsIndicators: false) {
                        VStack(alignment: .leading, spacing: 1) {
                            ForEach(visibleLines) { line in
                                terminalLine(line)
                                    .id(line.id)
                            }

                            Spacer().frame(height: 40)
                        }
                        .padding(.horizontal, 4)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .onChange(of: agentSession.terminalLines.count) { _ in
                        withAnimation(.easeOut(duration: 0.1)) {
                            if let last = visibleLines.last {
                                proxy.scrollTo(last.id, anchor: .bottom)
                            }
                        }
                    }
                }
            }
            .background(Theme.Background.primary)

            // FAB buttons
            HStack {
                // Clear button (left)
                Button { session.clearTerminal(sessionId: agentSession.id) } label: {
                    ZStack {
                        Circle()
                            .fill(Theme.Text.secondary.opacity(0.5))
                            .frame(width: 28, height: 28)
                        Image(systemName: "trash")
                            .font(.system(size: 11))
                            .foregroundColor(.white)
                    }
                    .shadow(color: .black.opacity(0.6), radius: 6, y: 3)
                }
                .buttonStyle(.plain)
                .padding(.leading, 16)

                Spacer()

                // Mic button (right)
                Button { showVoiceInput = true } label: {
                    ZStack {
                        Circle()
                            .fill(Theme.Text.primary.opacity(0.75))
                            .frame(width: 28, height: 28)
                        Image(systemName: "mic.fill")
                            .font(.system(size: 12))
                            .foregroundColor(.black)
                    }
                    .shadow(color: .black.opacity(0.6), radius: 6, y: 3)
                }
                .buttonStyle(.plain)
                .padding(.trailing, 16)
            }
            .frame(maxHeight: .infinity, alignment: .bottom)
            .padding(.bottom, 16)
        }
        .ignoresSafeArea(edges: .bottom)
        .sheet(item: pendingApprovalBinding) { request in
            ApprovalView(request: request, sessionId: agentSession.id)
        }
        .fullScreenCover(isPresented: $showVoiceInput) {
            VoiceInputView(sessionId: agentSession.id)
        }
    }

    // Binds directly to this session's own slot so an approval for a
    // different session never overwrites or hides what's shown here.
    private var pendingApprovalBinding: Binding<ApprovalRequest?> {
        Binding(
            get: {
                guard session.sessions.indices.contains(sessionIndex) else { return nil }
                return session.sessions[sessionIndex].pendingApproval
            },
            set: { newValue in
                guard session.sessions.indices.contains(sessionIndex) else { return }
                session.sessions[sessionIndex].pendingApproval = newValue
            }
        )
    }

    private var visibleLines: [TerminalLine] {
        agentSession.terminalLines
            .filter { !$0.text.isEmpty || $0.type == .thinking }
            .suffix(30)
            .map { $0 }
    }

    @ViewBuilder
    private func terminalLine(_ line: TerminalLine) -> some View {
        if line.type == .action {
            actionCard(line)
        } else if line.type == .thinking {
            Text("\(line.text)…")
                .font(.system(size: 10.5, weight: .medium))
                .foregroundColor(Theme.Text.secondary)
                .modifier(PulseModifier())
        } else {
            Text(line.text)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(colorForLine(line))
                .lineLimit(4)
                .truncationMode(.tail)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    @ViewBuilder
    private func actionCard(_ line: TerminalLine) -> some View {
        let style = ToolStyle.forTool(line.toolName ?? "")
        HStack(alignment: .top, spacing: 5) {
            Rectangle()
                .fill(style.color)
                .frame(width: 2)
            Image(systemName: style.symbol)
                .font(.system(size: 9))
                .foregroundColor(style.color)
                .frame(width: 12, alignment: .center)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 1) {
                Text(line.text)
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundColor(.white)
                    .lineLimit(1)
                    .truncationMode(.tail)
                if let detail = line.detail {
                    Text(detail)
                        .font(.system(size: 9.5))
                        .foregroundColor(detailColor(detail))
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
            }
        }
        .padding(.vertical, 1)
    }

    private func detailColor(_ detail: String) -> Color {
        let lower = detail.lowercased()
        if detail.hasPrefix("✓") || lower.contains("success") || lower.contains("complete") {
            return Theme.Accent.success
        }
        return Theme.Text.secondary
    }

    private func colorForLine(_ line: TerminalLine) -> Color {
        if line.type == .output && line.text.hasPrefix("  + ") {
            return Theme.Accent.success
        }
        return colorFor(line.type)
    }

    private var statusColor: Color {
        switch agentSession.activity {
        case .running: return Theme.Accent.success
        case .waitingApproval: return Theme.Accent.approval
        case .ended: return Theme.Accent.error
        case .idle: return Theme.Text.secondary
        }
    }

    private func colorFor(_ type: TerminalLine.LineType) -> Color {
        switch type {
        case .output:   return Theme.Text.primary
        case .command:  return .white
        case .system:   return Theme.Text.secondary
        case .thinking: return Theme.Text.primary.opacity(0.5)
        case .error:    return Theme.Accent.error
        case .action:   return .white // unused — actionCard renders its own colors
        }
    }
}

#Preview {
    let session = {
        var s = AgentSession(
            id: "preview-1",
            agent: .claude,
            cwd: "/Users/shobhit/projects/benchyy",
            folderName: "benchyy",
            activity: .running
        )
        s.terminalLines = [
            TerminalLine(text: "Read Session.swift", type: .action, sessionId: "preview-1", toolName: "Read"),
            TerminalLine(text: "grep \"class Session\"", type: .action, sessionId: "preview-1", toolName: "Grep", detail: "3 matches"),
            TerminalLine(text: "Edit SessionView.swift", type: .action, sessionId: "preview-1", toolName: "Edit"),
            TerminalLine(text: "> looks good, now add the timer", type: .command, sessionId: "preview-1"),
            TerminalLine(text: "Edit SessionView.swift", type: .action, sessionId: "preview-1", toolName: "Edit"),
            TerminalLine(text: "swift build 2>&1 | tail -3", type: .action, sessionId: "preview-1", toolName: "Bash", detail: "✓ Build complete (4.2s)"),
            TerminalLine(text: "Percolating", type: .thinking, sessionId: "preview-1"),
        ]
        return s
    }()

    SessionView(sessionIndex: 0)
        .environmentObject(WatchViewState.shared)
}

struct PulseModifier: ViewModifier {
    @State private var isPulsing = false
    func body(content: Content) -> some View {
        content
            .opacity(isPulsing ? 0.4 : 1.0)
            .animation(.easeInOut(duration: 0.8).repeatForever(autoreverses: true), value: isPulsing)
            .onAppear { isPulsing = true }
    }
}
