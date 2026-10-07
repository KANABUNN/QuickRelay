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
        static let expandedPreferences = "settings.expandedPreferences.v1"
        static let tsunamiNotificationsEnabled = "settings.tsunamiNotificationsEnabled"
        static let advisoryNotificationsEnabled = "settings.advisoryNotificationsEnabled"
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

    @Published var tsunamiNotificationsEnabled: Bool {
        didSet { defaults.set(tsunamiNotificationsEnabled, forKey: Key.tsunamiNotificationsEnabled) }
    }
    @Published var advisoryNotificationsEnabled: Bool {
        didSet { defaults.set(advisoryNotificationsEnabled, forKey: Key.advisoryNotificationsEnabled) }
    }
    var needsExpandedPreferences: Bool { !defaults.bool(forKey: Key.expandedPreferences) }
    func expandedPreferencesSaved() { defaults.set(true, forKey: Key.expandedPreferences) }

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        serverBaseURL = defaults.string(forKey: Key.serverBaseURL) ?? Self.placeholderServerURL
        notificationsEnabled = defaults.object(forKey: Key.notificationsEnabled) as? Bool ?? true
        timeSensitiveEnabled = defaults.object(forKey: Key.timeSensitiveEnabled) as? Bool ?? true
        customSoundEnabled = defaults.object(forKey: Key.customSoundEnabled) as? Bool ?? true
        earthquakeNotificationsEnabled = defaults.object(forKey: Key.earthquakeNotificationsEnabled) as? Bool ?? true
        tsunamiNotificationsEnabled = defaults.object(forKey: Key.tsunamiNotificationsEnabled) as? Bool ?? true
        advisoryNotificationsEnabled = defaults.object(forKey: Key.advisoryNotificationsEnabled) as? Bool ?? true
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
        if tsunamiNotificationsEnabled { types += [.tsunamiWarning, .tsunamiInfo] }
        if advisoryNotificationsEnabled { types += [.nankaiInfo, .seismicAdvisory, .earthquakeData] }
        return types.map(\.rawValue)
    }

    func apply(_ preferences: DevicePreferences) {
        notificationsEnabled = preferences.notificationsEnabled
        timeSensitiveEnabled = preferences.timeSensitiveEnabled
        customSoundEnabled = preferences.customSoundEnabled
        let types = Set(preferences.eventTypes)
        if !needsExpandedPreferences {
            tsunamiNotificationsEnabled = !types.isDisjoint(with: ["tsunami_warning", "tsunami_info"])
            advisoryNotificationsEnabled = !types.isDisjoint(with: ["nankai_info", "seismic_advisory", "earthquake_data"])
        }
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
