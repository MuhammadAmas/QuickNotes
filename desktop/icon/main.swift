// Renders the app icon PNG (see BubbleArt.swift):
//   make_icon out.png              transparent background (Mac app, web/Android)
//   make_icon out.png --backdrop   on a solid cream square (iOS home screen, which fills transparency with black)
import AppKit

let size: CGFloat = 1024
let backdrop = CommandLine.arguments.contains("--backdrop")
let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(size), pixelsHigh: Int(size), bitsPerSample: 8,
                           samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                           colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
if backdrop {
    NSColor(srgbRed: 0.969, green: 0.965, blue: 0.953, alpha: 1).setFill()
    NSRect(x: 0, y: 0, width: size, height: size).fill()
}
let diameter: CGFloat = backdrop ? 760 : 840
drawBubbleArt(in: NSRect(x: (size - diameter) / 2, y: (size - diameter) / 2 + 8, width: diameter, height: diameter), dropShadow: true)
NSGraphicsContext.restoreGraphicsState()
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
