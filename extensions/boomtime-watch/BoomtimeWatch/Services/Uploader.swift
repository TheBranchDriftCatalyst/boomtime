//
//  Uploader.swift
//  BoomtimeWatch
//
//  Posts bulk payloads to Boomtime with jittered exponential backoff.
//  Only returns success on HTTP 2xx — that's the signal SyncCoordinator
//  uses to commit the corresponding HKQueryAnchor.
//

import Foundation

enum UploadError: Error, LocalizedError {
    case notConfigured
    case invalidURL
    case http(status: Int, body: String)
    case network(Error)
    case encoding(Error)

    var errorDescription: String? {
        switch self {
        case .notConfigured: return "Server URL or token not set"
        case .invalidURL: return "Server URL is invalid"
        case .http(let s, let b): return "HTTP \(s): \(b.prefix(200))"
        case .network(let e): return "Network: \(e.localizedDescription)"
        case .encoding(let e): return "Encode: \(e.localizedDescription)"
        }
    }
}

final class Uploader {
    private let config: Config
    private let session: URLSession

    /// Retry schedule per plan spec: 1s -> 32s cap, ~5 attempts, jittered.
    private let baseDelays: [TimeInterval] = [1, 2, 4, 8, 16]

    init(config: Config, session: URLSession = .shared) {
        self.config = config
        self.session = session
    }

    /// POST a workouts bulk payload.
    func uploadWorkouts(_ workouts: [WorkoutPayload]) async throws {
        guard !workouts.isEmpty else { return }
        try await post(
            path: "/api/v1/users/current/workouts.bulk",
            body: WorkoutBulkRequest(data: workouts)
        )
    }

    /// POST a health-samples bulk payload.
    func uploadHealthSamples(_ samples: [HealthSamplePayload]) async throws {
        guard !samples.isEmpty else { return }
        try await post(
            path: "/api/v1/users/current/health_samples.bulk",
            body: HealthSampleBulkRequest(data: samples)
        )
    }

    // MARK: internal

    private func post<T: Encodable>(path: String, body: T) async throws {
        guard let base = config.serverURL,
              let token = Keychain.getToken(), !token.isEmpty else {
            throw UploadError.notConfigured
        }
        guard let url = URL(string: path, relativeTo: base) else {
            throw UploadError.invalidURL
        }

        let bodyData: Data
        do {
            let enc = JSONEncoder()
            // Backend accepts unix seconds as float64; our structs already emit
            // that shape, but be explicit about non-conforming floats just in case.
            enc.nonConformingFloatEncodingStrategy = .convertToString(
                positiveInfinity: "inf", negativeInfinity: "-inf", nan: "nan"
            )
            bodyData = try enc.encode(body)
        } catch {
            throw UploadError.encoding(error)
        }

        var req = URLRequest(url: url.absoluteURL, timeoutInterval: 30)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue(basicAuthHeader(token: token), forHTTPHeaderField: "Authorization")
        req.httpBody = bodyData

        try await sendWithRetry(req)
    }

    private func basicAuthHeader(token: String) -> String {
        // Same convention the wakatime-cli extension uses: base64(token) with
        // no username, which the Go handler decodes as an opaque bearer.
        let b64 = Data(token.utf8).base64EncodedString()
        return "Basic \(b64)"
    }

    private func sendWithRetry(_ req: URLRequest) async throws {
        var lastError: Error?

        for (attempt, delay) in baseDelays.enumerated() {
            do {
                let (data, resp) = try await session.data(for: req)
                guard let http = resp as? HTTPURLResponse else {
                    throw UploadError.http(status: -1, body: "no HTTPURLResponse")
                }
                if (200..<300).contains(http.statusCode) {
                    return
                }
                // 4xx: don't retry — the payload is bad or auth failed. Surface immediately.
                if (400..<500).contains(http.statusCode) {
                    throw UploadError.http(
                        status: http.statusCode,
                        body: String(data: data, encoding: .utf8) ?? ""
                    )
                }
                // 5xx / everything else: retry.
                lastError = UploadError.http(
                    status: http.statusCode,
                    body: String(data: data, encoding: .utf8) ?? ""
                )
            } catch let e as UploadError {
                // Already-classified 4xx bubbles up as fatal.
                if case .http(let s, _) = e, (400..<500).contains(s) { throw e }
                lastError = e
            } catch {
                lastError = UploadError.network(error)
            }

            // Don't sleep after the last attempt.
            if attempt == baseDelays.count - 1 { break }

            let jitter = TimeInterval.random(in: 0...(delay * 0.25))
            let sleepFor = delay + jitter
            try? await Task.sleep(nanoseconds: UInt64(sleepFor * 1_000_000_000))
        }

        throw lastError ?? UploadError.network(NSError(domain: "Uploader", code: -1))
    }
}
