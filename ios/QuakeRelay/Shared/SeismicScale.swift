import Foundation
import SwiftUI

// Shared by the iPhone app, the Watch app and the Live Activity extension.
// The values come from the server's display strings; nothing here estimates a
// missing value. An unparseable value keeps its original text and a neutral colour.

/// JMA seismic intensity classes, ordered from 0 to 7.
enum SeismicIntensity: Int, CaseIterable, Comparable, Sendable {
    case zero = 0, one, two, three, four, fiveLower, fiveUpper, sixLower, sixUpper, seven

    static func < (lhs: SeismicIntensity, rhs: SeismicIntensity) -> Bool { lhs.rawValue < rhs.rawValue }

    var label: String {
        switch self {
        case .zero: "0"
        case .one: "1"
        case .two: "2"
        case .three: "3"
        case .four: "4"
        case .fiveLower: "5弱"
        case .fiveUpper: "5強"
        case .sixLower: "6弱"
        case .sixUpper: "6強"
        case .seven: "7"
        }
    }

    /// JMA seismic intensity palette (colour universal design version).
    var fill: Color {
        switch self {
        case .zero: Color(red: 0.86, green: 0.87, blue: 0.89)
        case .one: Color(red: 242 / 255, green: 242 / 255, blue: 1)
        case .two: Color(red: 0, green: 170 / 255, blue: 1)
        case .three: Color(red: 0, green: 65 / 255, blue: 1)
        case .four: Color(red: 250 / 255, green: 230 / 255, blue: 150 / 255)
        case .fiveLower: Color(red: 1, green: 230 / 255, blue: 0)
        case .fiveUpper: Color(red: 1, green: 153 / 255, blue: 0)
        case .sixLower: Color(red: 1, green: 40 / 255, blue: 0)
        case .sixUpper: Color(red: 165 / 255, green: 0, blue: 33 / 255)
        case .seven: Color(red: 180 / 255, green: 0, blue: 104 / 255)
        }
    }

    var foreground: Color {
        switch self {
        case .two, .three, .sixLower, .sixUpper, .seven: .white
        default: Color(red: 0.11, green: 0.11, blue: 0.13)
        }
    }

    static func token(_ value: String) -> SeismicIntensity? {
        switch value {
        case "0": .zero
        case "1": .one
        case "2": .two
        case "3": .three
        case "4": .four
        case "5弱", "5-": .fiveLower
        case "5強", "5+": .fiveUpper
        case "6弱", "6-": .sixLower
        case "6強", "6+": .sixUpper
        case "7": .seven
        default: nil
        }
    }
}

/// A seismic intensity as written in a bulletin: exact, a range, open-ended or unknown.
struct IntensityReading: Equatable, Sendable {
    let text: String
    let lower: SeismicIntensity?
    let upper: SeismicIntensity?
    let openEnded: Bool
    let notReceived: Bool

    init?(_ raw: String?) {
        guard let raw else { return nil }
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        text = trimmed
        var normalized = trimmed.folding(options: .widthInsensitive, locale: Locale(identifier: "ja_JP"))
        for word in ["予想", "観測", "最大", "震度"] {
            normalized = normalized.replacingOccurrences(of: word, with: "")
        }
        openEnded = normalized.contains("以上") || normalized.contains("over")
        notReceived = normalized.contains("未入電")
        for word in ["以上", "未入電", "over"] {
            normalized = normalized.replacingOccurrences(of: word, with: "")
        }
        let levels = normalized.components(separatedBy: CharacterSet(charactersIn: "〜～~/"))
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .compactMap(SeismicIntensity.token)
        lower = levels.min()
        upper = levels.max()
    }

    /// The level used for colour and sorting: the upper bound of a range.
    var dominant: SeismicIntensity? { upper ?? lower }
    var isKnown: Bool { dominant != nil }

    var mainText: String {
        if let lower, let upper, lower != upper { return lower.label + "〜" + upper.label }
        if let upper { return upper.label }
        return text.contains("不明") ? "不明" : text
    }

    var suffixText: String? {
        if notReceived { return "以上未入電" }
        if openEnded { return "以上" }
        return nil
    }

    /// Full reading for text use, e.g. "5弱以上".
    var displayText: String { mainText + (suffixText ?? "") }

    static func sortKey(_ reading: IntensityReading?) -> Int {
        guard let level = reading?.dominant else { return -1 }
        return level.rawValue * 2 + (reading?.openEnded == true ? 1 : 0)
    }
}

