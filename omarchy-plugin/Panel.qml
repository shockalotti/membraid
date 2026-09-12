import QtQuick
import QtQuick.Controls
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// Membraid in the bar: one brain across every harness.
//
// All state comes from `membraid status --json`. The widget owns no memory of
// its own and never writes - if the panel and the CLI ever disagree, the CLI
// is right.
Panel {
  id: root
  moduleName: "shockalotti.membraid"
  ipcTarget: "shockalotti.membraid"
  manageIpc: false

  readonly property color foreground: bar ? bar.foreground : Color.foreground
  readonly property color dim: Qt.darker(foreground, 1.55)
  readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

  property var doing: []
  property var learned: []
  property var stats: ({})
  property string scopeName: ""
  property string vaultPath: ""
  property bool everLoaded: false

  readonly property string home: Quickshell.env("HOME") || ""
  // Quickshell does not inherit a login shell's PATH, so the binary is found
  // rather than assumed. A blank setting means "look in the usual places".
  readonly property string binary: {
    var configured = setting("binary", "")
    if (configured !== "") return configured
    return home + "/go/bin/membraid"
  }

  function refreshNow() { if (!statusProcess.running) statusProcess.running = true }

  Process {
    id: statusProcess
    running: false
    command: [root.binary, "status", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try {
          var d = JSON.parse(text)
          root.doing = d.doing || []
          root.learned = d.learned || []
          root.stats = d.stats || ({})
          root.scopeName = d.scope || ""
          root.vaultPath = d.vault || ""
          root.everLoaded = true
        } catch (e) {
          root.everLoaded = true
        }
      }
    }
  }

  Timer {
    interval: Math.max(10, root.setting("refreshIntervalSec", 60)) * 1000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refreshNow()
  }

  // Reload the moment the panel opens: the last poll may be a minute stale,
  // and a stale dashboard is worse than a slow one.
  onOpenedChanged: if (opened) refreshNow()

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
    text: "\udb82\uddd1" // U+F09D1 nf-md-brain. Escaped, not literal: a literal glyph was truncated mid-codepoint by the heredoc that first wrote this file.
    active: root.doing.length > 0
    onPressed: function(buttonCode) { root.toggle() }
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(420))
    contentHeight: panel.fittedContentHeight(column.implicitHeight, Style.space(560))

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      onCloseRequested: root.close()
      onActivateRequested: root.refreshNow()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onTextKey: function(t) { if (t === "r" || t === "R") root.refreshNow() }
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
                  ? (root.scopeName + "  ·  " + (root.stats.current || 0) + " current across "
                     + (root.stats.scopes || 0) + " projects")
                  : "reading memory…"
            foreground: root.foreground
            fontFamily: root.fontFamily
          }

          PanelSectionHeader {
            visible: root.doing.length > 0
            width: parent.width
            text: "Where you left off"
          }

          Repeater {
            model: root.doing
            delegate: Text {
              width: column.width
              wrapMode: Text.WordWrap
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
              text: modelData.content + "   ·  " + modelData.source
            }
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
                      + (modelData.key ? "  ·  " + modelData.key : "")
                      + "  ·  " + modelData.scope
                      + "  ·  via " + modelData.source
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

          // Plain Text + MouseArea rather than PanelActionButton: that
          // component is icon-only (iconText, no text), and guessing at a
          // component API is what broke this panel the first time.
          Text {
            id: openLink
            width: parent.width
            color: openMouse.containsMouse ? root.foreground : root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.body
            text: "Open vault folder"

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

  Process {
    id: openVault
    running: false
    command: ["xdg-open", root.vaultPath !== "" ? root.vaultPath : (root.home + "/.membraid/vault")]
  }
}
