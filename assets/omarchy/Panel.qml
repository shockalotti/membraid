import QtQuick
import QtQuick.Controls
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Membraid in the bar: one brain across every harness and every machine.
//
// All state comes from `membraid status --json`, and every action is a
// `membraid` command. The widget owns no memory of its own: if the panel and
// the CLI ever disagree, the CLI is right.
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
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var doing: []
  property var learned: []
  property var stats: ({})
  property var sync: ({})
  property string scopeName: ""
  property string hostName: ""
  property string vaultPath: ""
  property bool everLoaded: false
  property double nowMs: Date.now()

  readonly property bool syncFailed: !!sync.last_error
  readonly property string home: Quickshell.env("HOME") || ""
  // Quickshell does not inherit a login shell's PATH, so the binary is found
  // rather than assumed. A blank setting means the go install location.
  readonly property string binary: {
    var configured = setting("binary", "")
    return configured !== "" ? configured : home + "/go/bin/membraid"
  }

  function refreshNow() { if (!statusProcess.running) statusProcess.running = true }

  // One process for every action: a click runs a membraid command, and the
  // panel reloads from status once it exits, so what you see is what happened.
  function runAction(args) {
    if (actionProcess.running) return
    actionProcess.command = [root.binary].concat(args)
    actionProcess.running = true
  }

  function ago(iso) {
    if (!iso) return ""
    var s = Math.max(0, Math.round((root.nowMs - Date.parse(iso)) / 1000))
    if (s < 45) return "just now"
    if (s < 3600) return Math.round(s / 60) + "m ago"
    if (s < 86400) return Math.round(s / 3600) + "h ago"
    return Math.round(s / 86400) + "d ago"
  }

  function syncLine() {
    if (!root.everLoaded) return ""
    if (!sync.enabled) return "Auto-sync off"
    if (sync.last_error) return "Sync failed: " + sync.last_error
    if (sync.last_success) return "Synced " + ago(sync.last_success)
    if (sync.last_skipped) return "Not syncing: " + sync.last_skipped
    return "Not synced yet"
  }

  Process {
    id: statusProcess
    running: false
    // "--scope *": the bar is not standing in any project, so it shows all of
    // them. Resolving from its own working directory meant shared-only.
    command: [root.binary, "status", "--json", "--scope", "*"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var d = JSON.parse(text)
          root.doing = d.doing || []
          root.learned = d.learned || []
          root.stats = d.stats || ({})
          root.sync = d.sync || ({})
          root.scopeName = d.scope || ""
          root.hostName = d.host || ""
          root.vaultPath = d.vault || ""
        } catch (e) {}
        root.everLoaded = true
      }
    }
  }

  Process {
    id: actionProcess
    running: false
    onExited: function(exitCode) { root.refreshNow() }
  }

  Timer {
    interval: Math.max(10, root.setting("refreshIntervalSec", 60)) * 1000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: { root.nowMs = Date.now(); root.refreshNow() }
  }

  onOpenedChanged: if (opened) { root.nowMs = Date.now(); refreshNow() }

  IpcHandler {
    target: root.ipcTarget
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function refresh(): string { root.refreshNow(); return "ok" }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "\udb82\uddd1" // U+F09D1 nf-md-brain. Escaped, never literal: a literal glyph has been mangled on the way into this file twice.
    active: root.doing.length > 0 || root.syncFailed
    onPressed: function(buttonCode) { root.toggle() }
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(440))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(600))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onActivateRequested: root.refreshNow()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.refreshNow()
        if (t === "s" || t === "S") root.runAction(["sync", "--quiet"])
      }
      onMoveRequested: function(dx, dy) {
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
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: column
          width: flick.width
          spacing: Style.space(10)

          PanelHero {
            width: parent.width
            title: "Membraid"
            meta: root.everLoaded
                  ? ((root.stats.current || 0) + " current across " + (root.stats.scopes || 0)
                     + " projects  -  " + root.hostName)
                  : "reading memory..."
            foreground: root.foreground
            fontFamily: root.fontFamily
          }

          PanelSectionHeader {
            visible: root.doing.length > 0
            width: parent.width
            text: "Where you left off"
          }

          // Click a task to mark it finished. Before this there was no way to
          // clear one, and a task written without a key stayed here forever.
          Repeater {
            model: root.doing
            delegate: Text {
              width: column.width
              wrapMode: Text.WordWrap
              color: taskMouse.containsMouse ? root.dim : root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              font.strikeout: taskMouse.containsMouse
              text: modelData.content + "  -  " + (modelData.scope_name || modelData.scope) + ", via " + modelData.source

              MouseArea {
                id: taskMouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: root.runAction(["done", "--id", modelData.id])
              }
            }
          }

          Text {
            visible: root.doing.length > 0
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            text: "click a task to mark it done"
          }

          PanelSeparator { visible: root.doing.length > 0 && root.learned.length > 0; width: parent.width }

          PanelSectionHeader {
            visible: root.learned.length > 0
            width: parent.width
            text: "What your agents learned"
          }

          Repeater {
            model: root.learned
            delegate: Column {
              width: column.width
              spacing: Style.space(2)
              Text {
                width: parent.width
                wrapMode: Text.WordWrap
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.body
                text: modelData.content
              }
              Text {
                color: root.dim
                font.family: root.fontFamily
                font.pixelSize: Style.font.caption
                text: modelData.kind
                      + (modelData.key ? "  -  " + modelData.key : "")
                      + "  -  " + (modelData.scope_name || modelData.scope)
                      + "  -  via " + modelData.source
              }
            }
          }

          Text {
            visible: root.everLoaded && root.doing.length === 0 && root.learned.length === 0
            width: parent.width
            wrapMode: Text.WordWrap
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.body
            text: "Nothing yet. Your agents will fill this in as they work."
          }

          PanelSeparator { width: parent.width }

          Text {
            width: parent.width
            wrapMode: Text.WordWrap
            color: root.syncFailed ? Color.urgent : root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
            text: root.syncLine() + (actionProcess.running ? "  -  working..." : "")
          }

          // Plain Text + MouseArea throughout: PanelActionButton is icon-only,
          // and guessing at a component API is what broke this panel before.
          Row {
            spacing: Style.space(16)

            Text {
              color: syncMouse.containsMouse ? root.foreground : root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              text: "Sync now"
              MouseArea {
                id: syncMouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: root.runAction(["sync", "--quiet"])
              }
            }

            Text {
              color: toggleMouse.containsMouse ? root.foreground : root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              text: root.sync.enabled ? "Turn auto-sync off" : "Turn auto-sync on"
              MouseArea {
                id: toggleMouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: root.runAction(["config", "set", "auto_sync", root.sync.enabled ? "false" : "true"])
              }
            }

            Text {
              color: openMouse.containsMouse ? root.foreground : root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              text: "Open vault"
              MouseArea {
                id: openMouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: { openVault.running = true; root.close() }
              }
            }
          }
        }
      }
    }
  }

  Process {
    id: openVault
    running: false
    command: ["xdg-open", root.vaultPath !== "" ? root.vaultPath : (root.home + "/.membraid/vault")]
  }
}
