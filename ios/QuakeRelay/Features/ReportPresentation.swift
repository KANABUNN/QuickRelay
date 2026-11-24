import Foundation

// Pure presentation models. They only regroup what the VPS already parsed
// (Report fields, bulletin sections, affected areas); nothing is estimated.
// Every bulletin section is assigned to exactly one display group, and
// unrecognised sections are kept in `otherSections` so nothing is hidden.

// MARK: - Telegram names

enum TelegramCatalog {
    static func name(_ code: String?) -> String? {
        guard let code else { return nil }
        switch code {
        case "VXSE43": return "緊急地震速報（警報）"
        case "VXSE44": return "緊急地震速報（予報・旧形式）"
        case "VXSE45": return "緊急地震速報（地震動予報）"
        case "VXSE51": return "震度速報"
        case "VXSE52": return "震源に関する情報"
        case "VXSE53": return "震源・震度に関する情報"
        case "VXSE56": return "地震の活動状況等に関する情報"
        case "VXSE60": return "地震回数に関する情報"
        case "VXSE61": return "顕著な地震の震源要素更新のお知らせ"
        case "VXSE62": return "長周期地震動に関する観測情報"
        case "VTSE41": return "津波警報・注意報・予報"
        case "VTSE51": return "津波情報"
        case "VTSE52": return "沖合の津波観測に関する情報"
        case "VYSE50": return "南海トラフ地震臨時情報"
        case "VYSE51": return "南海トラフ地震関連解説情報"
        case "VYSE52": return "南海トラフ地震関連解説情報（定例）"
        case "VYSE60": return "北海道・三陸沖後発地震注意情報"
        case "VZSE40": return "地震・津波に関するお知らせ"
        case "WEPA60": return "国際津波関連情報（国内向け）"
        case "IXAC41": return "推計震度分布図作図用データ"
        default: return nil
        }
    }

    static func short(_ code: String?) -> String? {
        guard let code else { return nil }
        switch code {
        case "VXSE43": return "EEW警報"
        case "VXSE44": return "EEW予報（旧）"
        case "VXSE45": return "EEW予報"
        case "VXSE51": return "震度速報"
        case "VXSE52": return "震源"
        case "VXSE53": return "震源・震度"
        case "VXSE56": return "活動状況"
        case "VXSE60": return "地震回数"
        case "VXSE61": return "震源更新"
        case "VXSE62": return "長周期"
        case "VTSE41": return "津波警報等"
        case "VTSE51": return "津波情報"
        case "VTSE52": return "沖合観測"
        case "VYSE50": return "南海臨時"
        case "VYSE51": return "南海解説"
        case "VYSE52": return "南海定例"
        case "VYSE60": return "後発地震"
        case "VZSE40": return "お知らせ"
        case "WEPA60": return "国際津波"
        case "IXAC41": return "推計震度"
        default: return nil
        }
    }

    /// "VXSE53 震源・震度に関する情報" or the report title for older caches.
    static func label(for report: ReportEntity) -> String {
        guard let code = report.telegramType else { return report.title }
        return code + " " + (name(code) ?? report.title)
    }
}

extension String {
    func removingPrefix(_ prefix: String) -> String? {
        hasPrefix(prefix) ? String(dropFirst(prefix.count)) : nil
    }
}

// MARK: - Bulletin sections

enum BulletinTextRole: String, CaseIterable, Sendable {
    case infoKind = "情報の種類"
    case content = "発表内容"
    case appendix = "補足"
    case body = "本文"
    case nextAdvisory = "次の発表"
    case warningComment = "防災上の留意事項"
    case forecastComment = "地震・津波への留意事項"
    case relatedComment = "関連情報"
    case freeComment = "付記"
    case receipt = "受信資料"
    case rawText = "原文"
    case numericData = "数値データ"

    var isComment: Bool {
        switch self {
        case .warningComment, .forecastComment, .relatedComment, .freeComment: true
        default: false
        }
    }
}

