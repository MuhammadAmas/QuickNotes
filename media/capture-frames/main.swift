// Renders the Mac quick-note box (desktop/Capture.swift) while a note is typed, then saved.
//   swiftc -o /tmp/qn-frames media/capture-frames/main.swift desktop/Capture.swift desktop/BubbleArt.swift desktop/Screenshot.swift
//   /tmp/qn-frames media/shots/capture
import AppKit

// stand-ins for Cloud.swift / main.swift, so nothing touches the network
enum SaveResult { case synced, queued(String) }
final class Cloud {
    var isSignedIn = true
    func countNotes(on: String, _ done: @escaping (Int?) -> Void) { done(3) }
    func saveNote(_ text: String, _ done: @escaping (SaveResult) -> Void) { done(.synced) }
    func saveScreenshotNote(_ text: String, _ shot: Screenshot, _ done: @escaping (SaveResult) -> Void) { done(.synced) }
}
func isoDay(_ date: Date = Date()) -> String { "2026-10-11" }
func prettyDay(_ date: Date = Date()) -> String {
    let f = DateFormatter(); f.setLocalizedDateFormatFromTemplate("EEEMMMd"); return f.string(from: date)
}

let out = CommandLine.arguments[1]
try? FileManager.default.createDirectory(atPath: out, withIntermediateDirectories: true)
NSApplication.shared.setActivationPolicy(.prohibited)

let c = CaptureController(cloud: Cloud())
c.panel.appearance = NSAppearance(named: .aqua)
c.updateChip()
let field = Mirror(reflecting: c).children.first { $0.label == "field" }!.value as! NSTextField

func render(_ name: String) {
    let v = c.panel.contentView!
    v.layoutSubtreeIfNeeded()
    NSAppearance(named: .aqua)!.performAsCurrentDrawingAppearance {
        let rep = v.bitmapImageRepForCachingDisplay(in: v.bounds)!
        v.cacheDisplay(in: v.bounds, to: rep)
        try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: "\(out)/\(name).png"))
    }
}

let note = "Follow up with Sara on the pricing deck"
var frame = 0
for end in stride(from: 0, through: note.count, by: 2) + [note.count] {
    field.stringValue = String(note.prefix(end))
    c.controlTextDidChange(Notification(name: NSControl.textDidChangeNotification))
    render(String(format: "type-%02d", frame))
    frame += 1
}
_ = c.control(field, textView: NSTextView(), doCommandBy: #selector(NSResponder.insertNewline(_:)))
render("saved")
print("rendered \(frame) typing frames + saved")
