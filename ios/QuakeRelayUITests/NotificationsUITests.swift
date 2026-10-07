import Foundation
import XCTest

@MainActor
final class NotificationsUITests: XCTestCase {
    func testSimulatedNotificationsInThreeAppStates() async throws {
        guard let raw = ProcessInfo.processInfo.environment["QUAKERELAY_ACCEPTANCE_CONTROL_URL"],
              let control = URL(string: raw), control.scheme == "http",
              control.host == "127.0.0.1", control.port != nil else {
            throw XCTSkip("Run the dedicated no-device acceptance workflow")
        }
        continueAfterFailure = false
        let app = XCUIApplication()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        app.launchArguments += ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        app.launch()
        let allow = springboard.alerts.buttons["Allow"]
        if allow.waitForExistence(timeout: 10) { allow.tap() }
        XCTAssertTrue(app.buttons["ペアリング"].waitForExistence(timeout: 10))
        try await action("capture/launched", at: control,
                         body: XCUIScreen.main.screenshot().pngRepresentation)

        for state in ["foreground", "background", "terminated"] {
            switch state {
            case "foreground":
                app.activate()
                XCTAssertTrue(app.wait(for: .runningForeground, timeout: 5))
            case "background":
                XCUIDevice.shared.press(.home)
                XCTAssertTrue(app.wait(for: .runningBackground, timeout: 5))
            default:
                app.terminate()
                XCTAssertEqual(app.state, .notRunning)
            }
            try await action("push/" + state, at: control)
            let title = "W8 " + state
            let notification = springboard.staticTexts.matching(
                NSPredicate(format: "label CONTAINS %@", title)
            ).firstMatch
            let displayed = notification.waitForExistence(timeout: 15)
            XCTAssertTrue(displayed, "Missing simulated notification: " + state)
            // Capture in XCTest, then tap before PNG encoding/network/file I/O.
            // Spawning simctl for capture can outlast a temporary notification banner.
            let screenshot = XCUIScreen.main.screenshot()
            notification.tap()
            XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10),
                          "Notification tap did not foreground the app: " + state)
            try await action("capture/" + state, at: control, body: screenshot.pngRepresentation)
        }
    }

    private func action(_ path: String, at base: URL, body: Data? = nil) async throws {
        var request = URLRequest(url: base.appendingPathComponent(path))
        request.httpMethod = "POST"
        request.timeoutInterval = 30
        request.httpBody = body
        if body != nil { request.setValue("image/png", forHTTPHeaderField: "Content-Type") }
        let (_, response) = try await URLSession.shared.data(for: request)
        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 204)
    }
}