enum BulletinSectionRole: Equatable, Sendable {
    case tsunamiForecast(area: String)
    case tsunamiObservation(station: String)
    case tsunamiEstimate(area: String)
    case intensity(path: [String])
    case longPeriodSummary
    case relatedEarthquake
    case earthquakeCount(kind: String)
    case text(BulletinTextRole)
    case other

    init(_ section: BulletinSectionDTO) {
        let title = section.title
        let hasRows = !(section.rows ?? []).isEmpty
        if let area = title.removingPrefix("津波予報区："), !area.isEmpty {
            self = .tsunamiForecast(area: area)
        } else if let station = title.removingPrefix("津波観測："), !station.isEmpty {
            self = .tsunamiObservation(station: station)
        } else if let area = title.removingPrefix("沿岸の推定："), !area.isEmpty {
            self = .tsunamiEstimate(area: area)
        } else if title.hasPrefix("観測 / ") && hasRows {
            let path = Array(title.components(separatedBy: " / ").dropFirst()).filter { !$0.isEmpty }
            self = path.isEmpty ? .other : .intensity(path: path)
        } else if title == "長周期地震動" && hasRows {
            self = .longPeriodSummary
        } else if title == "関連する地震" && hasRows {
            self = .relatedEarthquake
        } else if let kind = title.removingPrefix("地震回数：") {
            self = .earthquakeCount(kind: kind)
        } else if let role = BulletinTextRole(rawValue: title), !hasRows,
                  let text = section.text, !text.isEmpty {
            self = .text(role)
        } else {
            self = .other
        }
    }
}

struct BulletinTextItem: Identifiable, Equatable {
    let id: Int
    let role: BulletinTextRole
    let text: String
}

/// One bulletin split into display groups. Order inside each group follows the telegram.
struct BulletinLayout {
    var headline: String?
    var textItems: [BulletinTextItem] = []
    var intensitySections: [BulletinSectionDTO] = []
    var longPeriodRows: [BulletinRowDTO] = []
    var tsunamiForecastSections: [BulletinSectionDTO] = []
    var tsunamiObservationSections: [BulletinSectionDTO] = []
    var tsunamiEstimateSections: [BulletinSectionDTO] = []
    var relatedEarthquakes: [BulletinSectionDTO] = []
    var earthquakeCounts: [BulletinSectionDTO] = []
    var otherSections: [BulletinSectionDTO] = []
    var document: SourceDocumentDTO?
    /// Number of input sections assigned to a group; equals the input count.
    private(set) var assignedCount = 0

    init(bulletin: BulletinDTO?) {
        let headline = bulletin?.headline?.trimmingCharacters(in: .whitespacesAndNewlines)
        self.headline = headline?.isEmpty == false ? headline : nil
        document = bulletin?.document
        for (index, section) in (bulletin?.sections ?? []).enumerated() {
            assignedCount += 1
            switch BulletinSectionRole(section) {
            case .tsunamiForecast: tsunamiForecastSections.append(section)
            case .tsunamiObservation: tsunamiObservationSections.append(section)
            case .tsunamiEstimate: tsunamiEstimateSections.append(section)
            case .intensity: intensitySections.append(section)
            case .longPeriodSummary: longPeriodRows += section.rows ?? []
            case .relatedEarthquake: relatedEarthquakes.append(section)
            case .earthquakeCount: earthquakeCounts.append(section)
            case let .text(role): textItems.append(BulletinTextItem(id: index, role: role, text: section.text ?? ""))
            case .other: otherSections.append(section)
            }
        }
    }

    func text(_ role: BulletinTextRole) -> String? {
        let values = textItems.filter { $0.role == role }.map(\.text)
        return values.isEmpty ? nil : values.joined(separator: "\n")
    }

    var comments: [BulletinTextItem] { textItems.filter { $0.role.isComment } }
    var isEmpty: Bool { headline == nil && assignedCount == 0 && document == nil }
}

// MARK: - Observed intensities

