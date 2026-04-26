// Package server implements a full-featured IRC server (ircd) with RFC 2812
// and IRCv3 support.
package server

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/natalie-o-perret/go-irc/irc"
	"github.com/natalie-o-perret/go-irc/server/mode"
)

// ---------------------------------------------------------------------------
// Session — a connected client
// ---------------------------------------------------------------------------

// SessionState tracks where a client is in the registration handshake.
type SessionState int

const (
	StatePreReg  SessionState = iota // NICK/USER not yet received
	StateCapNeg                      // CAP negotiation in progress
	StateRegistered                  // fully registered (001 sent)
)

// Membership represents a user's presence in a channel with a prefix.
type Membership struct {
	Session *Session
	Prefix  string // e.g. "@", "+", "~", "&", "%", ""
}

// Session is a single connected IRC client managed by the server.
type Session struct {
	conn     net.Conn
	writer   *bufio.Writer
	server   *Server
	writerMu sync.Mutex

	// Registration fields
	nick     string
	user     string
	host     string
	realname string
	account  string
	away     string
	modes    *mode.Set
	caps     map[string]bool
	state    SessionState

	// CAP negotiation
	capLSReceived bool
	capVersion    int // 302 or 0

	// SASL
	saslMech string
	saslDone bool

	// Timing
	idleAt   time.Time
	signOnAt time.Time

	// Oper flag
	isOper bool
}

func newSession(conn net.Conn, srv *Server) *Session {
	host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	return &Session{
		conn:     conn,
		writer:   bufio.NewWriterSize(conn, 4096),
		server:   srv,
		host:     host,
		modes:    mode.New(),
		caps:     make(map[string]bool),
		state:    StatePreReg,
		idleAt:   time.Now(),
		signOnAt: time.Now(),
	}
}

// Prefix returns the full nick!user@host prefix for this session.
func (s *Session) Prefix() *irc.Prefix {
	return &irc.Prefix{Nick: s.nick, User: s.user, Host: s.host}
}

// Send writes a message to this session.
func (s *Session) Send(msg *irc.Message) {
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	line := msg.String() + "\r\n"
	s.writer.WriteString(line)
	s.writer.Flush()
}

// Sendf builds and sends a simple message.
func (s *Session) Sendf(cmd string, params ...string) {
	s.Send(&irc.Message{Command: cmd, Params: params})
}

// SendNumeric sends an IRC numeric reply.
func (s *Session) SendNumeric(n irc.Numeric, params ...string) {
	all := append([]string{s.nick}, params...)
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: s.server.cfg.Name},
		Command: n.String(),
		Params:  all,
	})
}

// capEnabled returns true if the session has negotiated a cap.
func (s *Session) capEnabled(cap string) bool {
	return s.caps[cap]
}

// mask returns nick!user@host.
func (s *Session) mask() string {
	return s.nick + "!" + s.user + "@" + s.host
}

// ---------------------------------------------------------------------------
// Registry — nick and channel lookup
// ---------------------------------------------------------------------------

// Registry maps lowercase nicks to sessions.
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func newRegistry() *Registry {
	return &Registry{sessions: make(map[string]*Session)}
}

func (r *Registry) Add(s *Session) error {
	key := strings.ToLower(s.nick)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[key]; exists {
		return fmt.Errorf("nick %q already in use", s.nick)
	}
	r.sessions[key] = s
	return nil
}

func (r *Registry) Remove(nick string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, strings.ToLower(nick))
}

func (r *Registry) Rename(oldNick, newNick string) error {
	oldKey := strings.ToLower(oldNick)
	newKey := strings.ToLower(newNick)
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[oldKey]
	if !ok {
		return fmt.Errorf("nick %q not found", oldNick)
	}
	if _, exists := r.sessions[newKey]; exists {
		return fmt.Errorf("nick %q already in use", newNick)
	}
	delete(r.sessions, oldKey)
	r.sessions[newKey] = s
	return nil
}

func (r *Registry) Get(nick string) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[strings.ToLower(nick)]
	return s, ok
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

func (r *Registry) All() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}

// ---------------------------------------------------------------------------
// Channel
// ---------------------------------------------------------------------------

