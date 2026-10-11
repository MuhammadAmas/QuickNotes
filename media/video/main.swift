// Builds the QuickNotes demo video (1920×1080, 30 fps, H.264 + AAC voiceover) from the captures in media/shots.
// Run through media/make_video.sh, which records the voiceover first:
//   /tmp/qn-video <shots dir> <out.mp4> <voiceover.txt> <voice clips dir>
// Each scene lasts as long as its voiceover line; on-screen moments are cued to words in that line.
import AppKit
import AVFoundation
import ImageIO

let W = 1920, H = 1080, FPS = 30
let WF = CGFloat(W), HF = CGFloat(H)
let args = CommandLine.arguments
let shotsDir = args[1]
let outURL = URL(fileURLWithPath: args[2])
let lines = (try! String(contentsOfFile: args[3], encoding: .utf8)).split(separator: "\n").map(String.init).filter { !$0.isEmpty }
let voiceDir = args[4]

// MARK: - helpers

func load(_ name: String) -> CGImage {
    let url = URL(fileURLWithPath: "\(shotsDir)/\(name).png")
    guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
          let img = CGImageSourceCreateImageAtIndex(src, 0, nil) else { fatalError("missing \(url.path)") }
    return img
}

func color(_ hex: UInt32, _ a: CGFloat = 1) -> NSColor {
    NSColor(srgbRed: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
            blue: CGFloat(hex & 0xFF) / 255, alpha: a)
}

let cream = color(0xF7F6F3), ink = color(0x1C1B19), muted = color(0x6B665E), accent = color(0xE0612A)

func clamp(_ x: CGFloat, _ a: CGFloat = 0, _ b: CGFloat = 1) -> CGFloat { min(max(x, a), b) }
func progress(_ t: CGFloat, _ a: CGFloat, _ b: CGFloat) -> CGFloat { clamp((t - a) / (b - a)) }
func easeInOut(_ x: CGFloat) -> CGFloat { x < 0.5 ? 2 * x * x : 1 - pow(-2 * x + 2, 2) / 2 }
func easeOut(_ x: CGFloat) -> CGFloat { 1 - pow(1 - x, 3) }
func easeOutBack(_ x: CGFloat) -> CGFloat { let c = 1.70158; return 1 + (c + 1) * pow(x - 1, 3) + c * pow(x - 1, 2) }
func lerp(_ a: CGFloat, _ b: CGFloat, _ t: CGFloat) -> CGFloat { a + (b - a) * t }

var ctx: CGContext!

/// Rect in top-left coordinates → CoreGraphics (bottom-left) coordinates.
func R(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat) -> CGRect { CGRect(x: x, y: HF - y - h, width: w, height: h) }
func P(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: x, y: HF - y) }

func withAlpha(_ a: CGFloat, _ draw: () -> Void) {
    guard a > 0.001 else { return }
    ctx.saveGState()
    ctx.setAlpha(a)
    ctx.beginTransparencyLayer(auxiliaryInfo: nil)
    draw()
    ctx.endTransparencyLayer()
    ctx.restoreGState()
}

func fill(_ rect: CGRect, _ c: NSColor, radius: CGFloat = 0) {
    ctx.setFillColor(c.cgColor)
    ctx.addPath(CGPath(roundedRect: rect, cornerWidth: radius, cornerHeight: radius, transform: nil))
    ctx.fillPath()
}

func shadow(_ blur: CGFloat, _ dy: CGFloat, _ a: CGFloat) {
    ctx.setShadow(offset: CGSize(width: 0, height: -dy), blur: blur, color: NSColor(white: 0, alpha: a).cgColor)
}

@discardableResult
func text(_ s: String, size: CGFloat, weight: NSFont.Weight = .regular, color c: NSColor,
          x: CGFloat? = nil, centerX: CGFloat? = nil, top: CGFloat) -> CGSize {
    let str = NSAttributedString(string: s, attributes: [.font: NSFont.systemFont(ofSize: size, weight: weight), .foregroundColor: c])
    let sz = str.size()
    let left = centerX.map { $0 - sz.width / 2 } ?? x ?? 0
    str.draw(at: NSPoint(x: left, y: HF - top - sz.height))
    return sz
}

