// The glossy orange bubble with a pencil: used for the floating bubble and the app icon.
import AppKit

/// Draws the bubble filling `bubble` in the current graphics context.
func drawBubbleArt(in bubble: NSRect, dropShadow: Bool) {
    let d = bubble.width
    let circle = NSBezierPath(ovalIn: bubble)

    if dropShadow {
        NSGraphicsContext.saveGraphicsState()
        let shadow = NSShadow()
        shadow.shadowColor = NSColor(srgbRed: 0.45, green: 0.15, blue: 0.02, alpha: 0.35)
        shadow.shadowOffset = NSSize(width: 0, height: -d * 0.021)
        shadow.shadowBlurRadius = d * 0.043
        shadow.set()
        NSColor(srgbRed: 0.85, green: 0.38, blue: 0.17, alpha: 1).setFill()
        circle.fill()
        NSGraphicsContext.restoreGraphicsState()
    }

    // body
    NSGradient(starting: NSColor(srgbRed: 0.98, green: 0.56, blue: 0.30, alpha: 1),
               ending: NSColor(srgbRed: 0.80, green: 0.30, blue: 0.13, alpha: 1))!.draw(in: circle, angle: -65)

    // warm glow along the lower edge, like light passing through a bubble
    NSGraphicsContext.saveGraphicsState()
    circle.addClip()
    NSGradient(colors: [NSColor(white: 1, alpha: 0), NSColor(srgbRed: 1, green: 0.75, blue: 0.5, alpha: 0.35)],
               atLocations: [0.55, 1.0], colorSpace: .sRGB)!
        .draw(in: circle, relativeCenterPosition: NSPoint(x: 0, y: 0.15))
    NSGraphicsContext.restoreGraphicsState()

    // glossy sheen across the top and a small specular highlight
    let sheen = NSBezierPath(ovalIn: NSRect(x: bubble.minX + d * 0.16, y: bubble.minY + d * 0.52, width: d * 0.68, height: d * 0.42))
    NSGradient(starting: NSColor(white: 1, alpha: 0.42), ending: NSColor(white: 1, alpha: 0))!.draw(in: sheen, angle: -90)
    NSColor(white: 1, alpha: 0.55).setFill()
    NSBezierPath(ovalIn: NSRect(x: bubble.minX + d * 0.2, y: bubble.minY + d * 0.70, width: d * 0.11, height: d * 0.075)).fill()

    // thin bright rim
    let rim = NSBezierPath(ovalIn: bubble.insetBy(dx: d * 0.007, dy: d * 0.007))
    rim.lineWidth = max(1, d * 0.012)
    NSColor(white: 1, alpha: 0.5).setStroke()
    rim.stroke()

    // pencil
    let config = NSImage.SymbolConfiguration(pointSize: d * 0.40, weight: .semibold).applying(.init(paletteColors: [.white]))
    if let glyph = NSImage(systemSymbolName: "square.and.pencil", accessibilityDescription: nil)?.withSymbolConfiguration(config) {
        let s = glyph.size
        glyph.draw(in: NSRect(x: bubble.midX - s.width / 2 + d * 0.015, y: bubble.midY - s.height / 2 - d * 0.01,
                              width: s.width, height: s.height))
    }
}

/// The bubble as an image (no drop shadow; the floating bubble's layer adds its own).
func bubbleImage(diameter: CGFloat) -> NSImage {
    NSImage(size: NSSize(width: diameter, height: diameter), flipped: false) { rect in
        drawBubbleArt(in: rect, dropShadow: false)
        return true
    }
}
