// Package proto defines the messages exchanged between the server (the
// computer the mouse and keyboard are physically connected to) and clients.
//
// Every message starts with a one byte type. Integers are big endian.
// Messages travel inside the encrypted channel from package secure, either as
// TCP frames (reliable, ordered) or as UDP datagrams (cursor motion only).
package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Version is bumped whenever the wire format changes incompatibly.
const Version = 1

// Message types.
const (
	THello    = 1  // client -> server: version, screen size, os, name
	TWelcome  = 2  // server -> client: session id, server name
	TReject   = 3  // server -> client: reason
	TEnter    = 4  // server -> client: the cursor enters this screen at x,y
	TLeave    = 5  // server -> client: the cursor left this screen
	TMotion   = 6  // server -> client: absolute cursor position (TCP or UDP)
	TButton   = 7  // server -> client: mouse button, with the cursor position
	TWheel    = 8  // server -> client: scroll, in 1/120 of a notch
	TKey      = 9  // server -> client: key up / down / repeat
	TPing     = 10 // server -> client
	TPong     = 11 // client -> server
	TUDPHello = 12 // client -> server over UDP: "this is my UDP address"
)

// Msg is a decoded message. Only the fields of its Type are meaningful.
type Msg struct {
	Type    uint8
	Version uint8
	Session uint32
	Seq     uint32 // motion sequence number
	X, Y    int32
	W, H    uint32
	Button  uint8
	Down    bool
	DX, DY  int32
	Code    uint16
	State   uint8
	T       uint64
	Name    string
	OS      string
	Reason  string
}

var be = binary.BigEndian

func appendStr(b []byte, s string) []byte {
	if len(s) > 255 {
		s = s[:255]
	}
	b = append(b, byte(len(s)))
	return append(b, s...)
}

func AppendHello(b []byte, name, os string, w, h int) []byte {
	b = append(b, THello, Version)
	b = be.AppendUint32(b, uint32(w))
	b = be.AppendUint32(b, uint32(h))
	b = appendStr(b, os)
	return appendStr(b, name)
}

func AppendWelcome(b []byte, session uint32, serverName string) []byte {
	b = append(b, TWelcome)
	b = be.AppendUint32(b, session)
	return appendStr(b, serverName)
}

func AppendReject(b []byte, reason string) []byte {
	b = append(b, TReject)
	return appendStr(b, reason)
}

func appendPos(b []byte, typ uint8, seq uint32, x, y int32) []byte {
	b = append(b, typ)
	b = be.AppendUint32(b, seq)
	b = be.AppendUint32(b, uint32(x))
	return be.AppendUint32(b, uint32(y))
}

func AppendEnter(b []byte, seq uint32, x, y int32) []byte {
	return appendPos(b, TEnter, seq, x, y)
}

func AppendLeave(b []byte) []byte { return append(b, TLeave) }

func AppendMotion(b []byte, seq uint32, x, y int32) []byte {
	return appendPos(b, TMotion, seq, x, y)
}

func AppendButton(b []byte, seq uint32, x, y int32, button uint8, down bool) []byte {
	b = appendPos(b, TButton, seq, x, y)
	d := byte(0)
	if down {
		d = 1
	}
	return append(b, button, d)
}

func AppendWheel(b []byte, dx, dy int32) []byte {
	b = append(b, TWheel)
	b = be.AppendUint32(b, uint32(dx))
	return be.AppendUint32(b, uint32(dy))
}

func AppendKey(b []byte, code uint16, state uint8) []byte {
	b = append(b, TKey)
	b = be.AppendUint16(b, code)
	return append(b, state)
}

func AppendPing(b []byte, t uint64) []byte {
	return be.AppendUint64(append(b, TPing), t)
}

func AppendPong(b []byte, t uint64) []byte {
	return be.AppendUint64(append(b, TPong), t)
}

func AppendUDPHello(b []byte) []byte { return append(b, TUDPHello) }

var errShort = errors.New("proto: message too short")

type reader struct {
	b   []byte
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if len(r.b) < n {
		r.err = errShort
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *reader) u8() uint8 {
	if v := r.take(1); v != nil {
		return v[0]
	}
	return 0
}

func (r *reader) u16() uint16 {
	if v := r.take(2); v != nil {
		return be.Uint16(v)
	}
	return 0
}

func (r *reader) u32() uint32 {
	if v := r.take(4); v != nil {
		return be.Uint32(v)
	}
	return 0
}

func (r *reader) u64() uint64 {
	if v := r.take(8); v != nil {
		return be.Uint64(v)
	}
	return 0
}

func (r *reader) str() string {
	n := int(r.u8())
	return string(r.take(n))
}

// Decode parses one message.
func Decode(b []byte) (Msg, error) {
	var m Msg
	if len(b) == 0 {
		return m, errShort
	}
	r := reader{b: b[1:]}
	m.Type = b[0]
	switch m.Type {
	case THello:
		m.Version = r.u8()
		m.W, m.H = r.u32(), r.u32()
		m.OS = r.str()
		m.Name = r.str()
	case TWelcome:
		m.Session = r.u32()
		m.Name = r.str()
	case TReject:
		m.Reason = r.str()
	case TEnter, TMotion:
		m.Seq = r.u32()
		m.X, m.Y = int32(r.u32()), int32(r.u32())
	case TButton:
		m.Seq = r.u32()
		m.X, m.Y = int32(r.u32()), int32(r.u32())
		m.Button = r.u8()
		m.Down = r.u8() != 0
	case TWheel:
		m.DX, m.DY = int32(r.u32()), int32(r.u32())
	case TKey:
		m.Code = r.u16()
		m.State = r.u8()
	case TPing, TPong:
		m.T = r.u64()
	case TLeave, TUDPHello:
	default:
		return m, fmt.Errorf("proto: unknown message type %d", m.Type)
	}
	return m, r.err
}
