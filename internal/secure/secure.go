// Package secure provides the encrypted, authenticated channel between the
// server and a client. Everything typed on the keyboard crosses the network,
// so nothing is ever sent in the clear.
//
// Handshake (over TCP):
//
//	client -> server   "EHOP" | version | client ephemeral X25519 public key
//	server -> client   "EHOP" | version | server ephemeral X25519 public key
//	server -> client   encrypted confirmation frame
//
// Both sides derive two AES-256-GCM keys (one per direction) with
// HKDF-SHA256 from the X25519 shared secret, salted with the pre-shared key
// from the config file. Without the pre-shared key the session keys cannot
// be computed, so a peer that can complete the handshake is authenticated,
// and the ephemeral keys give forward secrecy.
//
// After the handshake, TCP frames are `u16 length | ciphertext` with an
// implicit counter nonce. UDP datagrams are
// `u32 session | u64 sequence | ciphertext`; the header is authenticated and
// a datagram is accepted only if its sequence number is newer than every
// datagram accepted before (which also discards reordered cursor updates).
package secure

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	magic        = "EHOP"
	wireVersion  = 1
	helloLen     = len(magic) + 1 + 32
	maxFrame     = 8 * 1024
	dgHeaderLen  = 12
	nonceTCP     = 0
	nonceUDP     = 1
	confirmation = "edgehop-ok"

	// HandshakeTimeout bounds the whole handshake.
	HandshakeTimeout = 5 * time.Second
	// WriteTimeout bounds a single TCP write, so a stalled peer cannot block
	// the sender forever.
	WriteTimeout = 3 * time.Second
)

var (
	// ErrBadKey means the peer does not have the same pre-shared key.
	ErrBadKey = errors.New("authentication failed: the `key` in the two config files does not match")
	// ErrReplay is returned for a datagram that is not newer than the last one.
	ErrReplay = errors.New("stale datagram")
	errFrame  = errors.New("invalid frame")
)

// NewKey returns a fresh random pre-shared key as a hex string.
func NewKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Channel is an established encrypted connection.
// Send may be called from several goroutines; Recv from one.
type Channel struct {
	conn net.Conn
	br   *bufio.Reader

	send, recv cipher.AEAD

	wmu     sync.Mutex
	sendCtr uint64
	wbuf    []byte

	recvCtr uint64
	rbuf    []byte

	dgSend atomic.Uint64
	dgRecv atomic.Uint64
}

func hkdf(salt, ikm, info []byte, n int) []byte {
	ext := hmac.New(sha256.New, salt)
	ext.Write(ikm)
	prk := ext.Sum(nil)
	var out, prev []byte
	for i := byte(1); len(out) < n; i++ {
		h := hmac.New(sha256.New, prk)
		h.Write(prev)
		h.Write(info)
		h.Write([]byte{i})
		prev = h.Sum(nil)
		out = append(out, prev...)
	}
	return out[:n]
}

func newAEAD(key []byte) cipher.AEAD {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return a
}

func nonce(domain byte, ctr uint64) [12]byte {
	var n [12]byte
	n[0] = domain
	binary.BigEndian.PutUint64(n[4:], ctr)
	return n
}

func newChannel(conn net.Conn, br *bufio.Reader, key string, shared, clientPub, serverPub []byte, isClient bool) *Channel {
	psk := sha256.Sum256([]byte("edgehop-psk:" + key))
	info := append([]byte("edgehop v1 session keys"), clientPub...)
	info = append(info, serverPub...)
	okm := hkdf(psk[:], shared, info, 64)
	c2s, s2c := newAEAD(okm[:32]), newAEAD(okm[32:])
	ch := &Channel{conn: conn, br: br, wbuf: make([]byte, 0, 512), rbuf: make([]byte, 512)}
	if isClient {
		ch.send, ch.recv = c2s, s2c
	} else {
		ch.send, ch.recv = s2c, c2s
	}
	return ch
}

func writeHello(conn net.Conn, pub []byte) error {
	msg := append([]byte(magic), wireVersion)
	msg = append(msg, pub...)
	_, err := conn.Write(msg)
	return err
}

