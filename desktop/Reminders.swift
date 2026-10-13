// Daily reminders: a small card beside the bubble at 9:00 (what's on today), 18:00 (what's left,
// with "Move all to tomorrow") and Friday 17:00 (the week in numbers). Drawn by the app itself,
// so it needs no notification permission and matches the quick-note box.

import AppKit

enum ReminderKind: String, CaseIterable {
    case morning, evening, weekly

    var hour: Int { self == .morning ? 9 : self == .evening ? 18 : 17 }
    var title: String {
        switch self {
        case .morning: return "Morning summary (9:00)"
        case .evening: return "End-of-day check (18:00)"
        case .weekly: return "Friday recap (17:00)"
        }
    }
    var enabledKey: String { "reminder.\(rawValue).enabled" }
    var lastShownKey: String { "reminder.\(rawValue).lastShown" }
}

final class ReminderCenter: NSObject {
    private let cloud: Cloud
    private let anchor: () -> NSRect?
    private let card = ReminderCard()
    private var timer: Timer?
    var onOpenNotes: ((String) -> Void)? // hash route, e.g. "#/summary"

    init(cloud: Cloud, anchor: @escaping () -> NSRect?) {
        self.cloud = cloud
        self.anchor = anchor
        super.init()
        UserDefaults.standard.register(defaults: Dictionary(uniqueKeysWithValues: ReminderKind.allCases.map { ($0.enabledKey, true) }))
    }

    func start() {
        timer = Timer.scheduledTimer(withTimeInterval: 30, repeats: true) { [weak self] _ in self?.tick() }
        DispatchQueue.main.asyncAfter(deadline: .now() + 5) { [weak self] in self?.tick() }
    }

    func isEnabled(_ kind: ReminderKind) -> Bool { UserDefaults.standard.bool(forKey: kind.enabledKey) }
    func toggle(_ kind: ReminderKind) { UserDefaults.standard.set(!isEnabled(kind), forKey: kind.enabledKey) }

    /// Shows a reminder at its time, once per day; catches up within 3 hours if the Mac was asleep or off.
    private func tick() {
        guard cloud.isSignedIn else { return }
        let now = Date()
        let cal = Calendar.current
        let hour = cal.component(.hour, from: now)
        let today = isoDay(now)
        for kind in ReminderKind.allCases where isEnabled(kind) {
            if kind == .weekly && cal.component(.weekday, from: now) != 6 { continue } // Friday
            guard hour >= kind.hour, hour < kind.hour + 3,
                  UserDefaults.standard.string(forKey: kind.lastShownKey) != today else { continue }
            UserDefaults.standard.set(today, forKey: kind.lastShownKey)
            show(kind)
            return // one card at a time
        }
    }

    /// Builds the card from the current notes and shows it (also used by "Show now" in the menu).
    func show(_ kind: ReminderKind) {
        cloud.myNotes { [weak self] notes in
            guard let self, let notes else { return }
            let today = isoDay()
            let openToday = notes.filter { $0.status == "open" && $0.noteDate == today }
            let overdue = notes.filter { $0.status == "open" && $0.noteDate < today }
            let doneToday = notes.filter { $0.status == "done" && $0.completedAt.map { isoDay(Date.fromISO($0)) } == today }

            switch kind {
            case .morning:
                let body = openToday.isEmpty && overdue.isEmpty
                    ? "Nothing planned yet. Press ⌘⇧N to jot down today’s first task."
                    : [openToday.isEmpty ? nil : "\(openToday.count) open today",
                       overdue.isEmpty ? nil : "\(overdue.count) overdue from earlier"].compactMap { $0 }.joined(separator: " · ")
                self.card.show(title: "Good morning ☀️", body: body, items: (overdue + openToday).prefix(4).map(\.content),
                               primary: ("Open today", { self.onOpenNotes?("") }), secondary: nil, near: self.anchor())

            case .evening:
                if openToday.isEmpty {
                    let body = doneToday.isEmpty ? "No open notes left for today." : "You finished \(doneToday.count) today. Nice work."
                    self.card.show(title: "All done for today 🎉", body: body, items: [], primary: nil, secondary: nil,
                                   near: self.anchor(), autoClose: 10)
                    return
                }
                let tomorrow = isoDay(Calendar.current.date(byAdding: .day, value: 1, to: Date())!)
                self.card.show(title: "Wrapping up", body: "\(openToday.count) still open today · \(doneToday.count) done",
                               items: openToday.prefix(4).map(\.content),
                               primary: ("Move all to tomorrow", {
                                   self.cloud.move(openToday, to: tomorrow) { ok in
                                       self.card.confirm(ok ? "✓ Moved \(openToday.count) to tomorrow" : "Couldn’t move them. Try from the app.")
                                   }
                               }),
                               secondary: ("Review", { self.onOpenNotes?("") }), near: self.anchor(), keepOpen: true)

            case .weekly:
                let weekAgo = Calendar.current.date(byAdding: .day, value: -7, to: Date())!
                let inWeek = { (iso: String?) in iso.map { Date.fromISO($0) > weekAgo } ?? false }
                let done = notes.filter { $0.status == "done" && inWeek($0.completedAt) }.count
                let added = notes.filter { inWeek($0.createdAt) }.count
                let open = notes.filter { $0.status == "open" && $0.noteDate <= today }.count
                let slipping = notes.filter { $0.status == "open" && $0.timesMoved >= 2 }
                var body = "\(done) done · \(added) added · \(open) still open"
                if !slipping.isEmpty { body += " · \(slipping.count) keep slipping" }
                self.card.show(title: "Your week", body: body, items: slipping.prefix(3).map { "↻ \($0.content)" },
                               primary: ("See weekly summary", { self.onOpenNotes?("#/summary") }), secondary: nil, near: self.anchor())
            }
        }
    }
}