// Channel represents an IRC channel.
type Channel struct {
	name        string
	topic       string
	topicSetBy  string
	topicSetAt  time.Time
	modes       *mode.Set
	createdAt   time.Time

	mu      sync.RWMutex
	members map[string]*Membership // lowercase nick -> Membership
}

func newChannel(name string) *Channel {
	return &Channel{
		name:      name,
		modes:     mode.New(),
		createdAt: time.Now(),
		members:   make(map[string]*Membership),
	}
}

// AddMember adds a session to the channel.
func (ch *Channel) AddMember(s *Session, prefix string) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.members[strings.ToLower(s.nick)] = &Membership{Session: s, Prefix: prefix}
}

// RemoveMember removes a session from the channel.  Returns true if the channel is now empty.
func (ch *Channel) RemoveMember(nick string) bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	delete(ch.members, strings.ToLower(nick))
	return len(ch.members) == 0
}

// HasMember returns true if nick is in the channel.
func (ch *Channel) HasMember(nick string) bool {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	_, ok := ch.members[strings.ToLower(nick)]
	return ok
}

// GetMembership returns the Membership for a nick, or nil.
func (ch *Channel) GetMembership(nick string) *Membership {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return ch.members[strings.ToLower(nick)]
}

// Members returns a snapshot of all memberships.
func (ch *Channel) Members() []*Membership {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	out := make([]*Membership, 0, len(ch.members))
	for _, m := range ch.members {
		out = append(out, m)
	}
	return out
}

// Broadcast sends a message to all members, optionally excluding one session.
func (ch *Channel) Broadcast(msg *irc.Message, exclude *Session) {
	ch.mu.RLock()
	members := make([]*Membership, 0, len(ch.members))
	for _, m := range ch.members {
		members = append(members, m)
	}
	ch.mu.RUnlock()

	for _, m := range members {
		if m.Session == exclude {
			continue
		}
		m.Session.Send(msg)
	}
}

// NamesReply returns the NAMES list for this channel, honouring multi-prefix.
func (ch *Channel) NamesReply(multiPrefix bool) []string {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	names := make([]string, 0, len(ch.members))
	for _, m := range ch.members {
		prefix := m.Prefix
		if !multiPrefix && len(prefix) > 1 {
			prefix = string(prefix[0])
		}
		names = append(names, prefix+m.Session.nick)
	}
	return names
}

// ChannelRegistry maps channel names to Channel objects.
type ChannelRegistry struct {
	mu       sync.RWMutex
	channels map[string]*Channel
}

func newChannelRegistry() *ChannelRegistry {
	return &ChannelRegistry{channels: make(map[string]*Channel)}
}

func (cr *ChannelRegistry) GetOrCreate(name string) *Channel {
	key := strings.ToLower(name)
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if ch, ok := cr.channels[key]; ok {
		return ch
	}
	ch := newChannel(name)
	cr.channels[key] = ch
	return ch
}

func (cr *ChannelRegistry) Get(name string) (*Channel, bool) {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	ch, ok := cr.channels[strings.ToLower(name)]
	return ch, ok
}

func (cr *ChannelRegistry) Remove(name string) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	delete(cr.channels, strings.ToLower(name))
}

func (cr *ChannelRegistry) All() []*Channel {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	out := make([]*Channel, 0, len(cr.channels))
	for _, ch := range cr.channels {
		out = append(out, ch)
	}
	return out
}

