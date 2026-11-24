import SwiftUI

struct RootView: View {
    @EnvironmentObject private var environment: AppEnvironment

    var body: some View {
        Group {
            if environment.isPaired {
                MainTabView()
            } else {
                NavigationStack {
                    PairingView()
                }
            }
        }
    }
}

private struct MainTabView: View {
    @EnvironmentObject private var router: DeepLinkRouter

    var body: some View {
        TabView(selection: $router.selectedTab) {
            NavigationStack(path: $router.overviewPath) {
                OverviewView()
                    .navigationDestination(for: AppRoute.self) { route in destination(route) }
            }
            .tabItem { Label("概況", systemImage: "gauge.with.dots.needle.33percent") }
            .tag(AppTab.overview)

            NavigationStack(path: $router.path) {
                EventListView()
                    .navigationDestination(for: AppRoute.self) { route in destination(route) }
            }
            .tabItem { Label("地震", systemImage: "waveform.path.ecg") }
            .tag(AppTab.events)

            NavigationStack(path: $router.tsunamiPath) {
                EventListView(category: "tsunami")
                    .navigationDestination(for: AppRoute.self) { route in destination(route) }
            }
            .tabItem { Label("津波", systemImage: "water.waves") }
            .tag(AppTab.tsunami)

            NavigationStack(path: $router.advisoryPath) {
                EventListView(category: "advisory")
                    .navigationDestination(for: AppRoute.self) { route in destination(route) }
            }
            .tabItem { Label("関連情報", systemImage: "doc.text") }
            .tag(AppTab.advisory)

            NavigationStack {
                SettingsView()
            }
            .tabItem { Label("設定", systemImage: "gearshape") }
            .tag(AppTab.settings)
        }
    }

    @ViewBuilder
    private func destination(_ route: AppRoute) -> some View {
        switch route {
        case let .event(id, reportID):
            EventDetailView(eventID: id, highlightedReportID: reportID)
        }
    }
}