func textWidth(_ s: String, size: CGFloat, weight: NSFont.Weight) -> CGFloat {
    NSAttributedString(string: s, attributes: [.font: NSFont.systemFont(ofSize: size, weight: weight)]).size().width
}

/// Draws `img` filling `rect`, zoomed by `zoom` towards a focus point given as fractions (0…1, top-left origin).
func drawImage(_ img: CGImage, in rect: CGRect, zoom: CGFloat = 1, focus: CGPoint = CGPoint(x: 0.5, y: 0.5)) {
    let w = rect.width * zoom, h = rect.height * zoom
    let drawn = CGRect(x: rect.minX - (w - rect.width) * focus.x, y: rect.minY - (h - rect.height) * (1 - focus.y), width: w, height: h)
    ctx.saveGState()
    ctx.clip(to: rect)
    ctx.draw(img, in: drawn)
    ctx.restoreGState()
}

func bubble(center c: CGPoint, diameter d: CGFloat, angle: CGFloat = 0, scale: CGFloat = 1) {
    ctx.saveGState()
    let pivot = CGPoint(x: c.x, y: c.y + d / 2) // hangs from its top, like the app
    ctx.translateBy(x: pivot.x, y: pivot.y)
    ctx.rotate(by: -angle)
    ctx.translateBy(x: -pivot.x, y: -pivot.y)
    ctx.translateBy(x: c.x, y: c.y)
    ctx.scaleBy(x: scale, y: scale)
    ctx.translateBy(x: -c.x, y: -c.y)
    drawBubbleArt(in: CGRect(x: c.x - d / 2, y: c.y - d / 2, width: d, height: d), dropShadow: true)
    ctx.restoreGState()
}

/// The app's swing: a short damped pendulum every few seconds.
func swing(_ t: CGFloat) -> CGFloat {
    let phase = t.truncatingRemainder(dividingBy: 3.2)
    return 0.2 * exp(-phase * 1.3) * sin(phase * 7.5)
}

func caption(_ step: Int, _ s: String, t: CGFloat, top: CGFloat = 942) {
    let a = easeOut(progress(t, 0.15, 0.6))
    let size: CGFloat = 30, weight: NSFont.Weight = .semibold
    let w = textWidth(s, size: size, weight: weight) + 46 + 30 + 34
    let x = (WF - w) / 2, y = top + (1 - a) * 14
    withAlpha(a) {
        ctx.saveGState(); shadow(24, 8, 0.25)
        fill(R(x, y, w, 72), color(0x1C1B19, 0.94), radius: 36)
        ctx.restoreGState()
        fill(R(x + 16, y + 16, 40, 40), accent, radius: 20)
        text("\(step)", size: 22, weight: .bold, color: .white, centerX: x + 36, top: y + 20)
        text(s, size: size, weight: weight, color: .white, x: x + 72, top: y + 17)
    }
}

/// A keyboard key; `pressed` 0…1 pushes it down and lights it up.
func key(_ label: String, x: CGFloat, top: CGFloat, size: CGFloat = 124, pressed: CGFloat) {
    let down = 6 * pressed
    ctx.saveGState(); shadow(lerp(22, 8, pressed), lerp(10, 3, pressed), 0.35)
    fill(R(x, top + down, size, size), .white, radius: 22)
    ctx.restoreGState()
    if pressed > 0 {
        ctx.saveGState()
        ctx.setStrokeColor(accent.withAlphaComponent(pressed).cgColor); ctx.setLineWidth(5)
        ctx.addPath(CGPath(roundedRect: R(x, top + down, size, size).insetBy(dx: 2.5, dy: 2.5), cornerWidth: 20, cornerHeight: 20, transform: nil))
        ctx.strokePath()
        ctx.restoreGState()
    }
    let c = NSColor(srgbRed: lerp(0.11, 0.88, pressed), green: lerp(0.106, 0.38, pressed), blue: lerp(0.098, 0.165, pressed), alpha: 1)
    text(label, size: size * 0.46, weight: .semibold, color: c, centerX: x + size / 2, top: top + down + size * 0.22)
}

