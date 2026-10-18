// speech-helper: MomentForge's bridge to macOS's on-device speech recognition
// (Speech framework, macOS 26+). Prints JSON lines on stdout.
//
//   speech-helper locales                       → {"supported":[…],"installed":[…],"korean":{"supported":b,"installed":b}}
//   speech-helper probe <media>                 → {"duration_ms":n,"audio":b,"video":b,"width":n,"height":n}
//   speech-helper transcribe <audio> [--from S] [--seconds N] [--locale ko-KR] [--install]
//                                               → one {"start_ms","end_ms","text"} per final result, then {"done":true,…}
//
// Nothing leaves the machine: the model is Apple's system asset, downloaded
// and managed by macOS (--install asks the OS to fetch it when missing).
import AVFoundation
import Foundation
import Speech

struct Args {
    var command = ""
    var file = ""
    var from: Double = 0
    var seconds: Double = 0
    var locale = "ko-KR"
    var install = false
}

func parse() -> Args {
    var a = Args()
    var it = CommandLine.arguments.dropFirst().makeIterator()
    a.command = it.next() ?? ""
    while let x = it.next() {
        switch x {
        case "--from": a.from = Double(it.next() ?? "0") ?? 0
        case "--seconds": a.seconds = Double(it.next() ?? "0") ?? 0
        case "--locale": a.locale = it.next() ?? a.locale
        case "--install": a.install = true
        default: a.file = x
        }
    }
    return a
}

func emit(_ obj: [String: Any]) {
    if let data = try? JSONSerialization.data(withJSONObject: obj, options: [.withoutEscapingSlashes]),
       let s = String(data: data, encoding: .utf8) {
        print(s)
        fflush(stdout)
    }
}

func fail(_ message: String) -> Never {
    emit(["error": message])
    exit(1)
}

func ms(_ t: CMTime) -> Int { Int((CMTimeGetSeconds(t) * 1000).rounded()) }

@available(macOS 26.0, *)
func locales(_ a: Args) async {
    let supported = await SpeechTranscriber.supportedLocales.map(\.identifier).sorted()
    let installed = await SpeechTranscriber.installedLocales.map(\.identifier).sorted()
    let want = Locale(identifier: a.locale)
    let isSupported = await SpeechTranscriber.supportedLocales.contains { $0.identifier(.bcp47) == want.identifier(.bcp47) }
    let isInstalled = await SpeechTranscriber.installedLocales.contains { $0.identifier(.bcp47) == want.identifier(.bcp47) }
    emit(["supported": supported, "installed": installed, "locale": a.locale,
          "korean": ["supported": isSupported, "installed": isInstalled]])
}

/// Exports [from, from+seconds) of the audio to a temporary file so a long
/// broadcast can be measured in parts (and so the analyzer reads a plain file).
@available(macOS 26.0, *)
func excerpt(of url: URL, from: Double, seconds: Double) async throws -> URL {
    if seconds <= 0 && from <= 0 { return url }
    let asset = AVURLAsset(url: url)
    let duration = try await asset.load(.duration)
    guard let session = AVAssetExportSession(asset: asset, presetName: AVAssetExportPresetAppleM4A) else {
        throw NSError(domain: "speech-helper", code: 1, userInfo: [NSLocalizedDescriptionKey: "export session unavailable"])
    }
    let start = CMTime(seconds: from, preferredTimescale: 600)
    let length = seconds > 0 ? CMTime(seconds: seconds, preferredTimescale: 600) : CMTimeSubtract(duration, start)
    let out = FileManager.default.temporaryDirectory.appendingPathComponent("mf-excerpt-\(UUID().uuidString).m4a")
    session.timeRange = CMTimeRange(start: start, duration: length)
    try await session.export(to: out, as: .m4a)
    return out
}

