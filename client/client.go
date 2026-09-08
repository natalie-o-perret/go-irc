// Package client provides a high-level IRC client library with IRCv3 support,
// SASL authentication, and DCC/XDCC file transfer capabilities.
package client

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/natalie-o-perret/go-irc/client/sasl"
	"github.com/natalie-o-perret/go-irc/irc"
)

// HandlerFunc is a function that handles an IRC message.
type HandlerFunc func(c *Client, msg *irc.Message)

// Mux dispatches incoming messages to registered handlers.
type Mux struct {
	mu       sync.RWMutex
	handlers map[string][]HandlerFunc
}

// On registers a handler for the given command (case-insensitive).
// Use "*" to receive all messages.
func (m *Mux) On(command string, fn HandlerFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handlers == nil {
		m.handlers = make(map[string][]HandlerFunc)
	}
	cmd := strings.ToUpper(command)
	m.handlers[cmd] = append(m.handlers[cmd], fn)
}

// dispatch calls all registered handlers for the command and all wildcard handlers.
func (m *Mux) dispatch(c *Client, msg *irc.Message) {
	m.mu.RLock()
	cmds := append([]HandlerFunc(nil), m.handlers[msg.Command]...)
	wilds := append([]HandlerFunc(nil), m.handlers["*"]...)
	m.mu.RUnlock()
	for _, fn := range cmds {
		fn(c, msg)
	}
	for _, fn := range wilds {
		fn(c, msg)
	}
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Config holds all connection parameters.
type Config struct {
	// Network address to connect to (host:port).
	Addr string
	// Nick to use.
	Nick string
	// Username (for USER command).
	User string
	// Real name.
	RealName string
	// Optional server password (PASS command).
	Password string
	// TLS configuration; nil means plaintext.
	TLS *tls.Config
	// SASL mechanism; nil disables SASL.
	SASL sasl.Mechanism
	// IRCv3 capabilities to request (in addition to defaults).
	RequestedCaps []string
	// Delay between reconnect attempts.
	ReconnectDelay time.Duration
	// If true, automatically reconnect on disconnect.
	AutoReconnect bool
	// Timeout for the initial connection.
	ConnectTimeout time.Duration
}

func (c *Config) setDefaults() {
	if c.Nick == "" {
		c.Nick = "gopher"
	}
	if c.User == "" {
		c.User = "gopher"
	}
	if c.RealName == "" {
		c.RealName = "Gopher"
	}
	if c.ReconnectDelay <= 0 {
		c.ReconnectDelay = 10 * time.Second
	}
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = 30 * time.Second
	}
}

// ---------------------------------------------------------------------------
// CAPState
// ---------------------------------------------------------------------------

type capPhase int

const (
	capPhaseIdle    capPhase = iota
	capPhaseLSSent           // waiting for CAP LS response
	capPhaseREQSent          // waiting for CAP ACK/NAK
	capPhaseSASL             // SASL negotiation in progress
	capPhaseDone             // CAP END sent
)

// CAPState tracks IRCv3 capability negotiation.
type CAPState struct {
	phase      capPhase
	advertised map[string]string // cap -> optional value
	enabled    map[string]bool
	pending    []string // caps currently being REQ'd
}

func newCAPState() *CAPState {
	return &CAPState{
		advertised: make(map[string]string),
		enabled:    make(map[string]bool),
	}
}

// Enabled returns whether a capability is currently active.
func (cs *CAPState) Enabled(cap string) bool {
	return cs.enabled[cap]
}

// Advertised returns the advertised caps map.
func (cs *CAPState) Advertised() map[string]string {
	return cs.advertised
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client is a single IRC connection.
type Client struct {
	cfg      Config
	mux      Mux
	capState *CAPState

	mu       sync.Mutex
	conn     net.Conn
	writer   *bufio.Writer
	nick     string
	loggedIn bool

	// SASL state
	saslMech   sasl.Mechanism
	saslBuffer string

	done chan struct{}
}

// New creates a new Client with the given config.
func New(cfg Config) *Client {
	cfg.setDefaults()
	c := &Client{
		cfg:      cfg,
		nick:     cfg.Nick,
		capState: newCAPState(),
		done:     make(chan struct{}),
	}
	c.installBuiltinHandlers()
	return c
}

// On registers a handler. Returns c for chaining.
func (c *Client) On(command string, fn HandlerFunc) *Client {
	c.mux.On(command, fn)
	return c
}

// Send writes a Message to the server.
func (c *Client) Send(msg *irc.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writer == nil {
		return fmt.Errorf("client: not connected")
	}
	line := msg.String() + "\r\n"
	if _, err := c.writer.WriteString(line); err != nil {
		return err
	}
	return c.writer.Flush()
}

// Sendf builds a simple message and sends it.
func (c *Client) Sendf(cmd string, params ...string) error {
	return c.Send(&irc.Message{Command: cmd, Params: params})
}

// Nick returns the current nick.
func (c *Client) Nick() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nick
}