/// ⌘ ⇧ N keys centred on screen, each pressed at its own time.
func shortcutKeys(t: CGFloat, show: CGFloat, presses: [CGFloat], top: CGFloat = 690, hide: CGFloat = 1e6) {
    let a = easeOut(progress(t, show, show + 0.35)) * (1 - progress(t, hide, hide + 0.35))
    let labels = ["⌘", "⇧", "N"], size: CGFloat = 124, gap: CGFloat = 70
    let total = size * 3 + gap * 2
    withAlpha(a) {
        for (i, label) in labels.enumerated() {
            let x = (WF - total) / 2 + CGFloat(i) * (size + gap)
            key(label, x: x, top: top + (1 - a) * 20, pressed: easeOut(progress(t, presses[i], presses[i] + 0.12)))
            if i < 2 { text("+", size: 48, weight: .bold, color: .white, centerX: x + size + gap / 2, top: top + 30) }
        }
    }
}

func creamBackground(glow: CGPoint = CGPoint(x: 960, y: 200)) {
    fill(R(0, 0, WF, HF), cream)
    let g = CGGradient(colorsSpace: CGColorSpace(name: CGColorSpace.sRGB), colors: [color(0xFCE3D3, 0.9).cgColor, color(0xF7F6F3, 0).cgColor] as CFArray, locations: [0, 1])!
    ctx.drawRadialGradient(g, startCenter: P(glow.x, glow.y), startRadius: 0, endCenter: P(glow.x, glow.y), endRadius: 900, options: [])
}

/// A light browser-style window: title bar with traffic lights and an address pill, content below.
func browserWindow(url: String, rect: CGRect, content: (CGRect) -> Void) {
    let bar: CGFloat = 46
    ctx.saveGState(); shadow(60, 24, 0.22)
    fill(rect, .white, radius: 16)
    ctx.restoreGState()
    ctx.saveGState()
    ctx.addPath(CGPath(roundedRect: rect, cornerWidth: 16, cornerHeight: 16, transform: nil)); ctx.clip()
    fill(CGRect(x: rect.minX, y: rect.maxY - bar, width: rect.width, height: bar), color(0xECEAE6))
    for (i, c) in [0xFF5F57, 0xFEBC2E, 0x28C840].enumerated() {
        fill(CGRect(x: rect.minX + 20 + CGFloat(i) * 22, y: rect.maxY - bar / 2 - 7, width: 14, height: 14), color(UInt32(c)), radius: 7)
    }
    let pillW: CGFloat = 420
    fill(CGRect(x: rect.midX - pillW / 2, y: rect.maxY - bar + 9, width: pillW, height: 28), .white, radius: 8)
    text("🔒 " + url, size: 15, weight: .medium, color: muted, centerX: rect.midX, top: HF - rect.maxY + 14)
    content(CGRect(x: rect.minX, y: rect.minY, width: rect.width, height: rect.height - bar))
    ctx.restoreGState()
}

// MARK: - captures & voiceover timing

let shots = [
    "today0": load("web-today-before"), "today": load("web-today"), "ticked": load("web-ticked"),
    "dragging": load("web-dragging"), "dragged": load("web-dragged"), "open": load("web-open"),
    "share": load("web-share"), "phone": load("phone-dark"), "popup": load("popup"),
]
let typing = (0...40).compactMap { i -> CGImage? in
    let name = "capture/type-\(String(format: "%02d", i))"
    return FileManager.default.fileExists(atPath: "\(shotsDir)/\(name).png") ? load(name) : nil
}
let saved = load("capture/saved")

let voiceURLs = lines.indices.map { URL(fileURLWithPath: "\(voiceDir)/\(String(format: "%02d", $0)).wav") }
let voiceLength: [CGFloat] = voiceURLs.map { url in
    let f = try! AVAudioFile(forReading: url)
    return CGFloat(Double(f.length) / f.fileFormat.sampleRate)
}
let lead: CGFloat = 0.45   // voice starts this far into its scene
let tail: CGFloat = 0.75   // breathing room after it

/// When (seconds into scene `i`) the narrator reaches `word`: estimated from its position in the line.
func cue(_ i: Int, _ word: String) -> CGFloat {
    let line = lines[i]
    guard let r = line.range(of: word) else { fatalError("“\(word)” not in line \(i)") }
    return lead + voiceLength[i] * CGFloat(line.distance(from: line.startIndex, to: r.lowerBound)) / CGFloat(line.count)
}

// MARK: - scenes

