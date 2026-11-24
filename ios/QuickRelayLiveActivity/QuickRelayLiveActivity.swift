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
            .widgetURL(context.attributes.detailURL(for: context.state))
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    QuickRelayActivityIndicator(state: context.state, isStale: context.isStale, size: 46)
                }
                DynamicIslandExpandedRegion(.trailing) {
                    VStack(alignment: .trailing, spacing: 2) {
                        Text(context.state.reportLabel).font(.caption.weight(.semibold)).monospacedDigit()
                        Text(Date(timeIntervalSince1970: TimeInterval(context.state.reportedAt)), style: .time)
                            .font(.caption2).foregroundStyle(.secondary)
                    }
                }
                DynamicIslandExpandedRegion(.center) {
                    Text(context.state.displayTitle)
                        .font(.headline)
                        .lineLimit(1)
                        .minimumScaleFactor(0.75)
                        .foregroundStyle(context.state.highlightsEEWWarning(isStale: context.isStale) ? Color.red : Color.primary)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(context.state.summary).font(.caption).lineLimit(2)
                        Text(context.state.freshnessLabel(isStale: context.isStale))
                            .font(.caption2.weight(.semibold))
                            .lineLimit(1)
                            .foregroundStyle(context.isStale && !context.state.ended ? Color.orange : Color.secondary)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
            } compactLeading: {
                QuickRelayCompactIntensity(state: context.state)
                    .opacity(context.isStale || context.state.isInactive ? 0.55 : 1)
            } compactTrailing: {
                Text(compactLabel(state: context.state, isStale: context.isStale))
                    .font(.caption2).lineLimit(1)
                    .foregroundStyle(compactIconColor(state: context.state, isStale: context.isStale))
            } minimal: {
                if context.isStale && !context.state.ended {
                    Image(systemName: "clock.badge.exclamationmark")
                } else {
                    QuickRelayCompactIntensity(state: context.state)
                }
            }
            .keylineTint(context.state.highlightsEEWWarning(isStale: context.isStale) ? .red : nil)
            .widgetURL(context.attributes.detailURL(for: context.state))
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
