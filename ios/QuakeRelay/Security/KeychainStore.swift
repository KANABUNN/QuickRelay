import Foundation
import Security

protocol DeviceCredentialStoring: AnyObject {
    func installationID() throws -> String
    func accessToken(for serverBaseURL: String) throws -> String?
    func saveAccessToken(_ token: String, for serverBaseURL: String) throws
    func clearAccessToken() throws
}

enum ServerCredentialOrigin {
    static func normalized(from value: String) -> String? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard
            let components = URLComponents(string: trimmed),
            let scheme = components.scheme?.lowercased(),
            scheme == "https" || scheme == "http",
            let host = components.host.map(normalizedHost),
            !host.isEmpty,
            components.user == nil,
            components.password == nil,
            components.url != nil
        else {
            return nil
        }

        // A trailing DNS root dot and an omitted default port do not create a
        // different network origin. Normalize both so an equivalent spelling
        // cannot accidentally strand a valid credential.
        let port = components.port ?? (scheme == "https" ? 443 : 80)
        guard (1...65_535).contains(port) else { return nil }
        let hostLiteral = host.contains(":") ? "[\(host)]" : host

        return "\(scheme)://\(hostLiteral):\(port)"
    }

    static func normalizedHost(_ value: String) -> String {
        var host = value.lowercased()
        // Foundation can include IPv6 brackets in host. Keep the canonical
        // host unwrapped and add exactly one pair when building a URL.
        if host.hasPrefix("[") && host.hasSuffix("]") {
            host.removeFirst()
            host.removeLast()
        }
        while host.hasSuffix(".") {
            host.removeLast()
        }
        return host
    }
}

enum KeychainStoreError: LocalizedError, Equatable {
    case unexpectedStatus(OSStatus)
    case invalidData

    var errorDescription: String? {
        switch self {
        case let .unexpectedStatus(status):
            let systemMessage = SecCopyErrorMessageString(status, nil) as String? ?? "unknown"
            return "Keychain error \(status): \(systemMessage)"
        case .invalidData:
            return "Keychain contained invalid credential data."
        }
    }
}

final class KeychainCredentialStore: DeviceCredentialStoring, @unchecked Sendable {
    private struct BoundAccessToken: Codable {
        let version: Int
        let token: String
        let serverOrigin: String
    }

    private enum Account {
        static let installationID = "installation-id"
        static let accessToken = "device-access-token"
    }

    private let service: String

    init(service: String = "jp.kb-dev.quickrelay.credentials") {
        self.service = service
    }

    func installationID() throws -> String {
        if let existing = try read(account: Account.installationID) {
            return existing
        }
        let identifier = UUID().uuidString.lowercased()
        try write(identifier, account: Account.installationID)
        return identifier
    }

    func accessToken(for serverBaseURL: String) throws -> String? {
        guard let requestedOrigin = ServerCredentialOrigin.normalized(from: serverBaseURL) else {
            throw KeychainStoreError.invalidData
        }
        guard let stored = try read(account: Account.accessToken) else { return nil }

        // Releases prior to origin binding stored the raw bearer token. Never
        // attach that legacy value to a request because its intended server is
        // unknowable; the user must pair again to bind a fresh credential.
        guard stored.hasPrefix("{") else { return nil }
        guard
            let data = stored.data(using: .utf8),
            let credential = try? JSONDecoder().decode(BoundAccessToken.self, from: data),
            credential.version == 1,
            !credential.token.isEmpty,
            credential.serverOrigin == requestedOrigin
        else {
            return nil
        }
        return credential.token
    }

    func saveAccessToken(_ token: String, for serverBaseURL: String) throws {
        guard
            !token.isEmpty,
            let origin = ServerCredentialOrigin.normalized(from: serverBaseURL),
            let encoded = try? JSONEncoder().encode(BoundAccessToken(
                version: 1,
                token: token,
                serverOrigin: origin
            )),
            let value = String(data: encoded, encoding: .utf8)
        else {
            throw KeychainStoreError.invalidData
        }
        // Token and origin are one Keychain value, so interruption cannot pair
        // a new token with a stale origin (or vice versa).
        try write(value, account: Account.accessToken)
    }

    func clearAccessToken() throws {
        let status = SecItemDelete(baseQuery(account: Account.accessToken) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainStoreError.unexpectedStatus(status)
        }
    }

    private func read(account: String) throws -> String? {
        var query = baseQuery(account: account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess else {
            throw KeychainStoreError.unexpectedStatus(status)
        }
        guard
            let data = result as? Data,
            let value = String(data: data, encoding: .utf8)
        else {
            throw KeychainStoreError.invalidData
        }
        return value
    }

    private func write(_ value: String, account: String) throws {
        let data = Data(value.utf8)
        let query = baseQuery(account: account)
        let attributes: [String: Any] = [kSecValueData as String: data]
        let updateStatus = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)

        if updateStatus == errSecSuccess { return }
        guard updateStatus == errSecItemNotFound else {
            throw KeychainStoreError.unexpectedStatus(updateStatus)
        }

        var insertion = query
        insertion[kSecValueData as String] = data
        insertion[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let addStatus = SecItemAdd(insertion as CFDictionary, nil)
        guard addStatus == errSecSuccess else {
            throw KeychainStoreError.unexpectedStatus(addStatus)
        }
    }

    private func baseQuery(account: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
    }
}