let webRect = R(320, 44, 1280, 846) // 1280×800 content + 46 bar

func webScene(_ url: String = "quicknotes-hq.web.app", _ draw: @escaping (CGRect) -> Void) {
    creamBackground()
    browserWindow(url: url, rect: webRect, content: draw)
}

let bubbleCenter = P(1600, 470)
let bubbleD: CGFloat = 116

func desktop(_ t: CGFloat) {
    let g = CGGradient(colorsSpace: CGColorSpace(name: CGColorSpace.sRGB),
                       colors: [color(0x2B3566).cgColor, color(0x7A4E7E).cgColor, color(0xE6845A).cgColor] as CFArray,
                       locations: [0, 0.55, 1])!
    ctx.drawLinearGradient(g, start: P(0, 0), end: P(WF, HF), options: [])
    fill(R(0, 0, WF, 34), NSColor(white: 1, alpha: 0.16))
    text("Mon 10:02 AM", size: 16, weight: .medium, color: NSColor(white: 1, alpha: 0.9), x: WF - 150, top: 7)

    // a generic video-call window
    let win = R(170, 110, 1180, 740)
    ctx.saveGState(); shadow(60, 24, 0.4); fill(win, color(0x1F2023), radius: 18); ctx.restoreGState()
    for (i, c) in [0xFF5F57, 0xFEBC2E, 0x28C840].enumerated() {
        fill(R(192 + CGFloat(i) * 22, 128, 14, 14), color(UInt32(c)), radius: 7)
    }
    text("Weekly sync", size: 17, weight: .semibold, color: NSColor(white: 1, alpha: 0.75), centerX: 760, top: 124)
    let people: [(String, UInt32, UInt32)] = [("Aisha", 0x34446A, 0x5D74A8), ("Sam", 0x4A3A5E, 0x8467A6),
                                              ("Priya", 0x2F5550, 0x4F9187), ("You", 0x5A4636, 0xB07A50)]
    for (i, p) in people.enumerated() {
        let x = 190 + CGFloat(i % 2) * 576, y = 166 + CGFloat(i / 2) * 340
        fill(R(x, y, 564, 328), color(p.1), radius: 14)
        fill(R(x + 282 - 52, y + 112, 104, 104), color(p.2), radius: 52)
        text(String(p.0.prefix(1)), size: 46, weight: .bold, color: .white, centerX: x + 282, top: y + 134)
        fill(R(x + 14, y + 284, textWidth(p.0, size: 17, weight: .semibold) + 24, 32), NSColor(white: 0, alpha: 0.35), radius: 8)
        text(p.0, size: 17, weight: .semibold, color: .white, x: x + 26, top: y + 290)
    }
    bubble(center: bubbleCenter, diameter: bubbleD, angle: swing(t))
}

/// Aisha asking for the follow-up, as a speech balloon over her tile.
func askBalloon(_ a: CGFloat) {
    let s = "“Can you follow up with Sara on the pricing deck?”"
    let w = textWidth(s, size: 26, weight: .medium) + 48
    let x: CGFloat = 250, y: CGFloat = 196 + (1 - a) * 12
    withAlpha(a) {
        ctx.saveGState(); shadow(24, 10, 0.3)
        fill(R(x, y, w, 66), .white, radius: 20)
        ctx.beginPath()
        ctx.move(to: P(x + 120, y + 64)); ctx.addLine(to: P(x + 150, y + 64)); ctx.addLine(to: P(x + 112, y + 96))
        ctx.closePath(); ctx.setFillColor(NSColor.white.cgColor); ctx.fillPath()
        ctx.restoreGState()
        text(s, size: 26, weight: .medium, color: ink, x: x + 24, top: y + 16)
    }
}

struct Scene { let draw: (CGFloat, CGFloat) -> Void } // (time in scene, scene duration)

