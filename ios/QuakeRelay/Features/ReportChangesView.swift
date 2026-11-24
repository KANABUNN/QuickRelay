import SwiftUI

struct ReportChangesView: View {
    let report: ReportEntity
    let previous: ReportEntity?
    @State private var showingAll = false
    private var changes: [ReportChange] { previous.map { ReportComparison.changes(from: $0, to: report) } ?? [] }
    private var inlineLimit: Int { 5 }

    var body: some View {
        Section {
            if let previous {
                Text("同じ情報種別（\(report.telegramType ?? "不明")）の \(DisplayTime.daySeconds(previous.occurredAt ?? previous.receivedAt)) の発表と比較")
                    .font(.caption).foregroundStyle(.secondary)
                if changes.isEmpty {
                    Text("表示項目に変更はありません。").font(.subheadline)
                } else {
                    ForEach(Array(changes.prefix(inlineLimit))) { change in changeRow(change) }
                    if changes.count > inlineLimit {
                        Button("変更点をすべて見る（\(changes.count)項目）") { showingAll = true }
                            .accessibilityIdentifier("allReportChanges")
                            .sheet(isPresented: $showingAll) {
                                NavigationStack {
                                    List(changes) { change in changeRow(change) }
                                        .navigationTitle("前回からの変更").navigationBarTitleDisplayMode(.inline)
                                        .toolbar { ToolbarItem(placement: .confirmationAction) { Button("閉じる") { showingAll = false } } }
                                }.presentationDetents([.large])
                            }
                    }
                }
            } else {
                Text("比較できる前回発表はありません。")
                    .font(.caption).foregroundStyle(.secondary)
            }
        } header: {
            Text(changes.isEmpty ? "前回からの変更" : "前回からの変更（\(changes.count)項目）")
        }
    }

    private func changeRow(_ change: ReportChange) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(change.label).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            if change.previous.count + change.current.count <= 28 {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(change.previous).font(.subheadline).foregroundStyle(.secondary).strikethrough()
                    Image(systemName: "arrow.right").font(.caption).foregroundStyle(.secondary).accessibilityLabel("から")
                    Text(change.current).font(.subheadline.weight(.semibold)).foregroundStyle(.orange).textSelection(.enabled)
                }
            } else {
                Text(change.previous).font(.caption).foregroundStyle(.secondary).strikethrough()
                Label(change.current, systemImage: "arrow.turn.down.right")
                    .font(.subheadline).foregroundStyle(.orange).textSelection(.enabled)
            }
        }
        .padding(.vertical, 2)
    }
}
