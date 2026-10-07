import UserNotifications
import XCTest
@testable import QuakeRelay

final class NotificationPermissionTests: XCTestCase {
    func testMapsAuthorizedTimeSensitivePermission() {
        let snapshot = NotificationPermissionSnapshot.map(
            authorizationStatus: .authorized,
            timeSensitiveSetting: .enabled
        )
        XCTAssertEqual(snapshot.authorization, .authorized)
        XCTAssertEqual(snapshot.timeSensitive, .enabled)
    }

    func testMapsDeniedPermissionWithoutClaimingTimeSensitiveAccess() {
        let snapshot = NotificationPermissionSnapshot.map(
            authorizationStatus: .denied,
            timeSensitiveSetting: .disabled
        )
        XCTAssertEqual(snapshot.authorization, .denied)
        XCTAssertEqual(snapshot.timeSensitive, .disabled)
    }
}