/// Long-period ground motion classes 0–4.
enum LongPeriodClass: Int, CaseIterable, Comparable, Sendable {
    case zero = 0, one, two, three, four

    static func < (lhs: LongPeriodClass, rhs: LongPeriodClass) -> Bool { lhs.rawValue < rhs.rawValue }

    init?(_ raw: String?) {
        guard let raw else { return nil }
        let digits = raw.folding(options: .widthInsensitive, locale: Locale(identifier: "ja_JP")).filter(\.isNumber)
        guard let value = Int(digits), let level = LongPeriodClass(rawValue: value) else { return nil }
        self = level
    }

    var fill: Color {
        switch self {
        case .zero: Color(red: 0.86, green: 0.87, blue: 0.89)
        case .one: Color(red: 1, green: 0.88, blue: 0.4)
        case .two: Color(red: 1, green: 0.6, blue: 0)
        case .three: Color(red: 1, green: 0.16, blue: 0)
        case .four: Color(red: 0.62, green: 0, blue: 0.47)
        }
    }

    var foreground: Color {
        switch self {
        case .three, .four: .white
        default: Color(red: 0.11, green: 0.11, blue: 0.13)
        }
    }
}

/// Display category of a tsunami forecast-zone kind name, e.g. "大津波警報".
enum TsunamiKindStyle: Int, CaseIterable, Comparable, Sendable {
    case other = 0, released, forecast, advisory, warning, major

    static func < (lhs: TsunamiKindStyle, rhs: TsunamiKindStyle) -> Bool { lhs.rawValue < rhs.rawValue }

    init(kind: String) {
        if kind.contains("解除") { self = .released }
        else if kind.contains("大津波警報") { self = .major }
        else if kind.contains("津波警報") { self = .warning }
        else if kind.contains("津波注意報") { self = .advisory }
        else if kind.contains("津波予報") { self = .forecast }
        else { self = .other }
    }

    var title: String {
        switch self {
        case .major: "大津波警報"
        case .warning: "津波警報"
        case .advisory: "津波注意報"
        case .forecast: "津波予報"
        case .released: "解除"
        case .other: "その他"
        }
    }

    var fill: Color {
        switch self {
        case .major: Color(red: 0.62, green: 0, blue: 0.86)
        case .warning: Color(red: 1, green: 0.16, blue: 0)
        case .advisory: Color(red: 1, green: 0.85, blue: 0)
        case .forecast: Color(red: 0.2, green: 0.6, blue: 0.95)
        case .released, .other: Color(red: 0.55, green: 0.57, blue: 0.6)
        }
    }

    var foreground: Color {
        switch self {
        case .advisory: Color(red: 0.11, green: 0.11, blue: 0.13)
        default: .white
        }
    }
}

/// Square badge showing a seismic intensity class with the JMA colour.
/// With `longPeriodText`, a strip below the square shows the long-period ground
/// motion class (e.g. "階級 3") in its own colour.
struct IntensityBadge: View {
    let reading: IntensityReading?
    var size: CGFloat = 44
    var caption: String? = nil
    var longPeriodText: String? = nil

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: size * 0.22, style: .continuous)
        VStack(spacing: 0) {
            intensityFace
            if let longPeriodText {
                Rectangle()
                    .fill(Color.black.opacity(0.18))
                    .frame(width: size, height: 0.5)
                longPeriodStrip(longPeriodText)
            }
        }
        .frame(width: size)
        .clipShape(shape)
        .overlay {
            shape.strokeBorder(Color.black.opacity(0.14), lineWidth: 0.5)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText)
    }

    private var intensityFace: some View {
        let level = reading?.dominant
        return VStack(spacing: 0) {
            if let caption {
                Text(caption)
                    .font(.system(size: max(8, size * 0.19), weight: .semibold))
                    .lineLimit(1)
                    .minimumScaleFactor(0.6)
            }
            Text(reading?.mainText ?? "−")
                .font(.system(size: mainFontSize, weight: .heavy, design: .rounded))
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.45)
            if let suffix = reading?.suffixText {
                Text(suffix)
                    .font(.system(size: max(7, size * 0.17), weight: .bold))
                    .lineLimit(1)
                    .minimumScaleFactor(0.5)
            }
        }
        .padding(.horizontal, 2)
        .foregroundStyle(level?.foreground ?? Color.primary)
        .frame(width: size, height: size)
        .background(level?.fill ?? Color.gray.opacity(0.22))
    }

    private func longPeriodStrip(_ text: String) -> some View {
        let level = LongPeriodClass(text)
        return HStack(alignment: .firstTextBaseline, spacing: max(1, size * 0.05)) {
            Text("階級")
                .font(.system(size: max(7, size * 0.19), weight: .semibold))
            Text(Self.longPeriodLabel(text))
                .font(.system(size: max(9, size * 0.3), weight: .heavy, design: .rounded))
                .monospacedDigit()
        }
        .lineLimit(1)
        .minimumScaleFactor(0.5)
        .padding(.horizontal, 2)
        .foregroundStyle(level?.foreground ?? Color.primary)
        .frame(width: size, height: max(12, size * 0.36))
        .background(level?.fill ?? Color.gray.opacity(0.22))
    }

    private var mainFontSize: CGFloat {
        let length = (reading?.mainText ?? "−").count
        let base = size * (caption == nil && reading?.suffixText == nil ? 0.5 : 0.4)
        return length > 2 ? base * 0.72 : base
    }

    private var accessibilityText: String {
        var text = caption.map { $0 + " " } ?? ""
        if let reading {
            text += "震度" + reading.displayText
        } else {
            text += "震度の記載なし"
        }
        if let longPeriodText {
            text += "、長周期地震動階級" + Self.longPeriodLabel(longPeriodText)
        }
        return text
    }

    /// "3" for a known class; otherwise the published text unchanged.
    private static func longPeriodLabel(_ text: String) -> String {
        guard let level = LongPeriodClass(text) else { return text }
        return String(level.rawValue)
    }
}

