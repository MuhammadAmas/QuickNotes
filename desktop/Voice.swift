// Voice notes: live dictation into the quick-note box with Apple's Speech framework.
// Tries on-device recognition first (nothing leaves the Mac), falls back to Apple's online
// recognition, and reports every failure in words. Steps are logged to ~/Library/Logs/QuickNotes.log.

import AVFoundation
import Speech

/// Appends a line to ~/Library/Logs/QuickNotes.log (and the system log).
func appLog(_ message: String) {
    NSLog("QuickNotes: %@", message)
    let url = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs/QuickNotes.log")
    let line = "\(ISO8601DateFormatter().string(from: Date())) \(message)\n"
    if let handle = try? FileHandle(forWritingTo: url) {
        handle.seekToEndOfFile()
        handle.write(Data(line.utf8))
        try? handle.close()
    } else {
        try? Data(line.utf8).write(to: url)
    }
}

/// What went wrong, in words, plus where in System Settings to fix it (if anywhere).
struct VoiceProblem {
    let message: String
    let settingsURL: URL?

    static let dictationSettings = URL(string: "x-apple.systempreferences:com.apple.Keyboard-Settings.extension")
    static let privacySpeech = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_SpeechRecognition")
    static let privacyMicrophone = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone")
}

final class VoiceDictation {
    /// Called on the main queue with the transcript so far.
    var onText: ((String) -> Void)?
    /// Called on the main queue when listening stops, with the problem if it failed.
    var onStop: ((VoiceProblem?) -> Void)?

    private let engine = AVAudioEngine()
    private var request: SFSpeechAudioBufferRecognitionRequest?
    private var task: SFSpeechRecognitionTask?
    private var recognizer: SFSpeechRecognizer?
    private var heardSomething = false
    private var triedOnline = false
    private(set) var isListening = false

    /// A recognizer for the user's language, mapped onto one the Speech framework supports
    /// (e.g. "en_US@rg=pkzzzz" → "en-US").
    private static func makeRecognizer() -> SFSpeechRecognizer? {
        let supported = SFSpeechRecognizer.supportedLocales()
        let preferred = Locale.preferredLanguages + [Locale.current.identifier, "en-US"]
        for id in preferred {
            let base = id.components(separatedBy: "@").first!.replacingOccurrences(of: "_", with: "-")
            if let match = supported.first(where: { $0.identifier.replacingOccurrences(of: "_", with: "-").lowercased() == base.lowercased() }) {
                return SFSpeechRecognizer(locale: match)
            }
            let lang = base.components(separatedBy: "-").first!.lowercased()
            if let match = supported.first(where: { $0.identifier.lowercased().hasPrefix(lang + "-") || $0.identifier.lowercased().hasPrefix(lang + "_") }) {
                return SFSpeechRecognizer(locale: match)
            }
        }
        return SFSpeechRecognizer(locale: Locale(identifier: "en-US"))
    }

    /// Asks for Speech Recognition and Microphone access (first time only), then starts listening.
    func start() {
        guard !isListening else { return }
        appLog("voice: start (speech auth \(SFSpeechRecognizer.authorizationStatus().rawValue), mic auth \(AVCaptureDevice.authorizationStatus(for: .audio).rawValue))")
        SFSpeechRecognizer.requestAuthorization { status in
            DispatchQueue.main.async {
                guard status == .authorized else {
                    appLog("voice: speech recognition not authorized (\(status.rawValue))")
                    return self.fail("Allow Speech Recognition for QuickNotes in System Settings › Privacy & Security › Speech Recognition.",
                                     VoiceProblem.privacySpeech)
                }
                AVCaptureDevice.requestAccess(for: .audio) { granted in
                    DispatchQueue.main.async {
                        guard granted else {
                            appLog("voice: microphone not authorized")
                            return self.fail("Allow Microphone for QuickNotes in System Settings › Privacy & Security › Microphone.",
                                             VoiceProblem.privacyMicrophone)
                        }
                        self.triedOnline = false
                        self.begin(onDevice: true)
                    }
                }
            }
        }
    }

