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
                        Text(context.state.displayTitle).font(.headline).lineLimit(2)
                        if !context.state.intensityText.isEmpty { Text(context.state.intensityText).font(.subheadline.bold()) }
                        Text(context.state.freshnessLabel(isStale: context.isStale)).font(.caption).lineLimit(2)
                        Text(Date(timeIntervalSince1970: TimeInterval(context.state.reportedAt)), style: .time)
                            .font(.caption2).foregroundStyle(.secondary)
                    }
                }
            } compactLeading: {
                Image(systemName: context.state.symbol)
                    .foregroundStyle(compactIconColor(state: context.state, isStale: context.isStale))
            } compactTrailing: {
                Text(compactLabel(state: context.state, isStale: context.isStale))
                    .font(.caption2).lineLimit(1)
            } minimal: {
                Image(systemName: context.isStale && !context.state.ended ? "clock.badge.exclamationmark" : context.state.symbol)
                    .foregroundStyle(context.state.highlightsEEWWarning(isStale: context.isStale) ? Color.red : Color.primary)
            }
            .keylineTint(context.state.highlightsEEWWarning(isStale: context.isStale) ? .red : nil)
            .widgetURL(context.attributes.detailURL)
        }
    }

    private func compactIconColor(state: QuickRelayActivityAttributes.ContentState, isStale: Bool) -> Color {
        if state.hasActiveEEWWarning { return isStale ? .primary : .red }
        return state.warning ? .orange : .cyan
    }

    private func compactLabel(state: QuickRelayActivityAttributes.ContentState, isStale: Bool) -> String {
        if state.ended { return "終了" }
        if isStale { return "未確認" }
        if state.hasActiveEEWWarning { return "警報" }
        return state.category == "tsunami" ? "津波" : state.reportLabel
    }
}
