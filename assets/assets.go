// Package assets carries the files membraid installs into each harness: the
// agent skill, and the plugins that put the session digest in front of the
// model. They are compiled into the binary so that one `go install` delivers
// everything, and the repo stays the single place they evolve.
package assets

import "embed"

//go:embed skill/SKILL.md opencode/membraid.js hermes/plugin.yaml hermes/__init__.py omarchy/manifest.json omarchy/Panel.qml pi/membraid.ts
var FS embed.FS
