import SwiftUI

@main
struct QuakeRelayApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @Environment(\.scenePhase) private var scenePhase
    @StateObject private var environment = AppEnvironment()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(environment)
                .environmentObject(environment.settings)
                .environmentObject(environment.repository)
                .environmentObject(environment.router)
                .environmentObject(environment.notifications)
                .environmentObject(environment.deviceRegistration)
                .environmentObject(environment.liveActivities)
                .task {
                    // Bind before start() requests APNs registration, so its
                    // callback cannot race the delegate/environment wiring.
                    appDelegate.bind(environment: environment)
                    await environment.start()
                }
                .task(id: scenePhase) {
                    if scenePhase == .active { await environment.monitorReceiverStatus() }
                }
                .onOpenURL { url in
                    environment.handle(url: url)
                }
                .onChange(of: scenePhase) { _, phase in
                    guard phase == .active else { return }
                    Task { await environment.becameActive() }
                }
        }
        .modelContainer(environment.modelContainer)
    }
}
