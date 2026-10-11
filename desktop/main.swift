// QuickNotes for Mac: a swinging floating bubble for quick capture. Notes are
// saved to Firestore (see Cloud.swift); the full notes app is the web app.

import AppKit
import Carbon.HIToolbox
import ServiceManagement

enum Defaults {
    static let bubbleOrigin = "bubbleOrigin"
    static let bubbleHidden = "bubbleHidden"
    static let swing = "swing"
    static let launchedBefore = "launchedBefore"
}

func isoDay(_ date: Date = Date()) -> String {
    let f = DateFormatter()
    f.calendar = Calendar(identifier: .gregorian)
    f.locale = Locale(identifier: "en_US_POSIX")
    f.timeZone = .current
    f.dateFormat = "yyyy-MM-dd"
    return f.string(from: date)
}

func prettyDay(_ date: Date = Date()) -> String {
    let f = DateFormatter()
    f.setLocalizedDateFormatFromTemplate("EEEMMMd")
    return f.string(from: date)
}

// MARK: - Floating bubble

final class BubblePanel: NSPanel {
    init(size: CGFloat) {
        super.init(contentRect: NSRect(x: 0, y: 0, width: size, height: size),
                   styleMask: [.borderless, .nonactivatingPanel],
                   backing: .buffered, defer: false)
        isFloatingPanel = true
        level = .floating
        hidesOnDeactivate = false
        backgroundColor = .clear
        isOpaque = false
        hasShadow = false
        collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary, .ignoresCycle]
    }

    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

final class BubbleView: NSView {
    var onClick: (() -> Void)?
    var onMoved: (() -> Void)?
    var contextMenu: (() -> NSMenu)?

    private let swingLayer = CALayer()
    private var dragStart = NSPoint.zero
    private var originStart = NSPoint.zero
    private var dragged = false
    static let diameter: CGFloat = 58

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        layer?.masksToBounds = false

        let d = BubbleView.diameter
        let circle = CGRect(x: (frame.width - d) / 2, y: (frame.height - d) / 2, width: d, height: d)

        // hang the bubble from its top edge so it swings like a pendulum
        swingLayer.bounds = CGRect(origin: .zero, size: circle.size)
        swingLayer.anchorPoint = CGPoint(x: 0.5, y: 1.0)
        swingLayer.position = CGPoint(x: circle.midX, y: circle.maxY)
        swingLayer.shadowColor = NSColor.black.cgColor
        swingLayer.shadowOpacity = 0.28
        swingLayer.shadowRadius = 6
        swingLayer.shadowOffset = CGSize(width: 0, height: -2)
        swingLayer.shadowPath = CGPath(ellipseIn: swingLayer.bounds, transform: nil)

        // the same glossy bubble as the app icon, rendered for this screen's resolution
        let scale = NSScreen.main?.backingScaleFactor ?? 2
        swingLayer.contents = bubbleImage(diameter: d).layerContents(forContentsScale: scale)
        swingLayer.contentsScale = scale
        swingLayer.contentsGravity = .resizeAspect
        layer?.addSublayer(swingLayer)
        toolTip = "Click to add a note · drag to move · right-click for options"
    }

    required init?(coder: NSCoder) { fatalError() }

    func setSwinging(_ on: Bool) {
        swingLayer.removeAnimation(forKey: "swing")
        guard on else { return }
        // a short damped swing, then a rest, repeated: noticeable but not distracting
        let swing = CAKeyframeAnimation(keyPath: "transform.rotation.z")
        swing.values = [0, 0.18, -0.14, 0.09, -0.05, 0.02, 0, 0] // radians; keeps the bubble inside its window
        swing.keyTimes = [0, 0.07, 0.16, 0.25, 0.33, 0.40, 0.46, 1]
        swing.timingFunctions = Array(repeating: CAMediaTimingFunction(name: .easeInEaseOut), count: 7)
        swing.duration = 5.5
        swing.repeatCount = .infinity
        swingLayer.add(swing, forKey: "swing")
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func mouseDown(with event: NSEvent) {
        if event.modifierFlags.contains(.control) { return showMenu(event) }
        dragStart = NSEvent.mouseLocation
        originStart = window?.frame.origin ?? .zero
        dragged = false
    }

    override func mouseDragged(with event: NSEvent) {
        let p = NSEvent.mouseLocation
        let dx = p.x - dragStart.x, dy = p.y - dragStart.y
        if !dragged && hypot(dx, dy) < 3 { return }
        dragged = true
        window?.setFrameOrigin(NSPoint(x: originStart.x + dx, y: originStart.y + dy))
    }

    override func mouseUp(with event: NSEvent) {
        if event.modifierFlags.contains(.control) { return }
        if dragged { onMoved?() } else { onClick?() }
    }

    override func rightMouseDown(with event: NSEvent) { showMenu(event) }

    private func showMenu(_ event: NSEvent) {
        guard let menu = contextMenu?() else { return }
        NSMenu.popUpContextMenu(menu, with: event, for: self)
    }
}

