package index

import "time"

// RunDue reports whether the named periodic job last ran at least every ago
// on this index, or has never run. Per-machine state: each machine runs its
// own maintenance.
func (ix *Index) RunDue(name string, every time.Duration) bool {
	t, err := time.Parse(time.RFC3339Nano, ix.metaGet("last_run:"+name))
	return err != nil || ix.now().Sub(t) >= every
}

// MarkRun records that the named job ran now.
func (ix *Index) MarkRun(name string) error {
	return ix.metaSet("last_run:"+name, ix.now().UTC().Format(time.RFC3339Nano))
}
