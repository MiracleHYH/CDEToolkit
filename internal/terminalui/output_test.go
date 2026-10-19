package terminalui

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestTaskCaptureBoundedWithoutUIMessageOrdering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Session{ctx: ctx, capture: true, output: io.Discard}
	for i := 0; i < 100; i++ {
		s.Write([]byte(strings.Repeat("界", 1000) + "\n"))
	}
	s.Write([]byte("安装目录：/tmp/nido\n. '/tmp/nido/env.sh'\n"))
	if len(s.transcript) > 16384 {
		t.Fatalf("capture grew to %d bytes", len(s.transcript))
	}
	if !strings.Contains(s.transcript, "/tmp/nido/env.sh") {
		t.Fatal("last result not retained")
	}
	var replay strings.Builder
	s.output = &replay
	if err := s.replay(); err != nil {
		t.Fatal(err)
	}
	if err := s.replay(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(replay.String(), ". '/tmp/nido/env.sh'") != 1 {
		t.Fatal("replay was lost or duplicated")
	}
}

func TestOutputRedactsURLCredentialsAndControls(t *testing.T) {
	value := safeOutput("\x1b[31merror http://token-only@proxy:123 and socks5://name:pass@proxy:123 and http://malformed-secret@proxy/%zz\x00\n")
	for _, secret := range []string{"token-only", "name:pass", "malformed-secret", "\x1b", "\x00"} {
		if strings.Contains(value, secret) {
			t.Fatalf("unsafe output %q", value)
		}
	}
	if !strings.Contains(value, "http://REDACTED@proxy:123") {
		t.Fatal(value)
	}
}
