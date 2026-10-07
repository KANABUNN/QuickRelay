import SwiftData
import SwiftUI

struct EventListView: View {
    @EnvironmentObject private var repository: EventRepository
    @Query(sort: \EventEntity.latestReportAt, order: .reverse) private var events: [EventEntity]

    var body: some View {
        Group {
            if events.isEmpty {
                ContentUnavailableView(
                    "地震情報はありません",
                    systemImage: "waveform.path.ecg",
                    description: Text("下へ引いてサーバーと同期できます。")
                )
            } else {
                List(events) { event in
                    NavigationLink(value: AppRoute.event(id: event.id, reportID: nil)) {
                        EventRow(event: event)
                    }
                }
                .listStyle(.plain)
            }
        }
        .navigationTitle("地震情報")
        .safeAreaInset(edge: .bottom) {
            if let error = repository.lastError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(2)
                    .padding(8)
                    .frame(maxWidth: .infinity)
                    .background(.thinMaterial)
            }
        }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                if repository.isSyncing {
                    ProgressView()
                } else {
                    Button("同期", systemImage: "arrow.clockwise") {
                        Task { _ = await repository.syncAll() }
                    }
                }
            }
        }
        .refreshable {
            _ = await repository.syncAll()
        }
    }
}

private struct EventRow: View {
    let event: EventEntity

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(event.numericHypocenter.epicenter ?? "震源不明")
                    .font(.headline)
                if event.numericHypocenter.isAssumed {
                    StatusBadge(text: "仮定震源", color: .orange)
                }
                Spacer()
                Text(event.latestReportAt, format: .dateTime.hour().minute())
                    .font(.subheadline.monospacedDigit())
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 10) {
                if let intensity = event.maxIntensity {
                    Label("\(event.intensityLabel) \(intensity)", systemImage: "gauge.with.dots.needle.67percent")
                }
                if !event.isWarningProduct, let magnitude = event.numericHypocenter.magnitude {
                    Text("M\(magnitude.formatted(.number.precision(.fractionLength(1))))\(event.numericHypocenter.isAssumed ? "（仮定値）" : "")")
                }
            }
            .font(.subheadline)

            if let note = event.numericHypocenter.qualification {
                Text(note).font(.caption).foregroundStyle(.orange)
            }

            HStack(spacing: 8) {
                Text(RelayEventType(rawValue: event.eventType)?.displayName ?? event.eventType)
                if let revision = event.sourceSerial {
                    Text("第\(revision)報")
                }
                if event.isCancelled {
                    StatusBadge(text: "取消", color: .orange)
                } else if event.isFinal {
                    StatusBadge(text: "最終", color: .blue)
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .padding(.vertical, 4)
    }
}

struct StatusBadge: View {
    let text: String
    let color: Color

    var body: some View {
        Text(text)
            .font(.caption2.bold())
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .foregroundStyle(color)
            .background(color.opacity(0.12), in: Capsule())
    }
}
