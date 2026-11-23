import SwiftUI
import UniformTypeIdentifiers

// Reusable building blocks for lists and detail screens.

/// Label/value row. The label is a separate text element so it can be found as-is.
struct KeyValueRow: View {
    let label: String
    let value: String
    var monospaced = false
    var emphasized = false
    var valueColor: Color? = nil

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(label)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .frame(minWidth: 96, maxWidth: 128, alignment: .leading)
                .fixedSize(horizontal: false, vertical: true)
            Text(value)
                .font(valueFont)
                .foregroundStyle(valueColor ?? Color.primary)
                .frame(maxWidth: .infinity, alignment: .leading)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
        }
    }

    private var valueFont: Font {
        let base: Font = emphasized ? .body.weight(.semibold) : .subheadline
        return monospaced ? base.monospacedDigit() : base
    }
}

/// Compact numeric value with a caption, used in header cards.
struct MetricTile: View {
    let title: String
    let value: String
    var note: String? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title)
                .font(.caption2)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.title3.weight(.semibold))
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.6)
            if let note {
                Text(note)
                    .font(.caption2)
                    .foregroundStyle(.orange)
                    .lineLimit(1)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// Small capsule tag.
struct StatusBadge: View {
    let text: String
    let color: Color
    var filled = false

    var body: some View {
        Text(text)
            .font(.caption2.bold())
            .lineLimit(1)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .foregroundStyle(filled ? Color.white : color)
            .background(filled ? color : color.opacity(0.13), in: Capsule())
    }
}

/// Coloured tag for a tsunami category.
struct TsunamiKindTag: View {
    let style: TsunamiKindStyle
    var text: String? = nil

    var body: some View {
        Text(text ?? style.title)
            .font(.caption.bold())
            .lineLimit(1)
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .foregroundStyle(style.foreground)
            .background(style.fill, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
    }
}

/// Highlighted text block for notes that must not be overlooked.
struct NoticeBox: View {
    let text: String
    var title: String? = nil
    var systemImage = "info.circle"
    var color: Color = .orange

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: systemImage)
                .foregroundStyle(color)
                .font(.subheadline.weight(.semibold))
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                if let title {
                    Text(title).font(.caption.weight(.semibold)).foregroundStyle(color)
                }
                Text(text)
                    .font(.subheadline)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            }
        }
        .padding(.vertical, 2)
    }
}

/// Text block with a caption title, for bulletin texts.
struct TitledText: View {
    let title: String
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title)
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            Text(text)
                .font(.subheadline)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
        }
        .padding(.vertical, 2)
    }
}

/// A list of names that collapses after `limit` entries.
struct ExpandableNameList: View {
    let names: [String]
    var limit = 12
    var font: Font = .subheadline
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            let shown = expanded ? names : Array(names.prefix(limit))
            Text(shown.joined(separator: "　"))
                .font(font)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
            if names.count > limit {
                Button(expanded ? "折りたたむ" : "ほか\(names.count - limit)件を表示") {
                    expanded.toggle()
                }
                .font(.caption)
                .buttonStyle(.borderless)
            }
        }
    }
}

/// Intensity class with the names that observed (or are forecast to have) it.
struct IntensityGroupRow: View {
    let reading: IntensityReading?
    let names: [String]
    var badgeCaption: String? = nil

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            IntensityBadge(reading: reading, size: 38, caption: badgeCaption)
            VStack(alignment: .leading, spacing: 2) {
                Text("震度\(reading?.displayText ?? "記載なし") · \(names.count)件")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                ExpandableNameList(names: names)
            }
        }
        .padding(.vertical, 2)
    }
}

/// Coloured vertical bar on the leading edge of a row, as tall as the content.
struct LeadingBar: ViewModifier {
    let color: Color
    var width: CGFloat = 4

    func body(content: Content) -> some View {
        content
            .padding(.leading, width + 8)
            .overlay(alignment: .leading) {
                RoundedRectangle(cornerRadius: width / 2)
                    .fill(color)
                    .frame(width: width)
                    .accessibilityHidden(true)
            }
    }
}

