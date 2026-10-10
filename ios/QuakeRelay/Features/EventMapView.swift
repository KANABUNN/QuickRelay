import MapKit
import SwiftUI

struct EventMapView: View {
    let report: ReportEntity
    @EnvironmentObject private var repository: EventRepository
    @Environment(\.dismiss) private var dismiss
    @State private var camera: MapCameraPosition = .automatic
    @State private var catalog: StationCatalogResponse?
    @State private var loading = false
    @State private var error: String?
    private let coasts = CoastCatalog.load()

    private var observations: IntensityMapResult {
        guard let catalog else { return IntensityMapResult(unmapped: MapDataBuilder.observationSections(report).count) }
        return MapDataBuilder.observations(report, catalog: catalog)
    }
    private var areas: [CoastMapArea] { coasts.map { MapDataBuilder.coasts(report, catalog: $0) } ?? [] }
    private var epicenter: CLLocationCoordinate2D? {
        guard !report.isCancelled, let value = report.numericHypocenter, let lat = value.latitude, let lon = value.longitude,
              MapDataBuilder.valid(latitude: lat, longitude: lon) else { return nil }
        return CLLocationCoordinate2D(latitude: lat, longitude: lon)
    }
    private var hasPositions: Bool { epicenter != nil || !observations.points.isEmpty || !areas.isEmpty }
    private var initialPosition: MapCameraPosition {
        if observations.points.isEmpty && areas.isEmpty, let epicenter {
            return .region(MKCoordinateRegion(center: epicenter, span: MKCoordinateSpan(latitudeDelta: 3, longitudeDelta: 3.5)))
        }
        return hasPositions ? .automatic : .region(MKCoordinateRegion(center: CLLocationCoordinate2D(latitude: 36, longitude: 138),
                                                                      span: MKCoordinateSpan(latitudeDelta: 22, longitudeDelta: 25)))
    }
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    Text(report.title).font(.headline)
                    Text("この発表の地図 · \((report.occurredAt ?? report.receivedAt).formatted(date: .abbreviated, time: .standard))")
                        .font(.caption).foregroundStyle(.secondary)
                    if report.isCancelled {
                        Label("取り消された発表のため、位置情報を描画しません。津波の解除とは異なります。", systemImage: "xmark.octagon")
                            .foregroundStyle(.orange)
                    }
                    Map(position: $camera) {
                        if let epicenter {
                            Annotation(report.numericHypocenter?.isAssumed == true ? "仮定震源" : "震源", coordinate: epicenter) {
                                Image(systemName: "star.fill").foregroundStyle(.red)
                                    .padding(6).background(.regularMaterial, in: Circle())
                            }
                        }
                        ForEach(observations.points) { point in
                            Annotation(point.name, coordinate: point.coordinate) {
                                Text(point.intensity).font(.caption.bold()).padding(5)
                                    .foregroundStyle(.white).background(intensityColor(point.intensity), in: RoundedRectangle(cornerRadius: 5))
                                    .accessibilityLabel("\(point.name) 観測震度\(point.intensity)")
                            }
                        }
                        ForEach(areas) { area in
                            ForEach(Array(area.region.coordinates.enumerated()), id: \.offset) { _, coordinates in
                                MapPolyline(coordinates: coordinates).stroke(coastColor(area.kind), lineWidth: 5)
                            }
                        }
                    }
                    .mapStyle(.standard(elevation: .flat))
                    .frame(height: 360).clipShape(RoundedRectangle(cornerRadius: 12))
                    .accessibilityIdentifier("eventMap")
                    if loading { ProgressView("観測地点の位置情報を取得中") }
                    if let error { Text(error).font(.caption).foregroundStyle(.orange) }
                    if let note = report.numericHypocenter?.qualification { Text(note).font(.subheadline).foregroundStyle(.orange) }
                    if let intensity = report.maxIntensity {
                        Text("\(report.isEEW ? "予想最大震度" : "観測最大震度")：\(intensity)").font(.subheadline.bold())
                    }
                    Text("★は震源の位置です。震度の数字は観測値です。緊急地震速報の予想震度を観測地点の値として配置しません。")
                        .font(.caption).foregroundStyle(.secondary)
                    if observations.unmapped > 0 {
                        Text("位置を特定できない観測地点：\(observations.unmapped)件。各地の震度一覧で確認できます。")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    if !hasPositions && !loading { Text("この発表には地図に表示できる位置情報がありません。") }
                    if !areas.isEmpty {
                        Text("津波の対象沿岸").font(.headline)
                        ForEach(areas) { area in
                            HStack(alignment: .top) {
                                Circle().fill(coastColor(area.kind)).frame(width: 10, height: 10).padding(.top, 5)
                                VStack(alignment: .leading) {
                                    Text(area.region.name).font(.subheadline.bold())
                                    Text(area.kind + (area.height.map { " · 予想高さ " + $0 } ?? "")).font(.caption)
                                }
                            }
                        }
                        Text("沿岸の線は発表対象の津波予報区です。観測・推定の対象沿岸と警報・注意報を区別します。解除後も海面変動などに注意してください。")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    if let catalog {
                        Text("観測地点：DMDATA \(catalog.version) · 取得 \(ServerDateParser.parse(catalog.fetchedAt)?.formatted(date: .abbreviated, time: .shortened) ?? catalog.fetchedAt)\(catalog.stale ? "（前回取得分）" : "")")
                            .font(.caption2).foregroundStyle(.secondary)
                        Text("地点の位置は取得したパラメータに基づきます。古い発表では、当時の名称・位置を特定できない場合があります。")
                            .font(.caption2).foregroundStyle(.secondary)
                    }
                    if let coasts { Text(coasts.source).font(.caption2).foregroundStyle(.secondary) }
                }.padding()
            }
            .navigationTitle("発表の地図").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) {
                Button("閉じる") { dismiss() }.accessibilityIdentifier("closeEventMap")
            } }
            .task(id: report.id) {
                camera = initialPosition
                error = nil
                guard !MapDataBuilder.observationSections(report).isEmpty else { return }
                loading = true
                defer { loading = false }
                do { catalog = try await repository.stationCatalog(); camera = initialPosition }
                catch { self.error = "観測地点の位置情報を取得できません。震源・沿岸と発表内容は引き続き確認できます。" }
            }
        }
    }
    private func intensityColor(_ value: String) -> Color {
        switch IntensityValue.rank(value) ?? 0 {
        case 7...: .red
        case 5...6: .orange
        case 4: .brown
        default: .blue
        }
    }
    private func coastColor(_ kind: String) -> Color {
        if kind == "大津波警報" { return .purple }
        if kind == "津波警報" { return .red }
        if kind == "津波注意報" { return .orange }
        if kind.contains("解除") { return .gray }
        return .blue
    }
}