func (cr *ChannelRegistry) Count() int {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	return len(cr.channels)
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Config holds the server configuration.
type Config struct {
	// Name is the server hostname used in messages.
	Name string
	// Network is the IRC network name reported in RPL_ISUPPORT.
	Network string
	// Listen is the address to listen on (e.g. ":6667").
	Listen string
	// TLSListen is an optional TLS listener address (e.g. ":6697").
	TLSListen string
	// TLSConfig is required if TLSListen is set.
	TLSConfig *tls.Config
	// MOTD is the message-of-the-day text (may contain \n lines).
	MOTD string
	// MaxClients is the maximum number of simultaneous connections (0 = unlimited).
	MaxClients int
	// Password is a global server password (optional).
	Password string
	// Opers maps oper name to bcrypt hashed password.
	Opers map[string]string
	// Caps is the list of extra caps to advertise.
	Caps []string
	// PingInterval is how often to ping idle clients.
	PingInterval time.Duration
	// PingTimeout is disconnect timeout after no PONG.
	PingTimeout time.Duration
}

func (c *Config) setDefaults() {
	if c.Name == "" {
		c.Name = "irc.local"
	}
	if c.Network == "" {
		c.Network = "LocalNet"
	}
	if c.Listen == "" {
		c.Listen = ":6667"
	}
	if c.PingInterval <= 0 {
		c.PingInterval = 90 * time.Second
	}
	if c.PingTimeout <= 0 {
		c.PingTimeout = 30 * time.Second
	}
}

// Server is the IRC server.
type Server struct {
	cfg      Config
	sessions *Registry
	channels *ChannelRegistry
	log      *slog.Logger

	// Stats
	clientsTotal   atomic.Int64
	clientsMax     atomic.Int64
	clientsCurrent atomic.Int64

	// Listeners (for graceful shutdown)
	listenerMu sync.Mutex
	listeners  []net.Listener

	// WhoWas ring buffer (simple slice for now)
	whowasMu sync.Mutex
	whowas   []whowasEntry
}

type whowasEntry struct {
	nick     string
	user     string
	host     string
	realname string
	quitAt   time.Time
}

// New creates a new Server with the given config.
func New(cfg Config) *Server {
	cfg.setDefaults()
	return &Server{
		cfg:      cfg,
		sessions: newRegistry(),
		channels: newChannelRegistry(),
		log:      slog.Default(),
		whowas:   make([]whowasEntry, 0, 64),
	}
}

// ListenAndServe starts the server listeners.
func (srv *Server) ListenAndServe() error {
	errCh := make(chan error, 2)

	ln, err := net.Listen("tcp", srv.cfg.Listen)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", srv.cfg.Listen, err)
	}
	srv.trackListener(ln)
	srv.log.Info("IRC server listening", "addr", srv.cfg.Listen)
	go func() { errCh <- srv.acceptLoop(ln, false) }()

	if srv.cfg.TLSListen != "" && srv.cfg.TLSConfig != nil {
		tlsLn, err := tls.Listen("tcp", srv.cfg.TLSListen, srv.cfg.TLSConfig)
		if err != nil {
			ln.Close()
			return fmt.Errorf("server: tls listen %s: %w", srv.cfg.TLSListen, err)
		}
		srv.trackListener(tlsLn)
		srv.log.Info("IRC/TLS server listening", "addr", srv.cfg.TLSListen)
		go func() { errCh <- srv.acceptLoop(tlsLn, true) }()
	}

	return <-errCh
}

// ListenRandom binds to a random port on the configured listen address
// (or 127.0.0.1:0 if Listen is empty) and returns the actual address.
// Call ServeListener to start accepting connections.
func (srv *Server) ListenRandom() (string, error) {
	listen := srv.cfg.Listen
	if listen == "" {
		listen = "127.0.0.1:0"
	}
	// Replace :0 or keep as-is if it already has a specific port
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return "", fmt.Errorf("server: listen: %w", err)
	}
	srv.trackListener(ln)
	srv.log.Info("IRC server listening", "addr", ln.Addr())
	return ln.Addr().String(), nil
}

// ServeListener starts accepting on all tracked listeners in the background.
func (srv *Server) ServeListener() {
	srv.listenerMu.Lock()
	lns := append([]net.Listener(nil), srv.listeners...)
	srv.listenerMu.Unlock()
	for _, ln := range lns {
		go srv.acceptLoop(ln, false) //nolint:errcheck
	}
}

// Close shuts down all listeners.
func (srv *Server) Close() {
	srv.listenerMu.Lock()
	defer srv.listenerMu.Unlock()
	for _, ln := range srv.listeners {
		ln.Close()
	}
}

func (srv *Server) trackListener(ln net.Listener) {
	srv.listenerMu.Lock()
	srv.listeners = append(srv.listeners, ln)
	srv.listenerMu.Unlock()
}

