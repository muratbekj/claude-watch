import SwiftUI

// The NavigationStack root shown at launch. With 2+ sessions running, it
// shows a picker; with 0 or 1, it skips straight to MultiSessionPager
// (which already renders its own "waiting for session" state for 0).
//
// That skip decision is made exactly once, in the first onAppear — not
// recomputed from session.sessions.count on every change. Otherwise a new
// session appearing while you're mid-conversation in the auto-entered pager
// would flip this view's condition and yank you back to a list you never
// asked to see.
struct SessionListView: View {
    @EnvironmentObject private var session: WatchViewState
    @State private var didAutoEnter = false
    @State private var showPager = false

    var body: some View {
        Group {
            if session.sessions.count >= 2 {
                List {
                    ForEach(Array(session.sessions.enumerated()), id: \.element.id) { index, agentSession in
                        NavigationLink {
                            MultiSessionPager()
                                .onAppear { session.activeSessionIndex = index }
                        } label: {
                            row(for: agentSession)
                        }
                    }
                }
                .navigationTitle("Sessions")
            } else {
                Color.clear
            }
        }
        .navigationDestination(isPresented: $showPager) {
            MultiSessionPager()
        }
        .onAppear {
            guard !didAutoEnter else { return }
            didAutoEnter = true
            if session.sessions.count < 2 {
                showPager = true
            }
        }
    }

    private func row(for agentSession: AgentSession) -> some View {
        HStack(spacing: 6) {
            AgentIcon(agent: agentSession.agent, size: 16)
            Text(agentSession.folderName.isEmpty ? agentSession.agent.rawValue.capitalized : agentSession.folderName)
                .font(.system(size: 13, weight: .semibold))
                .foregroundColor(Theme.Text.primary)
                .lineLimit(1)
            Spacer()
            Circle()
                .fill(agentSession.statusColor)
                .frame(width: 6, height: 6)
        }
    }
}

#Preview {
    let state = WatchViewState.shared
    state.sessions = [
        AgentSession(id: "1", agent: .claude, cwd: "/repo/app", folderName: "app", activity: .running),
        AgentSession(id: "2", agent: .claude, cwd: "/repo/bridge", folderName: "bridge", activity: .waitingApproval),
        AgentSession(id: "3", agent: .codex, cwd: "/repo/docs", folderName: "docs", activity: .idle),
    ]
    return NavigationStack {
        SessionListView()
    }
    .environmentObject(state)
}
