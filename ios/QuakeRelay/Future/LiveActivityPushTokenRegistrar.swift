import Foundation

@MainActor
struct LiveActivityPushTokenRegistrar {
    let api: APIClient

    func registerStart(pushToken: Data) async throws {
        try await api.registerLiveActivityStartToken(hex(pushToken))
    }
    func register(pushToken: Data, attributes: QuickRelayActivityAttributes, activityID: String) async throws {
        try await api.registerLiveActivityToken(
            eventID: attributes.eventID, telegramType: attributes.telegramType,
            activityID: activityID, pushToken: hex(pushToken), startSequence: attributes.startSequence)
    }
    func ended(attributes: QuickRelayActivityAttributes, activityID: String) async throws {
        try await api.liveActivityEnded(eventID: attributes.eventID,
                                       telegramType: attributes.telegramType, activityID: activityID)
    }
    private func hex(_ data: Data) -> String { data.map { String(format: "%02x", $0) }.joined() }
}
