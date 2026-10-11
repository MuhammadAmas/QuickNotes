// Screenshot notes: macOS's own selection tool (drag an area, Space for a window, Esc to cancel)
// or the whole screen, then a compressed JPEG small enough to live in a Firestore document, plus a thumbnail.

import AppKit
import ImageIO
import UniformTypeIdentifiers

struct Screenshot {
    let preview: NSImage
    let full: Data   // JPEG, kept under ~680 KB so it fits a Firestore document once base64-encoded
    let thumb: Data  // small JPEG shown in the notes list
    let width: Int
    let height: Int
}

enum CaptureMode {
    case area        // pick an area or window
    case fullScreen  // the whole display the mouse is on
}

enum ScreenshotTaker {
    /// True when macOS lets QuickNotes capture other apps' windows (Screen Recording permission).
    static var hasPermission: Bool { CGPreflightScreenCaptureAccess() }

    /// Adds QuickNotes to System Settings › Screen Recording and shows the system prompt (first time only).
    static func requestPermission() { _ = CGRequestScreenCaptureAccess() }

    /// Takes the screenshot; calls back on the main queue with nil if the user pressed Esc during selection.
    static func capture(_ mode: CaptureMode, _ done: @escaping (Screenshot?) -> Void) {
        let file = FileManager.default.temporaryDirectory.appendingPathComponent("quicknotes-\(UUID().uuidString).png")
        var args = ["-x"] // no shutter sound
        switch mode {
        case .area:
            args.append("-i") // interactive selection
        case .fullScreen:
            if let display = displayNumberUnderMouse() { args += ["-D", String(display)] } else { args.append("-m") }
        }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
        p.arguments = args + [file.path]
        p.terminationHandler = { _ in
            let shot = encode(file)
            try? FileManager.default.removeItem(at: file)
            DispatchQueue.main.async { done(shot) }
        }
        do { try p.run() } catch { done(nil) }
    }

    /// screencapture's 1-based display number for the screen under the mouse pointer.
    private static func displayNumberUnderMouse() -> Int? {
        let mouse = NSEvent.mouseLocation
        guard let screen = NSScreen.screens.first(where: { NSMouseInRect(mouse, $0.frame, false) }),
              let id = screen.deviceDescription[NSDeviceDescriptionKey("NSScreenNumber")] as? CGDirectDisplayID
        else { return nil }
        var count: UInt32 = 0
        CGGetActiveDisplayList(0, nil, &count)
        var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
        CGGetActiveDisplayList(count, &ids, &count)
        return ids.firstIndex(of: id).map { $0 + 1 }
    }

    static func encode(_ file: URL) -> Screenshot? {
        guard let source = CGImageSourceCreateWithURL(file as CFURL, nil),
              CGImageSourceGetCount(source) > 0 else { return nil }

        // shrink until the base64 copy fits comfortably inside Firestore's 1 MiB document limit
        var full: (data: Data, image: CGImage)?
        outer: for maxSide in [2400, 2000, 1600, 1280, 1024] { // 2400 keeps text readable in full-screen shots
            guard let image = scaled(source, maxSide: maxSide) else { return nil }
            for quality in [0.8, 0.68, 0.55] {
                if let data = jpeg(image, quality: quality), data.count < 680_000 {
                    full = (data, image)
                    break outer
                }
            }
        }
        guard let full, let thumbImage = scaled(source, maxSide: 480), let thumb = jpeg(thumbImage, quality: 0.55) else { return nil }
        return Screenshot(preview: NSImage(cgImage: full.image, size: .zero), full: full.data, thumb: thumb,
                          width: full.image.width, height: full.image.height)
    }

    private static func scaled(_ source: CGImageSource, maxSide: Int) -> CGImage? {
        CGImageSourceCreateThumbnailAtIndex(source, 0, [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceThumbnailMaxPixelSize: maxSide,
            kCGImageSourceCreateThumbnailWithTransform: true,
        ] as CFDictionary)
    }

    private static func jpeg(_ image: CGImage, quality: Double) -> Data? {
        let data = NSMutableData()
        guard let dest = CGImageDestinationCreateWithData(data, UTType.jpeg.identifier as CFString, 1, nil) else { return nil }
        CGImageDestinationAddImage(dest, image, [kCGImageDestinationLossyCompressionQuality: quality] as CFDictionary)
        return CGImageDestinationFinalize(dest) ? data as Data : nil
    }
}
