import Darwin
import Foundation

// Generates short, non-critical PCM CAF tones in the built app bundle. Keeping
// the generator in source control avoids committing opaque binary assets.
struct CAFWriter {
    static func makeTone(frequency: Double, duration: Double, amplitude: Double = 0.22) -> Data {
        let sampleRate = 44_100.0
        let frameCount = Int(sampleRate * duration)
        var samples = Data(capacity: frameCount * 2)

        for frame in 0..<frameCount {
            let time = Double(frame) / sampleRate
            let fadeFrames = min(1_200, frameCount / 4)
            let fadeIn = min(1.0, Double(frame) / Double(max(1, fadeFrames)))
            let fadeOut = min(1.0, Double(frameCount - frame - 1) / Double(max(1, fadeFrames)))
            let envelope = min(fadeIn, fadeOut)
            let value = sin(2.0 * .pi * frequency * time) * amplitude * envelope
            var pcm = Int16((value * Double(Int16.max)).rounded()).littleEndian
            withUnsafeBytes(of: &pcm) { samples.append(contentsOf: $0) }
        }

        var output = Data()
        output.append(contentsOf: [0x63, 0x61, 0x66, 0x66]) // caff
        append(UInt16(1), to: &output)
        append(UInt16(0), to: &output)

        output.append(contentsOf: [0x64, 0x65, 0x73, 0x63]) // desc
        append(UInt64(32), to: &output)
        append(sampleRate.bitPattern, to: &output)
        output.append(contentsOf: [0x6c, 0x70, 0x63, 0x6d]) // lpcm
        append(UInt32(12), to: &output) // signed integer, packed, little endian
        append(UInt32(2), to: &output) // bytes per packet
        append(UInt32(1), to: &output) // frames per packet
        append(UInt32(1), to: &output) // channels
        append(UInt32(16), to: &output) // bits per channel

        output.append(contentsOf: [0x64, 0x61, 0x74, 0x61]) // data
        append(UInt64(samples.count + 4), to: &output)
        append(UInt32(0), to: &output) // edit count
        output.append(samples)
        return output
    }

    private static func append<T: FixedWidthInteger>(_ value: T, to data: inout Data) {
        var bigEndian = value.bigEndian
        withUnsafeBytes(of: &bigEndian) { data.append(contentsOf: $0) }
    }
}

guard CommandLine.arguments.count == 2 else {
    FileHandle.standardError.write(Data("usage: GenerateNotificationSounds.swift OUTPUT_DIR\n".utf8))
    exit(64)
}

let outputDirectory = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
try FileManager.default.createDirectory(at: outputDirectory, withIntermediateDirectories: true)

let tones: [(String, Double, Double)] = [
    ("quake_warning.caf", 880, 0.75),
    ("quake_update.caf", 660, 0.45),
    ("normal.caf", 523.25, 0.30)
]

for (name, frequency, duration) in tones {
    let destination = outputDirectory.appendingPathComponent(name)
    try CAFWriter.makeTone(frequency: frequency, duration: duration).write(to: destination, options: .atomic)
}
