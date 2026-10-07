import Combine
import Foundation

enum AppTab: Hashable {
    case events
    case settings
}

enum AppRoute: Hashable {
    case event(id: String, reportID: String?)
}

enum DeepLinkParser {
    static func parse(url: URL) -> AppRoute? {
        guard url.scheme?.lowercased() == "quake-relay" else { return nil }
        guard url.host?.lowercased() == "event" else { return nil }
        let eventID = url.pathComponents
            .filter { $0 != "/" }
            .first?
            .removingPercentEncoding?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        guard let eventID, !eventID.isEmpty else { return nil }

        let components = URLComponents(url: url, resolvingAgainstBaseURL: false)
        let reportID = components?.queryItems?
            .first(where: { $0.name == "report" })?
            .value?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return .event(id: eventID, reportID: reportID?.isEmpty == false ? reportID : nil)
    }

    static func parse(userInfo: [AnyHashable: Any]) -> AppRoute? {
        guard let eventID = userInfo["event_id"] as? String, !eventID.isEmpty else {
            return nil
        }
        let reportID = (userInfo["report_id"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        return .event(id: eventID, reportID: reportID)
    }
}

@MainActor
final class DeepLinkRouter: ObservableObject {
    @Published var selectedTab: AppTab = .events
    @Published var path: [AppRoute] = []

    func open(_ route: AppRoute) {
        selectedTab = .events
        path = [route]
    }

    func open(_ url: URL) {
        guard let route = DeepLinkParser.parse(url: url) else { return }
        open(route)
    }

    func reset() {
        path.removeAll()
    }
}
