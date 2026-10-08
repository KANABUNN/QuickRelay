import ActivityKit
import SwiftUI
import WidgetKit

@main
struct QuickRelayLiveActivityBundle: WidgetBundle {
    var body: some Widget { QuickRelayLiveActivity() }
}

struct QuickRelayLiveActivity: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: QuickRelayActivityAttributes.self) { context in
            QuickRelayLiveActivityCard(state: context.state, isStale: context.isStale)
            .activityBackgroundTint(Color(.secondarySystemBackground))
            .activitySystemActionForegroundColor(.primary)
            .widgetURL(context.attributes.detailURL)
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    Label(context.state.category == "tsunami" ? "津波" : "EEW", systemImage: context.state.symbol)
                        .font(.headline)
                }
                DynamicIslandExpandedRegion(.trailing) { Text(context.state.reportLabel).font(.caption) }
                DynamicIslandExpandedRegion(.bottom) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(context.state.title).font(.headline).lineLimit(2)
                        if !context.state.intensityText.isEmpty { Text(context.state.intensityText).font(.subheadline.bold()) }
                        Text(context.state.freshnessLabel(isStale: context.isStale)).font(.caption).lineLimit(2)
                        Text(Date(timeIntervalSince1970: TimeInterval(context.state.reportedAt)), style: .time)
                            .font(.caption2).foregroundStyle(.secondary)
                    }
                }
            } compactLeading: {
                Image(systemName: context.state.symbol).foregroundStyle(context.state.warning ? .orange : .cyan)
            } compactTrailing: {
                Text(context.state.ended ? "終了" : context.isStale ? "未確認" :
                     context.state.category == "tsunami" ? "津波" : context.state.reportLabel)
                    .font(.caption2).lineLimit(1)
            } minimal: {
                Image(systemName: context.isStale && !context.state.ended ? "clock.badge.exclamationmark" : context.state.symbol)
            }
            .widgetURL(context.attributes.detailURL)
        }
    }
}