let scenes: [Scene] = [
    // 0 intro
    Scene { t, _ in
        creamBackground(glow: CGPoint(x: 960, y: 380))
        let pop = easeOutBack(progress(t, 0.1, 0.75))
        bubble(center: P(960, 380), diameter: 220, angle: t > 0.8 ? swing(t - 0.8) : 0, scale: max(pop, 0.001))
        let a = easeOut(progress(t, 0.6, 1.2))
        withAlpha(a) { text("QuickNotes", size: 96, weight: .bold, color: ink, centerX: 960, top: 540 + (1 - a) * 20) }
        let b = easeOut(progress(t, 1.0, 1.6))
        withAlpha(b) {
            text("Capture what you need to act on, without leaving your meeting.", size: 36, weight: .medium, color: muted, centerX: 960, top: 676 + (1 - b) * 16)
        }
    },
    // 1 in a meeting: someone asks for a follow-up → ⌘⇧N
    Scene { t, _ in
        desktop(t)
        askBalloon(easeOut(progress(t, cue(1, "someone") - 0.2, cue(1, "someone") + 0.3)))
        let presses = [cue(1, "Command"), cue(1, "Shift"), cue(1, "N.")]
        shortcutKeys(t: t, show: cue(1, "Just") - 0.3, presses: presses)
        caption(1, "In a meeting? Press ⌘⇧N, from any app", t: t)
    },
    // 2 the quick-note box: type, Enter, saved
    Scene { t, d in
        desktop(t + 6)
        askBalloon(1 - progress(t, 0, 0.4))
        shortcutKeys(t: t, show: -1, presses: [-1, -1, -1], hide: 0)
        // typing runs while the box "pops up" is said, finishing just before "Enter"
        let typeStart: CGFloat = 0.7, enter = cue(2, "Enter,")
        let highlight = cue(2, "Your") - 0.2

        withAlpha(0.45 * easeInOut(progress(t, highlight, highlight + 0.5))) { fill(R(0, 0, WF, HF), .black) }

        let img: CGImage
        if t >= enter + 0.1 { img = saved } else {
            img = typing[Int(progress(t, typeStart, enter - 0.5) * CGFloat(typing.count - 1))]
        }
        let w = CGFloat(img.width), h = CGFloat(img.height)
        let appear = easeOut(progress(t, 0.05, 0.35))
        let x = bubbleCenter.x - bubbleD / 2 - 14 - w, top = 402 + (1 - appear) * 12
        withAlpha(appear) {
            ctx.saveGState(); shadow(40, 16, 0.35)
            ctx.draw(img, in: R(x, top, w, h))
            ctx.restoreGState()
        }
        // the Enter key
        let k = progress(t, enter - 0.45, enter + 0.5)
        if k > 0 && k < 1 {
            withAlpha(sin(k * .pi)) {
                let p: CGFloat = k > 0.42 && k < 0.7 ? 1 : 0
                let kw: CGFloat = 170, kh: CGFloat = 84, kx = x + w - 170, ky = top + h + 30 + 4 * p
                ctx.saveGState(); shadow(14, 6, 0.3); fill(R(kx, ky, kw, kh), .white, radius: 16); ctx.restoreGState()
                text("return ↵", size: 28, weight: .semibold, color: p > 0 ? accent : ink, centerX: kx + kw / 2, top: ky + kh / 2 - 17)
            }
        }
        // the point of it all
        let hl = easeOut(progress(t, highlight + 0.2, highlight + 0.7))
        withAlpha(hl) {
            let s1 = "No app switching.", s2 = "No forgotten action items."
            let cw = max(textWidth(s1, size: 54, weight: .bold), textWidth(s2, size: 54, weight: .bold)) + 120
            let cy: CGFloat = 170 + (1 - hl) * 16 // above the quick-note box, over the dimmed call
            ctx.saveGState(); shadow(40, 14, 0.4); fill(R((WF - cw) / 2, cy, cw, 200), accent, radius: 28); ctx.restoreGState()
            text(s1, size: 54, weight: .bold, color: .white, centerX: 960, top: cy + 34)
            text(s2, size: 54, weight: .bold, color: .white, centerX: 960, top: cy + 104)
        }
        caption(2, "Type what you need to act on, press Enter. Saved to today.", t: t)
        _ = d
    },
    // 3 it's in today's list on the web
    Scene { t, _ in
        let z = lerp(1, 1.32, easeInOut(progress(t, 0.2, 2.2)))
        let f = CGPoint(x: 0.62, y: 0.3)
        webScene { r in
            drawImage(shots["today0"]!, in: r, zoom: z, focus: f)
            withAlpha(easeInOut(progress(t, cue(3, "Everything") - 0.3, cue(3, "Everything") + 0.3))) { drawImage(shots["today"]!, in: r, zoom: z, focus: f) }
        }
        caption(3, "Later: everything you captured is in today’s list", t: t)
    },
    // 4 tick it off
    Scene { t, _ in
        let f = CGPoint(x: 0.62, y: 0.3)
        webScene { r in
            drawImage(shots["today"]!, in: r, zoom: 1.32, focus: f)
            withAlpha(easeInOut(progress(t, cue(4, "off") - 0.2, cue(4, "off") + 0.3))) { drawImage(shots["ticked"]!, in: r, zoom: 1.32, focus: f) }
        }
        caption(4, "Tick things off and watch the day fill up", t: t)
    },
    // 5 drag to set priority
    Scene { t, d in
        let f = CGPoint(x: 0.62, y: 0.3)
        webScene { r in
            drawImage(shots["ticked"]!, in: r, zoom: 1.32, focus: f)
            withAlpha(easeInOut(progress(t, 0.5, 0.9))) { drawImage(shots["dragging"]!, in: r, zoom: 1.32, focus: f) }
            withAlpha(easeInOut(progress(t, d * 0.62, d * 0.62 + 0.4))) { drawImage(shots["dragged"]!, in: r, zoom: 1.32, focus: f) }
        }
        caption(5, "Drag a task by its grip to set priority", t: t)
    },
    // 6 everything still open
    Scene { t, d in
        webScene { r in
            drawImage(shots["open"]!, in: r, zoom: lerp(1.12, 1.3, easeInOut(progress(t, 0, d))), focus: CGPoint(x: 0.6, y: 0.12))
        }
        caption(6, "Unfinished? Move it to tomorrow; overdue days are flagged", t: t)
    },
    // 7 share
    Scene { t, _ in
        webScene { r in
            let z = lerp(1, 1.3, easeInOut(progress(t, 0.6, 2.2))), f = CGPoint(x: 0.5, y: 0.45)
            drawImage(shots["today0"]!, in: r, zoom: z, focus: f)
            withAlpha(easeInOut(progress(t, cue(7, "Share") - 0.3, cue(7, "Share") + 0.2))) { drawImage(shots["share"]!, in: r, zoom: z, focus: f) }
        }
        caption(7, "Share a note with a teammate by email", t: t)
    },
    // 8 phone
    Scene { t, _ in
        creamBackground(glow: CGPoint(x: 960, y: 420))
        let img = shots["phone"]!
        let ph: CGFloat = 800, pw = ph * CGFloat(img.width) / CGFloat(img.height)
        let rise = easeOut(progress(t, 0, 0.8))
        let top: CGFloat = 70 + (1 - rise) * 40
        let screen = R(960 - pw / 2, top, pw, ph)
        let body = screen.insetBy(dx: -14, dy: -14)
        ctx.saveGState(); shadow(60, 26, 0.35); fill(body, color(0x111111), radius: 64); ctx.restoreGState()
        ctx.saveGState()
        ctx.addPath(CGPath(roundedRect: screen, cornerWidth: 50, cornerHeight: 50, transform: nil)); ctx.clip()
        ctx.draw(img, in: screen)
        ctx.restoreGState()
        let chips: [(String, CGFloat, CGFloat, CGFloat)] = [("Add to Home Screen", 380, 300, 0.6), ("Dark mode", 1540, 420, cue(8, "dark")),
                                                            ("Works offline", 400, 560, cue(8, "dark") + 0.5), ("Syncs live", 1520, 680, cue(8, "syncs"))]
        for c in chips {
            let a = easeOut(progress(t, c.3, c.3 + 0.4))
            withAlpha(a) {
                let w = textWidth(c.0, size: 26, weight: .semibold) + 64
                ctx.saveGState(); shadow(20, 8, 0.15); fill(R(c.1 - w / 2, c.2 + (1 - a) * 10, w, 58), .white, radius: 29); ctx.restoreGState()
                fill(R(c.1 - w / 2 + 22, c.2 + 23 + (1 - a) * 10, 12, 12), accent, radius: 6)
                text(c.0, size: 26, weight: .semibold, color: ink, x: c.1 - w / 2 + 44, top: c.2 + 12 + (1 - a) * 10)
            }
        }
        caption(8, "On your phone too: add it to your Home Screen", t: t)
    },
    // 9 Chrome extension
    Scene { t, _ in
        creamBackground()
        browserWindow(url: "docs.yourteam.com/roadmap", rect: webRect) { r in
            fill(r, .white)
            fill(R(420, 150, 520, 34), color(0xE4E1DA), radius: 8)
            for i in 0..<9 {
                let w: CGFloat = i % 3 == 2 ? 560 : 860
                fill(R(420, 220 + CGFloat(i) * 44, w, 16), color(0xEFEDE8), radius: 8)
            }
            fill(R(420, 640, 860, 180), color(0xF4F2EE), radius: 12)
        }
        bubble(center: P(1560, 67), diameter: 28)
        fill(R(1566, 52, 22, 16), accent, radius: 8)
        text("3", size: 11, weight: .bold, color: .white, centerX: 1577, top: 52)

        let pop = shots["popup"]!
        let pw: CGFloat = 486, ph = pw * CGFloat(pop.height) / CGFloat(pop.width)
        let a = easeOut(progress(t, cue(9, "Option") - 0.2, cue(9, "Option") + 0.2))
        withAlpha(a) {
            ctx.saveGState(); shadow(40, 16, 0.3)
            ctx.draw(pop, in: R(1590 - pw, 96 + (1 - a) * 10, pw, ph))
            ctx.restoreGState()
        }
        caption(9, "Chrome extension: ⌥⇧N, or right-click → Save to QuickNotes", t: t)
    },
    // 10 outro
    Scene { t, _ in
        creamBackground(glow: CGPoint(x: 960, y: 300))
        bubble(center: P(960, 250), diameter: 170, angle: swing(t + 1.6))
        let a = easeOut(progress(t, 0.2, 0.8))
        withAlpha(a) {
            text("QuickNotes", size: 80, weight: .bold, color: ink, centerX: 960, top: 360)
            text("quicknotes-hq.web.app", size: 40, weight: .semibold, color: accent, centerX: 960, top: 470)
        }
        // the shortcut, one more time
        let keysAt = cue(10, "Command")
        let size: CGFloat = 96, gap: CGFloat = 54, total = size * 3 + gap * 2
        withAlpha(easeOut(progress(t, keysAt - 0.5, keysAt - 0.1))) {
            for (i, label) in ["⌘", "⇧", "N"].enumerated() {
                let x = (WF - total) / 2 + CGFloat(i) * (size + gap)
                let press = [cue(10, "Command"), cue(10, "Shift"), cue(10, "N,")][i]
                key(label, x: x, top: 580, size: size, pressed: easeOut(progress(t, press, press + 0.12)))
                if i < 2 { text("+", size: 40, weight: .bold, color: muted, centerX: x + size + gap / 2, top: 600) }
            }
        }
        withAlpha(easeOut(progress(t, cue(10, "never") - 0.2, cue(10, "never") + 0.3))) {
            text("Never lose an action item again.", size: 34, weight: .semibold, color: ink, centerX: 960, top: 730)
            text("Mac bubble  ·  Web & phone  ·  Chrome extension  ·  Sign in with Google", size: 24, weight: .medium, color: muted, centerX: 960, top: 790)
        }
    },
]
precondition(scenes.count == lines.count, "need one voiceover line per scene (\(scenes.count) scenes, \(lines.count) lines)")

