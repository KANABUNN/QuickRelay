import Foundation

// Intentionally separate from normal APNs device-token registration. The MVP
// has no ActivityKit dependency and does not create, update, or end activities.
protocol LiveActivityPushTokenRegistrar: Sendable {
    func register(pushToken: Data, eventID: String) async throws
    func unregister(eventID: String) async throws
}

struct DisabledLiveActivityPushTokenRegistrar: LiveActivityPushTokenRegistrar {
    func register(pushToken: Data, eventID: String) async throws {}
    func unregister(eventID: String) async throws {}
}
