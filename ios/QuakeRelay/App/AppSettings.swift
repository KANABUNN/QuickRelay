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

    @Published var earthquakeRegionsText: String {
        didSet { defaults.set(earthquakeRegionsText, forKey: "settings.earthquakeRegions") }
    }
    @Published var tsunamiRegionsText: String {
        didSet { defaults.set(tsunamiRegionsText, forKey: "settings.tsunamiRegions") }
    }
    @Published var minimumIntensity: String {
        didSet { defaults.set(minimumIntensity, forKey: "settings.minimumIntensity") }
    }
    @Published var eewForecastEnabled: Bool {
        didSet { defaults.set(eewForecastEnabled, forKey: "settings.eewForecast") }
    }
    @Published var eewWarningEnabled: Bool {
        didSet { defaults.set(eewWarningEnabled, forKey: "settings.eewWarning") }
    }
    @Published var tsunamiWarningEnabled: Bool {
        didSet { defaults.set(tsunamiWarningEnabled, forKey: "settings.tsunamiWarning") }
    }
    @Published var tsunamiObservationsEnabled: Bool {
        didSet { defaults.set(tsunamiObservationsEnabled, forKey: "settings.tsunamiObservations") }
    }
    @Published var nankaiEnabled: Bool {
        didSet { defaults.set(nankaiEnabled, forKey: "settings.nankai") }
    }
    @Published var seismicAdvisoryEnabled: Bool {
        didSet { defaults.set(seismicAdvisoryEnabled, forKey: "settings.seismicAdvisory") }
    }
    @Published var liveActivitiesEnabled: Bool {
        didSet { defaults.set(liveActivitiesEnabled, forKey: "settings.liveActivities") }
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
        earthquakeRegionsText = defaults.string(forKey: "settings.earthquakeRegions") ?? ""
        tsunamiRegionsText = defaults.string(forKey: "settings.tsunamiRegions") ?? ""
        minimumIntensity = defaults.string(forKey: "settings.minimumIntensity") ?? ""
        eewForecastEnabled = defaults.object(forKey: "settings.eewForecast") as? Bool ?? true
        eewWarningEnabled = defaults.object(forKey: "settings.eewWarning") as? Bool ?? true
        tsunamiWarningEnabled = defaults.object(forKey: "settings.tsunamiWarning") as? Bool ?? true
        tsunamiObservationsEnabled = defaults.object(forKey: "settings.tsunamiObservations") as? Bool ?? true
        nankaiEnabled = defaults.object(forKey: "settings.nankai") as? Bool ?? true
        seismicAdvisoryEnabled = defaults.object(forKey: "settings.seismicAdvisory") as? Bool ?? true
        liveActivitiesEnabled = defaults.object(forKey: "settings.liveActivities") as? Bool ?? false
    }

    var devicePreferences: DevicePreferences {
        DevicePreferences(
            notificationsEnabled: notificationsEnabled,
            timeSensitiveEnabled: timeSensitiveEnabled,
            customSoundEnabled: customSoundEnabled,
            eventTypes: enabledEventTypes,
            earthquakeRegions: RegionSelection.parse(earthquakeRegionsText),
            tsunamiRegions: RegionSelection.parse(tsunamiRegionsText),
            minimumIntensity: minimumIntensity,
            liveActivitiesEnabled: liveActivitiesEnabled
        )
    }

    var enabledEventTypes: [String] {
        guard notificationsEnabled else { return [] }
        var types: [RelayEventType] = [.systemTest]
        if earthquakeNotificationsEnabled {
            types += [.earthquakeInfo, .earthquakeUpdate]
        }
        if eewNotificationsEnabled {
            if eewForecastEnabled { types += [.eewForecast] }
            if eewWarningEnabled { types += [.eewWarning] }
            if eewForecastEnabled || eewWarningEnabled { types += [.eewCancel] }
        }
        if tsunamiNotificationsEnabled {
            if tsunamiWarningEnabled { types += [.tsunamiWarning] }
            if tsunamiObservationsEnabled { types += [.tsunamiInfo] }
        }
        if advisoryNotificationsEnabled {
            if nankaiEnabled { types += [.nankaiInfo] }
            if seismicAdvisoryEnabled { types += [.seismicAdvisory, .earthquakeData] }
        }
        return types.map(\.rawValue)
    }

    func apply(_ preferences: DevicePreferences) {
        notificationsEnabled = preferences.notificationsEnabled
        timeSensitiveEnabled = preferences.timeSensitiveEnabled
        customSoundEnabled = preferences.customSoundEnabled
        let types = Set(preferences.eventTypes)
        earthquakeRegionsText = (preferences.earthquakeRegions ?? []).joined(separator: "、")
        tsunamiRegionsText = (preferences.tsunamiRegions ?? []).joined(separator: "、")
        minimumIntensity = preferences.minimumIntensity ?? ""
        if let enabled = preferences.liveActivitiesEnabled { liveActivitiesEnabled = enabled }
        eewForecastEnabled = types.contains("eew_forecast")
        eewWarningEnabled = types.contains("eew_warning")
        if !needsExpandedPreferences {
            tsunamiWarningEnabled = types.contains("tsunami_warning")
            tsunamiObservationsEnabled = types.contains("tsunami_info")
            nankaiEnabled = types.contains("nankai_info")
            seismicAdvisoryEnabled = !types.isDisjoint(with: ["seismic_advisory", "earthquake_data"])
        }
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

enum RegionSelection {
    static func parse(_ text: String) -> [String] {
        var seen = Set<String>()
        return text.components(separatedBy: CharacterSet(charactersIn: "、,，\n"))
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty && seen.insert($0).inserted }
    }
}
