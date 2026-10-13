// The quick-capture box that opens from the bubble, ⌘⇧N or ⌃⌥N.

import AppKit

// MARK: - Colours (same tokens as the dashboard's styles.css)

extension NSColor {
    convenience init(hex: UInt32, alpha: CGFloat = 1) {
        self.init(srgbRed: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
                  blue: CGFloat(hex & 0xFF) / 255, alpha: alpha)
    }

    static func dynamic(_ light: NSColor, _ dark: NSColor) -> NSColor {
        NSColor(name: nil) { $0.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua ? dark : light }
    }
}

enum Palette {
    static let surface = NSColor.dynamic(NSColor(hex: 0xFFFFFF), NSColor(hex: 0x1D1C1A))
    static let well = NSColor.dynamic(NSColor(hex: 0xF7F6F3), NSColor(hex: 0x141312))
    static let border = NSColor.dynamic(NSColor(hex: 0xE4E0D8), NSColor(hex: 0x34312D))
    static let text = NSColor.dynamic(NSColor(hex: 0x1C1B19), NSColor(hex: 0xEEEBE5))
    static let muted = NSColor.dynamic(NSColor(hex: 0x6B665E), NSColor(hex: 0xAAA49A))
    static let faint = NSColor.dynamic(NSColor(hex: 0xA19B91), NSColor(hex: 0x76716A))
    static let accent = NSColor.dynamic(NSColor(hex: 0xE0612A), NSColor(hex: 0xF07A43))
    static let success = NSColor.dynamic(NSColor(hex: 0x2F9E5B), NSColor(hex: 0x4CC27C))
    static let danger = NSColor.dynamic(NSColor(hex: 0xC93A31), NSColor(hex: 0xF06B62))
}

/// Rounded, layer-backed box whose colours re-resolve when light/dark mode changes.
final class BoxView: NSView {
    var fill: NSColor? { didSet { needsDisplay = true } }
    var stroke: NSColor? { didSet { needsDisplay = true } }
    var strokeWidth: CGFloat = 1 { didSet { needsDisplay = true } }
    var radius: CGFloat = 0 { didSet { needsDisplay = true } }

    init(fill: NSColor? = nil, stroke: NSColor? = nil, radius: CGFloat = 0) {
        super.init(frame: .zero)
        wantsLayer = true
        layerContentsRedrawPolicy = .onSetNeedsDisplay
        self.fill = fill
        self.stroke = stroke
        self.radius = radius
    }

    required init?(coder: NSCoder) { fatalError() }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        // AppKit sets the current appearance here, so dynamic colours resolve correctly
        layer?.cornerRadius = radius
        layer?.cornerCurve = .continuous
        layer?.backgroundColor = fill?.cgColor
        layer?.borderColor = stroke?.cgColor
        layer?.borderWidth = stroke == nil ? 0 : strokeWidth
    }
}

private func label(_ text: String, size: CGFloat, weight: NSFont.Weight = .regular, color: NSColor) -> NSTextField {
    let l = NSTextField(labelWithString: text)
    l.font = .systemFont(ofSize: size, weight: weight)
    l.textColor = color
    return l
}

/// A small keyboard key followed by what it does, e.g. [↵] Save
private func keyHint(_ key: String, _ action: String) -> NSView {
    let cap = BoxView(fill: Palette.well, stroke: Palette.border, radius: 5)
    let keyLabel = label(key, size: 10.5, weight: .semibold, color: Palette.muted)
    keyLabel.translatesAutoresizingMaskIntoConstraints = false
    cap.addSubview(keyLabel)
    NSLayoutConstraint.activate([
        keyLabel.leadingAnchor.constraint(equalTo: cap.leadingAnchor, constant: 5),
        keyLabel.trailingAnchor.constraint(equalTo: cap.trailingAnchor, constant: -5),
        keyLabel.centerYAnchor.constraint(equalTo: cap.centerYAnchor),
        cap.heightAnchor.constraint(equalToConstant: 18),
        cap.widthAnchor.constraint(greaterThanOrEqualToConstant: 20),
    ])
    let stack = NSStackView(views: [cap, label(action, size: 11.5, weight: .medium, color: Palette.faint)])
    stack.spacing = 5
    return stack
}

