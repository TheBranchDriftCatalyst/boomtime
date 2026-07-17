//
//  Keychain.swift
//  BoomtimeWatch
//
//  Minimal Keychain wrapper for the API token. We use kSecClassGenericPassword
//  with a fixed service string so we don't need any framework beyond Security.
//

import Foundation
import Security

enum Keychain {
    private static let service = "com.boomtime.watch"
    private static let account = "api_token"

    /// Stores the token, overwriting any existing entry.
    /// Returns false on Security framework failure.
    @discardableResult
    static func setToken(_ token: String) -> Bool {
        // Delete first so we don't have to branch on SecItemUpdate vs Add.
        _ = deleteToken()

        guard let data = token.data(using: .utf8) else { return false }
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecValueData as String: data,
            // kSecAttrAccessibleAfterFirstUnlock — background sync fires after the
            // user's first unlock post-boot, which matches HealthKit's own posture.
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlock,
        ]
        let status = SecItemAdd(query as CFDictionary, nil)
        return status == errSecSuccess
    }

    /// Returns nil if no token has ever been stored or Keychain refused us.
    static func getToken() -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess,
              let data = item as? Data,
              let token = String(data: data, encoding: .utf8) else {
            return nil
        }
        return token
    }

    @discardableResult
    static func deleteToken() -> Bool {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        let status = SecItemDelete(query as CFDictionary)
        return status == errSecSuccess || status == errSecItemNotFound
    }
}
