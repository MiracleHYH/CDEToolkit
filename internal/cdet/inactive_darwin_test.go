//go:build darwin

package cdet

import (
	"encoding/binary"
	"testing"
)

func TestDarwinProcessArgumentParserKeepsPrivatePathsWithSpaces(t *testing.T) {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, 2)
	buf = append(buf, []byte("/tmp/program with spaces\x00\x00\x00codex\x00daemon\x00CODEX_HOME=/tmp/data with spaces\x00PRIVATE=not-logged\x00EMPTY=\x00")...)
	buf = append(buf, make([]byte, 256<<10)...)
	args, env, e := parseDarwinProcessArgs(buf)
	if e != nil || len(args) != 3 || args[0] != "/tmp/program with spaces" || args[2] != "daemon" || len(env) != 3 || env[0] != "CODEX_HOME=/tmp/data with spaces" || env[2] != "EMPTY=" {
		t.Fatal("invalid process argument decoding", e)
	}
	for _, malformed := range [][]byte{nil, {1, 0, 0, 0}, {2, 0, 0, 0, 'a', 0, 'b', 0}} {
		if _, _, e = parseDarwinProcessArgs(malformed); e == nil {
			t.Fatal("incomplete process arguments accepted")
		}
	}
}