    private func begin(onDevice: Bool) {
        recognizer = recognizer ?? VoiceDictation.makeRecognizer()
        guard let recognizer else { return fail("Speech recognition isn’t available for your language.", nil) }
        appLog("voice: recognizer \(recognizer.locale.identifier), available \(recognizer.isAvailable), on-device supported \(recognizer.supportsOnDeviceRecognition)")
        guard recognizer.isAvailable else {
            return fail("Speech recognition isn’t available. Turn on Dictation in System Settings › Keyboard, then try again.",
                        VoiceProblem.dictationSettings)
        }

        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        request.addsPunctuation = true
        let useOnDevice = onDevice && recognizer.supportsOnDeviceRecognition
        request.requiresOnDeviceRecognition = useOnDevice
        self.request = request
        heardSomething = false

        let input = engine.inputNode
        let format = input.outputFormat(forBus: 0)
        appLog("voice: input \(format.sampleRate) Hz, \(format.channelCount) ch, on-device \(useOnDevice)")
        guard format.channelCount > 0, format.sampleRate > 0 else {
            return fail("QuickNotes can’t hear your microphone. Check the input device in System Settings › Sound (Bluetooth headsets sometimes need a moment to switch to the mic).", nil)
        }
        input.removeTap(onBus: 0)
        input.installTap(onBus: 0, bufferSize: 1024, format: format) { buffer, _ in request.append(buffer) }

        task = recognizer.recognitionTask(with: request) { [weak self] result, error in
            DispatchQueue.main.async { self?.handle(result: result, error: error, onDevice: useOnDevice) }
        }
        do {
            engine.prepare()
            try engine.start()
            isListening = true
            appLog("voice: listening")
        } catch {
            appLog("voice: engine failed: \(error)")
            fail("Couldn’t start the microphone: \(error.localizedDescription)", nil)
        }
    }

    private func handle(result: SFSpeechRecognitionResult?, error: Error?, onDevice: Bool) {
        if let result {
            heardSomething = heardSomething || !result.bestTranscription.formattedString.isEmpty
            onText?(result.bestTranscription.formattedString)
            if result.isFinal { return finish(nil) }
        }
        guard let error else { return }
        let ns = error as NSError
        appLog("voice: recognition error \(ns.domain) \(ns.code): \(ns.localizedDescription)")
        let text = ns.localizedDescription.lowercased()

        // on-device model missing or failing: try Apple's online recognition once
        if onDevice && !triedOnline && !heardSomething && !text.contains("dictation") && !text.contains("siri") {
            triedOnline = true
            appLog("voice: retrying with online recognition")
            stopAudio()
            task = nil
            return begin(onDevice: false)
        }
        if text.contains("dictation") || text.contains("siri") || ns.code == 1700 {
            return finish(VoiceProblem(message: "Turn on Dictation in System Settings › Keyboard › Dictation; voice notes use it to recognise speech.",
                                       settingsURL: VoiceProblem.dictationSettings))
        }
        if ns.code == 1110 || text.contains("no speech") {
            return finish(heardSomething ? nil : VoiceProblem(message: "Didn’t catch anything. Try again a little closer to the mic.", settingsURL: nil))
        }
        if ns.code == 216 || ns.code == 301 || text.contains("cancel") { return finish(nil) } // stopped by us
        finish(heardSomething ? nil : VoiceProblem(message: "Voice note didn’t work: \(ns.localizedDescription)", settingsURL: nil))
    }

    /// Stops listening; the last words arrive through `onText` before `onStop`.
    func stop() {
        guard isListening else { return }
        appLog("voice: stop")
        request?.endAudio()
        stopAudio()
        // let the recognizer deliver its final result, then clean up
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { [weak self] in self?.finish(nil) }
    }

    private func stopAudio() {
        if engine.isRunning { engine.stop() }
        engine.inputNode.removeTap(onBus: 0)
    }

    private func fail(_ message: String, _ settings: URL?) {
        stopAudio()
        task?.cancel()
        task = nil
        request = nil
        isListening = false
        onStop?(VoiceProblem(message: message, settingsURL: settings))
    }

    private func finish(_ problem: VoiceProblem?) {
        guard isListening || problem != nil else { return }
        stopAudio()
        task?.cancel()
        task = nil
        request = nil
        isListening = false
        appLog("voice: finished\(problem.map { " (\($0.message))" } ?? "")")
        onStop?(problem)
    }
}