// MARK: - render video

let fade: CGFloat = 0.5
let durations = voiceLength.map { $0 + lead + tail }
var starts: [CGFloat] = []
var acc: CGFloat = 0
for d in durations { starts.append(acc); acc += d }
let total = acc
let frameCount = Int(total * CGFloat(FPS))

let silentURL = FileManager.default.temporaryDirectory.appendingPathComponent("qn-silent.mp4")
try? FileManager.default.removeItem(at: silentURL)
let writer = try! AVAssetWriter(outputURL: silentURL, fileType: .mp4)
let input = AVAssetWriterInput(mediaType: .video, outputSettings: [
    AVVideoCodecKey: AVVideoCodecType.h264,
    AVVideoWidthKey: W, AVVideoHeightKey: H,
    AVVideoCompressionPropertiesKey: [
        AVVideoAverageBitRateKey: 8_000_000,
        AVVideoProfileLevelKey: AVVideoProfileLevelH264HighAutoLevel,
        AVVideoMaxKeyFrameIntervalKey: FPS * 2,
    ],
])
input.expectsMediaDataInRealTime = false
let adaptor = AVAssetWriterInputPixelBufferAdaptor(assetWriterInput: input, sourcePixelBufferAttributes: [
    kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32ARGB,
    kCVPixelBufferWidthKey as String: W, kCVPixelBufferHeightKey as String: H,
    kCVPixelBufferCGBitmapContextCompatibilityKey as String: true,
])
writer.add(input)
writer.startWriting()
writer.startSession(atSourceTime: .zero)

