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
            NavigationStack(path: $router.path) {
                EventListView()
                    .navigationDestination(for: AppRoute.self) { route in
                        switch route {
                        case let .event(id, reportID):
                            EventDetailView(eventID: id, highlightedReportID: reportID)
                        }
                    }
            }
            .tabItem {
                Label("地震情報", systemImage: "waveform.path.ecg")
            }
            .tag(AppTab.events)

            NavigationStack {
                SettingsView()
            }
            .tabItem {
                Label("設定", systemImage: "gearshape")
            }
            .tag(AppTab.settings)
        }
    }
}