struct IntensityNode: Identifiable {
    let id: String
    let name: String
    let path: [String]
    let intensity: IntensityReading?
    let longPeriodText: String?
    let svaText: String?
    let otherRows: [BulletinRowDTO]
    let children: [IntensityNode]

    var depth: Int { path.count }
    var longPeriod: LongPeriodClass? { LongPeriodClass(longPeriodText) }
    var parentPath: [String] { Array(path.dropLast()) }

    /// Pre-order descendants, excluding this node.
    var descendants: [IntensityNode] {
        children.flatMap { [$0] + $0.descendants }
    }
}

struct LongPeriodGroup: Identifiable {
    let id: String
    let level: LongPeriodClass?
    let nodes: [IntensityNode]
}

struct IntensityGroup: Identifiable {
    let id: String
    let reading: IntensityReading?
    let nodes: [IntensityNode]
    var names: [String] { nodes.map(\.name) }
}

/// Prefecture → region → municipality → station hierarchy from "観測 / …" sections.
struct IntensityObservationTree {
    let roots: [IntensityNode]

    init(sections: [BulletinSectionDTO]) {
        let root = Builder(path: [])
        for section in sections {
            guard case let .intensity(path) = BulletinSectionRole(section) else { continue }
            var node = root
            for name in path { node = node.child(name) }
            node.rows += section.rows ?? []
        }
        roots = root.children.map { $0.build() }
    }

    var isEmpty: Bool { roots.isEmpty }
    var allNodes: [IntensityNode] { roots.flatMap { [$0] + $0.descendants } }
    var maxDepth: Int { allNodes.map(\.depth).max() ?? 0 }
    var leaves: [IntensityNode] { allNodes.filter { $0.children.isEmpty } }
    var hasSeismicIntensity: Bool { allNodes.contains { $0.intensity != nil } }
    var hasLongPeriod: Bool { allNodes.contains { $0.longPeriodText != nil } }

    func nodes(atDepth depth: Int) -> [IntensityNode] { allNodes.filter { $0.depth == depth } }

    /// Regions (細分区域) when present, otherwise prefectures.
    var summaryDepth: Int { nodes(atDepth: 2).contains(where: { $0.intensity != nil }) ? 2 : 1 }

    /// Section anchors for the full list: two levels above the deepest level.
    var listSections: [IntensityNode] {
        let depth = max(1, maxDepth - 2)
        let anchors = allNodes.filter { $0.depth == depth }
        // Shallow branches without nodes at the anchor depth are kept as their own sections.
        let shallow = allNodes.filter { $0.depth < depth && $0.children.isEmpty }
        return anchors + shallow
    }

    /// Nodes at a depth grouped by seismic intensity, strongest first.
    func groups(atDepth depth: Int) -> [IntensityGroup] {
        group(nodes(atDepth: depth).filter { $0.intensity != nil })
    }

    /// Deepest entries (stations, or regions for 震度速報) grouped by intensity.
    var leafGroups: [IntensityGroup] { group(leaves.filter { $0.intensity != nil }) }

    func longPeriodGroups(atDepth depth: Int) -> [LongPeriodGroup] {
        let candidates = nodes(atDepth: depth).filter { $0.longPeriodText != nil }
        var order: [String] = []
        var grouped: [String: [IntensityNode]] = [:]
        for node in candidates {
            let key = node.longPeriodText ?? ""
            if grouped[key] == nil { order.append(key) }
            grouped[key, default: []].append(node)
        }
        let result: [LongPeriodGroup] = order.map { key in
            LongPeriodGroup(id: key, level: LongPeriodClass(key), nodes: grouped[key] ?? [])
        }
        return result.sorted { ($0.level?.rawValue ?? -1) > ($1.level?.rawValue ?? -1) }
    }

    private func group(_ nodes: [IntensityNode]) -> [IntensityGroup] {
        var order: [String] = []
        var grouped: [String: [IntensityNode]] = [:]
        for node in nodes {
            let key = node.intensity?.displayText ?? "記載なし"
            if grouped[key] == nil { order.append(key) }
            grouped[key, default: []].append(node)
        }
        return order.map { key in
            IntensityGroup(id: key, reading: grouped[key]?.first?.intensity, nodes: grouped[key] ?? [])
        }.sorted { IntensityReading.sortKey($0.reading) > IntensityReading.sortKey($1.reading) }
    }

