import Foundation
import CoreLocation

struct MapStationDTO: Codable, Equatable, Sendable {
    let code: String
    let name: String
    let regionName: String
    let cityName: String
    let latitude: Double
    let longitude: Double
    let status: String
}
struct StationCatalogResponse: Decodable, Sendable {
    let ok: Bool
    let version: String
    let changeTime: String
    let fetchedAt: String
    let stale: Bool
    let items: [MapStationDTO]
}
struct CoastCatalog: Decodable {
    let source: String
    let regions: [CoastRegion]
    static func load() -> CoastCatalog? {
        guard let url = Bundle.main.url(forResource: "MapRegions", withExtension: "json"),
              let data = try? Data(contentsOf: url) else { return nil }
        return try? JSONDecoder().decode(CoastCatalog.self, from: data)
    }
}
struct CoastRegion: Decodable, Identifiable {
    var id: String { code }
    let code: String
    let name: String
    let lines: [[[Double]]]
    var coordinates: [[CLLocationCoordinate2D]] {
        lines.map { line in
            line.compactMap { point in
                guard point.count == 2, MapDataBuilder.valid(latitude: point[1], longitude: point[0]) else { return nil }
                return CLLocationCoordinate2D(latitude: point[1], longitude: point[0])
            }
        }.filter { $0.count > 1 }
    }
}
struct IntensityMapPoint: Identifiable {
    let id: String
    let name: String
    let intensity: String
    let coordinate: CLLocationCoordinate2D
}
struct IntensityMapResult {
    var points: [IntensityMapPoint] = []
    var unmapped = 0
}
struct CoastMapArea: Identifiable {
    var id: String { region.code }
    let region: CoastRegion
    let kind: String
    let height: String?
}
enum MapDataBuilder {
    static func valid(latitude: Double, longitude: Double) -> Bool {
        latitude.isFinite && longitude.isFinite && (-90...90).contains(latitude) && (-180...180).contains(longitude)
    }
    static func nameKey(_ name: String) -> String {
        name.folding(options: [.widthInsensitive, .caseInsensitive], locale: Locale(identifier: "ja_JP"))
            .replacingOccurrences(of: "＊", with: "").replacingOccurrences(of: "*", with: "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }
    static func observationSections(_ report: ReportEntity) -> [BulletinSectionDTO] {
        guard !report.isCancelled, !report.isEEW else { return [] }
        return (report.bulletin?.sections ?? []).filter {
            $0.title.hasPrefix("観測") && $0.rows?.contains(where: { $0.label == "震度" }) == true
        }
    }
    static func observations(_ report: ReportEntity, catalog: StationCatalogResponse) -> IntensityMapResult {
        let change = ServerDateParser.parse(catalog.changeTime)
        let beforeChange = change.map { (report.occurredAt ?? report.receivedAt) < $0 } ?? false
        // A changed station can share its code with the old 'current' record.
        // Select the applicable version before matching names or locations.
        let eligible = catalog.items.filter {
            valid(latitude: $0.latitude, longitude: $0.longitude) &&
            (beforeChange ? ["現", "廃止"].contains($0.status) : ["現", "変更", "新規"].contains($0.status))
        }
        let stations = Dictionary(grouping: eligible, by: \.code).values.flatMap { group in
            if !beforeChange, group.contains(where: { $0.status == "変更" }) { return group.filter { $0.status == "変更" } }
            return group
        }
        let byName = Dictionary(grouping: stations, by: { nameKey($0.name) })
        var result = IntensityMapResult()
        for section in observationSections(report) {
            // Bulletin hierarchy is '観測 / prefecture / region / city / station'.
            let path = section.title.components(separatedBy: " / ").map(nameKey)
            guard let name = path.last, let intensity = section.rows?.first(where: { $0.label == "震度" })?.value else { continue }
            let candidates = (byName[name] ?? []).filter { station in
                path.count <= 2 || (path.contains(nameKey(station.cityName)) && path.contains(nameKey(station.regionName)))
            }
            // Ambiguous or missing positions stay in the observation list.
            guard candidates.count == 1, let station = candidates.first else { result.unmapped += 1; continue }
            result.points.append(IntensityMapPoint(id: section.title, name: station.name, intensity: intensity,
                coordinate: CLLocationCoordinate2D(latitude: station.latitude, longitude: station.longitude)))
        }
        return result
    }
    static func coasts(_ report: ReportEntity, catalog: CoastCatalog) -> [CoastMapArea] {
        guard !report.isCancelled else { return [] }
        var result: [CoastMapArea] = []
        for area in TsunamiPublication(report: report).areas {
            guard let region = catalog.regions.first(where: { nameKey($0.name) == nameKey(area.name) }) else { continue }
            result.append(CoastMapArea(region: region, kind: area.kind, height: area.height))
        }
        // Observation/estimation products identify a coast, not a gauge position.
        // Draw the coast and label the basis instead of inventing a station marker.
        for section in report.bulletin?.sections ?? [] {
            let name: String?
            let kind: String
            if section.title.hasPrefix("津波観測：") {
                name = section.rows?.first(where: { $0.label == "地域" })?.value; kind = "観測情報の対象沿岸"
            } else if section.title.hasPrefix("沿岸の推定：") {
                name = String(section.title.dropFirst("沿岸の推定：".count)); kind = "推定情報の対象沿岸"
            } else { continue }
            guard let name, let region = catalog.regions.first(where: { nameKey($0.name) == nameKey(name) }),
                  !result.contains(where: { $0.id == region.code }) else { continue }
            result.append(CoastMapArea(region: region, kind: kind, height: nil))
        }
        return result
    }
}