extension Date {
    static func fromISO(_ s: String) -> Date {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = f.date(from: s) { return d }
        f.formatOptions = [.withInternetDateTime]
        return f.date(from: s) ?? .distantPast
    }
}

// MARK: - the card

final class ReminderCard: NSObject {
    private let panel: NSPanel
    private let card = BoxView(fill: Palette.surface, stroke: Palette.border, radius: 16)
    private let titleLabel = NSTextField(labelWithString: "")
    private let bodyLabel = NSTextField(wrappingLabelWithString: "")
    private let itemsLabel = NSTextField(wrappingLabelWithString: "")
    private let buttons = NSStackView()
    private var actions: [(run: () -> Void, keepOpen: Bool)] = []
    private var closeTimer: Timer?
    private static let width: CGFloat = 340

    override init() {
        panel = NSPanel(contentRect: NSRect(x: 0, y: 0, width: ReminderCard.width, height: 160),
                        styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: false)
        super.init()
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.hidesOnDeactivate = false
        panel.backgroundColor = .clear
        panel.isOpaque = false
        panel.hasShadow = true
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .ignoresCycle]
        panel.contentView = card

        let mark = BoxView(fill: Palette.accent, radius: 11)
        let glyph = NSImageView(image: NSImage(systemSymbolName: "bell.fill", accessibilityDescription: nil)!
            .withSymbolConfiguration(.init(pointSize: 10, weight: .bold))!)
        glyph.contentTintColor = .white
        titleLabel.font = .systemFont(ofSize: 14, weight: .semibold)
        titleLabel.textColor = Palette.text
        bodyLabel.font = .systemFont(ofSize: 13)
        bodyLabel.textColor = Palette.muted
        itemsLabel.font = .systemFont(ofSize: 12.5)
        itemsLabel.textColor = Palette.text
        itemsLabel.maximumNumberOfLines = 4
        buttons.spacing = 8
        let close = NSButton(image: NSImage(systemSymbolName: "xmark", accessibilityDescription: "Dismiss")!
            .withSymbolConfiguration(.init(pointSize: 10, weight: .bold))!, target: self, action: #selector(dismiss))
        close.isBordered = false
        close.contentTintColor = Palette.faint

        let stack = NSStackView(views: [bodyLabel, itemsLabel, buttons])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        for v in [mark, glyph, titleLabel, close, stack] as [NSView] { v.translatesAutoresizingMaskIntoConstraints = false }
        mark.addSubview(glyph)
        for v in [mark, titleLabel, close, stack] as [NSView] { card.addSubview(v) }
        NSLayoutConstraint.activate([
            mark.topAnchor.constraint(equalTo: card.topAnchor, constant: 14),
            mark.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 14),
            mark.widthAnchor.constraint(equalToConstant: 22), mark.heightAnchor.constraint(equalToConstant: 22),
            glyph.centerXAnchor.constraint(equalTo: mark.centerXAnchor), glyph.centerYAnchor.constraint(equalTo: mark.centerYAnchor),
            titleLabel.leadingAnchor.constraint(equalTo: mark.trailingAnchor, constant: 9),
            titleLabel.centerYAnchor.constraint(equalTo: mark.centerYAnchor),
            close.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -12),
            close.centerYAnchor.constraint(equalTo: mark.centerYAnchor),
            stack.topAnchor.constraint(equalTo: mark.bottomAnchor, constant: 10),
            stack.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 16),
            stack.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -16),
            stack.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -14),
            card.widthAnchor.constraint(equalToConstant: ReminderCard.width),
        ])
    }

    /// `keepOpen`: the primary button reports back with `confirm(_:)` instead of closing the card.
    func show(title: String, body: String, items: [String], primary: (String, () -> Void)?, secondary: (String, () -> Void)?,
              near anchor: NSRect?, autoClose: TimeInterval? = nil, keepOpen: Bool = false) {
        closeTimer?.invalidate()
        titleLabel.stringValue = title
        bodyLabel.stringValue = body
        itemsLabel.stringValue = items.map { "• \($0.replacingOccurrences(of: "\n", with: " "))" }.joined(separator: "\n")
        itemsLabel.isHidden = items.isEmpty
        actions = []
        buttons.setViews([], in: .leading)
        for (i, action) in [primary, secondary].compactMap({ $0 }).enumerated() {
            let b = NSButton(title: action.0, target: self, action: #selector(run(_:)))
            b.tag = i
            b.bezelStyle = .rounded
            b.controlSize = .regular
            if i == 0 { b.keyEquivalent = "\r"; b.bezelColor = Palette.accent }
            actions.append((action.1, i == 0 && keepOpen))
            buttons.addArrangedSubview(b)
        }
        buttons.isHidden = actions.isEmpty

        card.layoutSubtreeIfNeeded()
        let size = NSSize(width: ReminderCard.width, height: card.fittingSize.height)
        panel.setFrame(NSRect(origin: origin(for: size, near: anchor), size: size), display: true)
        panel.alphaValue = 0
        panel.orderFrontRegardless()
        NSAnimationContext.runAnimationGroup { ctx in
            ctx.duration = 0.2
            panel.animator().alphaValue = 1
        }
        if let autoClose { closeTimer = Timer.scheduledTimer(withTimeInterval: autoClose, repeats: false) { [weak self] _ in self?.dismiss() } }
    }

    /// Replaces the buttons with a short confirmation, then closes.
    func confirm(_ text: String) {
        bodyLabel.stringValue = text
        itemsLabel.isHidden = true
        buttons.isHidden = true
        card.layoutSubtreeIfNeeded()
        var frame = panel.frame
        let h = card.fittingSize.height
        frame.origin.y += frame.height - h
        frame.size.height = h
        panel.setFrame(frame, display: true)
        closeTimer = Timer.scheduledTimer(withTimeInterval: 2.5, repeats: false) { [weak self] _ in self?.dismiss() }
    }

    @objc private func run(_ sender: NSButton) {
        let action = actions[sender.tag]
        if !action.keepOpen { dismiss() }
        action.run()
    }

    @objc func dismiss() {
        closeTimer?.invalidate()
        NSAnimationContext.runAnimationGroup({ ctx in
            ctx.duration = 0.15
            panel.animator().alphaValue = 0
        }, completionHandler: { [weak self] in self?.panel.orderOut(nil) })
    }

    private func origin(for size: NSSize, near anchor: NSRect?) -> NSPoint {
        let screen = anchor.flatMap { a in NSScreen.screens.first { $0.frame.intersects(a) } } ?? NSScreen.main
        let visible = screen?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        var p: NSPoint
        if let a = anchor {
            let leftSide = a.midX > visible.midX
            p = NSPoint(x: leftSide ? a.minX - size.width + 4 : a.maxX - 4, y: a.minY - size.height + 12)
        } else {
            p = NSPoint(x: visible.maxX - size.width - 16, y: visible.maxY - size.height - 16)
        }
        p.x = min(max(p.x, visible.minX + 8), visible.maxX - size.width - 8)
        p.y = min(max(p.y, visible.minY + 8), visible.maxY - size.height - 8)
        return p
    }
}