func readHello(r io.Reader) (*ecdh.PublicKey, error) {
	buf := make([]byte, helloLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	if string(buf[:len(magic)]) != magic {
		return nil, errors.New("the peer is not edgehop")
	}
	if v := buf[len(magic)]; v != wireVersion {
		return nil, fmt.Errorf("incompatible edgehop version on the other side (wire version %d, ours %d)", v, wireVersion)
	}
	return ecdh.X25519().NewPublicKey(buf[len(magic)+1:])
}

// ClientHandshake runs the client side of the handshake.
func ClientHandshake(conn net.Conn, key string) (*Channel, error) {
	conn.SetDeadline(time.Now().Add(HandshakeTimeout))
	defer conn.SetDeadline(time.Time{})

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := writeHello(conn, priv.PublicKey().Bytes()); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(conn, 4096)
	peer, err := readHello(br)
	if err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	shared, err := priv.ECDH(peer)
	if err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	ch := newChannel(conn, br, key, shared, priv.PublicKey().Bytes(), peer.Bytes(), true)
	msg, err := ch.Recv()
	if err != nil {
		if errors.Is(err, errAuth) {
			return nil, ErrBadKey
		}
		return nil, fmt.Errorf("handshake: %w", err)
	}
	if string(msg) != confirmation {
		return nil, ErrBadKey
	}
	return ch, nil
}

// ServerHandshake runs the server side of the handshake. A client with the
// wrong key is detected when its first frame fails to decrypt.
func ServerHandshake(conn net.Conn, key string) (*Channel, error) {
	conn.SetDeadline(time.Now().Add(HandshakeTimeout))
	defer conn.SetDeadline(time.Time{})

	br := bufio.NewReaderSize(conn, 4096)
	peer, err := readHello(br)
	if err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(peer)
	if err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	if err := writeHello(conn, priv.PublicKey().Bytes()); err != nil {
		return nil, err
	}
	ch := newChannel(conn, br, key, shared, peer.Bytes(), priv.PublicKey().Bytes(), false)
	if err := ch.Send([]byte(confirmation)); err != nil {
		return nil, err
	}
	return ch, nil
}

// Conn returns the underlying connection.
func (c *Channel) Conn() net.Conn { return c.conn }

// Close closes the underlying connection.
func (c *Channel) Close() error { return c.conn.Close() }

// Send encrypts p and writes it as one frame.
func (c *Channel) Send(p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	n := len(p) + c.send.Overhead()
	if n > maxFrame {
		return errFrame
	}
	buf := append(c.wbuf[:0], byte(n>>8), byte(n))
	nc := nonce(nonceTCP, c.sendCtr)
	c.sendCtr++
	buf = c.send.Seal(buf, nc[:], p, nil)
	c.wbuf = buf
	c.conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	_, err := c.conn.Write(buf)
	return err
}

var errAuth = errors.New("frame failed authentication")

// Recv reads and decrypts one frame. The returned slice is valid until the
// next call to Recv.
func (c *Channel) Recv() ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		return nil, err
	}
	n := int(hdr[0])<<8 | int(hdr[1])
	if n < c.recv.Overhead() || n > maxFrame {
		return nil, errFrame
	}
	if cap(c.rbuf) < n {
		c.rbuf = make([]byte, n)
	}
	buf := c.rbuf[:n]
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, err
	}
	nc := nonce(nonceTCP, c.recvCtr)
	c.recvCtr++
	pt, err := c.recv.Open(buf[:0], nc[:], buf, nil)
	if err != nil {
		return nil, errAuth
	}
	return pt, nil
}

// SealDatagram encrypts payload into a datagram, reusing dst's storage.
// It may be called concurrently with Send.
func (c *Channel) SealDatagram(dst []byte, session uint32, payload []byte) []byte {
	seq := c.dgSend.Add(1)
	dst = binary.BigEndian.AppendUint32(dst[:0], session)
	dst = binary.BigEndian.AppendUint64(dst, seq)
	nc := nonce(nonceUDP, seq)
	return c.send.Seal(dst, nc[:], payload, dst[:dgHeaderLen])
}

// DatagramSession returns the session id in a datagram header.
func DatagramSession(pkt []byte) (uint32, bool) {
	if len(pkt) < dgHeaderLen {
		return 0, false
	}
	return binary.BigEndian.Uint32(pkt), true
}

// OpenDatagram verifies and decrypts a datagram into dst's storage.
// Datagrams that are not newer than the newest one accepted so far are
// rejected with ErrReplay.
func (c *Channel) OpenDatagram(dst, pkt []byte) ([]byte, error) {
	if len(pkt) < dgHeaderLen+c.recv.Overhead() {
		return nil, errFrame
	}
	seq := binary.BigEndian.Uint64(pkt[4:dgHeaderLen])
	if seq <= c.dgRecv.Load() {
		return nil, ErrReplay
	}
	nc := nonce(nonceUDP, seq)
	pt, err := c.recv.Open(dst[:0], nc[:], pkt[dgHeaderLen:], pkt[:dgHeaderLen])
	if err != nil {
		return nil, errAuth
	}
	for {
		old := c.dgRecv.Load()
		if seq <= old {
			return nil, ErrReplay
		}
		if c.dgRecv.CompareAndSwap(old, seq) {
			return pt, nil
		}
	}
}
