// Package dcc implements the DCC (Direct Client-to-Client) protocol.
// It supports DCC SEND/RECV with resume and passive DCC.
package dcc

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// TransferState represents the state of a DCC transfer.
type TransferState int32

const (
	StatePending   TransferState = iota // waiting to start
	StateActive                         // transfer in progress
	StateCompleted                      // successfully finished
	StateFailed                         // error occurred
	StateCancelled                      // user cancelled
)

func (s TransferState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateActive:
		return "active"
	case StateCompleted:
		return "completed"
	case StateFailed:
		return "failed"
	case StateCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// Session represents a single DCC file transfer session.
type Session struct {
	ID          uint32
	Filename    string // cleaned base filename
	Size        int64  // -1 if unknown
	Transferred atomic.Int64
	State       atomic.Int32 // TransferState
	Passive     bool         // passive DCC (bot dials us)
	Token       uint32       // passive DCC token
	Resume      int64        // byte offset to resume from
	Err         error        // set on failure

	conn   net.Conn
	mu     sync.Mutex
	doneCh chan struct{}
}

// newSession creates a new Session.
func newSession(id uint32, filename string, size int64) *Session {
	s := &Session{
		ID:       id,
		Filename: filepath.Base(filename),
		Size:     size,
		doneCh:   make(chan struct{}),
	}
	s.State.Store(int32(StatePending))
	return s
}

// Wait blocks until the session is done (completed, failed, or cancelled).
func (s *Session) Wait() error {
	<-s.doneCh
	return s.Err
}

// Cancel attempts to cancel the transfer.
func (s *Session) Cancel() {
	s.State.Store(int32(StateCancelled))
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.mu.Unlock()
}

func (s *Session) setConn(c net.Conn) {
	s.mu.Lock()
	s.conn = c
	s.mu.Unlock()
}

func (s *Session) finish(err error) {
	s.mu.Lock()
	if err != nil && TransferState(s.State.Load()) != StateCancelled {
		s.Err = err
		s.State.Store(int32(StateFailed))
	} else if err == nil {
		s.State.Store(int32(StateCompleted))
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.mu.Unlock()
	select {
	case <-s.doneCh:
	default:
		close(s.doneCh)
	}
}

// Progress returns bytes transferred and total size.
func (s *Session) Progress() (transferred, total int64) {
	return s.Transferred.Load(), s.Size
}

// ---------------------------------------------------------------------------
// Send request / offer
// ---------------------------------------------------------------------------

// SendRequest describes an incoming DCC SEND offer.
type SendRequest struct {
	Filename string
	IP       net.IP
	Port     uint16
	Size     int64
	Token    uint32 // non-zero → passive DCC
	Passive  bool
}

// ParseSendRequest parses a DCC SEND CTCP argument string.
// Format: SEND <filename> <ip-as-uint32> <port> [<size>] [<token>]
func ParseSendRequest(args string) (*SendRequest, error) {
	parts := strings.Fields(args)
	if len(parts) < 3 {
		return nil, fmt.Errorf("dcc: malformed SEND: %q", args)
	}
	req := &SendRequest{Filename: parts[0]}

	ipInt, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		// Try dotted-quad
		req.IP = net.ParseIP(parts[1])
		if req.IP == nil {
			return nil, fmt.Errorf("dcc: invalid IP %q", parts[1])
		}
	} else {
		req.IP = uint32ToIP(uint32(ipInt))
	}

	port, err := strconv.ParseUint(parts[2], 10, 16)
	if err != nil {
		return nil, fmt.Errorf("dcc: invalid port %q", parts[2])
	}
	req.Port = uint16(port)
	if req.Port == 0 {
		req.Passive = true
	}

	if len(parts) >= 4 {
		req.Size, _ = strconv.ParseInt(parts[3], 10, 64)
	}
	if len(parts) >= 5 {
		tok, _ := strconv.ParseUint(parts[4], 10, 32)
		req.Token = uint32(tok)
		req.Passive = true
	}

	return req, nil
}

// FormatSendOffer formats a DCC SEND CTCP message for an active offer.
// ip is the local IP; port is the listening port (0 for passive).
func FormatSendOffer(filename string, ip net.IP, port uint16, size int64, token uint32) string {
	ipInt := ipToUint32(ip)
	if token != 0 {
		return fmt.Sprintf("SEND %s %d %d %d %d", filename, ipInt, port, size, token)
	}
	return fmt.Sprintf("SEND %s %d %d %d", filename, ipInt, port, size)
}

// FormatResumeRequest formats a DCC RESUME CTCP argument.
func FormatResumeRequest(filename string, port uint16, pos int64) string {
	return fmt.Sprintf("RESUME %s %d %d", filename, port, pos)
}

// FormatResumeAccept formats a DCC ACCEPT CTCP argument.
func FormatResumeAccept(filename string, port uint16, pos int64) string {
	return fmt.Sprintf("ACCEPT %s %d %d", filename, port, pos)
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// Manager manages DCC transfer sessions.
type Manager struct {
	mu       sync.Mutex
	sessions map[uint32]*Session
	nextID   uint32
}

// NewManager creates a new Manager.
func NewManager() *Manager {
	return &Manager{sessions: make(map[uint32]*Session)}
}

// newID generates a new session ID.
func (m *Manager) newID() uint32 {
	m.nextID++
	return m.nextID
}

// Get returns an active session by ID.
func (m *Manager) Get(id uint32) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// Sessions returns all current sessions.
func (m *Manager) Sessions() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// Receive accepts an incoming DCC SEND offer and downloads the file to destDir.
// Returns the Session immediately; the transfer runs in the background.
func (m *Manager) Receive(req *SendRequest, destDir string) (*Session, error) {
	m.mu.Lock()
	id := m.newID()
	sess := newSession(id, req.Filename, req.Size)
	m.sessions[id] = sess
	m.mu.Unlock()

	go m.runReceive(sess, req, destDir)
	return sess, nil
}

func (m *Manager) runReceive(sess *Session, req *SendRequest, destDir string) {
	defer func() {
		m.mu.Lock()
		delete(m.sessions, sess.ID)
		m.mu.Unlock()
	}()

	// Determine dest path
	destPath := filepath.Join(destDir, filepath.Base(req.Filename))

	// Open file
	flags := os.O_CREATE | os.O_WRONLY
	offset := sess.Resume
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(destPath, flags, 0o644)
	if err != nil {
		sess.finish(err)
		return
	}
	defer func() { _ = f.Close() }()

	// Dial the sender
	addr := net.JoinHostPort(req.IP.String(), strconv.Itoa(int(req.Port)))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		sess.finish(fmt.Errorf("dcc: dial %s: %w", addr, err))
		return
	}
	sess.setConn(conn)
	sess.State.Store(int32(StateActive))
	sess.Transferred.Store(offset)

	buf := make([]byte, 32*1024)
	ackBuf := make([]byte, 4)
	for {
		if TransferState(sess.State.Load()) == StateCancelled {
			sess.finish(nil)
			return
		}
		n, rerr := conn.Read(buf)
		if n > 0 {
			written, werr := f.Write(buf[:n])
			if werr != nil {
				sess.finish(werr)
				return
			}
			transferred := sess.Transferred.Add(int64(written))
			// Send ACK (big-endian uint32)
			binary.BigEndian.PutUint32(ackBuf, uint32(transferred))
			if _, err := conn.Write(ackBuf); err != nil {
				sess.finish(err)
				return
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			sess.finish(rerr)
			return
		}
	}
	sess.finish(nil)
}

// Send offers a local file via DCC SEND.  Returns the session and the CTCP
// message body to send to the target nick (caller must wrap it in a PRIVMSG
// with CTCP delimiters).  The transfer begins once the receiver connects.
func (m *Manager) Send(filename string, listenAddr string) (*Session, string, error) {
	fi, err := os.Stat(filename)
	if err != nil {
		return nil, "", err
	}

	m.mu.Lock()
	id := m.newID()
	sess := newSession(id, filename, fi.Size())
	m.sessions[id] = sess
	m.mu.Unlock()

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
		_ = ln.Close()
		return nil, "", err
	}

	addr := ln.Addr().(*net.TCPAddr)
	localIP := addr.IP
	if localIP == nil || localIP.IsUnspecified() {
		// Fallback to loopback for display (caller should override)
		localIP = net.IP{127, 0, 0, 1}
	}

	ctcp := FormatSendOffer(filepath.Base(filename), localIP, uint16(addr.Port), fi.Size(), 0)

	go m.runSend(sess, ln, filename)

	return sess, ctcp, nil
}

func (m *Manager) runSend(sess *Session, ln net.Listener, filename string) {
	defer func() {
		_ = ln.Close()
		m.mu.Lock()
		delete(m.sessions, sess.ID)
		m.mu.Unlock()
	}()

	conn, err := ln.Accept()
	if err != nil {
		sess.finish(err)
		return
	}
	sess.setConn(conn)
	sess.State.Store(int32(StateActive))

	f, err := os.Open(filename)
	if err != nil {
		sess.finish(err)
		return
	}
	defer func() { _ = f.Close() }()

	if sess.Resume > 0 {
		if _, err := f.Seek(sess.Resume, io.SeekStart); err != nil {
			sess.finish(err)
			return
		}
		sess.Transferred.Store(sess.Resume)
	}

	buf := make([]byte, 32*1024)
	ackBuf := make([]byte, 4)
	for {
		if TransferState(sess.State.Load()) == StateCancelled {
			sess.finish(nil)
			return
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			if _, err := conn.Write(buf[:n]); err != nil {
				sess.finish(err)
				return
			}
			sess.Transferred.Add(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			sess.finish(rerr)
			return
		}
		// Read ACK (we don't strictly need to verify it but we consume it)
		_, _ = conn.Read(ackBuf)
	}
	// Wait for final ACK
	_, _ = conn.Read(ackBuf)
	sess.finish(nil)
}

// ---------------------------------------------------------------------------
// CTCP helpers
// ---------------------------------------------------------------------------

const ctcpDelim = "\x01"

// EncodeCTCP wraps a CTCP command and args in x01 delimiters.
func EncodeCTCP(cmd, args string) string {
	if args == "" {
		return ctcpDelim + cmd + ctcpDelim
	}
	return ctcpDelim + cmd + " " + args + ctcpDelim
}

// DecodeCTCP strips x01 delimiters and returns the command and args.
func DecodeCTCP(text string) (cmd, args string, ok bool) {
	if len(text) < 2 || text[0] != '\x01' || text[len(text)-1] != '\x01' {
		return "", "", false
	}
	inner := text[1 : len(text)-1]
	if sp := strings.IndexByte(inner, ' '); sp >= 0 {
		return strings.ToUpper(inner[:sp]), inner[sp+1:], true
	}
	return strings.ToUpper(inner), "", true
}

// ---------------------------------------------------------------------------
// IP helpers
// ---------------------------------------------------------------------------

func uint32ToIP(n uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}

func ipToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return binary.BigEndian.Uint32(ip4)
}