    private final class Builder {
        let path: [String]
        var rows: [BulletinRowDTO] = []
        var children: [Builder] = []
        private var index: [String: Builder] = [:]

        init(path: [String]) { self.path = path }

        func child(_ name: String) -> Builder {
            if let existing = index[name] { return existing }
            let node = Builder(path: path + [name])
            children.append(node)
            index[name] = node
            return node
        }

        func build() -> IntensityNode {
            func value(_ labels: [String]) -> String? {
                for label in labels {
                    if let row = rows.first(where: { $0.label == label }) { return row.value }
                }
                return nil
            }
            let known: Set<String> = ["震度", "最大震度", "長周期地震動階級", "最大長周期地震動階級", "絶対速度応答"]
            return IntensityNode(id: path.joined(separator: " / "), name: path.last ?? "", path: path,
                                 intensity: IntensityReading(value(["震度", "最大震度"])),
                                 longPeriodText: value(["長周期地震動階級", "最大長周期地震動階級"]),
                                 svaText: value(["絶対速度応答"]),
                                 otherRows: rows.filter { !known.contains($0.label) },
                                 children: children.map { $0.build() })
        }
    }
}

// MARK: - Earthquake event (all telegram types of one earthquake)

enum EEWStream: Equatable {
    case forecast, warning

    init(_ report: ReportEntity) {
        if report.telegramType == "VXSE43" || report.classification == "eew.warning" || report.eventType == "eew_warning" {
            self = .warning
        } else {
            self = .forecast
        }
    }
}

enum EEWPresentation {
    /// EEW reports in display order; VXSE44 is hidden once VXSE45 exists for the event.
    static func reports(_ reports: [ReportEntity]) -> [ReportEntity] {
        ReportTimelineDisplay.visible(reports).filter(\.isEEW).sorted {
            let a = $0.occurredAt ?? $0.receivedAt, b = $1.occurredAt ?? $1.receivedAt
            if a != b { return a < b }
            return $0.serverSequence < $1.serverSequence
        }
    }

    /// Latest report of one stream by its own report number, cancellations included.
    static func latest(_ stream: EEWStream, in reports: [ReportEntity]) -> ReportEntity? {
        let candidates = Self.reports(reports).filter { EEWStream($0) == stream }
        let hasModern = candidates.contains { $0.telegramType == "VXSE45" }
        return candidates.filter { !hasModern || $0.telegramType != "VXSE44" }.max {
            let a = $0.revision ?? -1, b = $1.revision ?? -1
            if a != b { return a < b }
            return $0.serverSequence < $1.serverSequence
        }
    }

    /// Forecast areas with a known intensity, strongest first.
    static func forecastAreas(_ report: ReportEntity) -> [AffectedAreaDTO] {
        (report.affectedAreas ?? []).filter { IntensityReading($0.maxIntensity)?.isKnown == true }
            .enumerated()
            .sorted {
                let a = IntensityReading.sortKey(IntensityReading($0.element.maxIntensity))
                let b = IntensityReading.sortKey(IntensityReading($1.element.maxIntensity))
                return a != b ? a > b : $0.offset < $1.offset
            }
            .map { $0.element }
    }

    /// Area names without intensity (VXSE43 target areas, or prefecture containers).
    static func namedAreas(_ report: ReportEntity) -> [String] {
        (report.affectedAreas ?? []).filter { IntensityReading($0.maxIntensity)?.isKnown != true }.map(\.name)
    }
}

/// Consolidates the latest publication of each telegram type for one earthquake.
struct EarthquakeEventSummary {
    let latestByType: [String: ReportEntity]
    let hypocenterReport: ReportEntity?
    let observedReport: ReportEntity?
    let longPeriodReport: ReportEntity?
    let commentReport: ReportEntity?
    let forecastReport: ReportEntity?
    let warningReport: ReportEntity?

