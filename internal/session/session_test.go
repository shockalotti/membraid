package session

import (
	"strconv"
	"strings"
	"testing"
)

// A resumed session with no receipt must come back loud; everything else
// stays quiet. The ledger is a receipt book, not a tripwire.
func TestResumeWithoutReceiptIsLoud(t *testing.T) {
	dir := t.TempDir()
	if banner := Check(dir, "sess-1", "resume"); !strings.HasPrefix(banner, "MEMORY NEVER LOADED") {
		t.Errorf("first resume must warn loudly, got %q", banner)
	}
	if !Seen(dir, "sess-1") {
		t.Error("the check must record the receipt it just warned about")
	}
}

func TestResumeWithReceiptIsQuiet(t *testing.T) {
	dir := t.TempDir()
	if banner := Check(dir, "sess-1", "startup"); banner != "" {
		t.Errorf("fresh startup must stay quiet, got %q", banner)
	}
	if banner := Check(dir, "sess-1", "resume"); banner != "" {
		t.Errorf("resume with a receipt must stay quiet, got %q", banner)
	}
	if banner := Check(dir, "sess-1", ""); banner != "" {
		t.Errorf("repeat delivery with a receipt must stay quiet, got %q", banner)
	}
}

// Plugin harnesses pass a session id with no source. An unseen one gets the
// banner too: a fresh session hearing it once is the price of never staying
// silent about first contact.
func TestUnseenWithoutSourceIsLoud(t *testing.T) {
	dir := t.TempDir()
	if banner := Check(dir, "sess-2", ""); !strings.HasPrefix(banner, "MEMORY NEVER LOADED") {
		t.Errorf("unseen session with no source must warn loudly, got %q", banner)
	}
}

func TestStartupRecordsButNeverWarns(t *testing.T) {
	dir := t.TempDir()
	if banner := Check(dir, "sess-9", "startup"); banner != "" {
		t.Errorf("startup must never warn, got %q", banner)
	}
	if !Seen(dir, "sess-9") {
		t.Error("startup must still record the receipt for later resumes")
	}
}

func TestNoSessionIsNoOp(t *testing.T) {
	dir := t.TempDir()
	if banner := Check(dir, "", ""); banner != "" {
		t.Errorf("no hook payload must be a no-op, got %q", banner)
	}
	if Seen(dir, "") {
		t.Error("empty session must not be recorded")
	}
}

func TestLedgerIsCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxEntries+50; i++ {
		Check(dir, "sess-"+strconv.Itoa(i), "startup")
	}
	ledger := load(dir)
	if len(ledger) > maxEntries {
		t.Errorf("ledger must be capped at %d, holds %d", maxEntries, len(ledger))
	}
}