// CAPEnabled returns true if the given IRCv3 cap is currently negotiated.
func (c *Client) CAPEnabled(cap string) bool {
	return c.capState.Enabled(cap)
}

// Connect dials the server and runs the read loop.  It blocks until the
// connection is closed, then returns the connection error (nil on clean close).
func (c *Client) Connect() error {
	var conn net.Conn
	var err error

	dialer := &net.Dialer{Timeout: c.cfg.ConnectTimeout}
	if c.cfg.TLS != nil {
		conn, err = tls.DialWithDialer(dialer, "tcp", c.cfg.Addr, c.cfg.TLS)
	} else {
		conn, err = dialer.Dial("tcp", c.cfg.Addr)
	}
	if err != nil {
		return fmt.Errorf("client: connect %s: %w", c.cfg.Addr, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.writer = bufio.NewWriter(conn)
	c.capState = newCAPState()
	c.loggedIn = false
	c.saslMech = nil
	c.saslBuffer = ""
	c.done = make(chan struct{})
	c.mu.Unlock()

	// Kick off CAP negotiation
	c.capState.phase = capPhaseLSSent
	if err := c.Sendf(irc.CAP, irc.CapLS, "302"); err != nil {
		_ = conn.Close()
		return err
	}

	if c.cfg.Password != "" {
		_ = c.Sendf(irc.PASS, c.cfg.Password)
	}
	_ = c.Sendf(irc.NICK, c.cfg.Nick)
	_ = c.Sendf(irc.USER, c.cfg.User, "0", "*", c.cfg.RealName)

	return c.readLoop(conn)
}

// Disconnect sends QUIT and closes the connection.
func (c *Client) Disconnect(message string) {
	if message != "" {
		_ = c.Sendf(irc.QUIT, message)
	} else {
		_ = c.Sendf(irc.QUIT)
	}
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.mu.Unlock()
}

// Wait blocks until the client's run loop exits.
func (c *Client) Wait() {
	<-c.done
}

func (c *Client) readLoop(conn net.Conn) error {
	defer func() {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 16384), 16384)
	for scanner.Scan() {
		line := scanner.Text()
		msg, err := irc.Parse(line)
		if err != nil {
			continue
		}
		c.mux.dispatch(c, msg)
	}
	return scanner.Err()
}

// ---------------------------------------------------------------------------
// Built-in message handlers
// ---------------------------------------------------------------------------

func (c *Client) installBuiltinHandlers() {
	c.mux.On(irc.PING, c.handlePing)
	c.mux.On(irc.CAP, c.handleCAP)
	c.mux.On(irc.AUTHENTICATE, c.handleAuthenticate)
	c.mux.On("001", c.handleWelcome)
	c.mux.On("433", c.handleNickInUse)
	c.mux.On("902", c.handleSASLFail)
	c.mux.On("903", c.handleSASLSuccess)
	c.mux.On("904", c.handleSASLFail)
	c.mux.On("905", c.handleSASLFail)
	c.mux.On("906", c.handleSASLFail)
	c.mux.On("907", c.handleSASLFail)
	c.mux.On(irc.NICK, c.handleNickChange)
}

func (c *Client) handlePing(_ *Client, msg *irc.Message) {
	_ = c.Sendf(irc.PONG, msg.Params...)
}

func (c *Client) handleNickChange(_ *Client, msg *irc.Message) {
	if msg.Prefix != nil && msg.Prefix.Nick == c.Nick() && len(msg.Params) > 0 {
		c.mu.Lock()
		c.nick = msg.Params[0]
		c.mu.Unlock()
	}
}

func (c *Client) handleNickInUse(_ *Client, _ *irc.Message) {
	c.mu.Lock()
	newNick := c.nick + "_"
	c.mu.Unlock()
	_ = c.Sendf(irc.NICK, newNick)
}

func (c *Client) handleWelcome(_ *Client, msg *irc.Message) {
	if len(msg.Params) > 0 {
		c.mu.Lock()
		c.nick = msg.Params[0]
		c.loggedIn = true
		c.mu.Unlock()
	}
}

// handleCAP drives the IRCv3 CAP negotiation state machine.
func (c *Client) handleCAP(_ *Client, msg *irc.Message) {
	if len(msg.Params) < 2 {
		return
	}
	subCmd := strings.ToUpper(msg.Params[1])

	switch subCmd {
	case irc.CapLS:
		multiLine := false
		capStr := ""
		if len(msg.Params) >= 3 && msg.Params[2] == "*" {
			multiLine = true
			if len(msg.Params) >= 4 {
				capStr = msg.Params[3]
			}
		} else if len(msg.Params) >= 3 {
			capStr = msg.Params[2]
		}

		for _, token := range strings.Fields(capStr) {
			k, v, _ := strings.Cut(token, "=")
			c.capState.advertised[k] = v
		}

		if multiLine {
			return // more LS lines incoming
		}
		c.capState.enabled[irc.CapCapNotify] = true

		want := c.capWantList()
		if len(want) > 0 {
			c.capState.pending = want
			c.capState.phase = capPhaseREQSent
			_ = c.Sendf(irc.CAP, irc.CapREQ, strings.Join(want, " "))
		} else {
			c.capEnd()
		}

	case irc.CapACK:
		capStr := ""
		if len(msg.Params) >= 3 {
			capStr = strings.TrimPrefix(msg.Params[2], ":")
		}
		for _, cap := range strings.Fields(capStr) {
			if strings.HasPrefix(cap, "-") {
				delete(c.capState.enabled, strings.TrimPrefix(cap, "-"))
			} else {
				c.capState.enabled[cap] = true
			}
		}

		if c.capState.enabled[irc.CapSASL] && c.cfg.SASL != nil {
			c.capState.phase = capPhaseSASL
			c.saslMech = c.cfg.SASL
			if resetter, ok := c.saslMech.(interface{ Reset() }); ok {
				resetter.Reset()
			}
			_ = c.Sendf(irc.AUTHENTICATE, c.saslMech.Name())
		} else {
			c.capEnd()
		}

	case irc.CapNAK:
		// Cap request denied; move on
		c.capEnd()

	case irc.CapNEW:
		if len(msg.Params) >= 3 {
			for _, token := range strings.Fields(msg.Params[2]) {
				k, v, _ := strings.Cut(token, "=")
				c.capState.advertised[k] = v
			}
		}

	case irc.CapDEL:
		if len(msg.Params) >= 3 {
			for _, cap := range strings.Fields(msg.Params[2]) {
				delete(c.capState.advertised, cap)
				delete(c.capState.enabled, cap)
			}
		}
	}
}

func (c *Client) handleAuthenticate(_ *Client, msg *irc.Message) {
	if c.saslMech == nil || len(msg.Params) == 0 {
		return
	}
	chunk := msg.Params[0]
	if len(chunk) > 400 {
		c.abortSASL()
		return
	}
	if chunk != "+" {
		c.saslBuffer += chunk
	}
	if len(chunk) == 400 {
		return
	}

	var challenge []byte
	if c.saslBuffer != "" {
		var err error
		challenge, err = base64.StdEncoding.DecodeString(c.saslBuffer)
		if err != nil {
			c.abortSASL()
			return
		}
	}
	c.saslBuffer = ""

	resp, _, err := c.saslMech.Next(challenge)
	if err != nil {
		c.abortSASL()
		return
	}
	encoded := base64.StdEncoding.EncodeToString(resp)
	if encoded == "" {
		_ = c.Sendf(irc.AUTHENTICATE, "+")
		return
	}
	for len(encoded) >= 400 {
		_ = c.Sendf(irc.AUTHENTICATE, encoded[:400])
		encoded = encoded[400:]
	}
	if encoded != "" {
		_ = c.Sendf(irc.AUTHENTICATE, encoded)
	} else {
		_ = c.Sendf(irc.AUTHENTICATE, "+")
	}
}

func (c *Client) handleSASLSuccess(_ *Client, _ *irc.Message) {
	c.saslMech = nil
	c.saslBuffer = ""
	c.capEnd()
}

func (c *Client) handleSASLFail(_ *Client, _ *irc.Message) {
	c.saslMech = nil
	c.saslBuffer = ""
	c.capEnd()
}

func (c *Client) abortSASL() {
	_ = c.Sendf(irc.AUTHENTICATE, "*")
	c.saslMech = nil
	c.saslBuffer = ""
}

func (c *Client) capEnd() {
	c.capState.phase = capPhaseDone
	_ = c.Sendf(irc.CAP, irc.CapEND)
}

// capWantList returns the subset of desired caps that the server advertises.
func (c *Client) capWantList() []string {
	defaults := []string{
		irc.CapServerTime,
		irc.CapMessageTags,
		irc.CapBatch,
		irc.CapEchoMessage,
		irc.CapMultiPrefix,
		irc.CapAwayNotify,
		irc.CapExtendedJoin,
		irc.CapChghost,
		irc.CapSetname,
		irc.CapAccountTag,
		irc.CapCapNotify,
		irc.CapChatHistory,
		irc.CapLabeledResponse,
		irc.CapUserHostInNames,
		irc.CapInviteNotify,
		irc.CapAccountNotify,
	}
	if c.cfg.SASL != nil {
		defaults = append(defaults, irc.CapSASL)
	}
	defaults = append(defaults, c.cfg.RequestedCaps...)

	seen := make(map[string]bool)
	var want []string
	for _, cap := range defaults {
		if seen[cap] {
			continue
		}
		seen[cap] = true
		if c.capState.enabled[cap] {
			continue
		}
		value, ok := c.capState.advertised[cap]
		if ok && cap == irc.CapSASL && value != "" {
			ok = false
			for _, mechanism := range strings.Split(value, ",") {
				if strings.EqualFold(mechanism, c.cfg.SASL.Name()) {
					ok = true
					break
				}
			}
		}
		if ok {
			want = append(want, cap)
		}
	}
	return want
}