    init(reports: [ReportEntity]) {
        let latest = Self.latestPublications(reports)
        latestByType = latest
        forecastReport = EEWPresentation.latest(.forecast, in: reports)
        warningReport = EEWPresentation.latest(.warning, in: reports)

        func active(_ types: [String]) -> [ReportEntity] { Self.activeReports(types, in: latest) }
        let newest: ([ReportEntity]) -> ReportEntity? = { $0.max(by: ReportPublicationOrder.areInAscendingOrder) }

        let located = active(["VXSE61", "VXSE53", "VXSE52", "VXSE62"]).filter { report in
            guard let value = report.numericHypocenter else { return false }
            return value.epicenter != nil || value.latitude != nil || value.magnitude != nil
        }
        if let ordinary = newest(located) {
            hypocenterReport = ordinary
        } else {
            // Forecast values first: a warning never carries M/depth, and the two
            // streams' report numbers are not comparable.
            hypocenterReport = [forecastReport, warningReport].compactMap { $0 }
                .first { !$0.isCancelled && $0.numericHypocenter != nil }
        }
        observedReport = Self.selectObservedReport(in: latest)
        longPeriodReport = active(["VXSE62"]).first
        commentReport = newest(active(["VXSE53", "VXSE52", "VXSE51", "VXSE61", "VXSE62"]).filter {
            !BulletinLayout(bulletin: $0.bulletin).comments.isEmpty
        })
    }

    var hasEEW: Bool { forecastReport != nil || warningReport != nil }
    var hypocenterIsFromEEW: Bool { hypocenterReport?.isEEW == true }

    var observedMax: IntensityReading? {
        Self.observedReading(observedReport: observedReport, latest: latestByType)
    }

    var forecastMax: IntensityReading? {
        Self.forecastReading([warningReport, forecastReport])
    }

    /// 最大長周期地震動階級 of the latest 長周期地震動に関する観測情報, as published.
    var maxLongPeriodText: String? {
        Self.longPeriodClassText(longPeriodReport)
    }

    // Selection rules shared with EarthquakeIntensitySummary (list rows), so the
    // list and the detail header always show the same values.

    /// Latest publication of each ordinary (non-EEW) telegram type, cancellations included.
    static func latestPublications(_ reports: [ReportEntity]) -> [String: ReportEntity] {
        var latest: [String: ReportEntity] = [:]
        for report in reports where !report.isEEW {
            guard let type = report.telegramType else { continue }
            if let current = latest[type], !ReportPublicationOrder.areInAscendingOrder(current, report) { continue }
            latest[type] = report
        }
        return latest
    }

    static func activeReports(_ types: [String], in latest: [String: ReportEntity]) -> [ReportEntity] {
        types.compactMap { latest[$0] }.filter { !$0.isCancelled }
    }

    /// 震源・震度情報, else 震度速報. 震源要素更新 (VXSE61) carries no intensity.
    static func selectObservedReport(in latest: [String: ReportEntity]) -> ReportEntity? {
        let candidates = activeReports(["VXSE53", "VXSE51"], in: latest)
        return candidates.first { $0.maxIntensity != nil || !BulletinLayout(bulletin: $0.bulletin).intensitySections.isEmpty }
            ?? candidates.first
    }

    static func observedReading(observedReport: ReportEntity?, latest: [String: ReportEntity]) -> IntensityReading? {
        IntensityReading(observedReport?.maxIntensity ?? activeReports(["VXSE62"], in: latest).first?.maxIntensity)
    }

    static func forecastReading(_ reports: [ReportEntity?]) -> IntensityReading? {
        let live = reports.compactMap { $0 }.filter { !$0.isCancelled }
        return live.compactMap { IntensityReading($0.maxIntensity) }.max { IntensityReading.sortKey($0) < IntensityReading.sortKey($1) }
    }