func (srv *Server) acceptLoop(ln net.Listener, tls bool) error {
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		if srv.cfg.MaxClients > 0 && int(srv.clientsCurrent.Load()) >= srv.cfg.MaxClients {
			conn.Write([]byte("ERROR :Server is full\r\n"))
			conn.Close()
			continue
		}
		s := newSession(conn, srv)
		if tls {
			s.modes.Apply([]irc.ModeChange{{Add: true, Mode: 'z'}}, mode.UserValidator{})
		}
		srv.clientsTotal.Add(1)
		srv.clientsCurrent.Add(1)
		go srv.handleSession(s)
	}
}

func (srv *Server) handleSession(s *Session) {
	defer func() {
		srv.clientsCurrent.Add(-1)
		srv.removeSession(s)
	}()

	scanner := bufio.NewScanner(s.conn)
	scanner.Buffer(make([]byte, 8192+4096), 8192+4096)

	for scanner.Scan() {
		line := scanner.Text()
		msg, err := irc.Parse(line)
		if err != nil {
			continue
		}
		s.idleAt = time.Now()
		srv.dispatch(s, msg)
	}
}

func (srv *Server) removeSession(s *Session) {
	if s.nick != "" {
		// Record in whowas
		srv.whowasMu.Lock()
		srv.whowas = append(srv.whowas, whowasEntry{
			nick:     s.nick,
			user:     s.user,
			host:     s.host,
			realname: s.realname,
			quitAt:   time.Now(),
		})
		if len(srv.whowas) > 256 {
			srv.whowas = srv.whowas[1:]
		}
		srv.whowasMu.Unlock()

		srv.sessions.Remove(s.nick)
		// Part all channels
		for _, ch := range srv.channels.All() {
			if !ch.HasMember(s.nick) {
				continue
			}
			ch.Broadcast(&irc.Message{
				Prefix:  s.Prefix(),
				Command: irc.QUIT,
				Params:  []string{"Connection closed"},
			}, s)
			empty := ch.RemoveMember(s.nick)
			if empty {
				srv.channels.Remove(ch.name)
			}
		}
	}
	s.conn.Close()
}

// Broadcast sends a message to all registered sessions.
func (srv *Server) Broadcast(msg *irc.Message) {
	for _, s := range srv.sessions.All() {
		s.Send(msg)
	}
}

// isupport returns the 005 ISUPPORT tokens.
func (srv *Server) isupport() []string {
	return []string{
		"NETWORK=" + srv.cfg.Network,
		"CASEMAPPING=ascii",
		"CHANMODES=beIq,k,l,imnpstrcCzGSNufjQKLPOARTF",
		"CHANTYPES=#&",
		"PREFIX=(qaohv)~&@%+",
		"STATUSMSG=@+",
		"MODES=4",
		"MAXCHANNELS=25",
		"MAXNICKLEN=30",
		"MAXBANS=100",
		"TOPICLEN=390",
		"KICKLEN=255",
		"AWAYLEN=307",
		"CHANNELLEN=64",
		"EXTBAN=$,ajoqrz",
		"WHOX",
		"WALLCHOPS",
		"ELIST=MNUCT",
		"INVEX",
		"EXCEPTS",
		"CALLERID",
	}
}

// supportedCaps returns the set of caps this server supports.
func (srv *Server) supportedCaps() map[string]string {
	caps := map[string]string{
		irc.CapSASL:            "PLAIN,EXTERNAL",
		irc.CapServerTime:      "",
		irc.CapMessageTags:     "",
		irc.CapBatch:           "",
		irc.CapLabeledResponse: "",
		irc.CapEchoMessage:     "",
		irc.CapMultiPrefix:     "",
		irc.CapAwayNotify:      "",
		irc.CapExtendedJoin:    "",
		irc.CapChghost:         "",
		irc.CapSetname:         "",
		irc.CapAccountTag:      "",
		irc.CapCapNotify:       "",
		irc.CapUserHostInNames: "",
		irc.CapInviteNotify:    "",
		irc.CapAccountNotify:   "",
	}
	for _, c := range srv.cfg.Caps {
		k, v, _ := strings.Cut(c, "=")
		caps[k] = v
	}
	return caps
}

// serverTime returns the current time in IRCv3 server-time format.
func serverTime() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