// MARK: - Global hotkeys (⌘⇧N / ⌃⌥N note, ⌃⌥S screenshot), via Carbon so no Accessibility permission is needed

enum HotKey: UInt32 { case note = 1, noteAlt = 2, screenshot = 3 }
var hotKeyAction: ((HotKey) -> Void)?

func registerHotKeys() {
    var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
    InstallEventHandler(GetApplicationEventTarget(), { _, event, _ in
        var id = EventHotKeyID()
        GetEventParameter(event, EventParamName(kEventParamDirectObject), EventParamType(typeEventHotKeyID),
                          nil, MemoryLayout<EventHotKeyID>.size, nil, &id)
        if let key = HotKey(rawValue: id.id) { DispatchQueue.main.async { hotKeyAction?(key) } }
        return noErr
    }, 1, &spec, nil, nil)

    // ⌘⇧N takes over that combination in every app while QuickNotes runs
    let shortcuts: [(HotKey, Int, Int, String)] = [
        (.noteAlt, kVK_ANSI_N, controlKey | optionKey, "⌃⌥N"),
        (.note, kVK_ANSI_N, cmdKey | shiftKey, "⌘⇧N"),
        (.screenshot, kVK_ANSI_S, controlKey | optionKey, "⌃⌥S"),
    ]
    for (key, code, modifiers, label) in shortcuts {
        var ref: EventHotKeyRef?
        let id = EventHotKeyID(signature: OSType(0x514E_5445), id: key.rawValue) // 'QNTE'
        let status = RegisterEventHotKey(UInt32(code), UInt32(modifiers), id, GetApplicationEventTarget(), 0, &ref)
        if status != noErr { NSLog("QuickNotes: \(label) is already taken by another app (\(status))") }
    }
}

// MARK: - App

final class AppDelegate: NSObject, NSApplicationDelegate {
    let cloud = Cloud()
    lazy var capture = CaptureController(cloud: cloud)
    let bubble = BubblePanel(size: 80)
    var bubbleView: BubbleView!
    private var retryTimer: Timer?

