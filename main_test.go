package main

import "testing"

func TestToJobUUID(t *testing.T) {
	initial := "00000000-0000-0000-0000-000000000000.json"
	expected := "00000000-0000-0000-0000-000000000000"
	actual := tojobuuid(initial)
	if actual != expected {
		t.Errorf("uuid was %s, not %s", actual, expected)
	}
}