    static func longPeriodClassText(_ report: ReportEntity?) -> String? {
        guard let report, !report.isCancelled else { return nil }
        let value = BulletinLayout(bulletin: report.bulletin).longPeriodRows
            .first { $0.label == "最大長周期地震動階級" }?.value
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return value?.isEmpty == false ? value : nil
    }

    /// Telegram types whose latest publication is a cancellation.
    var cancelledTypes: [String] {
        latestByType.filter { $0.value.isCancelled }.map(\.key).sorted()
    }
}

/// Strongest values of one earthquake for compact displays: list rows, statistics
/// and the intensity filter. The event row only mirrors the latest publication, and
/// a later 震源要素更新 (VXSE61) or 長周期地震動に関する観測情報 (VXSE62) replaces it,
/// so the values are taken from all stored reports with the detail header's rules.
/// Hypocentres and comments are not decoded here, to keep list rows light.
struct EarthquakeIntensitySummary: Equatable {
    /// Observed maximum: 震源・震度情報, else 震度速報, else 長周期地震動に関する観測情報.
    var observed: IntensityReading?
    /// Forecast maximum of the live EEW streams.
    var forecast: IntensityReading?
    /// 最大長周期地震動階級 (observed), as published.
    var longPeriodText: String?

    init(observed: IntensityReading?, forecast: IntensityReading?, longPeriodText: String?) {
        self.observed = observed
        self.forecast = forecast
        self.longPeriodText = longPeriodText
    }

    init(reports: [ReportEntity]) {
        let latest = EarthquakeEventSummary.latestPublications(reports)
        observed = EarthquakeEventSummary.observedReading(
            observedReport: EarthquakeEventSummary.selectObservedReport(in: latest), latest: latest)
        forecast = EarthquakeEventSummary.forecastReading([
            EEWPresentation.latest(.warning, in: reports), EEWPresentation.latest(.forecast, in: reports),
        ])
        longPeriodText = EarthquakeEventSummary.longPeriodClassText(latest["VXSE62"])
    }

    /// Uses the event's own value only when none of its reports are stored locally.
    init(event: EventEntity, reports: [ReportEntity]) {
        guard reports.isEmpty else {
            self.init(reports: reports)
            return
        }
        let reading = event.isCancelled ? nil : IntensityReading(event.maxIntensity)
        self.init(observed: event.isEEW ? nil : reading, forecast: event.isEEW ? reading : nil, longPeriodText: nil)
    }

    /// Observed values take precedence over EEW forecasts.
    var display: IntensityReading? { observed ?? forecast }
    var isForecast: Bool { observed == nil && forecast != nil }
}

// MARK: - Tsunami forecasts

struct TsunamiStationForecast: Identifiable, Equatable {
    let name: String
    var arrival: String?
    var highTide: String?
    var id: String { name }
}

struct TsunamiForecastEntry: Identifiable {
    let name: String
    let kind: String
    let previousKind: String?
    let firstWave: String?
    let maxHeight: String?
    let stations: [TsunamiStationForecast]
    let otherRows: [BulletinRowDTO]
    var id: String { name }

    /// A forecast that replaced a warning or advisory is shown with the releases.
    var switchedFromWarning: Bool {
        kind.contains("津波予報") && previousKind.map { TsunamiKindStyle(kind: $0) >= .advisory } == true
    }

    var style: TsunamiKindStyle {
        switchedFromWarning ? .released : TsunamiKindStyle(kind: kind)
    }