    func applicationDidFinishLaunching(_ notification: Notification) {
        UserDefaults.standard.register(defaults: [Defaults.swing: true])
        NSApp.mainMenu = buildMainMenu()

        bubbleView = BubbleView(frame: NSRect(x: 0, y: 0, width: 80, height: 80))
        bubbleView.onClick = { [weak self] in self?.toggleCapture() }
        bubbleView.onMoved = { [weak self] in self?.saveBubblePosition() }
        bubbleView.contextMenu = { [weak self] in self?.buildBubbleMenu() ?? NSMenu() }
        bubble.contentView = bubbleView
        bubble.setFrameOrigin(initialBubbleOrigin())
        bubbleView.setSwinging(UserDefaults.standard.bool(forKey: Defaults.swing))
        if !UserDefaults.standard.bool(forKey: Defaults.bubbleHidden) { bubble.orderFrontRegardless() }

        capture.onOpenDashboard = { [weak self] in self?.showDashboard() }
        cloud.anchorWindow = bubble
        capture.onTakeScreenshot = { [weak self] in self?.takeScreenshot() }
        hotKeyAction = { [weak self] key in key == .screenshot ? self?.takeScreenshot() : self?.toggleCapture() }
        registerHotKeys()

        NotificationCenter.default.addObserver(forName: NSApplication.didChangeScreenParametersNotification,
                                               object: nil, queue: .main) { [weak self] _ in self?.keepBubbleOnScreen() }

        // anything saved while offline or signed out goes up as soon as it can
        retryTimer = Timer.scheduledTimer(withTimeInterval: 120, repeats: true) { [weak self] _ in self?.cloud.flushPending() }

        if cloud.isSignedIn {
            cloud.recordUser()
            cloud.importLegacyNotes { [weak self] in self?.cloud.flushPending() }
        } else {
            promptSignIn()
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showDashboard()
        return true
    }

    func applicationDockMenu(_ sender: NSApplication) -> NSMenu? { buildBubbleMenu(includeQuit: false) }

    // MARK: bubble

    func toggleCapture() {
        if capture.isVisible { return capture.hide() }
        capture.show(near: bubble.isVisible ? bubble.frame : nil)
    }

    private func initialBubbleOrigin() -> NSPoint {
        if let saved = UserDefaults.standard.string(forKey: Defaults.bubbleOrigin) {
            let p = NSPointFromString(saved)
            let rect = NSRect(origin: p, size: bubble.frame.size)
            if NSScreen.screens.contains(where: { $0.visibleFrame.intersects(rect.insetBy(dx: 20, dy: 20)) }) { return p }
        }
        let v = NSScreen.main?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        return NSPoint(x: v.maxX - bubble.frame.width - 16, y: v.midY)
    }

    private func saveBubblePosition() {
        UserDefaults.standard.set(NSStringFromPoint(bubble.frame.origin), forKey: Defaults.bubbleOrigin)
    }

    private func keepBubbleOnScreen() {
        let f = bubble.frame
        if !NSScreen.screens.contains(where: { $0.visibleFrame.intersects(f.insetBy(dx: 20, dy: 20)) }) {
            UserDefaults.standard.removeObject(forKey: Defaults.bubbleOrigin)
            bubble.setFrameOrigin(initialBubbleOrigin())
        }
    }

    @objc func toggleBubble() {
        let hidden = bubble.isVisible
        hidden ? bubble.orderOut(nil) : bubble.orderFrontRegardless()
        if hidden { capture.hide() }
        UserDefaults.standard.set(hidden, forKey: Defaults.bubbleHidden)
    }

    @objc func toggleSwing() {
        let on = !UserDefaults.standard.bool(forKey: Defaults.swing)
        UserDefaults.standard.set(on, forKey: Defaults.swing)
        bubbleView.setSwinging(on)
    }

    @objc func newNote() { toggleCapture() }

    // MARK: screenshot notes

    /// Hides the bubble and the note box, lets the user pick an area, then reopens the box with it attached.
    @objc func takeScreenshot() {
        guard ScreenshotTaker.hasPermission else { return askForScreenRecording() }
        let bubbleWasVisible = bubble.isVisible
        let boxWasVisible = capture.isVisible
        capture.panel.orderOut(nil)
        bubble.orderOut(nil)
        ScreenshotTaker.capture { [weak self] shot in
            guard let self else { return }
            if bubbleWasVisible { self.bubble.orderFrontRegardless() }
            let anchor = self.bubble.isVisible ? self.bubble.frame : nil
            if let shot {
                self.capture.attach(shot, near: anchor)
            } else if boxWasVisible {
                self.capture.show(near: anchor) // Esc during selection: back to where you were
            }
        }
    }

    private func askForScreenRecording() {
        ScreenshotTaker.requestPermission()
        let a = NSAlert()
        a.messageText = "Allow QuickNotes to take screenshots"
        a.informativeText = "Turn on QuickNotes in System Settings › Privacy & Security › Screen & System Audio Recording, "
            + "then quit and reopen QuickNotes. Your screen is only captured when you take a screenshot note."
        a.addButton(withTitle: "Open System Settings")
        a.addButton(withTitle: "Cancel")
        NSApp.activate(ignoringOtherApps: true)
        if a.runModal() == .alertFirstButtonReturn,
           let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture") {
            NSWorkspace.shared.open(url)
        }
    }

    @objc func toggleLoginItem() {
        let service = SMAppService.mainApp
        do {
            if service.status == .enabled { try service.unregister() } else { try service.register() }
        } catch {
            alert("Couldn’t change the login setting.",
                  "Add QuickNotes yourself in System Settings › General › Login Items.\n\n\(error.localizedDescription)")
        }
    }

    // MARK: account

    private func promptSignIn() {
        let a = NSAlert()
        a.messageText = "Sign in to sync your notes"
        a.informativeText = "QuickNotes saves to your account so your notes show up on your phone and in the web app. "
            + "Until you sign in, notes from the bubble are kept on this Mac."
        a.addButton(withTitle: "Sign In with Google")
        a.addButton(withTitle: "Later")
        NSApp.activate(ignoringOtherApps: true)
        if a.runModal() == .alertFirstButtonReturn { signIn() }
    }