// MARK: - Panel

/// Can take keyboard focus without activating the app, so the meeting app stays in front.
final class CapturePanel: NSPanel {
    override var canBecomeKey: Bool { true }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        // the app is usually inactive while this panel is up, so route ⌘C/⌘V/... ourselves
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        if flags == .command || flags == [.command, .shift], let key = event.charactersIgnoringModifiers?.lowercased() {
            let action: Selector?
            switch (key, flags.contains(.shift)) {
            case ("x", false): action = #selector(NSText.cut(_:))
            case ("c", false): action = #selector(NSText.copy(_:))
            case ("v", false): action = #selector(NSText.paste(_:))
            case ("a", false): action = #selector(NSText.selectAll(_:))
            case ("z", false): action = Selector(("undo:"))
            case ("z", true): action = Selector(("redo:"))
            default: action = nil
            }
            if let action, NSApp.sendAction(action, to: nil, from: self) { return true }
        }
        return super.performKeyEquivalent(with: event)
    }
}

// MARK: - Controller

final class CaptureController: NSObject, NSTextFieldDelegate, NSWindowDelegate {
    let panel: CapturePanel
    var onOpenDashboard: (() -> Void)?
    /// Asks the app to hide this box, take the screenshot, then come back with `attach(_:)`.
    var onTakeScreenshot: ((CaptureMode) -> Void)?

    private let cloud: Cloud
    private let field = NSTextField()
    private let well = BoxView(fill: Palette.well, stroke: Palette.accent, radius: 11)
    private let dateChip = label("", size: 11.5, weight: .medium, color: Palette.muted)
    private let hints = NSStackView()
    private let status = label("", size: 12, weight: .semibold, color: Palette.success)
    private var wellHeight: NSLayoutConstraint!
    private let previewBox = BoxView(fill: Palette.well, stroke: Palette.border, radius: 10)
    private let previewImage = NSImageView()
    private var previewHeight: NSLayoutConstraint!
    private var previewGap: NSLayoutConstraint!
    private(set) var screenshot: Screenshot?
    private let voice = VoiceDictation()
    private let mic = NSButton()
    private var dictationBase = ""
    private static let previewSize: CGFloat = 170
    private var closeTimer: Timer?
    private var saving = false
    private var todayCount: Int?

    private static let width: CGFloat = 430
    private static let font = NSFont.systemFont(ofSize: 15)
    private static let fieldInset = NSEdgeInsets(top: 12, left: 13, bottom: 12, right: 13)
    private static let maxLines: CGFloat = 6
    /// everything around the text well: header + footer + paddings
    private static let chrome: CGFloat = 16 + 24 + 12 + 12 + 24 + 14
    private var previewExtra: CGFloat { screenshot == nil ? 0 : CaptureController.previewSize + 10 }

    init(cloud: Cloud) {
        self.cloud = cloud
        panel = CapturePanel(contentRect: NSRect(x: 0, y: 0, width: CaptureController.width, height: 150),
                             styleMask: [.borderless, .nonactivatingPanel],
                             backing: .buffered, defer: false)
        super.init()

        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.backgroundColor = .clear
        panel.isOpaque = false
        panel.hasShadow = true
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .ignoresCycle]
        panel.delegate = self

        let card = BoxView(fill: Palette.surface, stroke: Palette.border, radius: 16)
        panel.contentView = card

