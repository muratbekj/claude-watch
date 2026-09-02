import SwiftUI

// The NavigationStack root shown at launch, always — even with just one
// session (or none) running. Tap a row to enter that session; the pushed
// view's back button returns here to pick another.
struct SessionListView: View {
    @EnvironmentObject private var session: WatchViewState

    var body: some View {
        Group {
            if session.sessions.isEmpty {
                waitingView
            } else {
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
            }
        }
        .navigationTitle("Sessions")
    }

    private var waitingView: some View {
        VStack(spacing: 8) {
            AppLogo(size: 56)
                .opacity(0.6)
            Text("Waiting for session...")
                .font(.system(size: 11, weight: .medium))
                .foregroundColor(.white.opacity(0.5))
            Text("Start Claude or Codex on your Mac")
                .font(.system(size: 9))
                .foregroundColor(.white.opacity(0.3))
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black)
    }

    private func row(for agentSession: AgentSession) -> some View {
        HStack(spacing: 6) {
            AgentIcon(agent: agentSession.agent, size: 16)
            Text(agentSession.folderName.isEmpty ? agentSession.agent.rawValue.capitalized : agentSession.folderName)
                .font(.system(size: 13, weight: .semibold))
                .foregroundColor(Theme.Text.primary)
                .lineLimit(1)
            Spacer()
            activityIndicator(for: agentSession.activity)
        }
    }

    // .running gets a calm "still working" breathing pulse; .waitingApproval
    // gets a faster, size-based pulse so it reads as needing your attention,
    // not just quietly active. Idle/ended stay as plain static dots — there's
    // nothing happening to animate.
    @ViewBuilder
    private func activityIndicator(for activity: SessionActivity) -> some View {
        switch activity {
        case .running:
            Image(systemName: "circle.fill")
                .font(.system(size: 8))
                .foregroundColor(Theme.Accent.success)
                .modifier(PulseModifier())
        case .waitingApproval:
            Image(systemName: "exclamationmark.circle.fill")
                .font(.system(size: 10))
                .foregroundColor(Theme.Accent.approval)
                .modifier(AttentionPulseModifier())
        case .ended:
            Circle()
                .fill(Theme.Accent.error)
                .frame(width: 6, height: 6)
        case .idle:
            Circle()
                .fill(Theme.Text.secondary)
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
