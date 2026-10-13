// Voice notes: live dictation into the quick-note box with Apple's Speech framework,
// on-device when the Mac supports it (nothing leaves the Mac in that case).

import AVFoundation
import Speech

final class VoiceDictation {
    /// Called on the main queue with the transcript so far.
    var onText: ((String) -> Void)?
    /// Called on the main queue when listening stops; `error` is a message to show, if any.
    var onStop: ((String?) -> Void)?

    private let engine = AVAudioEngine()
    private var request: SFSpeechAudioBufferRecognitionRequest?
    private var task: SFSpeechRecognitionTask?
    private let recognizer = SFSpeechRecognizer(locale: Locale.current) ?? SFSpeechRecognizer(locale: Locale(identifier: "en-US"))
    private(set) var isListening = false

    /// Asks for Speech Recognition and Microphone access (first time only), then starts listening.
    func start() {
        guard !isListening else { return }
        SFSpeechRecognizer.requestAuthorization { status in
            DispatchQueue.main.async {
                guard status == .authorized else {
                    return self.fail("Allow Speech Recognition for QuickNotes in System Settings › Privacy & Security.")
                }
                AVCaptureDevice.requestAccess(for: .audio) { granted in
                    DispatchQueue.main.async {
                        guard granted else {
                            return self.fail("Allow Microphone for QuickNotes in System Settings › Privacy & Security.")
                        }
                        self.begin()
                    }
                }
            }
        }
    }

    private func begin() {
        guard let recognizer, recognizer.isAvailable else {
            return fail("Speech recognition isn’t available right now.")
        }
        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        request.addsPunctuation = true
        if recognizer.supportsOnDeviceRecognition { request.requiresOnDeviceRecognition = true }
        self.request = request

        let input = engine.inputNode
        let format = input.outputFormat(forBus: 0)
        guard format.channelCount > 0 else { return fail("No microphone found.") }
        input.installTap(onBus: 0, bufferSize: 1024, format: format) { buffer, _ in request.append(buffer) }

        task = recognizer.recognitionTask(with: request) { [weak self] result, error in
            DispatchQueue.main.async {
                guard let self else { return }
                if let result { self.onText?(result.bestTranscription.formattedString) }
                if error != nil || result?.isFinal == true { self.finish(error: nil) }
            }
        }
        do {
            engine.prepare()
            try engine.start()
            isListening = true
        } catch {
            finish(error: "Couldn’t start the microphone: \(error.localizedDescription)")
        }
    }

    /// Stops listening; the last words are delivered through `onText` before `onStop`.
    func stop() {
        guard isListening else { return }
        request?.endAudio()
        engine.stop()
        engine.inputNode.removeTap(onBus: 0)
        // let the recognizer deliver its final result, then clean up
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.6) { [weak self] in self?.finish(error: nil) }
    }

    private func fail(_ message: String) { onStop?(message) }

    private func finish(error: String?) {
        guard isListening || error != nil else { return }
        if engine.isRunning {
            engine.stop()
            engine.inputNode.removeTap(onBus: 0)
        }
        task?.cancel()
        task = nil
        request = nil
        isListening = false
        onStop?(error)
    }
}
