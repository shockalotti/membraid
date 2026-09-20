package install

import (
	"strconv"
	"strings"
	"testing"
)

// The binary moves — Go relocates it, people standardise their own layout —
// while every hook holds the absolute path from install day. The digest
// command must resolve dynamically and, when nothing resolves, say so where
// the harness shows it instead of failing silently.
func TestDigestCommandResolvesDynamicallyAndWarnsLoudly(t *testing.T) {
	for _, format := range []string{"", "claude", "copilot", "cursor"} {
		cmd := digestCommand(bin, format)
		if strings.Contains(cmd, "|| true") {
			t.Errorf("format %q: silent failure is back: %s", format, cmd)
		}
		if !strings.Contains(cmd, "command -v membraid") {
			t.Errorf("format %q: must try PATH first: %s", format, cmd)
		}
		if !strings.Contains(cmd, strconv.Quote(bin)) {
			t.Errorf("format %q: must keep the install-time path as fallback: %s", format, cmd)
		}
		wantArgs := "context"
		if format != "" {
			wantArgs += " --format " + format
		}
		if !strings.Contains(cmd, wantArgs) {
			t.Errorf("format %q: must still run the digest: %s", format, cmd)
		}
		if !strings.Contains(cmd, ">&2") || !strings.Contains(cmd, "re-run membraid install") {
			t.Errorf("format %q: a dead binary must warn loudly with the repair: %s", format, cmd)
		}
	}
}

func TestDigestCommandSurvivesAMovedBinary(t *testing.T) {
	// The install-day path is gone; PATH holds the new one. The hook must
	// still produce a digest instead of pointing at the corpse.
	cmd := digestCommand("/gone/go/bin/membraid", "claude")
	if strings.HasPrefix(cmd, "/gone/go/bin/membraid context") {
		t.Errorf("a moved binary must not be the first resort: %s", cmd)
	}
}
