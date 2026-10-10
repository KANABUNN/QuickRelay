import Combine
import Foundation

@MainActor
final class HistoryPreferences: ObservableObject {
    private struct State: Codable {
        var read: [String: Int64] = [:]
        var pinned: Set<String> = []
    }
    private let defaults: UserDefaults
    private let key = "history.preferences.v1"
    @Published private var state: State

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        state = defaults.data(forKey: key).flatMap { try? JSONDecoder().decode(State.self, from: $0) } ?? State()
    }
    private func identity(_ id: String, scope: String) -> String { scope + "\n" + id }
    func isUnread(_ event: EventEntity, scope: String) -> Bool {
        Int64(event.latestRevision ?? 0) > (state.read[identity(event.id, scope: scope)] ?? -1)
    }
    func markRead(_ event: EventEntity, scope: String) {
        let id = identity(event.id, scope: scope)
        let sequence = Int64(event.latestRevision ?? 0)
        guard sequence > (state.read[id] ?? -1) else { return }
        state.read[id] = sequence
        save()
    }
    func markAllRead(_ events: [EventEntity], scope: String) {
        for event in events {
            let id = identity(event.id, scope: scope)
            state.read[id] = max(state.read[id] ?? -1, Int64(event.latestRevision ?? 0))
        }
        save()
    }
    func isPinned(_ id: String, scope: String) -> Bool { state.pinned.contains(identity(id, scope: scope)) }
    func togglePinned(_ id: String, scope: String) {
        let key = identity(id, scope: scope)
        if state.pinned.contains(key) { state.pinned.remove(key) } else { state.pinned.insert(key) }
        save()
    }
    private func save() {
        if let data = try? JSONEncoder().encode(state) { defaults.set(data, forKey: key) }
    }
}

struct HistoryFilter {
    var search = ""
    var eventType = ""
    var minimumIntensity = ""
    var dateRangeEnabled = false
    var startDate = Calendar.current.startOfDay(for: Date())
    var endDate = Date()
    var unreadOnly = false
    var pinnedOnly = false

    var isActive: Bool {
        !search.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !eventType.isEmpty ||
        !minimumIntensity.isEmpty || dateRangeEnabled || unreadOnly || pinnedOnly
    }
    func matches(_ event: EventEntity, searchText: String, unread: Bool, pinned: Bool) -> Bool {
        if unreadOnly && !unread || pinnedOnly && !pinned { return false }
        if !eventType.isEmpty && event.eventType != eventType { return false }
        if !minimumIntensity.isEmpty {
            guard let intensity = event.maxIntensity, let rank = IntensityValue.rank(intensity),
                  let minimum = IntensityValue.rank(minimumIntensity), rank >= minimum else { return false }
        }
        if dateRangeEnabled {
            let start = Calendar.current.startOfDay(for: startDate)
            let end = Calendar.current.date(byAdding: .day, value: 1, to: Calendar.current.startOfDay(for: endDate)) ?? endDate
            guard event.latestReportAt >= start && event.latestReportAt < end else { return false }
        }
        let terms = search.split(whereSeparator: \.isWhitespace)
        return terms.allSatisfy { searchText.localizedStandardContains(String($0)) }
    }
}

enum IntensityValue {
    static let choices = ["1", "2", "3", "4", "5弱", "5強", "6弱", "6強", "7"]
    static func rank(_ value: String) -> Int? {
        let normalized = value.replacingOccurrences(of: "5-", with: "5弱").replacingOccurrences(of: "5+", with: "5強")
            .replacingOccurrences(of: "6-", with: "6弱").replacingOccurrences(of: "6+", with: "6強")
        if normalized.contains("以上") || normalized.contains("over") { return choices.count }
        let ranks = normalized.components(separatedBy: CharacterSet(charactersIn: "〜～~/"))
            .compactMap { part -> Int? in
                let s = part.replacingOccurrences(of: "以上", with: "").trimmingCharacters(in: .whitespaces)
                return choices.firstIndex(of: s).map { $0 + 1 }
            }
        return ranks.max()
    }
}
