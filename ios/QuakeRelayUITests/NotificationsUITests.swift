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
        if let base = ProcessInfo.processInfo.environment["QUAKERELAY_UI_BASE_URL"] {
            app.launchArguments += ["-settings.serverBaseURL", base]
        }
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
        guard let code = ProcessInfo.processInfo.environment["QUAKERELAY_UI_PAIRING_CODE"] else {
            XCTFail("Missing loopback fixture pairing code"); return
        }
        // A notification launch does not carry XCUIApplication.launchArguments.
        // Relaunch with the loopback URL before pairing; never contact the example host.
        app.terminate()
        app.launch()
        let input = app.textFields["12345678"]
        XCTAssertTrue(input.waitForExistence(timeout: 10))
        input.tap()
        input.typeText(code)
        app.buttons["ペアリング"].tap()
        guard app.tabBars.buttons["津波"].waitForExistence(timeout: 15) else {
            XCTFail("Loopback pairing did not complete"); return
        }
        let ordinary = app.staticTexts["合成震源・通常情報"]
        XCTAssertTrue(ordinary.waitForExistence(timeout: 10))
        ordinary.tap()
        XCTAssertTrue(app.staticTexts["発表区分"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["第9報"].exists)
        try await action("capture/ordinary", at: control, body: XCUIScreen.main.screenshot().pngRepresentation)
        app.tabBars.buttons["津波"].tap()
        let tsunami = app.staticTexts["合成津波警報"].firstMatch
        XCTAssertTrue(tsunami.waitForExistence(timeout: 10))
        tsunami.tap()
        // LabeledContent exposes the displayed value with its label or accessibility value.
        let height = app.descendants(matching: .any).matching(
            NSPredicate(format: "label CONTAINS %@ OR value == %@", "巨大", "巨大")
        ).firstMatch
        if !height.waitForExistence(timeout: 3) { app.swipeUp() }
        XCTAssertTrue(height.waitForExistence(timeout: 5))
        try await action("capture/tsunami", at: control, body: XCUIScreen.main.screenshot().pngRepresentation)
        app.tabBars.buttons["関連情報"].tap()
        let advisory = app.staticTexts["合成南海トラフ情報"].firstMatch
        XCTAssertTrue(advisory.waitForExistence(timeout: 10))
        advisory.tap()
        let text = app.staticTexts["合成補足"].firstMatch
        if !text.waitForExistence(timeout: 3) { app.swipeUp() }
        XCTAssertTrue(text.waitForExistence(timeout: 5))
        try await action("capture/advisory", at: control, body: XCUIScreen.main.screenshot().pngRepresentation)
        app.tabBars.buttons["設定"].tap()
        let live = app.switches["liveActivityToggle"].firstMatch
        for _ in 0..<6 {
            if live.exists && live.isHittable { break }
            app.swipeUp()
        }
        XCTAssertTrue(live.exists, "Live Activity preference missing")
        try await action("capture/notification-settings", at: control, body: XCUIScreen.main.screenshot().pngRepresentation)
        let test = app.buttons["sendNotificationTest"]
        for _ in 0..<6 {
            if test.exists && test.isHittable { break }
            app.swipeUp()
        }
        XCTAssertTrue(app.textFields["地震の通知対象地域"].exists)
        XCTAssertTrue(app.textFields["津波の通知対象地域"].exists)
        XCTAssertTrue(test.exists, "Own-device notification test control missing")
        try await action("capture/notification-test", at: control, body: XCUIScreen.main.screenshot().pngRepresentation)
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
