import QtQuick
import QtQuick.Controls as QQC
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Membraid in the bar: one brain across every harness and every machine.
//
// Every number comes from a `membraid ... --json` command and every button runs
// a `membraid` command. The widget owns no memory of its own: if the panel and
// the CLI ever disagree, the CLI is right, and anything here can also be done
// from a terminal.
Panel {
  id: root
  moduleName: "shockalotti.membraid"
  ipcTarget: "shockalotti.membraid"
  manageIpc: false

  // The bar sizes each slot from the widget's implicit size. Without these the
  // widget loads, gets placed, raises no error, and occupies zero pixels.
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property color urgent: bar && bar.urgent ? bar.urgent : Color.urgent
  readonly property color amber: bar && bar.warning ? bar.warning : "#b98a3c"
  // Icon severity, red wins: broken plumbing (sync, binary, orphans) is red,
  // unfinished business (open or stale tasks) is yellow, neither is dim.
  readonly property bool hasUrgent: root.syncFailed || root.binMissing || status.unscoped > 0
  readonly property bool hasAction: root.doing.length > 0 || root.staleCount > 0
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property string home: Quickshell.env("HOME") || ""
  // Quickshell does not inherit a login shell's PATH, so the binary is found
  // rather than assumed. A blank setting means the go install location.
  // The install rewrites the first candidate to the true installed path; the
  // rest cover where Go puts it next. If the binary moves again, the probe
  // below finds it - and if nothing resolves, the panel says so loudly
  // instead of showing an empty widget.
  readonly property var binCandidates: [
    home + "/go/bin/membraid",
    home + "/.local/share/go/bin/membraid",
    "/usr/local/bin/membraid"
  ]
  property string binResolved: ""
  property bool binMissing: false
  property bool binProbed: false
  readonly property string binary: {
    var configured = setting("binary", "")
    if (configured !== "") return configured
    if (root.binResolved !== "") return root.binResolved
    return root.binCandidates[0]
  }
  // First executable wins: explicit candidates, then whatever is on PATH.
  // On success everything reloads against the resolved binary; on failure
  // binMissing drives the banner in attention(), never a silent empty panel.
  function probeBinary() {
    var args = ["sh", "-c", 'for b in "$@"; do [ -x "$b" ] && { echo "$b"; exit 0; }; done; command -v membraid 2>/dev/null; exit 0', "membraid-probe"].concat(root.binCandidates)
    binProbeProcess.command = args
    binProbeProcess.running = true
  }

  // ---------- State, all read from membraid ----------
  readonly property var tabs: [
    { id: "overview", label: "Overview" },
    { id: "memories", label: "Memories" },
    { id: "projects", label: "Projects" },
    { id: "insights", label: "Insights" },
    { id: "settings", label: "Settings" }
  ]
  property string tab: "overview"
  property var status: ({})
  property var memories: []
  property var projects: []
  property var insights: ({})
  property var config: ({})
  property var harnesses: []
  property var updateInfo: ({})
  property double updateCheckedMs: 0
  property bool everLoaded: false
  property double nowMs: Date.now()
  property string actionError: ""

  // Memories tab
  property string memScope: ""
  property string memKind: ""
  property int memLimit: 20
  property bool rememberOpen: false
  property string rememberKind: "preference"
  property string rememberScope: "shared"
  property string correctingId: ""
  property string confirmForgetId: ""
  // A memory whose scope resolves to no project (an orphan), waiting for the
  // user to pick the project it belongs to. Empty = not picking for anyone.
  property string attachProjectId: ""

  // Settings tab: ranking tuning
  property bool tuningOpen: false

  // Projects tab: knowledge locations
  property var sources: []
  property string addingSourceScope: ""
  property bool sourceLogin: false
  property var preview: []

  readonly property var doing: status.doing || []
  readonly property var sync: status.sync || ({})
  readonly property var search: status.search || ({})
  readonly property bool syncFailed: !!sync.last_error
  readonly property int staleCount: status.stale_tasks ? Object.keys(status.stale_tasks).length : 0
  readonly property string vaultPath: status.vault || (home + "/.membraid/vault")
  // Typing in a field must not also drive the panel's keyboard shortcuts.
  readonly property bool typing: panel.activeFocusItem ? panel.activeFocusItem.placeholderText !== undefined : false

  // ---------- Helpers ----------
  function ago(iso) {
    if (!iso) return ""
    var s = Math.max(0, Math.round((root.nowMs - Date.parse(iso)) / 1000))
    if (s < 45) return "just now"
    if (s < 3600) return Math.round(s / 60) + "m ago"
    if (s < 86400) return Math.round(s / 3600) + "h ago"
    return Math.round(s / 86400) + "d ago"
  }

  function projectName(scope) {
    if (scope === "shared") return "shared"
    for (var i = 0; i < root.projects.length; i++)
      if (root.projects[i].scope === scope) return root.projects[i].name
    return scope
  }

  // An orphan is a memory no project reads: the unscoped quarantine bucket,
  // or a scope that matches no known project. Shared is a legitimate
  // workspace-wide scope, not an orphan. The Move picker stays available on
  // every row for voluntary moves.
  function isScoped(m) {
    if (!m || !m.scope || m.scope === "unscoped") return false
    for (var i = 0; i < root.projects.length; i++)
      if (root.projects[i].scope === m.scope) return true
    return false
  }

  function kindLabel(kind) {
    return ({ preference: "preference", project_param: "project value", insight: "insight", task_state: "task" })[kind] || kind
  }

  function syncLine() {
    if (!root.everLoaded) return "reading memory..."
    if (!sync.enabled) return "Auto-sync is off"
    if (sync.last_error) return "Sync failed: " + sync.last_error
    if (sync.last_success) return "Synced " + ago(sync.last_success)
    if (sync.last_skipped) return "Not syncing: " + sync.last_skipped
    return "Not synced yet"
  }

  function searchLine() {
    if (search.error) return "Search by meaning is unavailable: " + search.error
    if (search.mode === "vector") return "Search by meaning: " + search.embedded + " of " + search.current + " memories embedded"
    return "Search by keywords (embeddings are off)"
  }

  // Things worth a look, most urgent first. Empty most of the time.
  function attention() {
    var out = []
    if (root.binMissing) out.push({ text: "membraid binary not found (looked on PATH and in " + root.binCandidates.join(", ") + ") - set it in plugin settings or re-run membraid install", urgent: true })
    if (root.syncFailed) out.push({ text: "Sync failed: " + sync.last_error, urgent: true })
    if (search.mode === "vector" && search.embedded < search.current)
      out.push({ text: (search.current - search.embedded) + " memories are not searchable by meaning yet", urgent: false, action: ["embed", "--quiet"], actionLabel: "Embed now" })
    if (root.staleCount > 0) out.push({ text: root.staleCount + " open task" + (root.staleCount === 1 ? " has" : "s have") + " not been touched in 14+ days", urgent: false })
    if (status.unscoped > 0) out.push({ text: status.unscoped + (status.unscoped === 1 ? " memory has" : " memories have") + " no project, so no project reads " + (status.unscoped === 1 ? "it" : "them") + ": an agent is running where no project can be worked out. See Projects, unscoped.", urgent: true })
    for (var i = 0; i < root.projects.length; i++) {
      var p = root.projects[i]
      if (p.missing && p.memories > 0)
        out.push({ text: p.name + " has " + p.memories + " memories but its folder is gone. If it moved, run `membraid rescope --from " + p.scope + "` in the new folder.", urgent: false })
    }
    if (root.updateInfo.newer) out.push({ text: "membraid " + root.updateInfo.latest + " is available", urgent: false, terminal: [root.binary, "update"], actionLabel: "Update" })
    return out
  }

  // ---------- Loading ----------
  function refreshStatus() { if (!statusProcess.running) statusProcess.running = true }
  function load(proc) { if (!proc.running) proc.running = true }

  function loadTab() {
    root.nowMs = Date.now()
    if (!root.binProbed) { root.binProbed = true; root.probeBinary() }
    refreshStatus()
    load(projectsProcess)
    if (root.tab === "projects") load(sourcesProcess)
    if (root.tab === "memories") loadMemories()
    else if (root.tab === "insights") load(insightsProcess)
    else if (root.tab === "settings") { load(configProcess); load(harnessProcess); checkUpdates(false); loadPreview() }
  }

  function loadMemories() {
    var q = searchField.text.trim()
    var cmd = [root.binary]
    if (q !== "") {
      cmd = cmd.concat(["search", q, "--json", "--no-track", "-n", "30", "--scope", root.memScope !== "" ? root.memScope : "*"])
    } else {
      cmd = cmd.concat(["memories", "--json", "-n", String(root.memLimit)])
      if (root.memScope !== "") cmd = cmd.concat(["--scope", root.memScope])
      if (root.memKind !== "") cmd = cmd.concat(["--kind", root.memKind])
    }
    if (memoriesProcess.running) { memoriesProcess.again = true; return }
    memoriesProcess.command = cmd
    memoriesProcess.running = true
  }

  // At most once an hour unless asked: it is a request to GitHub.
  function checkUpdates(force) {
    if (!force && Date.now() - root.updateCheckedMs < 3600 * 1000) return
    root.updateCheckedMs = Date.now()
    load(updateProcess)
  }

  // Ranking presets. Each is a halflife and a repeat-use boost; digest settings
  // are left as they are.
  readonly property var presets: [
    { value: "recent", label: "Favour recent", halflife: 14, boost: 0.5 },
    { value: "balanced", label: "Balanced", halflife: 30, boost: 1 },
    { value: "long", label: "Favour long-lived", halflife: 90, boost: 1.5 }
  ]
  function presetOf() {
    for (var i = 0; i < root.presets.length; i++) {
      var p = root.presets[i]
      if (Number(root.config.halflife_days) === p.halflife && Math.abs(Number(root.config.frequency_boost) - p.boost) < 0.001) return p.value
    }
    return ""
  }
  function applyPreset(value) {
    for (var i = 0; i < root.presets.length; i++) {
      var p = root.presets[i]
      if (p.value !== value) continue
      root.runAction(["config", "set", "halflife_days", String(p.halflife)])
      root.runAction(["config", "set", "frequency_boost", String(p.boost)])
    }
  }

  // A live search with its score parts, so a change of setting can be judged
  // by how real results reorder. Never counts as use.
  function loadPreview() {
    var q = tuneQuery.text.trim()
    if (q === "") { root.preview = []; return }
    if (previewProcess.running) { previewProcess.again = true; return }
    previewProcess.command = [root.binary, "search", q, "--json", "--no-track", "--scope", "*", "-n", "6"]
    previewProcess.running = true
  }

  function sourcesFor(scope) {
    return root.sources.filter(function(s) { return s.scope === scope })
  }

  function startAddingSource(scope) {
    root.addingSourceScope = scope
    root.confirmForgetId = ""
    flick.contentY = 0
    Qt.callLater(function() { sourceFolderField.forceActiveFocus() })
  }

  // What membraid will record the input as. A hint only: membraid itself
  // decides when saving, and a folder inside a git repo becomes the repo.
  function sourceTypeOf(text) {
    var t = text.trim()
    if (t === "") return ""
    if (/^(git@|ssh:\/\/|git:\/\/)/.test(t)) return "git"
    var m = t.match(/^https?:\/\/(?:[^@\/]*@)?([^\/:?#]+)[^\/?#]*(\/[^?#]*)?/i)
    if (m) {
      var host = m[1].toLowerCase()
      var p = m[2] || ""
      var segs = p.split("/").filter(function(x) { return x !== "" })
      var gitHost = ["github.com", "gitlab.com", "codeberg.org", "bitbucket.org"].indexOf(host) >= 0
      if (/\.git$/.test(p) || (gitHost && segs.length === 2)) return "git"
      return "web"
    }
    return "folder"
  }

  function saveSource() {
    var target = sourceFolderField.text.trim()
    var about = sourceAboutField.text.trim()
    if (target === "" || about === "") return
    var args = ["source", "add", target, "--about", about, "--scope", root.addingSourceScope, "--source", "user"]
    if (root.sourceTypeOf(target) === "web" && root.sourceLogin) args.push("--login")
    root.runAction(args)
    sourceFolderField.text = ""
    sourceAboutField.text = ""
    root.sourceLogin = false
    root.addingSourceScope = ""
    keyCatcher.forceActiveFocus()
  }

  // Where a location is and who can reach it, for the Projects list.
  function sourceWhere(s) {
    if (s.type === "folder") return "Folder  " + s.path + (s.here ? "" : "  (only on " + s.host + ")")
    if (s.type === "git") return "Git repo  " + s.repo + (s.subdir ? "  /" + s.subdir : "")
                                 + (s.here ? "  (checked out here)" : s.checkout ? "  (checked out on " + s.host + ")" : "")
    if (s.type === "web") return "Web page  " + s.url + (s.private ? "  (private network)"
                                 : s.login ? "  (needs " + (s.service || "a login") + ")" : "  (public)")
    return ""
  }

  function selectTab(id) {
    if (root.tab === id) return
    root.tab = id
    root.correctingId = ""
    root.confirmForgetId = ""
    flick.contentY = 0
    loadTab()
  }

  function stepTab(direction) {
    var i = 0
    for (var j = 0; j < root.tabs.length; j++) if (root.tabs[j].id === root.tab) i = j
    selectTab(root.tabs[(i + direction + root.tabs.length) % root.tabs.length].id)
  }

  // Actions run one after another, then everything visible reloads, so what
  // you see is what happened.
  property var actionQueue: []
  function runAction(args) {
    root.actionQueue = root.actionQueue.concat([args])
    if (!actionProcess.running) nextAction()
  }
  function nextAction() {
    if (root.actionQueue.length === 0) { loadTab(); return }
    var args = root.actionQueue[0]
    root.actionQueue = root.actionQueue.slice(1)
    root.actionError = ""
    actionProcess.command = [root.binary].concat(args)
    actionProcess.running = true
  }

  // A floating terminal, for commands that ask questions or show progress.
  function inTerminal(argv) {
    var quoted = argv.map(function(a) { return "'" + String(a).replace(/'/g, "'\\''") + "'" }).join(" ")
    Quickshell.execDetached(["bash", "-lc", "omarchy-launch-floating-terminal-with-presentation " + quoted])
    root.close()
  }

  function openPath(path) {
    Quickshell.execDetached(["xdg-open", path])
    root.close()
  }

  // Settings change as a control moves; wait until it stops before saving.
  property var pendingSettings: ({})
  function queueSetting(key, value) {
    var p = root.pendingSettings
    p[key] = String(value)
    root.pendingSettings = p
    settingTimer.restart()
  }

  // ---------- Processes ----------
  Process {
    id: binProbeProcess
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        var hit = text.trim().split("\n")[0] || ""
        if (hit !== "") {
          root.binResolved = hit
          root.binMissing = false
          root.loadTab()
        } else {
          root.binMissing = true
        }
      }
    }
  }

  Process {
    id: statusProcess
    // "--scope *": the bar is not standing in any project, so it shows all of them.
    command: [root.binary, "status", "--json", "--scope", "*"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try { root.status = JSON.parse(text) } catch (e) {}
        root.everLoaded = true
      }
    }
  }

  Process {
    id: projectsProcess
    command: [root.binary, "projects", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.projects = JSON.parse(text) || [] } catch (e) {} }
    }
  }

  Process {
    id: memoriesProcess
    property bool again: false
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.memories = JSON.parse(text) || [] } catch (e) { root.memories = [] } }
    }
    onExited: if (again) { again = false; root.loadMemories() }
  }

  Process {
    id: insightsProcess
    command: [root.binary, "insights", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.insights = JSON.parse(text) } catch (e) {} }
    }
  }

  Process {
    id: configProcess
    command: [root.binary, "config", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.config = JSON.parse(text) } catch (e) {} }
    }
  }

  Process {
    id: harnessProcess
    command: [root.binary, "install", "--list"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.harnesses = JSON.parse(text) || [] } catch (e) {} }
    }
  }

  Process {
    id: updateProcess
    command: [root.binary, "update", "--check", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.updateInfo = JSON.parse(text) } catch (e) {} }
    }
  }

  Process {
    id: sourcesProcess
    command: [root.binary, "source", "list", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.sources = JSON.parse(text) || [] } catch (e) {} }
    }
  }

  // The desktop's folder chooser. The panel may close while it has focus; the
  // folder is still filled in when the panel is opened again.
  Process {
    id: browseProcess
    command: ["bash", "-lc", "omarchy-file-select --directory --title 'Knowledge location'"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        var picked = text.trim().split("\n")[0]
        if (picked !== "") sourceFolderField.text = picked
      }
    }
  }

  Process {
    id: previewProcess
    property bool again: false
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: { try { root.preview = JSON.parse(text) || [] } catch (e) { root.preview = [] } }
    }
    onExited: if (again) { again = false; root.loadPreview() }
  }

  Process {
    id: actionProcess
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: if (text.trim() !== "") root.actionError = text.trim().split("\n").pop()
    }
    onExited: function(exitCode) { root.nextAction() }
  }

  Timer {
    id: settingTimer
    interval: 700
    onTriggered: {
      var p = root.pendingSettings
      root.pendingSettings = ({})
      for (var k in p) root.runAction(["config", "set", k, p[k]])
    }
  }

  Timer {
    id: previewTimer
    interval: 400
    onTriggered: root.loadPreview()
  }

  Timer {
    id: searchTimer
    interval: 350
    onTriggered: root.loadMemories()
  }

  Timer {
    interval: Math.max(10, root.setting("refreshIntervalSec", 60)) * 1000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: { root.nowMs = Date.now(); root.refreshStatus(); root.load(projectsProcess) }
  }

  onOpenedChanged: if (opened) {
    root.loadTab()
    Qt.callLater(function() { keyCatcher.forceActiveFocus() })
  }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.loadTab(); return "ok" }
    function show(tab: string): void { root.selectTab(tab); root.open() }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "\udb82\uddd1" // U+F09D1 nf-md-brain. Escaped, never literal: a literal glyph has been mangled on the way into this file twice.
    active: root.hasUrgent || root.hasAction
    activeColor: root.hasUrgent ? root.urgent : root.amber
    onPressed: function(buttonCode) { root.toggle() }
  }

  // ---------- Reusable pieces ----------
  component Link: Text {
    id: link
    signal activated()
    property bool danger: false
    color: linkMouse.containsMouse ? (danger ? root.urgent : root.foreground) : root.dim
    font.family: root.fontFamily
    font.pixelSize: Style.font.bodySmall
    MouseArea {
      id: linkMouse
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: link.activated()
    }
  }

  component Caption: Text {
    color: root.dim
    font.family: root.fontFamily
    font.pixelSize: Style.font.caption
    wrapMode: Text.WordWrap
  }

  component Body: Text {
    color: root.foreground
    font.family: root.fontFamily
    font.pixelSize: Style.font.body
    wrapMode: Text.WordWrap
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(460))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(640))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      blocked: root.typing
      onCloseRequested: root.close()
      onActivateRequested: root.loadTab()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.loadTab()
        if (t === "/" ) { root.selectTab("memories"); Qt.callLater(function() { searchField.forceActiveFocus() }) }
      }
      onMoveRequested: function(dx, dy) {
        if (dx !== 0) root.stepTab(dx)
        if (dy !== 0)
          flick.contentY = Math.max(0, Math.min(flick.contentY + dy * Style.space(56),
                                                Math.max(0, flick.contentHeight - flick.height)))
      }

      Flickable {
        id: flick
        anchors.fill: parent
        contentWidth: width
        contentHeight: column.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        QQC.ScrollBar.vertical: QQC.ScrollBar { policy: QQC.ScrollBar.AsNeeded }

        Column {
          id: column
          width: flick.width
          spacing: Style.space(10)

          PanelHero {
            title: "Membraid"
            meta: root.everLoaded
                  ? ((root.status.stats ? root.status.stats.current : 0) + " memories, "
                     + root.projects.filter(function(p) { return p.scope !== "shared" }).length + " projects  -  " + (root.status.host || ""))
                  : "reading memory..."
            foreground: root.foreground
            fontFamily: root.fontFamily
          }

          Row {
            id: tabRow
            width: parent.width
            spacing: Style.spacing.sm
            readonly property real cellWidth: (width - spacing * (root.tabs.length - 1)) / root.tabs.length
            Repeater {
              model: root.tabs
              Button {
                width: tabRow.cellWidth
                text: modelData.label
                selected: root.tab === modelData.id
                bordered: true
                foreground: root.foreground
                fontFamily: root.fontFamily
                fontSize: Style.font.bodySmall
                verticalPadding: Style.spacing.controlPaddingY
                onClicked: root.selectTab(modelData.id)
              }
            }
          }

          Text {
            visible: root.actionError !== ""
            width: parent.width
            wrapMode: Text.WordWrap
            color: root.urgent
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            text: root.actionError
          }

          // ================= Overview =================
          Column {
            visible: root.tab === "overview"
            width: parent.width
            spacing: Style.space(8)

            Caption { width: parent.width; color: root.syncFailed ? root.urgent : root.dim; text: root.syncLine() }
            Caption { width: parent.width; text: root.searchLine() }

            PanelSectionHeader {
              visible: root.attention().length > 0
              text: "Worth a look"
              foreground: root.foreground
              fontFamily: root.fontFamily
            }
            Repeater {
              model: root.attention()
              Column {
                width: column.width
                spacing: Style.space(2)
                Body { width: parent.width; color: modelData.urgent ? root.urgent : root.foreground; text: modelData.text }
                Link {
                  visible: !!modelData.actionLabel
                  text: modelData.actionLabel || ""
                  onActivated: modelData.terminal ? root.inTerminal(modelData.terminal) : root.runAction(modelData.action)
                }
              }
            }

            PanelSectionHeader {
              text: "Where you left off"
              foreground: root.foreground
              fontFamily: root.fontFamily
            }
            // Click a task to mark it finished: without this, a task written
            // without a key stayed here forever.
            Repeater {
              model: root.doing
              Text {
                width: column.width
                wrapMode: Text.WordWrap
                color: taskMouse.containsMouse ? root.dim : root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.body
                font.strikeout: taskMouse.containsMouse
                text: modelData.content + "  -  " + (modelData.scope_name || root.projectName(modelData.scope)) + ", via " + modelData.source
                MouseArea {
                  id: taskMouse
                  anchors.fill: parent
                  hoverEnabled: true
                  cursorShape: Qt.PointingHandCursor
                  onClicked: root.runAction(["done", "--id", modelData.id, "--scope", modelData.scope])
                }
              }
            }
            Caption {
              width: parent.width
              text: root.doing.length > 0 ? "click a task to mark it done" : (root.everLoaded ? "No open tasks. Your agents record them when work is left unfinished." : "")
            }

            Row {
              spacing: Style.space(16)
              Link { text: "Sync now"; onActivated: root.runAction(["sync", "--quiet"]) }
              Link { text: "Open vault"; onActivated: root.openPath(root.vaultPath) }
            }
          }

          // ================= Memories =================
          Column {
            visible: root.tab === "memories"
            width: parent.width
            spacing: Style.space(8)

            Link {
              visible: !root.rememberOpen
              text: "+ Remember something"
              font.pixelSize: Style.font.body
              onActivated: { root.rememberOpen = true; Qt.callLater(function() { rememberField.forceActiveFocus() }) }
            }

            Column {
              visible: root.rememberOpen
              width: parent.width
              spacing: Style.space(6)

              TextField {
                id: rememberField
                width: parent.width
                placeholderText: "What should every agent know?"
                foreground: root.foreground
                Keys.onEscapePressed: { root.rememberOpen = false; keyCatcher.forceActiveFocus() }
                onAccepted: rememberSave.activate()
              }
              ButtonGroup {
                options: [
                  { value: "preference", label: "Preference" },
                  { value: "project_param", label: "Project value" },
                  { value: "insight", label: "Insight" },
                  { value: "task_state", label: "Task" }
                ]
                value: root.rememberKind
                foreground: root.foreground
                fontFamily: root.fontFamily
                fontSize: Style.font.bodySmall
                focusable: false
                onChanged: function(v) { root.rememberKind = v }
              }
              Dropdown {
                label: "Applies to"
                showLabel: false
                width: parent.width
                value: root.rememberScope
                options: root.projects.map(function(p) { return { value: p.scope, label: p.scope === "shared" ? "Every project (shared)" : p.name } })
                onChanged: function(v) { root.rememberScope = v }
              }
              TextField {
                id: rememberKey
                width: parent.width
                placeholderText: "Key, optional: e.g. deploy.target (a later note with the same key replaces this one)"
                foreground: root.foreground
                Keys.onEscapePressed: { root.rememberOpen = false; keyCatcher.forceActiveFocus() }
                onAccepted: rememberSave.activate()
              }
              Row {
                spacing: Style.space(16)
                Link {
                  id: rememberSave
                  text: "Save"
                  font.pixelSize: Style.font.body
                  function activate() {
                    var text = rememberField.text.trim()
                    if (text === "") return
                    var args = ["write", text, "--kind", root.rememberKind, "--scope", root.rememberScope, "--source", "user"]
                    if (rememberKey.text.trim() !== "") args = args.concat(["--key", rememberKey.text.trim()])
                    root.runAction(args)
                    rememberField.text = ""
                    rememberKey.text = ""
                    root.rememberOpen = false
                    keyCatcher.forceActiveFocus()
                  }
                  onActivated: activate()
                }
                Link { text: "Cancel"; font.pixelSize: Style.font.body; onActivated: { root.rememberOpen = false; keyCatcher.forceActiveFocus() } }
              }
            }

            TextField {
              id: searchField
              width: parent.width
              placeholderText: root.search.mode === "vector" ? "Search by meaning  ( / )" : "Search by words  ( / )"
              foreground: root.foreground
              onTextChanged: searchTimer.restart()
              Keys.onEscapePressed: { if (text !== "") text = ""; else keyCatcher.forceActiveFocus() }
            }

            Row {
              width: parent.width
              spacing: Style.spacing.md
              Dropdown {
                width: (parent.width - parent.spacing) / 2
                label: "Project"
                showLabel: false
                value: root.memScope
                options: [{ value: "", label: "All projects" }]
                       .concat(root.projects.map(function(p) { return { value: p.scope, label: p.scope === "shared" ? "Shared / no project" : (p.name + (p.path ? "" : " (no folder)")) } }))
                onChanged: function(v) { root.memScope = v; root.memLimit = 20; root.loadMemories() }
              }
              Dropdown {
                width: (parent.width - parent.spacing) / 2
                label: "Kind"
                showLabel: false
                value: root.memKind
                enabled: searchField.text.trim() === ""
                options: [
                  { value: "", label: "All kinds" },
                  { value: "preference", label: "Preferences" },
                  { value: "project_param", label: "Project values" },
                  { value: "insight", label: "Insights" },
                  { value: "task_state", label: "Tasks" }
                ]
                onChanged: function(v) { root.memKind = v; root.memLimit = 20; root.loadMemories() }
              }
            }

            Caption {
              visible: root.memories.length === 0
              width: parent.width
              text: searchField.text.trim() !== "" ? "Nothing found." : "Nothing here yet."
            }

            Repeater {
              model: root.memories
              Column {
                id: memItem
                width: column.width
                spacing: Style.space(3)
                readonly property var m: modelData
                readonly property bool correcting: root.correctingId === m.id
                property bool expanded: false

                // Long memories are clipped to a few lines; click to read the rest.
                Body {
                  visible: !memItem.correcting
                  width: parent.width
                  text: memItem.m.content
                  maximumLineCount: memItem.expanded ? 1000 : 3
                  elide: Text.ElideRight
                  MouseArea {
                    anchors.fill: parent
                    cursorShape: parent.truncated || memItem.expanded ? Qt.PointingHandCursor : Qt.ArrowCursor
                    onClicked: memItem.expanded = !memItem.expanded
                  }
                }
                TextField {
                  id: correctField
                  visible: memItem.correcting
                  width: parent.width
                  text: memItem.m.content
                  foreground: root.foreground
                  placeholderText: "The correct statement"
                  onVisibleChanged: if (visible) Qt.callLater(function() { correctField.forceActiveFocus(); correctField.selectAll() })
                  Keys.onEscapePressed: { root.correctingId = ""; keyCatcher.forceActiveFocus() }
                  onAccepted: {
                    var text = correctField.text.trim()
                    if (text !== "" && text !== memItem.m.content) {
                      var args = ["write", text, "--kind", memItem.m.kind, "--scope", memItem.m.scope, "--source", "user"]
                      args = args.concat(memItem.m.key ? ["--key", memItem.m.key] : ["--replaces", memItem.m.id])
                      root.runAction(args)
                    }
                    root.correctingId = ""
                    keyCatcher.forceActiveFocus()
                  }
                }
                Caption {
                  width: parent.width
                  text: root.kindLabel(memItem.m.kind)
                        + (memItem.m.key ? "  -  " + memItem.m.key : "")
                        + "  -  " + (memItem.m.scope_name || root.projectName(memItem.m.scope))
                        + "  -  via " + memItem.m.source
                        + (memItem.m.at ? "  -  " + root.ago(memItem.m.at) : "")
                }
                Row {
                  spacing: Style.space(14)
                  Link {
                    visible: !memItem.correcting && root.confirmForgetId !== memItem.m.id
                    text: "Correct"
                    onActivated: { root.confirmForgetId = ""; root.correctingId = memItem.m.id }
                  }
                  Link {
                    visible: memItem.correcting
                    text: "Save correction (Enter)"
                    onActivated: correctField.accepted()
                  }
                  Link {
                    visible: memItem.correcting
                    text: "Cancel"
                    onActivated: { root.correctingId = ""; keyCatcher.forceActiveFocus() }
                  }
                  Link {
                    visible: !memItem.correcting && root.confirmForgetId !== memItem.m.id
                    text: "Forget"
                    danger: true
                    onActivated: root.confirmForgetId = memItem.m.id
                  }
                  Link {
                    visible: root.confirmForgetId === memItem.m.id
                    text: "Forget it everywhere"
                    danger: true
                    onActivated: { root.confirmForgetId = ""; root.runAction(["forget", "--id", memItem.m.id, "--scope", memItem.m.scope]) }
                  }
                  Link {
                    visible: root.confirmForgetId === memItem.m.id
                    text: "Keep"
                    onActivated: root.confirmForgetId = ""
                  }
                  Link {
                    visible: root.attachProjectId === memItem.m.id
                    text: "Cancel"
                    onActivated: { root.attachProjectId = ""; keyCatcher.forceActiveFocus() }
                  }
                  Link {
                    visible: !memItem.correcting && root.confirmForgetId !== memItem.m.id && root.attachProjectId !== memItem.m.id
                    text: root.isScoped(memItem.m) ? "Move to project" : "Attach to project"
                    onActivated: { root.confirmForgetId = ""; root.attachProjectId = memItem.m.id }
                  }
                  Link {
                    visible: !!memItem.m.concept && !memItem.correcting
                    text: "Open note"
                    onActivated: root.openPath(root.vaultPath + "/" + memItem.m.concept)
                  }
                }
                Column {
                  visible: root.attachProjectId === memItem.m.id
                  width: memItem.width
                  spacing: Style.space(4)
                  SearchableDropdown {
                    width: parent.width
                    label: "Attach to"
                    showLabel: false
                    value: ""
                    options: [{ value: "", label: "Choose project…" }].concat(root.projects.filter(function(p) { return p.scope !== memItem.m.scope && p.scope !== "unscoped" })
                                          .map(function(p) { return { value: p.scope, label: p.scope === "shared" ? "Shared / no project" : (p.name + (p.path ? "" : " (no folder)")) } }))
                    onChanged: function(v) {
                      if (v === "") return
                      root.attachProjectId = ""
                      root.runAction(["rescope", "--id", memItem.m.id, "--from", memItem.m.scope, "--scope", v])
                    }
                  }
                }
                PanelSeparator { width: parent.width; opacity: 0.4 }
              }
            }
            Link {
              visible: searchField.text.trim() === "" && root.memories.length >= root.memLimit
              text: "Show more (" + root.memories.length + " shown)"
              font.pixelSize: Style.font.body
              onActivated: { root.memLimit = root.memLimit + 20; root.loadMemories() }
            }
          }

          // ================= Projects =================
          Column {
            visible: root.tab === "projects"
            width: parent.width
            spacing: Style.space(8)

            // Adding a knowledge location: a memory pointing agents at a
            // folder they should read. membraid never reads the folder.
            Column {
              visible: root.addingSourceScope !== ""
              width: parent.width
              spacing: Style.space(6)
              Body {
                width: parent.width
                font.bold: true
                text: "New knowledge location for " + (root.addingSourceScope === "shared" ? "every project" : root.projectName(root.addingSourceScope))
              }
              Row {
                width: parent.width
                spacing: Style.spacing.lg
                TextField {
                  id: sourceFolderField
                  width: parent.width - browseLink.width - parent.spacing
                  placeholderText: "Folder, git repo or web address"
                  foreground: root.foreground
                  Keys.onEscapePressed: { root.addingSourceScope = ""; keyCatcher.forceActiveFocus() }
                  onAccepted: sourceAboutField.forceActiveFocus()
                }
                Link {
                  id: browseLink
                  anchors.verticalCenter: sourceFolderField.verticalCenter
                  text: browseProcess.running ? "Choosing..." : "Browse"
                  onActivated: if (!browseProcess.running) browseProcess.running = true
                }
              }
              Caption {
                visible: root.sourceTypeOf(sourceFolderField.text) !== ""
                width: parent.width
                text: ({
                  folder: "Folder: only on this machine. A folder inside a git repo is saved as the repo, so other machines can clone it.",
                  git: "Git repo: any machine can read it in a checkout or clone it.",
                  web: "Web page: membraid never opens it; say below if it needs a login."
                })[root.sourceTypeOf(sourceFolderField.text)] || ""
              }
              Row {
                visible: root.sourceTypeOf(sourceFolderField.text) === "web"
                width: parent.width
                spacing: Style.spacing.lg
                ToggleSwitch {
                  id: loginSwitch
                  checked: root.sourceLogin
                  onToggled: root.sourceLogin = !root.sourceLogin
                }
                Body {
                  anchors.verticalCenter: loginSwitch.verticalCenter
                  width: parent.width - loginSwitch.width - parent.spacing
                  text: "Needs a login (Notion and Google Drive links are detected)"
                }
              }
              TextField {
                id: sourceAboutField
                width: parent.width
                placeholderText: "What it holds and when to read it, e.g. API specs; read before changing endpoints"
                foreground: root.foreground
                Keys.onEscapePressed: { root.addingSourceScope = ""; keyCatcher.forceActiveFocus() }
                onAccepted: root.saveSource()
              }
              Caption {
                width: parent.width
                text: "Agents see this in search and in the digest, and open it with their own tools. membraid never reads, clones or fetches it."
              }
              Row {
                spacing: Style.space(16)
                Link { text: "Save"; font.pixelSize: Style.font.body; onActivated: root.saveSource() }
                Link { text: "Cancel"; font.pixelSize: Style.font.body; onActivated: { root.addingSourceScope = ""; keyCatcher.forceActiveFocus() } }
              }
              PanelSeparator { width: parent.width }
            }

            Repeater {
              model: root.projects
              Column {
                width: column.width
                spacing: Style.space(2)
                readonly property var p: modelData
                Body { width: parent.width; font.bold: true; text: parent.p.scope === "shared" ? "Shared (every project)" : parent.p.name }
                Caption {
                  width: parent.width
                  text: parent.p.memories + " memories  -  " + parent.p.open_tasks + " open"
                        + (parent.p.last_write ? "  -  last write " + root.ago(parent.p.last_write) : "")
                        + (parent.p.sources.length > 0 ? "  -  via " + parent.p.sources.join(", ") : "")
                }
                Caption {
                  visible: !!parent.p.path
                  width: parent.width
                  color: parent.p.missing ? root.urgent : root.dim
                  text: (parent.p.path || "") + (parent.p.missing ? "  (folder not found on this machine)" : "")
                }
                Row {
                  spacing: Style.space(14)
                  Link {
                    visible: parent.parent.p.memories > 0
                    text: "Show memories"
                    onActivated: { root.memScope = parent.parent.p.scope; root.memKind = ""; searchField.text = ""; root.selectTab("memories") }
                  }
                  Link {
                    visible: !!parent.parent.p.path && !parent.parent.p.missing
                    text: "Open folder"
                    onActivated: root.openPath(parent.parent.p.path)
                  }
                  Link {
                    text: "+ Knowledge location"
                    onActivated: root.startAddingSource(parent.parent.p.scope)
                  }
                }
                Repeater {
                  model: root.sourcesFor(parent.p.scope)
                  Column {
                    id: srcItem
                    width: column.width
                    spacing: Style.space(1)
                    leftPadding: Style.space(12)
                    readonly property var s: modelData
                    Body { width: parent.width - parent.leftPadding; text: srcItem.s.about }
                    Caption {
                      width: parent.width - parent.leftPadding
                      color: srcItem.s.type === "folder" && !srcItem.s.here ? root.urgent : root.dim
                      text: root.sourceWhere(srcItem.s)
                    }
                    Row {
                      spacing: Style.space(14)
                      Link { visible: !!srcItem.s.open; text: "Open"; onActivated: root.openPath(srcItem.s.open) }
                      Link {
                        visible: root.confirmForgetId !== srcItem.s.id
                        text: "Remove"
                        danger: true
                        onActivated: root.confirmForgetId = srcItem.s.id
                      }
                      Link {
                        visible: root.confirmForgetId === srcItem.s.id
                        text: "Remove everywhere"
                        danger: true
                        onActivated: { root.confirmForgetId = ""; root.runAction(["source", "remove", srcItem.s.key, "--scope", srcItem.s.scope]) }
                      }
                      Link { visible: root.confirmForgetId === srcItem.s.id; text: "Keep"; onActivated: root.confirmForgetId = "" }
                    }
                  }
                }
              }
            }

            Link {
              readonly property int empty: root.projects.filter(function(p) { return p.memories === 0 && p.scope !== "shared" }).length
              visible: empty > 0
              text: "Forget " + empty + " project" + (empty === 1 ? "" : "s") + " with no memories"
              onActivated: root.runAction(["projects", "prune"])
            }
            Caption {
              width: parent.width
              text: "Projects are recognised by their git history, so a repo is the same project on every machine. A folder outside git is known by its path."
            }
          }

          // ================= Insights =================
          Column {
            visible: root.tab === "insights"
            width: parent.width
            spacing: Style.space(8)

            readonly property var days: root.insights.writes_by_day || []
            readonly property int maxDay: Math.max(1, days.reduce(function(m, d) { return Math.max(m, d.count) }, 0))
            readonly property int weekTotal: days.reduce(function(s, d) { return s + d.count }, 0)

            PanelSectionHeader { text: "Writes, last " + (root.insights.days || 7) + " days: " + parent.weekTotal; foreground: root.foreground; fontFamily: root.fontFamily }
            Row {
              id: chart
              width: parent.width
              height: Style.space(56)
              spacing: Style.spacing.sm
              readonly property real barWidth: parent.days.length > 0 ? (width - spacing * (parent.days.length - 1)) / parent.days.length : 0
              Repeater {
                model: chart.parent.days
                Column {
                  width: chart.barWidth
                  height: chart.height
                  spacing: Style.space(2)
                  Item {
                    width: parent.width
                    height: chart.height - Style.space(14)
                    Rectangle {
                      anchors.bottom: parent.bottom
                      width: parent.width
                      height: Math.max(2, parent.height * modelData.count / chart.parent.maxDay)
                      radius: 2
                      color: modelData.count > 0 ? Color.accent : Qt.darker(root.foreground, 3)
                    }
                  }
                  Caption {
                    width: parent.width
                    horizontalAlignment: Text.AlignHCenter
                    text: ["Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"][new Date(modelData.day + "T12:00:00Z").getUTCDay()]
                  }
                }
              }
            }

            PanelSectionHeader { text: "By agent"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: Object.keys(root.insights.writes_by_source || {}).sort(function(a, b) { return root.insights.writes_by_source[b] - root.insights.writes_by_source[a] })
              Body { width: column.width; text: modelData + "   " + root.insights.writes_by_source[modelData] }
            }
            Caption {
              visible: Object.keys(root.insights.writes_by_source || {}).length === 0
              width: parent.width
              text: "No agent has written this week. If you have been using one, check that membraid is set up in it (Settings)."
            }

            PanelSectionHeader { text: "Reported uses by agent"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: Object.keys(root.insights.uses_by_source || {}).sort(function(a, b) { return root.insights.uses_by_source[b] - root.insights.uses_by_source[a] })
              Body { width: column.width; text: modelData + "   " + Math.round(root.insights.uses_by_source[modelData]) }
            }
            Caption {
              visible: Object.keys(root.insights.uses_by_source || {}).length === 0
              width: parent.width
              text: "No agent has reported using a memory this week. Agents call memory_used when a memory changes what they do; one that never does ranks nothing by use."
            }

            PanelSectionHeader { text: "Use"; foreground: root.foreground; fontFamily: root.fontFamily }
            Body {
              width: parent.width
              text: (root.insights.current || 0) + " current memories; " + (root.insights.never_used || 0) + " never retrieved by an agent"
            }
            Caption { visible: (root.insights.recently_used || []).length > 0; width: parent.width; text: "Recently used by agents:" }
            Repeater {
              model: root.insights.recently_used || []
              Caption {
                width: column.width
                color: root.foreground
                maximumLineCount: 2
                elide: Text.ElideRight
                text: "- " + modelData.content + "  (" + root.ago(modelData.at) + ")"
              }
            }

            PanelSectionHeader {
              visible: (root.insights.key_drift || []).length > 0
              text: "Keys that may be one subject"
              foreground: root.foreground
              fontFamily: root.fontFamily
            }
            Caption {
              visible: (root.insights.key_drift || []).length > 0
              width: parent.width
              text: "Agents used different keys for what looks like one thing, so each keeps its own answer. Correct the memory under the key to keep, then forget the other."
            }
            Repeater {
              model: root.insights.key_drift || []
              Body { width: column.width; text: modelData.a + "  ~  " + modelData.b + "   (" + (modelData.scope_name || modelData.scope) + ")" }
            }
          }

          // ================= Settings =================
          Column {
            visible: root.tab === "settings"
            width: parent.width
            spacing: Style.space(10)

            PanelSectionHeader { text: "Sync"; foreground: root.foreground; fontFamily: root.fontFamily }
            Row {
              width: parent.width
              spacing: Style.spacing.lg
              ToggleSwitch {
                id: autoSync
                checked: !!root.config.auto_sync
                onToggled: root.runAction(["config", "set", "auto_sync", checked ? "false" : "true"])
              }
              Body {
                anchors.verticalCenter: autoSync.verticalCenter
                width: parent.width - autoSync.width - parent.spacing
                text: "Auto-sync: push after writes, pull when sessions start"
              }
            }
            Row {
              spacing: Style.spacing.xxl
              NumberField {
                label: "Push delay (s)"
                from: 0; to: 3600; stepSize: 10
                value: root.config.push_delay_sec || 60
                onModified: function(v) { root.queueSetting("push_delay_sec", v) }
              }
              NumberField {
                label: "Pull every (min)"
                from: 1; to: 1440; stepSize: 5
                value: root.config.pull_interval_min || 15
                onModified: function(v) { root.queueSetting("pull_interval_min", v) }
              }
            }
            Row {
              spacing: Style.spacing.xxl
              NumberField {
                label: "Notes every (min)"
                from: 5; to: 1440; stepSize: 5
                value: root.config.distill_every_min || 30
                onModified: function(v) { root.queueSetting("distill_every_min", v) }
              }
              NumberField {
                label: "Upkeep every (days)"
                from: 1; to: 90; stepSize: 1
                value: root.config.sweep_every_days || 7
                onModified: function(v) { root.queueSetting("sweep_every_days", v) }
              }
            }

            PanelSectionHeader { text: "Ranking (saved in the vault: every machine ranks alike)"; foreground: root.foreground; fontFamily: root.fontFamily }
            Caption {
              width: parent.width
              text: "Memories are ordered by relevance x use. Use counts every write and every time an agent reported a memory changed what it did, each fading over time. Showing up in results does not count."
            }
            ButtonGroup {
              options: root.presets.map(function(p) { return { value: p.value, label: p.label } })
              value: root.presetOf()
              foreground: root.foreground
              fontFamily: root.fontFamily
              fontSize: Style.font.bodySmall
              focusable: false
              onChanged: function(v) { root.applyPreset(v) }
            }
            Link {
              text: root.tuningOpen ? "Hide advanced" : "Advanced"
              onActivated: root.tuningOpen = !root.tuningOpen
            }
            Column {
              visible: root.tuningOpen
              width: parent.width
              spacing: Style.space(8)

              Row {
                spacing: Style.spacing.xxl
                NumberField {
                  label: "Half-life (days)"
                  from: 1; to: 3650; stepSize: 5
                  value: root.config.halflife_days || 30
                  onModified: function(v) { root.queueSetting("halflife_days", v) }
                }
                NumberField {
                  label: "Digest size"
                  from: 1; to: 50; stepSize: 1
                  value: root.config.digest_items || 12
                  onModified: function(v) { root.queueSetting("digest_items", v) }
                }
              }
              Row {
                spacing: Style.spacing.xxl
                NumberField {
                  label: "Stale after (days)"
                  from: 7; to: 3650; stepSize: 10
                  value: root.config.sweep_unused_days || 90
                  onModified: function(v) { root.queueSetting("sweep_unused_days", v) }
                }
                NumberField {
                  label: "Task stale after (days)"
                  from: 1; to: 365; stepSize: 1
                  value: root.config.stale_task_days || 14
                  onModified: function(v) { root.queueSetting("stale_task_days", v) }
                }
              }
              Body {
                width: parent.width
                text: "Repeat-use boost: " + Number(root.config.frequency_boost !== undefined ? root.config.frequency_boost : 1).toFixed(2)
                      + "   (0: use only keeps a memory fresh)"
              }
              PanelSlider {
                width: parent.width
                bar: root.bar
                minimum: 0; maximum: 3; step: 0.1
                value: root.config.frequency_boost !== undefined ? Number(root.config.frequency_boost) : 1
                onReleased: function(v) { root.queueSetting("frequency_boost", Number(v).toFixed(2)) }
              }
              Body {
                width: parent.width
                text: "Shared memories in a project's digest: x" + Number(root.config.digest_shared_weight || 0.7).toFixed(2)
              }
              PanelSlider {
                width: parent.width
                bar: root.bar
                minimum: 0.1; maximum: 1; step: 0.05
                value: Number(root.config.digest_shared_weight || 0.7)
                onReleased: function(v) { root.queueSetting("digest_shared_weight", Number(v).toFixed(2)) }
              }

              TextField {
                id: tuneQuery
                width: parent.width
                placeholderText: "Try a search to see how it ranks"
                foreground: root.foreground
                onTextChanged: previewTimer.restart()
                Keys.onEscapePressed: { if (text !== "") text = ""; else keyCatcher.forceActiveFocus() }
              }
              Repeater {
                model: root.preview
                Column {
                  width: column.width
                  spacing: Style.space(1)
                  Body { width: parent.width; maximumLineCount: 1; elide: Text.ElideRight; text: (index + 1) + ". " + modelData.content }
                  Caption {
                    visible: !!modelData.why
                    width: parent.width
                    text: modelData.why
                          ? "score " + modelData.why.score.toFixed(3) + " = match " + (modelData.why.score / (modelData.why.use_factor || 1)).toFixed(2)
                            + " of the best x use " + Number(modelData.why.use_factor || 1).toFixed(2) + "   (boost " + modelData.why.boost.toFixed(2) + ", " + modelData.why.writes + " write"
                            + (modelData.why.writes === 1 ? "" : "s") + ", " + modelData.why.uses.toFixed(1) + " uses"
                            + (modelData.why.last_used ? ", last " + root.ago(modelData.why.last_used) : "") + ")"
                          : ""
                  }
                }
              }
            }

            PanelSectionHeader { text: "Search"; foreground: root.foreground; fontFamily: root.fontFamily }
            Caption { width: parent.width; text: "Search by meaning (embeddings are computed on this machine, never sent anywhere):" }
            ButtonGroup {
              options: [
                { value: "off", label: "Off" },
                { value: "ollama", label: "Ollama" },
                { value: "builtin", label: "Built-in model" }
              ]
              value: root.config.embeddings || "off"
              foreground: root.foreground
              fontFamily: root.fontFamily
              fontSize: Style.font.bodySmall
              focusable: false
              onChanged: function(v) {
                root.runAction(["config", "set", "embeddings", v])
                if (v !== "off") root.runAction(["embed", "--quiet"])
              }
            }
            Caption { visible: root.config.embeddings === "ollama"; width: parent.width; text: "Model: " + (root.config.embed_model || "") }
            Row {
              spacing: Style.space(16)
              Link { text: "Sync now"; onActivated: root.runAction(["sync", "--quiet"]) }
              Link { text: "Embed now"; onActivated: root.runAction(["embed", "--quiet"]) }
              Link { text: "Sweep now"; onActivated: root.runAction(["sweep"]) }
              Link { text: "Distill now"; onActivated: root.runAction(["distill"]) }
            }

            PanelSectionHeader { text: "Harnesses"; foreground: root.foreground; fontFamily: root.fontFamily }
            Repeater {
              model: root.harnesses
              Row {
                width: column.width
                spacing: Style.space(10)
                Body {
                  width: parent.width * 0.5
                  color: modelData.detected || modelData.configured ? root.foreground : root.dim
                  text: modelData.name
                }
                Caption {
                  width: parent.width * 0.3
                  text: modelData.configured === true ? "set up"
                        : modelData.configured === false ? (modelData.detected ? "found, not set up" : "not found")
                        : ""
                }
                Link {
                  visible: modelData.configured !== true
                  text: "Set up"
                  onActivated: root.inTerminal([root.binary, "install", "--harness", modelData.id])
                }
              }
            }
            Link { text: "Run the installer"; font.pixelSize: Style.font.body; onActivated: root.inTerminal([root.binary, "install"]) }

            PanelSectionHeader { text: "membraid"; foreground: root.foreground; fontFamily: root.fontFamily }
            Body {
              width: parent.width
              text: "Version " + (root.status.version || "?")
                    + (root.updateInfo.newer ? "  -  " + root.updateInfo.latest + " available" : (root.updateInfo.latest ? "  -  up to date" : ""))
            }
            Row {
              spacing: Style.space(16)
              Link { visible: !!root.updateInfo.newer; text: "Update"; onActivated: root.inTerminal([root.binary, "update"]) }
              Link { text: "Check for updates"; onActivated: root.checkUpdates(true) }
              Link { text: "Open vault"; onActivated: root.openPath(root.vaultPath) }
            }
            Caption { width: parent.width; text: "Vault: " + root.vaultPath + (root.config.path ? "\nSettings: " + root.config.path : "") }
          }

          Caption {
            width: parent.width
            text: (actionProcess.running || root.actionQueue.length > 0) ? "working..." : "h / l switch tabs  -  / search  -  r refresh"
          }
        }
      }
    }
  }
}
