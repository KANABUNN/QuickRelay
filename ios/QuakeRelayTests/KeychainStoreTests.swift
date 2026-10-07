import Foundation
import Security
import XCTest
@testable import QuakeRelay

final class KeychainStoreTests: XCTestCase {
    private var service = ""
    private var store: KeychainCredentialStore!

    override func setUp() {
        super.setUp()
        OriginBindingURLProtocol.reset()
        service = "jp.kb-dev.quickrelay.tests.\(UUID().uuidString)"
        store = KeychainCredentialStore(service: service)
    }

    override func tearDown() {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service
        ]
        SecItemDelete(query as CFDictionary)
        OriginBindingURLProtocol.reset()
        store = nil
        super.tearDown()
    }

    func testInstallationIDIsStableAndAccessTokenCanBeCleared() throws {
        let firstID = try store.installationID()
        let secondID = try store.installationID()
        XCTAssertEqual(firstID, secondID)
        XCTAssertNotNil(UUID(uuidString: firstID))

        XCTAssertNil(try store.accessToken(for: "https://quake.example.jp/api/v1"))
        try store.saveAccessToken("secret-test-token", for: "HTTPS://QUAKE.EXAMPLE.JP/api/v1")
        XCTAssertEqual(
            try store.accessToken(for: "https://quake.example.jp:443/another/path"),
            "secret-test-token"
        )
        XCTAssertNil(try store.accessToken(for: "https://other.example.jp/api/v1"))
        XCTAssertNil(try store.accessToken(for: "https://quake.example.jp:8443/api/v1"))
        XCTAssertNil(try store.accessToken(for: "http://quake.example.jp/api/v1"))
        try store.clearAccessToken()
        XCTAssertNil(try store.accessToken(for: "https://quake.example.jp/api/v1"))
    }

    func testRejectsEmptyAccessToken() {
        XCTAssertThrowsError(try store.saveAccessToken("", for: "https://quake.example.jp/api/v1")) { error in
            XCTAssertEqual(error as? KeychainStoreError, .invalidData)
        }
    }

    func testOriginNormalizationUsesOnlyCanonicalNetworkOrigin() {
        XCTAssertEqual(
            ServerCredentialOrigin.normalized(from: "  HTTPS://QUAKE.Example.JP../api/v1?x=1#fragment  "),
            "https://quake.example.jp:443"
        )
        XCTAssertEqual(
            ServerCredentialOrigin.normalized(from: "http://[::1]/api/v1"),
            "http://[::1]:80"
        )
        XCTAssertEqual(
            ServerCredentialOrigin.normalized(from: "https://quake.example.jp:8443/api/v1"),
            "https://quake.example.jp:8443"
        )
        XCTAssertEqual(
            ServerCredentialOrigin.normalized(from: "https://[2001:DB8::1]:8443/api/v1"),
            "https://[2001:db8::1]:8443"
        )
        XCTAssertNil(ServerCredentialOrigin.normalized(from: "https://user@quake.example.jp/api/v1"))
        XCTAssertNil(ServerCredentialOrigin.normalized(from: "/api/v1"))
        XCTAssertNil(ServerCredentialOrigin.normalized(from: "https:///api/v1"))
    }

    func testPairingOriginGuardAcceptsEquivalentSpellingAndRejectsOriginChange() throws {
        let captured = try PairingServerBinding.capture(
            serverBaseURL: "HTTPS://QUAKE.EXAMPLE.JP/api/v1"
        )
        XCTAssertNoThrow(try PairingServerBinding.validateUnchanged(
            capturedOrigin: captured,
            currentServerBaseURL: "https://quake.example.jp:443/another/base/path"
        ))
        XCTAssertThrowsError(try PairingServerBinding.validateUnchanged(
            capturedOrigin: captured,
            currentServerBaseURL: "https://other.example.jp/api/v1"
        )) { error in
            XCTAssertEqual(error as? PairingSafetyError, .serverChangedDuringPairing)
        }
        XCTAssertThrowsError(try PairingServerBinding.capture(serverBaseURL: "not-a-url")) { error in
            XCTAssertEqual(error as? PairingSafetyError, .invalidServer)
        }
    }

    func testLegacyUnboundTokenFailsClosedUntilRepaired() throws {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: "device-access-token",
            kSecValueData as String: Data("legacy-secret-token".utf8),
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        ]
        XCTAssertEqual(SecItemAdd(query as CFDictionary, nil), errSecSuccess)

        XCTAssertNil(try store.accessToken(for: "https://quake.example.jp/api/v1"))
        try store.saveAccessToken("new-bound-token", for: "https://quake.example.jp/api/v1")
        XCTAssertEqual(
            try store.accessToken(for: "https://quake.example.jp/api/v1"),
            "new-bound-token"
        )
    }

    @MainActor
    func testAPIClientNeverSendsTokenToDifferentOrigin() async throws {
        let settings = makeSettings(serverURL: "https://other.example.jp/api/v1")
        try store.saveAccessToken("real-device-token", for: "https://quake.example.jp/api/v1")
        OriginBindingURLProtocol.lastRequest = nil
        let client = APIClient(settings: settings, credentials: store, session: makeSession())

        do {
            _ = try await client.currentDevice()
            XCTFail("A credential bound to another origin must not authorize the request.")
        } catch {
            XCTAssertEqual(error as? APIClientError, .notPaired)
        }
        XCTAssertNil(OriginBindingURLProtocol.lastRequest)
    }

    @MainActor
    func testAPIClientAttachesTokenForEquivalentNormalizedOrigin() async throws {
        let settings = makeSettings(serverURL: "https://QUAKE.example.jp:443/api/v1")
        try store.saveAccessToken("real-device-token", for: "https://quake.example.jp/api/v1")
        OriginBindingURLProtocol.lastRequest = nil
        let client = APIClient(settings: settings, credentials: store, session: makeSession())

        do {
            _ = try await client.currentDevice()
            XCTFail("The fixture returns an intentional HTTP error.")
        } catch let error as APIClientError {
            guard case .server(status: 503, code: _, message: _, requestID: _) = error else {
                return XCTFail("Expected the fixture HTTP error, got \(error)")
            }
        }
        XCTAssertEqual(
            OriginBindingURLProtocol.lastRequest?.value(forHTTPHeaderField: "Authorization"),
            "Bearer real-device-token"
        )
    }

    @MainActor
    func testAPIClientSupportsIPv6LoopbackWithBoundCredentials() async throws {
        let settings = makeSettings(serverURL: "http://[::1]:8080/api/v1")
        try store.saveAccessToken("ipv6-test-token", for: "http://[::1]:8080")
        let client = APIClient(settings: settings, credentials: store, session: makeSession())

        do {
            _ = try await client.currentDevice()
            XCTFail("The fixture returns an intentional HTTP error.")
        } catch let error as APIClientError {
            guard case .server(status: 503, code: _, message: _, requestID: _) = error else {
                return XCTFail("Expected the fixture HTTP error, got \(error)")
            }
        }
        XCTAssertEqual(
            OriginBindingURLProtocol.lastRequest?.url?.absoluteString,
            "http://[::1]:8080/api/v1/devices/me"
        )
        XCTAssertEqual(
            OriginBindingURLProtocol.lastRequest?.value(forHTTPHeaderField: "Authorization"),
            "Bearer ipv6-test-token"
        )
        XCTAssertNil(try store.accessToken(for: "http://[::1]:8081/api/v1"))
    }

    @MainActor
    func testPairingRequestUsesCapturedURLInsteadOfMutableSettings() async throws {
        let settings = makeSettings(serverURL: "https://changed.example.jp/api/v1")
        OriginBindingURLProtocol.responseStatus = 200
        OriginBindingURLProtocol.responseBody = Data(#"""
        {
          "ok":true,
          "device_access_token":"abcdefghijklmnopqrstuvwxyzABCDEFGH123456789",
          "expires_at":null
        }
        """#.utf8)
        let client = APIClient(settings: settings, credentials: store, session: makeSession())

        let response = try await client.completePairing(
            code: "12345678",
            installationID: "1777a91b-13c5-4c8c-9374-1888ead66fac",
            serverBaseURL: "https://quake.example.jp/api/v1"
        )

        XCTAssertEqual(response.deviceAccessToken, "abcdefghijklmnopqrstuvwxyzABCDEFGH123456789")
        XCTAssertEqual(OriginBindingURLProtocol.lastRequest?.url?.host, "quake.example.jp")
        XCTAssertEqual(OriginBindingURLProtocol.lastRequest?.url?.path, "/api/v1/pair/complete")
        XCTAssertNil(OriginBindingURLProtocol.lastRequest?.value(forHTTPHeaderField: "Authorization"))
    }

    @MainActor
    func testAPIClientRejectsUserInfoAndRemotePlainHTTPBeforeNetwork() async {
        var settings = makeSettings(serverURL: "https://user@quake.example.jp/api/v1")
        var client = APIClient(settings: settings, credentials: store, session: makeSession())
        do {
            _ = try await client.health()
            XCTFail("A server URL with userinfo must be rejected.")
        } catch {
            XCTAssertEqual(error as? APIClientError, .invalidBaseURL)
        }
        XCTAssertNil(OriginBindingURLProtocol.lastRequest)

        settings = makeSettings(serverURL: "http://quake.example.jp/api/v1")
        client = APIClient(settings: settings, credentials: store, session: makeSession())
        do {
            _ = try await client.health()
            XCTFail("Plain HTTP is only allowed for loopback development hosts.")
        } catch {
            XCTAssertEqual(error as? APIClientError, .insecureBaseURL)
        }
        XCTAssertNil(OriginBindingURLProtocol.lastRequest)

        settings = makeSettings(serverURL: "http://[2001:db8::1]/api/v1")
        client = APIClient(settings: settings, credentials: store, session: makeSession())
        do {
            _ = try await client.health()
            XCTFail("Remote IPv6 hosts must also require HTTPS.")
        } catch {
            XCTAssertEqual(error as? APIClientError, .insecureBaseURL)
        }
        XCTAssertNil(OriginBindingURLProtocol.lastRequest)
    }

    @MainActor
    func testAPIClientRejectsResponseClaimingAnotherOrigin() async {
        let settings = makeSettings(serverURL: "https://quake.example.jp/api/v1")
        OriginBindingURLProtocol.responseStatus = 200
        OriginBindingURLProtocol.responseURL = URL(string: "https://other.example.jp/api/v1/health")
        OriginBindingURLProtocol.responseBody = Data(#"""
        {
          "ok":true,
          "version":"1.0.0",
          "db":true,
          "apns_http2":true,
          "config_loaded":true,
          "migration_current":true,
          "time":"2026-08-13T13:31:00.000Z"
        }
        """#.utf8)
        let client = APIClient(settings: settings, credentials: store, session: makeSession())

        do {
            _ = try await client.health()
            XCTFail("A response from another origin must never be accepted.")
        } catch {
            XCTAssertEqual(error as? APIClientError, .invalidResponse)
        }
    }

    @MainActor
    private func makeSettings(serverURL: String) -> AppSettings {
        let suite = "jp.kb-dev.quickrelay.origin-tests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        let settings = AppSettings(defaults: defaults)
        settings.serverBaseURL = serverURL
        return settings
    }

    private func makeSession() -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [OriginBindingURLProtocol.self]
        return URLSession(configuration: configuration)
    }
}

private final class OriginBindingURLProtocol: URLProtocol {
    static var lastRequest: URLRequest?
    static var responseStatus = 503
    static var responseURL: URL?
    static var responseBody = Data(#"{"ok":false,"error":{"code":"fixture","message":"fixture"}}"#.utf8)

    static func reset() {
        lastRequest = nil
        responseStatus = 503
        responseURL = nil
        responseBody = Data(#"{"ok":false,"error":{"code":"fixture","message":"fixture"}}"#.utf8)
    }

    override class func canInit(with _: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.lastRequest = request
        let response = HTTPURLResponse(
            url: Self.responseURL ?? request.url!,
            statusCode: Self.responseStatus,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": "application/json"]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Self.responseBody)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