for frame in 0..<frameCount {
    let t = CGFloat(frame) / CGFloat(FPS)
    var buffer: CVPixelBuffer?
    CVPixelBufferPoolCreatePixelBuffer(nil, adaptor.pixelBufferPool!, &buffer)
    let pb = buffer!
    CVPixelBufferLockBaseAddress(pb, [])
    ctx = CGContext(data: CVPixelBufferGetBaseAddress(pb), width: W, height: H, bitsPerComponent: 8,
                    bytesPerRow: CVPixelBufferGetBytesPerRow(pb), space: CGColorSpace(name: CGColorSpace.sRGB)!,
                    bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue)!
    ctx.interpolationQuality = .high
    NSGraphicsContext.current = NSGraphicsContext(cgContext: ctx, flipped: false)

    // each scene fades in over the one before it
    for (i, scene) in scenes.enumerated() {
        let local = t - starts[i]
        let visible = local >= 0 && (i == scenes.count - 1 || t < starts[i + 1] + fade)
        guard visible else { continue }
        let a = i == 0 ? 1 : easeInOut(progress(local, 0, fade))
        withAlpha(a) { scene.draw(local, durations[i]) }
    }
    let edge = min(progress(t, 0, 0.4), 1 - progress(t, total - 0.6, total))
    if edge < 1 { fill(R(0, 0, WF, HF), cream.withAlphaComponent(1 - edge)) }

    NSGraphicsContext.current = nil
    CVPixelBufferUnlockBaseAddress(pb, [])
    while !input.isReadyForMoreMediaData { usleep(2000) }
    adaptor.append(pb, withPresentationTime: CMTime(value: CMTimeValue(frame), timescale: CMTimeScale(FPS)))
    if frame % (FPS * 15) == 0 { print("frame \(frame)/\(frameCount)") }
}
input.markAsFinished()
let written = DispatchSemaphore(value: 0)
writer.finishWriting { written.signal() }
written.wait()
guard writer.status == .completed else { print("video failed: \(String(describing: writer.error))"); exit(1) }

