//go:build linux

package cdet

import "testing"

func TestLinuxProcessOwnershipRequiresAnEffectiveUID(t *testing.T) {
	uid, ok := processUID("Name:\tcodex\nUid:\t1000\t1001\t1000\t1000\n")
	if !ok || uid != 1001 {
		t.Fatal("effective process owner was not decoded")
	}
	for _, malformed := range []string{"", "Uid:\t1000", "Uid:\t1000 invalid"} {
		if _, ok := processUID(malformed); ok {
			t.Fatal("unknown process ownership accepted")
		}
	}
}