/// probe reads a media file's length and tracks (AVFoundation reads only the headers).
func probe(_ a: Args) async {
    guard !a.file.isEmpty else { fail("media file missing") }
    let asset = AVURLAsset(url: URL(fileURLWithPath: a.file))
    do {
        let duration = try await asset.load(.duration)
        let tracks = try await asset.load(.tracks)
        var out: [String: Any] = ["duration_ms": ms(duration), "audio": false, "video": false]
        for t in tracks {
            if t.mediaType == .audio { out["audio"] = true }
            if t.mediaType == .video {
                out["video"] = true
                let size = try await t.load(.naturalSize)
                out["width"] = Int(size.width)
                out["height"] = Int(size.height)
            }
        }
        emit(out)
    } catch {
        fail("cannot read media: \(error)")
    }
}

@available(macOS 26.0, *)
func transcribe(_ a: Args) async {
    guard !a.file.isEmpty else { fail("audio file missing") }
    let locale = Locale(identifier: a.locale)
    let transcriber = SpeechTranscriber(locale: locale, transcriptionOptions: [], reportingOptions: [],
                                        attributeOptions: [.audioTimeRange])
    if let request = try? await AssetInventory.assetInstallationRequest(supporting: [transcriber]) {
        if !a.install { fail("language asset for \(a.locale) is not installed; run with --install") }
        emit(["status": "installing", "locale": a.locale])
        do { try await request.downloadAndInstall() } catch { fail("asset install failed: \(error)") }
        emit(["status": "installed", "locale": a.locale])
    }
    let started = Date()
    let input: URL
    do { input = try await excerpt(of: URL(fileURLWithPath: a.file), from: a.from, seconds: a.seconds) } catch { fail("excerpt failed: \(error)") }
    defer { if input.path != a.file { try? FileManager.default.removeItem(at: input) } }
    let exported = Date().timeIntervalSince(started)
    let offsetMs = Int(a.from * 1000)
    let audioFile: AVAudioFile
    do { audioFile = try AVAudioFile(forReading: input) } catch { fail("cannot read audio: \(error)") }
    let audioSeconds = Double(audioFile.length) / audioFile.fileFormat.sampleRate
    let analyzer = SpeechAnalyzer(modules: [transcriber])
    var count = 0
    var chars = 0
    let collector = Task {
        do {
            for try await result in transcriber.results where result.isFinal {
                let text = String(result.text.characters)
                // Per-run audio time ranges when the model gives them; the result's own range otherwise.
                var startMs = ms(result.range.start) + offsetMs
                var endMs = ms(result.range.end) + offsetMs
                var first = true
                for run in result.text.runs {
                    if let r = run.audioTimeRange {
                        if first { startMs = ms(r.start) + offsetMs; first = false }
                        endMs = ms(r.end) + offsetMs
                    }
                }
                let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
                if trimmed.isEmpty { continue }
                count += 1
                chars += trimmed.count
                emit(["start_ms": startMs, "end_ms": endMs, "text": trimmed])
            }
        } catch {
            emit(["error": "results: \(error)"])
        }
    }
    do {
        if let last = try await analyzer.analyzeSequence(from: audioFile) {
            try await analyzer.finalizeAndFinish(through: last)
        } else {
            await analyzer.cancelAndFinishNow()
        }
    } catch {
        emit(["error": "analyze: \(error)"])
    }
    _ = await collector.result
    let total = Date().timeIntervalSince(started)
    emit(["done": true, "utterances": count, "characters": chars, "audio_seconds": audioSeconds,
          "export_seconds": exported, "total_seconds": total, "realtime_factor": audioSeconds / max(total - exported, 0.001)])
}

let args = parse()
if #available(macOS 26.0, *) {
    let sem = DispatchSemaphore(value: 0)
    Task {
        switch args.command {
        case "locales": await locales(args)
        case "probe": await probe(args)
        case "transcribe": await transcribe(args)
        default: emit(["error": "usage: speech-helper locales | probe <media> | transcribe <audio> [--from S] [--seconds N] [--locale ko-KR] [--install]"])
        }
        sem.signal()
    }
    sem.wait()
} else {
    fail("macOS 26 or later is required for on-device speech recognition")
}
