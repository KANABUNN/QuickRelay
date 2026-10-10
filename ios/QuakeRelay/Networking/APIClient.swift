import Foundation

private final class RejectHTTPRedirectsDelegate: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    func urlSession(
        _: URLSession,
        task _: URLSessionTask,
        willPerformHTTPRedirection _: HTTPURLResponse,
        newRequest _: URLRequest,
        completionHandler: @escaping (URLRequest?) -> Void
    ) {
        // The configured API URL must be final. Refusing redirects ensures an
        // Authorization header can never be replayed to another origin.
        completionHandler(nil)
    }
}

enum APIClientError: LocalizedError, Equatable {
    case invalidBaseURL
    case insecureBaseURL
    case notPaired
    case invalidResponse
    case server(status: Int, code: String, message: String, requestID: String?)
    case decoding(String)

    var errorDescription: String? {
        switch self {
        case .invalidBaseURL:
            return "サーバーURLが正しくありません。"
        case .insecureBaseURL:
            return "サーバーURLにはHTTPSを使用してください。"
        case .notPaired:
            return "この端末はまだペアリングされていません。"
        case .invalidResponse:
            return "サーバーからHTTP応答を取得できませんでした。"
        case let .server(status, code, message, requestID):
            let suffix = requestID.map { " (request: \($0))" } ?? ""
            return "サーバーエラー \(status) [\(code)]: \(message)\(suffix)"
        case let .decoding(message):
            return "サーバー応答を読み取れませんでした: \(message)"
        }
    }
}

@MainActor
final class APIClient {
    private enum Authentication {
        case none
        case device
    }

    private let settings: AppSettings
    private let credentials: DeviceCredentialStoring
    private let session: URLSession
    private let decoder = JSONDecoder.quakeRelay
    private let encoder = JSONEncoder.quakeRelay

    init(
        settings: AppSettings,
        credentials: DeviceCredentialStoring,
        session: URLSession? = nil
    ) {
        self.settings = settings
        self.credentials = credentials
        if let session {
            self.session = session
        } else {
            let configuration = URLSessionConfiguration.ephemeral
            configuration.timeoutIntervalForRequest = 20
            configuration.timeoutIntervalForResource = 45
            configuration.waitsForConnectivity = true
            configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
            self.session = URLSession(
                configuration: configuration,
                delegate: RejectHTTPRedirectsDelegate(),
                delegateQueue: nil
            )
        }
    }

    func completePairing(
        code: String,
        installationID: String,
        serverBaseURL: String
    ) async throws -> PairCompleteResponse {
        let body = try encoder.encode(PairCompleteRequest(pairingCode: code, installationId: installationID))
        return try await request(
            path: "pair/complete",
            method: "POST",
            body: body,
            authentication: .none,
            serverBaseURL: serverBaseURL
        )
    }

    func sync(after sequence: UInt64, limit: Int = 200) async throws -> SyncResponse {
        let safeLimit = min(200, max(1, limit))
        return try await request(
            path: "sync",
            queryItems: [
                URLQueryItem(name: "after_sequence", value: String(sequence)),
                URLQueryItem(name: "limit", value: String(safeLimit))
            ],
            authentication: .device
        )
    }

    func events(limit: Int = 50, cursor: String? = nil) async throws -> EventsResponse {
        var query = [URLQueryItem(name: "limit", value: String(min(200, max(1, limit))))]
        if let cursor { query.append(URLQueryItem(name: "cursor", value: cursor)) }
        return try await request(path: "events", queryItems: query, authentication: .device)
    }

    func event(id: String) async throws -> EventDetailResponse {
        try await request(path: "events/\(id)", authentication: .device)
    }

    func registerDevice(_ registration: DeviceRegistrationRequest) async throws -> DeviceResponse {
        let body = try encoder.encode(registration)
        return try await request(path: "devices/register", method: "POST", body: body, authentication: .device)
    }

    func currentDevice() async throws -> DeviceResponse {
        try await request(path: "devices/me", authentication: .device)
    }

    func updatePreferences(_ preferences: DevicePreferences) async throws -> PreferencesResponse {
        let patch = PreferencesPatchRequest(
            notificationsEnabled: preferences.notificationsEnabled,
            timeSensitiveEnabled: preferences.timeSensitiveEnabled,
            customSoundEnabled: preferences.customSoundEnabled,
            eventTypes: preferences.eventTypes,
            earthquakeRegions: preferences.earthquakeRegions ?? [],
            tsunamiRegions: preferences.tsunamiRegions ?? [],
            minimumIntensity: preferences.minimumIntensity ?? "",
            liveActivitiesEnabled: preferences.liveActivitiesEnabled ?? false
        )
        let body = try encoder.encode(patch)
        return try await request(
            path: "devices/me/preferences",
            method: "PATCH",
            body: body,
            authentication: .device
        )
    }

    func revokeDevice() async throws {
        let _: SuccessResponse = try await request(path: "devices/me", method: "DELETE", authentication: .device)
    }

    func serverIdentity() throws -> String {
        let request = try makeRequest(path: "", queryItems: [], serverBaseURL: settings.serverBaseURL)
        guard let url = request.url else { throw APIClientError.invalidBaseURL }
        return url.absoluteString
    }

    func stationCatalog() async throws -> StationCatalogResponse {
        try await request(path: "map/stations", authentication: .device)
    }

    func receiverStatus() async throws -> ReceiverStatusResponse {
        try await request(path: "status", authentication: .device)
    }

    func requestNotificationTest(id: String, style: String) async throws -> NotificationTestResponse {
        let body = try encoder.encode(NotificationTestRequest(requestId: id, style: style))
        return try await request(path: "devices/me/notification-tests", method: "POST", body: body, authentication: .device)
    }