        // header: [mark] Quick note ............ (Sun, Oct 11 · 3 today)
        let mark = BoxView(fill: Palette.accent, radius: 12) // round, like the bubble
        let glyph = NSImageView()
        glyph.image = NSImage(systemSymbolName: "square.and.pencil", accessibilityDescription: nil)?
            .withSymbolConfiguration(.init(pointSize: 11, weight: .bold))
        glyph.contentTintColor = .white
        let title = label("Quick note", size: 13.5, weight: .semibold, color: Palette.text)
        let chip = BoxView(fill: Palette.well, radius: 10)

        // the text well
        field.font = CaptureController.font
        field.textColor = Palette.text
        field.placeholderAttributedString = NSAttributedString(string: "What’s on your mind?", attributes: [
            .font: CaptureController.font, .foregroundColor: Palette.faint,
        ])
        field.isBordered = false
        field.drawsBackground = false
        field.focusRingType = .none
        field.usesSingleLineMode = false
        field.cell?.wraps = true
        field.cell?.isScrollable = false
        field.lineBreakMode = .byWordWrapping
        field.delegate = self
        well.strokeWidth = 1.5

        // footer: Open all notes ↗ .............. [↵] Save  [⇧↵] New line  [esc] Close
        let open = NSButton(title: "All notes", target: self, action: #selector(openDashboard))
        open.isBordered = false
        open.image = NSImage(systemSymbolName: "arrow.up.right", accessibilityDescription: nil)?
            .withSymbolConfiguration(.init(pointSize: 10, weight: .bold))
        open.imagePosition = .imageTrailing
        open.imageHugsTitle = true
        open.contentTintColor = Palette.accent
        open.attributedTitle = NSAttributedString(string: "All notes ", attributes: [
            .font: NSFont.systemFont(ofSize: 12, weight: .semibold), .foregroundColor: Palette.accent,
        ])
        open.toolTip = "Open all notes"

        let camera = NSButton(title: "Screenshot", target: self, action: #selector(takeScreenshot))
        camera.isBordered = false
        camera.image = NSImage(systemSymbolName: "camera.viewfinder", accessibilityDescription: "Screenshot")?
            .withSymbolConfiguration(.init(pointSize: 12, weight: .semibold))
        camera.imagePosition = .imageLeading
        camera.imageHugsTitle = true
        camera.contentTintColor = Palette.accent
        camera.attributedTitle = NSAttributedString(string: " Screenshot ▾", attributes: [
            .font: NSFont.systemFont(ofSize: 12, weight: .semibold), .foregroundColor: Palette.accent,
        ])
        camera.toolTip = "Add a screenshot: an area (⌃⌥S) or the full screen (⌃⌥F)"

        mic.isBordered = false
        mic.image = NSImage(systemSymbolName: "mic.fill", accessibilityDescription: "Voice note")?
            .withSymbolConfiguration(.init(pointSize: 13, weight: .semibold))
        mic.contentTintColor = Palette.accent
        mic.target = self
        mic.action = #selector(micClicked)
        mic.toolTip = "Voice note: speak and it’s typed for you (⌃⌥V)"
        voice.onText = { [weak self] text in self?.showDictated(text) }
        voice.onStop = { [weak self] error in self?.dictationEnded(error) }

        // screenshot preview, shown above the text when one is attached
        previewImage.imageScaling = .scaleProportionallyUpOrDown
        // a 2000px screenshot must not stretch the box: let the preview shrink to fit
        for axis in [NSLayoutConstraint.Orientation.horizontal, .vertical] {
            previewImage.setContentCompressionResistancePriority(.init(1), for: axis)
            previewImage.setContentHuggingPriority(.init(1), for: axis)
        }
        previewImage.wantsLayer = true
        previewBox.layer?.masksToBounds = true
        let remove = NSButton(image: NSImage(systemSymbolName: "xmark.circle.fill", accessibilityDescription: "Remove screenshot")!
            .withSymbolConfiguration(.init(pointSize: 16, weight: .semibold))!, target: self, action: #selector(removeScreenshot))
        remove.isBordered = false
        remove.contentTintColor = NSColor(white: 0.15, alpha: 0.8)
        remove.toolTip = "Remove screenshot"
        previewBox.isHidden = true

        hints.setViews([keyHint("↵", "Save"), keyHint("esc", "Close")], in: .leading)
        hints.spacing = 12
        status.isHidden = true

        for v in [mark, glyph, title, chip, dateChip, well, field, open, camera, mic, hints, status, previewBox, previewImage, remove] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
        }
        mark.addSubview(glyph)
        chip.addSubview(dateChip)
        well.addSubview(field)
        previewBox.addSubview(previewImage)
        previewBox.addSubview(remove)
        for v in [mark, title, chip, previewBox, well, open, camera, mic, hints, status] as [NSView] { card.addSubview(v) }

        let inset = CaptureController.fieldInset
        wellHeight = well.heightAnchor.constraint(equalToConstant: 46)
        previewHeight = previewBox.heightAnchor.constraint(equalToConstant: 0)
        previewGap = well.topAnchor.constraint(equalTo: previewBox.bottomAnchor, constant: 0)
        NSLayoutConstraint.activate([
            mark.topAnchor.constraint(equalTo: card.topAnchor, constant: 16),
            mark.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 16),
            mark.widthAnchor.constraint(equalToConstant: 24),
            mark.heightAnchor.constraint(equalToConstant: 24),
            glyph.centerXAnchor.constraint(equalTo: mark.centerXAnchor, constant: 0.5),
            glyph.centerYAnchor.constraint(equalTo: mark.centerYAnchor),

            title.leadingAnchor.constraint(equalTo: mark.trailingAnchor, constant: 9),
            title.centerYAnchor.constraint(equalTo: mark.centerYAnchor),

            chip.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),
            chip.centerYAnchor.constraint(equalTo: mark.centerYAnchor),
            chip.heightAnchor.constraint(equalToConstant: 22),
            dateChip.leadingAnchor.constraint(equalTo: chip.leadingAnchor, constant: 9),
            dateChip.trailingAnchor.constraint(equalTo: chip.trailingAnchor, constant: -9),
            dateChip.centerYAnchor.constraint(equalTo: chip.centerYAnchor),

            previewBox.topAnchor.constraint(equalTo: mark.bottomAnchor, constant: 12),
            previewBox.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 12),
            previewBox.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -12),
            previewHeight,
            previewImage.topAnchor.constraint(equalTo: previewBox.topAnchor, constant: 6),
            previewImage.bottomAnchor.constraint(equalTo: previewBox.bottomAnchor, constant: -6),
            previewImage.leadingAnchor.constraint(equalTo: previewBox.leadingAnchor, constant: 6),
            previewImage.trailingAnchor.constraint(equalTo: previewBox.trailingAnchor, constant: -6),
            remove.topAnchor.constraint(equalTo: previewBox.topAnchor, constant: 6),
            remove.trailingAnchor.constraint(equalTo: previewBox.trailingAnchor, constant: -6),
            previewGap,
            well.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 12),
            well.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -12),
            wellHeight,
            field.topAnchor.constraint(equalTo: well.topAnchor, constant: inset.top),
            field.leadingAnchor.constraint(equalTo: well.leadingAnchor, constant: inset.left),
            field.trailingAnchor.constraint(equalTo: well.trailingAnchor, constant: -inset.right),
            field.bottomAnchor.constraint(equalTo: well.bottomAnchor, constant: -inset.bottom),

            open.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 14),
            open.topAnchor.constraint(equalTo: well.bottomAnchor, constant: 12),
            open.heightAnchor.constraint(equalToConstant: 24),
            camera.leadingAnchor.constraint(equalTo: open.trailingAnchor, constant: 14),
            camera.centerYAnchor.constraint(equalTo: open.centerYAnchor),
            mic.leadingAnchor.constraint(equalTo: camera.trailingAnchor, constant: 12),
            mic.centerYAnchor.constraint(equalTo: open.centerYAnchor),
            hints.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),
            hints.centerYAnchor.constraint(equalTo: open.centerYAnchor),
            status.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),
            status.centerYAnchor.constraint(equalTo: open.centerYAnchor),
        ])
        fitToText(keepTop: false)
    }

    var isVisible: Bool { panel.isVisible }

    func show(near anchor: NSRect?) {
        closeTimer?.invalidate()
        showHints()
        updateChip()
        todayCount = nil
        cloud.countNotes(on: isoDay()) { [weak self] n in
            self?.todayCount = n
            self?.updateChip()
        }
        fitToText(keepTop: false)

        let target = position(near: anchor)
        panel.setFrame(target.offsetBy(dx: 0, dy: -8), display: false)
        panel.alphaValue = 0
        panel.makeKeyAndOrderFront(nil)
        panel.makeFirstResponder(field)
        (field.currentEditor() as? NSTextView)?.insertionPointColor = Palette.accent
        NSAnimationContext.runAnimationGroup { ctx in
            ctx.duration = 0.18
            ctx.timingFunction = CAMediaTimingFunction(name: .easeOut)
            panel.animator().alphaValue = 1
            panel.animator().setFrame(target, display: true)
        }
    }

    func hide() {
        closeTimer?.invalidate()
        if voice.isListening { voice.stop() }
        guard panel.isVisible else { return }
        NSAnimationContext.runAnimationGroup({ ctx in
            ctx.duration = 0.12
            panel.animator().alphaValue = 0
        }, completionHandler: { [weak self] in
            guard let self, self.panel.alphaValue == 0 else { return } // reopened meanwhile
            self.panel.orderOut(nil)
        })
    }

    // MARK: layout

    private var currentText: String {
        (field.currentEditor() as? NSTextView)?.string ?? field.stringValue
    }

    /// Grows the box with the note (up to a few lines) instead of leaving empty space.
    private func fitToText(keepTop: Bool) {
        let inset = CaptureController.fieldInset
        let width = CaptureController.width - 24 - inset.left - inset.right - 4
        var text = currentText
        if text.isEmpty || text.hasSuffix("\n") { text += "X" }
        let lineHeight = ceil(CaptureController.font.ascender - CaptureController.font.descender + CaptureController.font.leading)
        let measured = ceil(NSAttributedString(string: text, attributes: [.font: CaptureController.font])
            .boundingRect(with: NSSize(width: width, height: .greatestFiniteMagnitude),
                          options: [.usesLineFragmentOrigin, .usesFontLeading]).height)
        let textHeight = min(max(measured, lineHeight), lineHeight * CaptureController.maxLines)
        let newWell = textHeight + 4 + inset.top + inset.bottom // the cell pads its text by ~2pt top and bottom
        let wantedHeight = CaptureController.chrome + newWell + previewExtra
        guard abs(wellHeight.constant - newWell) > 0.5 || abs(panel.frame.height - wantedHeight) > 0.5 || !keepTop else { return }
        wellHeight.constant = newWell

        let height = CaptureController.chrome + newWell + previewExtra
        var frame = panel.frame
        if keepTop { frame.origin.y = frame.maxY - height }
        frame.size = NSSize(width: CaptureController.width, height: height)
        panel.setFrame(frame, display: true)
        panel.invalidateShadow()
    }

    private func position(near anchor: NSRect?) -> NSRect {
        let size = panel.frame.size
        let screen = anchor.flatMap { a in NSScreen.screens.first { $0.frame.intersects(a) } }
            ?? NSScreen.screens.first { $0.frame.contains(NSEvent.mouseLocation) } ?? NSScreen.main
        guard let visible = screen?.visibleFrame else { return panel.frame }
        var origin: NSPoint
        if let a = anchor {
            // open beside the bubble, on whichever side has room, tops roughly aligned
            let leftSide = a.midX > visible.midX
            origin = NSPoint(x: leftSide ? a.minX - size.width + 4 : a.maxX - 4, y: a.maxY - size.height - 8)
        } else {
            origin = NSPoint(x: visible.midX - size.width / 2, y: visible.maxY - size.height - visible.height * 0.2)
        }
        origin.x = min(max(origin.x, visible.minX + 8), visible.maxX - size.width - 8)
        origin.y = min(max(origin.y, visible.minY + 8), visible.maxY - size.height - 8)
        return NSRect(origin: origin, size: size)
    }

    // MARK: footer states

    func updateChip() {
        var text = prettyDay()
        if !cloud.isSignedIn { text += "  ·  not signed in" } else if let n = todayCount, n > 0 { text += "  ·  \(n) today" }
        dateChip.stringValue = text
    }

    private func showHints() {
        hints.isHidden = false
        status.isHidden = true
        well.stroke = Palette.accent
    }

    private func showStatus(_ text: String, color: NSColor) {
        status.stringValue = text
        status.textColor = color
        status.isHidden = false
        hints.isHidden = true
    }

    // MARK: voice notes

    @objc private func micClicked() { toggleDictation() }

    /// Starts or stops dictation into the text box (the ⌃⌥V shortcut and the 🎤 button).
    func toggleDictation() {
        if voice.isListening { return voice.stop() }
        closeTimer?.invalidate()
        let current = currentText.trimmingCharacters(in: .whitespacesAndNewlines)
        dictationBase = current.isEmpty ? "" : current + " "
        well.stroke = Palette.danger
        mic.contentTintColor = Palette.danger
        mic.image = NSImage(systemSymbolName: "stop.circle.fill", accessibilityDescription: "Stop")?
            .withSymbolConfiguration(.init(pointSize: 14, weight: .semibold))
        showStatus("●  Listening… ⌃⌥V or Enter to stop", color: Palette.danger)
        voice.start()
    }

    private func showDictated(_ text: String) {
        let full = dictationBase + text
        if let editor = field.currentEditor() as? NSTextView {
            editor.string = full
            editor.setSelectedRange(NSRange(location: (full as NSString).length, length: 0))
        } else {
            field.stringValue = full
        }
        fitToText(keepTop: true)
    }

    private func dictationEnded(_ error: String?) {
        mic.contentTintColor = Palette.accent
        mic.image = NSImage(systemSymbolName: "mic.fill", accessibilityDescription: "Voice note")?
            .withSymbolConfiguration(.init(pointSize: 13, weight: .semibold))
        if let error {
            well.stroke = Palette.danger
            showStatus(error, color: Palette.danger)
        } else if !saving {
            showHints()
        }
        panel.makeFirstResponder(field)
    }

    // MARK: screenshot

    /// Shows the box with a screenshot attached, ready for an optional description.
    func attach(_ shot: Screenshot, near anchor: NSRect?) {
        screenshot = shot
        previewImage.image = shot.preview
        previewBox.isHidden = false
        previewHeight.constant = CaptureController.previewSize
        previewGap.constant = 10
        setPlaceholder("Add a description (optional), then press Enter")
        if isVisible {
            fitToText(keepTop: true)
            panel.makeKeyAndOrderFront(nil)
            panel.makeFirstResponder(field)
        } else {
            show(near: anchor)
        }
    }

    @objc private func removeScreenshot() {
        clearScreenshot()
        fitToText(keepTop: true)
        panel.makeFirstResponder(field)
    }

    private func clearScreenshot() {
        screenshot = nil
        previewImage.image = nil
        previewBox.isHidden = true
        previewHeight.constant = 0
        previewGap.constant = 0
        setPlaceholder("What’s on your mind?")
    }

    private func setPlaceholder(_ s: String) {
        field.placeholderAttributedString = NSAttributedString(string: s, attributes: [
            .font: CaptureController.font, .foregroundColor: Palette.faint,
        ])
    }

    /// The 📷 button: choose an area or the whole screen.
    @objc private func takeScreenshot(_ sender: NSButton) {
        let menu = NSMenu()
        let area = menu.addItem(withTitle: "Select Area…", action: #selector(captureArea), keyEquivalent: "s")
        area.keyEquivalentModifierMask = [.control, .option]
        area.image = NSImage(systemSymbolName: "rectangle.dashed", accessibilityDescription: nil)
        let full = menu.addItem(withTitle: "Full Screen", action: #selector(captureFullScreen), keyEquivalent: "f")
        full.keyEquivalentModifierMask = [.control, .option]
        full.image = NSImage(systemSymbolName: "display", accessibilityDescription: nil)
        for item in menu.items { item.target = self }
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: sender.bounds.height + 4), in: sender)
    }

    @objc private func captureArea() { onTakeScreenshot?(.area) }
    @objc private func captureFullScreen() { onTakeScreenshot?(.fullScreen) }

    // MARK: actions

    @objc private func openDashboard() {
        hide()
        onOpenDashboard?()
    }

    private func save() {
        if voice.isListening {
            // Enter while speaking: stop, wait for the last words, then save
            voice.stop()
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { [weak self] in self?.save() }
            return
        }
        let text = currentText.trimmingCharacters(in: .whitespacesAndNewlines)
        let shot = screenshot
        guard !text.isEmpty || shot != nil else { return hide() }
        guard !saving else { return }
        saving = true
        showStatus("Saving…", color: Palette.muted)
        let finished: (SaveResult) -> Void = { [weak self] result in
            guard let self else { return }
            self.saving = false
            if self.currentText.trimmingCharacters(in: .whitespacesAndNewlines) == text {
                self.field.stringValue = ""
            }
            if shot != nil { self.clearScreenshot() }
            self.fitToText(keepTop: true)
            var linger = 1.1
            switch result {
            case .synced:
                self.todayCount = (self.todayCount ?? 0) + 1
                self.updateChip()
                self.well.stroke = Palette.success
                self.showStatus("✓  Saved to today", color: Palette.success)
            case .queued(let message):
                // kept on this Mac and uploaded later, so nothing is lost
                self.well.stroke = Palette.accent
                self.showStatus("✓  \(message)", color: Palette.muted)
                linger = 2.2
            }
            self.closeTimer = Timer.scheduledTimer(withTimeInterval: linger, repeats: false) { [weak self] _ in
                self?.hide()
            }
        }
        if let shot {
            cloud.saveScreenshotNote(text, shot, finished)
        } else {
            cloud.saveNote(text, finished)
        }
    }

    // MARK: NSTextFieldDelegate / NSWindowDelegate

    func control(_ control: NSControl, textView: NSTextView, doCommandBy selector: Selector) -> Bool {
        switch selector {
        case #selector(NSResponder.insertNewline(_:)):
            if NSApp.currentEvent?.modifierFlags.contains(.shift) == true {
                textView.insertNewlineIgnoringFieldEditor(nil)
                fitToText(keepTop: true)
            } else {
                save()
            }
            return true
        case #selector(NSResponder.insertNewlineIgnoringFieldEditor(_:)):
            textView.insertNewlineIgnoringFieldEditor(nil)
            fitToText(keepTop: true)
            return true
        case #selector(NSResponder.cancelOperation(_:)):
            if screenshot != nil { clearScreenshot(); fitToText(keepTop: true) }
            hide()
            return true
        default:
            return false
        }
    }

    func controlTextDidChange(_ obj: Notification) {
        closeTimer?.invalidate() // typing again after a save keeps the box open
        if !saving && hints.isHidden { showHints() }
        fitToText(keepTop: true)
    }

    func windowDidResignKey(_ notification: Notification) {
        // clicking elsewhere closes an empty box; a half-written note stays put
        if currentText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && screenshot == nil && !saving {
            hide()
        }
    }
}
