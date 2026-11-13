package proto

import "testing"

func TestRoundTrip(t *testing.T) {
	check := func(name string, b []byte, want Msg) {
		t.Helper()
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Fatalf("%s:\n got %+v\nwant %+v", name, got, want)
		}
	}
	check("hello", AppendHello(nil, "macbook", "darwin", 1728, 1117, CapClipboard),
		Msg{Type: THello, Version: Version, W: 1728, H: 1117, OS: "darwin", Name: "macbook", Caps: CapClipboard})
	check("welcome", AppendWelcome(nil, 0xDEADBEEF, "desktop", CapClipboard),
		Msg{Type: TWelcome, Session: 0xDEADBEEF, Name: "desktop", Caps: CapClipboard})
	check("datahello", AppendDataHello(nil, 77), Msg{Type: TDataHello, Session: 77})
	check("clipstate", AppendClipState(nil, true), Msg{Type: TClipState, Changed: true})
	check("clipsend", AppendClipSend(nil, 9), Msg{Type: TClipSend, Bundle: 9})

	// Messages from a version without capabilities end before the last field.
	oldHello := AppendHello(nil, "macbook", "darwin", 1728, 1117, CapClipboard)
	check("hello from an older version", oldHello[:len(oldHello)-4],
		Msg{Type: THello, Version: Version, W: 1728, H: 1117, OS: "darwin", Name: "macbook"})
	oldWelcome := AppendWelcome(nil, 5, "desktop", CapClipboard)
	check("welcome from an older version", oldWelcome[:len(oldWelcome)-4],
		Msg{Type: TWelcome, Session: 5, Name: "desktop"})
	check("reject", AppendReject(nil, "unknown screen"),
		Msg{Type: TReject, Reason: "unknown screen"})
	check("enter", AppendEnter(nil, 7, 2, 540), Msg{Type: TEnter, Seq: 7, X: 2, Y: 540})
	check("motion", AppendMotion(nil, 8, -3, 99999), Msg{Type: TMotion, Seq: 8, X: -3, Y: 99999})
	check("button", AppendButton(nil, 9, 10, 20, 3, true),
		Msg{Type: TButton, Seq: 9, X: 10, Y: 20, Button: 3, Down: true})
	check("wheel", AppendWheel(nil, -120, 240), Msg{Type: TWheel, DX: -120, DY: 240})
	check("key", AppendKey(nil, 125, 2), Msg{Type: TKey, Code: 125, State: 2})
	check("ping", AppendPing(nil, 1<<40), Msg{Type: TPing, T: 1 << 40})
	check("pong", AppendPong(nil, 5), Msg{Type: TPong, T: 5})
	check("leave", AppendLeave(nil), Msg{Type: TLeave})
	check("udphello", AppendUDPHello(nil), Msg{Type: TUDPHello})
}

func TestDecodeErrors(t *testing.T) {
	if _, err := Decode(nil); err == nil {
		t.Error("empty message must fail")
	}
	if _, err := Decode([]byte{200}); err == nil {
		t.Error("unknown type must fail")
	}
	full := AppendButton(nil, 1, 2, 3, 1, true)
	for n := 1; n < len(full); n++ {
		if _, err := Decode(full[:n]); err == nil {
			t.Errorf("truncated to %d bytes must fail", n)
		}
	}
	// A string length that points past the end of the message.
	if _, err := Decode([]byte{TReject, 50, 'x'}); err == nil {
		t.Error("overlong string must fail")
	}
}

func TestAppendReusesBuffer(t *testing.T) {
	buf := make([]byte, 0, 64)
	a := AppendMotion(buf[:0], 1, 2, 3)
	if &a[0] != &buf[:1][0] {
		t.Error("AppendMotion should write into the provided buffer")
	}
}