/// Small capsule showing a long-period ground motion class.
struct LongPeriodBadge: View {
    let level: LongPeriodClass?
    let text: String

    var body: some View {
        Text("長周期 階級" + (level.map { String($0.rawValue) } ?? text))
            .font(.caption2.weight(.bold))
            .monospacedDigit()
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .foregroundStyle(level?.foreground ?? Color.primary)
            .background(level?.fill ?? Color.gray.opacity(0.22), in: Capsule())
    }
}

/// Fixed 24-hour formats in the device time zone, independent of locale settings.
enum DisplayTime {
    private static func formatter(_ format: String) -> DateFormatter {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.timeZone = .current
        formatter.dateFormat = format
        return formatter
    }

    private static let fullFormatter = formatter("yyyy/MM/dd HH:mm:ss")
    private static let dayTimeFormatter = formatter("MM/dd HH:mm")
    private static let daySecondsFormatter = formatter("MM/dd HH:mm:ss")
    private static let timeFormatter = formatter("HH:mm:ss")
    private static let minuteFormatter = formatter("HH:mm")

    /// 2026/11/23 23:14:47
    static func full(_ date: Date) -> String { fullFormatter.string(from: date) }
    /// 11/23 23:14
    static func dayTime(_ date: Date) -> String { dayTimeFormatter.string(from: date) }
    /// 11/23 23:14:47
    static func daySeconds(_ date: Date) -> String { daySecondsFormatter.string(from: date) }
    /// 23:14:47
    static func time(_ date: Date) -> String { timeFormatter.string(from: date) }
    /// 23:14
    static func minute(_ date: Date) -> String { minuteFormatter.string(from: date) }

    /// "+1.2秒" style delay between two instants, or nil for implausible values.
    static func delay(from start: Date?, to end: Date?) -> String? {
        guard let start, let end else { return nil }
        let seconds = end.timeIntervalSince(start)
        guard seconds.isFinite, abs(seconds) < 86_400 * 30 else { return nil }
        let sign = seconds >= 0 ? "+" : "−"
        let value = abs(seconds)
        if value < 60 { return sign + String(format: "%.1f秒", value) }
        let whole = Int(value)
        if value < 3600 { return sign + "\(whole / 60)分" + pad(whole % 60) + "秒" }
        return sign + "\(whole / 3600)時間" + pad((whole % 3600) / 60) + "分"
    }

    private static func pad(_ value: Int) -> String { value < 10 ? "0\(value)" : "\(value)" }

    /// "3分前" style relative age for freshness labels.
    static func age(_ date: Date, now: Date) -> String {
        let seconds = max(0, now.timeIntervalSince(date))
        if seconds < 60 { return "\(Int(seconds))秒前" }
        if seconds < 3600 { return "\(Int(seconds / 60))分前" }
        if seconds < 86_400 { return "\(Int(seconds / 3600))時間前" }
        return "\(Int(seconds / 86_400))日前"
    }
}
