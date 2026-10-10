import SwiftUI

struct ReportChangesView: View {
    let report: ReportEntity
    let previous: ReportEntity?
    @State private var showingAll = false
    private var changes: [ReportChange] { previous.map { ReportComparison.changes(from: $0, to: report) } ?? [] }
    var body: some View {
        Section("前回からの変更") {
            if let previous {
                Text("同じ情報種別の \((previous.occurredAt ?? previous.receivedAt).formatted(date: .abbreviated, time: .standard)) の発表と比較")
                    .font(.caption).foregroundStyle(.secondary)
                if changes.isEmpty {
                    Text("表示項目に変更はありません。")
                } else {
                    ForEach(Array(changes.prefix(3))) { change in changeRow(change) }
                    if changes.count > 3 {
                        Button("変更点をすべて見る（\(changes.count)項目）") { showingAll = true }
                            .accessibilityIdentifier("allReportChanges")
                    }
                }
            } else {
                Text("比較できる前回発表はありません。")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .sheet(isPresented: $showingAll) {
            NavigationStack {
                List(changes) { change in changeRow(change) }
                    .navigationTitle("前回からの変更").navigationBarTitleDisplayMode(.inline)
                    .toolbar { ToolbarItem(placement: .confirmationAction) { Button("閉じる") { showingAll = false } } }
            }.presentationDetents([.large])
        }
    }
    private func changeRow(_ change: ReportChange) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(change.label).font(.subheadline.bold())
            Text(change.previous).font(.caption).foregroundStyle(.secondary).strikethrough()
            Label(change.current, systemImage: "arrow.turn.down.right")
                .font(.subheadline).foregroundStyle(.orange).textSelection(.enabled)
        }.padding(.vertical, 3)
    }
}