    @objc func signIn() {
        NSApp.activate(ignoringOtherApps: true)
        cloud.signIn { [weak self] error in
            if let error { self?.alert("Sign-in didn’t finish.", error) }
        }
    }

    @objc func signOut() {
        cloud.signOut()
    }

    // MARK: dashboard (the web app)

    @objc func showDashboard() {
        NSWorkspace.shared.open(CloudConfig.webURL)
    }

    // MARK: menus

    private func buildBubbleMenu(includeQuit: Bool = true) -> NSMenu {
        let menu = NSMenu()
        menu.addItem(withTitle: "New Note   ⌘⇧N  or  ⌃⌥N", action: #selector(newNote), keyEquivalent: "")
        menu.addItem(withTitle: "Screenshot Note   ⌃⌥S", action: #selector(takeScreenshot), keyEquivalent: "")
        menu.addItem(withTitle: "Open All Notes", action: #selector(showDashboard), keyEquivalent: "")
        menu.addItem(.separator())
        if let s = cloud.session {
            menu.addItem(withTitle: "Signed in as \(s.email.isEmpty ? "…" : s.email)", action: nil, keyEquivalent: "").isEnabled = false
            if cloud.pendingCount > 0 {
                menu.addItem(withTitle: "\(cloud.pendingCount) note(s) waiting to sync", action: nil, keyEquivalent: "").isEnabled = false
            }
            menu.addItem(withTitle: "Sign Out", action: #selector(signOut), keyEquivalent: "")
        } else {
            menu.addItem(withTitle: "Sign In with Google…", action: #selector(signIn), keyEquivalent: "")
        }
        menu.addItem(.separator())
        let swing = menu.addItem(withTitle: "Swing", action: #selector(toggleSwing), keyEquivalent: "")
        swing.state = UserDefaults.standard.bool(forKey: Defaults.swing) ? .on : .off
        menu.addItem(withTitle: bubble.isVisible ? "Hide Bubble" : "Show Bubble", action: #selector(toggleBubble), keyEquivalent: "")
        let login = menu.addItem(withTitle: "Open at Login", action: #selector(toggleLoginItem), keyEquivalent: "")
        login.state = SMAppService.mainApp.status == .enabled ? .on : .off
        if includeQuit {
            menu.addItem(.separator())
            menu.addItem(withTitle: "Quit QuickNotes", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "")
        }
        for item in menu.items where item.action != nil && item.action != #selector(NSApplication.terminate(_:)) {
            item.target = self
        }
        return menu
    }

    private func buildMainMenu() -> NSMenu {
        let main = NSMenu()

        let appItem = main.addItem(withTitle: "QuickNotes", action: nil, keyEquivalent: "")
        let appMenu = NSMenu(title: "QuickNotes")
        appMenu.addItem(withTitle: "About QuickNotes", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "Hide QuickNotes", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        appMenu.addItem(withTitle: "Quit QuickNotes", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu

        let fileItem = main.addItem(withTitle: "File", action: nil, keyEquivalent: "")
        let fileMenu = NSMenu(title: "File")
        fileMenu.addItem(withTitle: "New Quick Note", action: #selector(newNote), keyEquivalent: "n").target = self
        fileMenu.addItem(withTitle: "New Screenshot Note", action: #selector(takeScreenshot), keyEquivalent: "").target = self
        fileMenu.addItem(withTitle: "Open All Notes", action: #selector(showDashboard), keyEquivalent: "o").target = self
        fileMenu.addItem(withTitle: "Show/Hide Bubble", action: #selector(toggleBubble), keyEquivalent: "b").target = self
        fileItem.submenu = fileMenu

        let editItem = main.addItem(withTitle: "Edit", action: nil, keyEquivalent: "")
        let editMenu = NSMenu(title: "Edit")
        editMenu.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        editMenu.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
        editMenu.addItem(.separator())
        editMenu.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = editMenu

        return main
    }

    private func alert(_ title: String, _ text: String) {
        let a = NSAlert()
        a.messageText = title
        a.informativeText = text
        NSApp.activate(ignoringOtherApps: true)
        a.runModal()
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
