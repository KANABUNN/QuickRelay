import Combine
import Foundation

@MainActor
final class AppSettings: ObservableObject {
    private enum Key {
        static let serverBaseURL = "settings.serverBaseURL"
        static let notificationsEnabled = "settings.notificationsEnabled"
        static let timeSensitiveEnabled = "settings.timeSensitiveEnabled"
        static let customSoundEnabled = "settings.customSoundEnabled"
        static let earthquakeNotificationsEnabled = "settings.earthquakeNotificationsEnabled"
        static let eewNotificationsEnabled = "settings.eewNotificationsEnabled"
    }

    static let placeholderServerURL = "https://quake.example.jp/api/v1"

    private let defaults: UserDefaults

    @Published var serverBaseURL: String {
        didSet { defaults.set(serverBaseURL, forKey: Key.serverBaseURL) }
    }

    @Published var notificationsEnabled: Bool {
        didSet { defaults.set(notificationsEnabled, forKey: Key.notificationsEnabled) }
    }

    @Published var timeSensitiveEnabled: Bool {
        didSet { defaults.set(timeSensitiveEnabled, forKey: Key.timeSensitiveEnabled) }
    }

    @Published var customSoundEnabled: Bool {
        didSet { defaults.set(customSoundEnabled, forKey: Key.customSoundEnabled) }
    }

    @Published var earthquakeNotificationsEnabled: Bool {
        didSet { defaults.set(earthquakeNotificationsEnabled, forKey: Key.earthquakeNotificationsEnabled) }
    }

    @Published var eewNotificationsEnabled: Bool {
        didSet { defaults.set(eewNotificationsEnabled, forKey: Key.eewNotificationsEnabled) }
    }

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        serverBaseURL = defaults.string(forKey: Key.serverBaseURL) ?? Self.placeholderServerURL
        notificationsEnabled = defaults.object(forKey: Key.notificationsEnabled) as? Bool ?? true
        timeSensitiveEnabled = defaults.object(forKey: Key.timeSensitiveEnabled) as? Bool ?? true
        customSoundEnabled = defaults.object(forKey: Key.customSoundEnabled) as? Bool ?? true
        earthquakeNotificationsEnabled = defaults.object(forKey: Key.earthquakeNotificationsEnabled) as? Bool ?? true
        eewNotificationsEnabled = defaults.object(forKey: Key.eewNotificationsEnabled) as? Bool ?? true
    }

    var devicePreferences: DevicePreferences {
        DevicePreferences(
            notificationsEnabled: notificationsEnabled,
            timeSensitiveEnabled: timeSensitiveEnabled,
            customSoundEnabled: customSoundEnabled,
            eventTypes: enabledEventTypes
        )
    }

    var enabledEventTypes: [String] {
        guard notificationsEnabled else { return [] }
        var types: [RelayEventType] = [.systemTest]
        if earthquakeNotificationsEnabled {
            types += [.earthquakeInfo, .earthquakeUpdate]
        }
        if eewNotificationsEnabled {
            types += [.eewForecast, .eewWarning, .eewCancel]
        }
        return types.map(\.rawValue)
    }

    func apply(_ preferences: DevicePreferences) {
        notificationsEnabled = preferences.notificationsEnabled
        timeSensitiveEnabled = preferences.timeSensitiveEnabled
        customSoundEnabled = preferences.customSoundEnabled
        let types = Set(preferences.eventTypes)
        earthquakeNotificationsEnabled = !types.isDisjoint(with: [
            RelayEventType.earthquakeInfo.rawValue,
            RelayEventType.earthquakeUpdate.rawValue
        ])
        eewNotificationsEnabled = !types.isDisjoint(with: [
            RelayEventType.eewForecast.rawValue,
            RelayEventType.eewWarning.rawValue,
            RelayEventType.eewCancel.rawValue
        ])
    }
}
