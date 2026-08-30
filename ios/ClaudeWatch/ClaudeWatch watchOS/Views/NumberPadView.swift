import SwiftUI

// A compact on-screen numeric keypad for entering a fixed-length code.
// watchOS's TextField has no numeric-only input mode (no `.keyboardType`
// like iOS) — its default text entry sheet leads with Scribble/dictation,
// which is slow and error-prone for a 6-digit pairing code. This gives
// direct tap-to-enter digit buttons instead.
struct NumberPadView: View {
    @Binding var code: String
    let length: Int
    let onComplete: (String) -> Void

    private let rows: [[String]] = [
        ["1", "2", "3"],
        ["4", "5", "6"],
        ["7", "8", "9"],
        ["", "0", "⌫"],
    ]

    var body: some View {
        VStack(spacing: 6) {
            codeDisplay

            VStack(spacing: 3) {
                ForEach(rows, id: \.self) { row in
                    HStack(spacing: 3) {
                        ForEach(row, id: \.self) { key in
                            keyButton(key)
                        }
                    }
                }
            }
        }
    }

    private var codeDisplay: some View {
        HStack(spacing: 5) {
            ForEach(0..<length, id: \.self) { i in
                Circle()
                    .fill(i < code.count ? Theme.Text.primary : Theme.Text.dimmed.opacity(0.4))
                    .frame(width: 7, height: 7)
            }
        }
    }

    @ViewBuilder
    private func keyButton(_ key: String) -> some View {
        if key.isEmpty {
            Color.clear.frame(width: 36, height: 26)
        } else {
            Button {
                handleTap(key)
            } label: {
                Text(key)
                    .font(.system(size: key == "⌫" ? 12 : 14, weight: .semibold))
                    .foregroundColor(.white)
                    .frame(width: 36, height: 26)
                    .background(Theme.Background.overlay)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
            }
            .buttonStyle(.plain)
        }
    }

    private func handleTap(_ key: String) {
        if key == "⌫" {
            if !code.isEmpty { code.removeLast() }
        } else if code.count < length {
            code += key
            if code.count == length { onComplete(code) }
        }
    }
}
