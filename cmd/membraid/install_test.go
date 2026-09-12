package main

import "testing"

func TestToggle(t *testing.T) {
	sel := []bool{true, true, false, false}
	if err := toggle("2 4", sel); err != nil || sel[1] || !sel[3] {
		t.Fatalf("toggle 2 4: %v %v", err, sel)
	}
	if err := toggle("n", sel); err != nil || sel[0] || sel[3] {
		t.Fatalf("none: %v", sel)
	}
	if err := toggle("a", sel); err != nil || !sel[2] {
		t.Fatalf("all: %v", sel)
	}
	before := append([]bool(nil), sel...)
	if err := toggle("1 9", sel); err == nil {
		t.Fatal("9 is out of range")
	}
	for i := range sel {
		if sel[i] != before[i] {
			t.Fatal("an invalid line must change nothing")
		}
	}
}

func TestSelectByIDRejectsUnknown(t *testing.T) {
	choices := []choice{{id: "claude-code"}, {id: "grok"}}
	sel := make([]bool, 2)
	if err := selectByID(choices, sel, "grok"); err != nil || sel[0] || !sel[1] {
		t.Fatalf("select grok: %v %v", err, sel)
	}
	if err := selectByID(choices, sel, "grok,cursor"); err == nil {
		t.Fatal("cursor is not a known harness")
	}
}