    init?(section: BulletinSectionDTO) {
        guard case let .tsunamiForecast(area) = BulletinSectionRole(section) else { return nil }
        let rows = section.rows ?? []
        name = area
        kind = rows.first { $0.label == "発表" }?.value ?? "記載なし"
        previousKind = rows.first { $0.label == "前回" }?.value
        firstWave = rows.first { $0.label == "第1波の到達予想" }?.value
        maxHeight = rows.first { $0.label == "予想される高さ" }?.value
        var stations: [TsunamiStationForecast] = []
        var others: [BulletinRowDTO] = []
        func station(_ name: String) -> Int {
            if let index = stations.firstIndex(where: { $0.name == name }) { return index }
            stations.append(TsunamiStationForecast(name: name))
            return stations.count - 1
        }
        for row in rows {
            switch row.label {
            case "発表", "前回", "第1波の到達予想", "予想される高さ": continue
            default: break
            }
            if let name = row.label.hasSuffix(" 到達予想") ? String(row.label.dropLast(" 到達予想".count)) : nil {
                let index = station(name)
                stations[index].arrival = row.value
            } else if let name = row.label.hasSuffix(" 満潮") ? String(row.label.dropLast(" 満潮".count)) : nil {
                let index = station(name)
                stations[index].highTide = row.value
            } else {
                others.append(row)
            }
        }
        self.stations = stations
        otherRows = others
    }
}

struct TsunamiForecastGroup: Identifiable {
    let style: TsunamiKindStyle
    let entries: [TsunamiForecastEntry]
    var id: Int { style.rawValue }

    var title: String {
        style == .released ? "解除（津波予報への切替を含む）" : style.title
    }

    static func groups(_ report: ReportEntity?) -> [TsunamiForecastGroup] {
        let entries = (report?.bulletin?.sections ?? []).compactMap(TsunamiForecastEntry.init(section:))
        return TsunamiKindStyle.allCases.reversed().compactMap { style in
            let matching = entries.filter { $0.style == style }
            return matching.isEmpty ? nil : TsunamiForecastGroup(style: style, entries: matching)
        }
    }
}

// MARK: - Report metadata

struct ReportMetadataRow: Identifiable {
    let label: String
    let value: String
    var monospaced = false
    var id: String { label }
}

enum ReportMetadata {
    static func rows(for report: ReportEntity) -> [ReportMetadataRow] {
        var rows: [ReportMetadataRow] = []
        func add(_ label: String, _ value: String?, monospaced: Bool = false) {
            guard let value, !value.isEmpty else { return }
            rows.append(ReportMetadataRow(label: label, value: value, monospaced: monospaced))
        }
        add("電文種別", report.telegramType.map { code in code + (TelegramCatalog.name(code).map { " " + $0 } ?? "") })
        add("表題", report.title)
        add("配信区分", report.classification, monospaced: true)
        add("発表区分（InfoType）", report.infoType)
        if report.isEEW {
            add("報数", report.revision.map { "第\($0)報" })
        } else {
            add("通番（Serial）", report.revision.map(String.init), monospaced: true)
        }
        add("状態", [report.isFinal ? "最終報" : nil, report.isCancelled ? "取消" : nil,
                   report.isWarning == true ? "警報" : nil].compactMap { $0 }.joined(separator: "・"))
        add("発表時刻（報告時刻）", report.occurredAt.map(DisplayTime.full), monospaced: true)
        add("配信時刻", report.pressTime.map(DisplayTime.full), monospaced: true)
        add("受信時刻", DisplayTime.full(report.receivedAt), monospaced: true)
        add("受信までの時間", DisplayTime.delay(from: report.pressTime ?? report.occurredAt, to: report.receivedAt),
            monospaced: true)
        add("有効期限", report.validUntil.map(DisplayTime.full), monospaced: true)
        add("server_sequence", String(report.serverSequence), monospaced: true)
        add("イベントID", report.eventId, monospaced: true)
        add("報告ID", report.id, monospaced: true)
        add("メッセージID", report.messageId, monospaced: true)
        if let document = report.bulletin?.document {
            add("原電文の形式", document.format.uppercased())
            add("原電文のサイズ", ByteCountFormatter.string(fromByteCount: Int64(document.byteCount), countStyle: .file)
                + "（\(document.byteCount) bytes）")
            add("指示コード", document.designation, monospaced: true)
            add("分割番号", document.part.map { "受信片 \($0 + 1)" })
            add("完全性", document.complete ? "完全な資料" : "分割・不完全な資料")
            add("SHA-256", document.sha256, monospaced: true)
        }
        return rows
    }
}