    func health() async throws -> HealthResponse {
        try await request(path: "health", authentication: .none)
    }

    private func request<Response: Decodable>(
        path: String,
        method: String = "GET",
        queryItems: [URLQueryItem] = [],
        body: Data? = nil,
        authentication: Authentication,
        serverBaseURL: String? = nil
    ) async throws -> Response {
        let data = try await requestData(path: path, method: method, queryItems: queryItems, body: body,
                                         authentication: authentication, serverBaseURL: serverBaseURL)
        do { return try decoder.decode(Response.self, from: data) }
        catch { throw APIClientError.decoding(String(describing: error)) }
    }

    func sourceDocument(reportID: String) async throws -> Data {
        guard !reportID.isEmpty, reportID.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }) else {
            throw APIClientError.invalidResponse
        }
        return try await requestData(path: "reports/\(reportID)/source", authentication: .device)
    }

    private func requestData(
        path: String, method: String = "GET", queryItems: [URLQueryItem] = [], body: Data? = nil,
        authentication: Authentication, serverBaseURL: String? = nil
    ) async throws -> Data {
        var request = try makeRequest(
            path: path,
            queryItems: queryItems,
            serverBaseURL: serverBaseURL ?? settings.serverBaseURL
        )
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        if case .device = authentication {
            guard
                let requestURL = request.url,
                let token = try credentials.accessToken(for: requestURL.absoluteString),
                !token.isEmpty
            else {
                throw APIClientError.notPaired
            }
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }

        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw APIClientError.invalidResponse
        }
        guard
            let requestURL = request.url,
            let responseURL = httpResponse.url,
            let requestOrigin = ServerCredentialOrigin.normalized(from: requestURL.absoluteString),
            let responseOrigin = ServerCredentialOrigin.normalized(from: responseURL.absoluteString),
            requestOrigin == responseOrigin
        else {
            // The production session refuses redirects. Keep this independent
            // origin check as a second boundary for injected/test sessions and
            // any future transport implementation.
            throw APIClientError.invalidResponse
        }

        guard (200...299).contains(httpResponse.statusCode) else {
            if let envelope = try? decoder.decode(APIErrorEnvelope.self, from: data) {
                throw APIClientError.server(
                    status: httpResponse.statusCode,
                    code: envelope.error.code,
                    message: envelope.error.message,
                    requestID: envelope.requestId
                )
            }
            throw APIClientError.server(
                status: httpResponse.statusCode,
                code: "http_error",
                message: HTTPURLResponse.localizedString(forStatusCode: httpResponse.statusCode),
                requestID: httpResponse.value(forHTTPHeaderField: "X-Request-ID")
            )
        }

        return data
    }

    private func makeRequest(
        path: String,
        queryItems: [URLQueryItem],
        serverBaseURL: String
    ) throws -> URLRequest {
        let trimmed = serverBaseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard
            ServerCredentialOrigin.normalized(from: trimmed) != nil,
            var components = URLComponents(string: trimmed),
            let scheme = components.scheme?.lowercased(),
            let host = components.host.map(ServerCredentialOrigin.normalizedHost),
            !host.isEmpty,
            components.user == nil,
            components.password == nil
        else {
            throw APIClientError.invalidBaseURL
        }

        components.scheme = scheme
        components.host = host.contains(":") ? "[\(host)]" : host
        let localDevelopmentHost = host == "localhost" || host == "127.0.0.1" || host == "::1"
        guard scheme == "https" || (scheme == "http" && localDevelopmentHost) else {
            throw APIClientError.insecureBaseURL
        }

        let cleanPath = path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        let basePath = components.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        components.path = "/" + [basePath, cleanPath].filter { !$0.isEmpty }.joined(separator: "/")
        components.queryItems = queryItems.isEmpty ? nil : queryItems
        components.fragment = nil
        guard let url = components.url else { throw APIClientError.invalidBaseURL }
        return URLRequest(url: url)
    }
}

extension APIClient {
    func clearLiveActivityStartToken() async throws {
        let _: SuccessResponse = try await request(path: "devices/me/live-activity/start-token",
                                                   method: "DELETE", authentication: .device)
    }
    func registerLiveActivityStartToken(_ token: String) async throws {
        let body = try encoder.encode(["push_token": token])
        let _: SuccessResponse = try await request(path: "devices/me/live-activity/start-token",
                                                   method: "PUT", body: body, authentication: .device)
    }
    func registerLiveActivityToken(eventID: String, telegramType: String, activityID: String, pushToken: String, startSequence: Int64) async throws {
        struct Registration: Encodable {
            let eventId: String
            let telegramType: String
            let activityId: String
            let pushToken: String
            let startSequence: Int64
        }
        let body = try encoder.encode(Registration(eventId: eventID, telegramType: telegramType,
                                                   activityId: activityID, pushToken: pushToken, startSequence: startSequence))
        let _: SuccessResponse = try await request(path: "devices/me/live-activity/token",
                                                   method: "PUT", body: body, authentication: .device)
    }
    func liveActivityEnded(eventID: String, telegramType: String, activityID: String, startSequence: Int64, dismissed: Bool) async throws {
        struct End: Encodable {
            let eventId: String
            let telegramType: String
            let activityId: String
            let startSequence: Int64
            let dismissed: Bool
        }
        let body = try encoder.encode(End(eventId: eventID, telegramType: telegramType, activityId: activityID, startSequence: startSequence, dismissed: dismissed))
        let _: SuccessResponse = try await request(path: "devices/me/live-activity/ended",
                                                   method: "POST", body: body, authentication: .device)
    }
}