extension View {
    func leadingBar(_ color: Color, width: CGFloat = 4) -> some View {
        modifier(LeadingBar(color: color, width: width))
    }
}

/// Wrapping horizontal layout for tags and area names.
struct FlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, lineHeight: CGFloat = 0, maxX: CGFloat = 0
        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x > 0 && x + size.width > width {
                x = 0
                y += lineHeight + spacing
                lineHeight = 0
            }
            x += size.width + spacing
            maxX = max(maxX, x - spacing)
            lineHeight = max(lineHeight, size.height)
        }
        return CGSize(width: min(maxX, width), height: y + lineHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, lineHeight: CGFloat = 0
        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x > bounds.minX && x + size.width > bounds.maxX {
                x = bounds.minX
                y += lineHeight + spacing
                lineHeight = 0
            }
            subview.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            lineHeight = max(lineHeight, size.height)
        }
    }
}

/// Telegram-type tags of the products received for one event.
struct ProductTags: View {
    let codes: [String]

    var body: some View {
        FlowLayout(spacing: 5) {
            ForEach(codes, id: \.self) { code in
                Text(TelegramCatalog.short(code) ?? code)
                    .font(.caption2.weight(.semibold))
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(Color.secondary.opacity(0.12), in: Capsule())
            }
        }
    }
}

enum ValueText {
    static func magnitude(_ value: HypocenterDTO?) -> String? {
        guard let magnitude = value?.magnitude else { return nil }
        return "M" + magnitude.formatted(.number.precision(.fractionLength(1)))
    }

    static func coordinate(_ value: HypocenterDTO?) -> String? {
        guard let latitude = value?.latitude, let longitude = value?.longitude else { return nil }
        let lat = (latitude >= 0 ? "北緯 " : "南緯 ") + abs(latitude).formatted(.number.precision(.fractionLength(1...4))) + "°"
        let lon = (longitude >= 0 ? "東経 " : "西経 ") + abs(longitude).formatted(.number.precision(.fractionLength(1...4))) + "°"
        return lat + "　" + lon
    }

    static func originTime(_ value: HypocenterDTO?) -> Date? {
        ServerDateParser.parse(value?.originTime)
    }
}

/// Raw telegram export for non-EEW reports. The VPS refuses EEW downloads.
struct SourceDocumentButton: View {
    let report: ReportEntity
    @EnvironmentObject private var repository: EventRepository
    @State private var file: SourceFile?
    @State private var exporting = false
    @State private var loading = false
    @State private var error: String?

    var body: some View {
        if let document = report.bulletin?.document {
            VStack(alignment: .leading, spacing: 4) {
                if !document.complete {
                    Text("この資料は分割データです。各受信片は発表履歴から保存できます。")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Button {
                    loading = true
                    Task {
                        defer { loading = false }
                        do {
                            file = SourceFile(data: try await repository.sourceDocument(reportID: report.id))
                            error = nil
                            exporting = true
                        } catch { self.error = error.localizedDescription }
                    }
                } label: {
                    if loading { ProgressView() } else { Label("原電文を保存", systemImage: "square.and.arrow.down") }
                }
                .disabled(loading)
                if let error { Text(error).font(.caption).foregroundStyle(.red) }
            }
            .fileExporter(isPresented: $exporting, document: file, contentType: .data,
                          defaultFilename: "\(report.id).\(document.fileExtension)") { result in
                if case let .failure(failure) = result { error = failure.localizedDescription }
            }
        }
    }
}

struct SourceFile: FileDocument {
    static var readableContentTypes: [UTType] { [.data] }
    var data: Data
    init(data: Data) { self.data = data }
    init(configuration: ReadConfiguration) throws { data = configuration.file.regularFileContents ?? Data() }
    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper { FileWrapper(regularFileWithContents: data) }
}
