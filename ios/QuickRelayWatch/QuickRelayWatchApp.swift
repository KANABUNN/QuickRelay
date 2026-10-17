import SwiftUI

@main
struct QuickRelayWatchApp: App {
    @StateObject private var snapshots = WatchSnapshotStore()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            WatchHomeView()
                .environmentObject(snapshots)
                .task { snapshots.activate() }
                .onChange(of: scenePhase) { _, phase in
                    if phase == .active { snapshots.requestLatest() }
                }
        }
    }
}
