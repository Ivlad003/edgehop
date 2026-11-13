package secure

import (
	"bytes"
	"errors"
	"net"
	"testing"
)

// pair runs both sides of the handshake over a real loopback TCP connection.
func pair(t *testing.T, clientKey, serverKey string) (client, server *Channel, cerr, serr error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type res struct {
		ch  *Channel
		err error
	}
	done := make(chan res, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- res{nil, err}
			return
		}
		ch, err := ServerHandshake(conn, serverKey)
		done <- res{ch, err}
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	client, cerr = ClientHandshake(conn, clientKey)
	r := <-done
	if r.ch != nil {
		t.Cleanup(func() { r.ch.Close() })
	}
	return client, r.ch, cerr, r.err
}

func TestHandshakeAndFrames(t *testing.T) {
	key := NewKey()
	c, s, cerr, serr := pair(t, key, key)
	if cerr != nil || serr != nil {
		t.Fatalf("handshake: client=%v server=%v", cerr, serr)
	}
	for i := 0; i < 50; i++ {
		msg := bytes.Repeat([]byte{byte(i)}, i*7)
		if err := c.Send(msg); err != nil {
			t.Fatal(err)
		}
		got, err := s.Recv()
		if err != nil {
			t.Fatalf("recv %d: %v", i, err)
		}
		if !bytes.Equal(got, msg) {
			t.Fatalf("frame %d corrupted", i)
		}
		if err := s.Send(msg); err != nil {
			t.Fatal(err)
		}
		if got, err = c.Recv(); err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("reply %d: %v", i, err)
		}
	}
}

func TestWrongKeyIsRejected(t *testing.T) {
	c, s, cerr, _ := pair(t, "key-one", "key-two")
	if !errors.Is(cerr, ErrBadKey) {
		t.Fatalf("client error = %v, want ErrBadKey", cerr)
	}
	if c != nil {
		t.Fatal("client must not get a channel")
	}
	// The server cannot tell yet, but nothing the client sends would decrypt.
	_ = s
}

func TestServerRejectsFramesFromWrongKey(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	result := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		ch, err := ServerHandshake(conn, "right")
		if err != nil {
			result <- err
			return
		}
		_, err = ch.Recv()
		result <- err
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A client that ignores the failed confirmation and sends anyway.
	ch, _ := ClientHandshake(conn, "wrong")
	if ch != nil {
		t.Fatal("handshake with the wrong key must fail")
	}
	conn.Write([]byte{0, 20, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20})
	if err := <-result; err == nil {
		t.Fatal("server accepted a frame from a client with the wrong key")
	}
}

func TestDatagrams(t *testing.T) {
	key := NewKey()
	c, s, cerr, serr := pair(t, key, key)
	if cerr != nil || serr != nil {
		t.Fatalf("handshake: %v %v", cerr, serr)
	}
	buf := make([]byte, 0, 128)
	out := make([]byte, 0, 128)

	p1 := append([]byte(nil), s.SealDatagram(buf, 42, []byte("one"))...)
	p2 := append([]byte(nil), s.SealDatagram(buf, 42, []byte("two"))...)
	p3 := append([]byte(nil), s.SealDatagram(buf, 42, []byte("three"))...)

	if id, ok := DatagramSession(p1); !ok || id != 42 {
		t.Fatalf("session = %d, %v", id, ok)
	}
	// Out of order delivery: 2 first, then 1 (stale), then 3.
	if got, err := c.OpenDatagram(out, p2); err != nil || string(got) != "two" {
		t.Fatalf("p2: %q %v", got, err)
	}
	if _, err := c.OpenDatagram(out, p1); !errors.Is(err, ErrReplay) {
		t.Fatalf("stale datagram: err = %v, want ErrReplay", err)
	}
	if _, err := c.OpenDatagram(out, p2); !errors.Is(err, ErrReplay) {
		t.Fatalf("replayed datagram: err = %v, want ErrReplay", err)
	}
	if got, err := c.OpenDatagram(out, p3); err != nil || string(got) != "three" {
		t.Fatalf("p3: %q %v", got, err)
	}

	// Tampering with the header or the body must be detected.
	p4 := append([]byte(nil), s.SealDatagram(buf, 42, []byte("four"))...)
	bad := append([]byte(nil), p4...)
	bad[0] ^= 1
	if _, err := c.OpenDatagram(out, bad); err == nil {
		t.Fatal("tampered header accepted")
	}
	bad = append([]byte(nil), p4...)
	bad[len(bad)-1] ^= 1
	if _, err := c.OpenDatagram(out, bad); err == nil {
		t.Fatal("tampered body accepted")
	}
	// A failed attempt must not burn the sequence number.
	if got, err := c.OpenDatagram(out, p4); err != nil || string(got) != "four" {
		t.Fatalf("p4 after tampering attempts: %q %v", got, err)
	}

	// Datagrams and frames use separate nonce spaces and keep working together.
	if err := s.Send([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Recv(); err != nil || string(got) != "frame" {
		t.Fatalf("frame after datagrams: %q %v", got, err)
	}
	// The server must not accept its own datagrams (different direction key).
	if _, err := s.OpenDatagram(out, p4); err == nil {
		t.Fatal("datagram accepted in the wrong direction")
	}
	if _, err := c.OpenDatagram(out, []byte{1, 2, 3}); err == nil {
		t.Fatal("short datagram accepted")
	}
}

func TestNotEdgehopPeer(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() {
		buf := make([]byte, 64)
		a.Read(buf)
		a.Write(bytes.Repeat([]byte("HTTP/1.1 400 Bad Request\r\n"), 3))
	}()
	if _, err := ClientHandshake(b, "k"); err == nil {
		t.Fatal("expected an error when the peer is not edgehop")
	}
}

func TestNewKey(t *testing.T) {
	a, b := NewKey(), NewKey()
	if len(a) != 32 || a == b {
		t.Fatalf("keys: %q %q", a, b)
	}
}