// MARK: - add the voiceover

let composition = AVMutableComposition()
let video = AVURLAsset(url: silentURL)
let videoTrack = composition.addMutableTrack(withMediaType: .video, preferredTrackID: kCMPersistentTrackID_Invalid)!
try! videoTrack.insertTimeRange(CMTimeRange(start: .zero, duration: video.duration), of: video.tracks(withMediaType: .video)[0], at: .zero)
let audioTrack = composition.addMutableTrack(withMediaType: .audio, preferredTrackID: kCMPersistentTrackID_Invalid)!
for (i, url) in voiceURLs.enumerated() {
    let clip = AVURLAsset(url: url)
    let at = CMTime(seconds: Double(starts[i] + lead), preferredTimescale: 44100)
    let end = audioTrack.timeRange.end
    if end < at { audioTrack.insertEmptyTimeRange(CMTimeRange(start: end, end: at)) }
    try! audioTrack.insertTimeRange(CMTimeRange(start: .zero, duration: clip.duration), of: clip.tracks(withMediaType: .audio)[0], at: at)
}

try? FileManager.default.removeItem(at: outURL)
let export = AVAssetExportSession(asset: composition, presetName: AVAssetExportPresetHighestQuality)!
export.outputURL = outURL
export.outputFileType = .mp4
let exported = DispatchSemaphore(value: 0)
export.exportAsynchronously { exported.signal() }
exported.wait()
guard export.status == .completed else { print("export failed: \(String(describing: export.error))"); exit(1) }
try? FileManager.default.removeItem(at: silentURL)
print("wrote \(outURL.path), \(String(format: "%.1f", total))s with voiceover")
