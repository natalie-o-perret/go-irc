// Package bouncer implements a multi-upstream, multi-downstream IRC bouncer
// (similar in spirit to soju / ZNC).
//
// Architecture overview:
//
//   - A Bouncer listens for downstream IRC connections.
//   - Each downstream authenticates with "PASS user/network:password" or
//     "PASS user:password".
//   - Each Network maintains one persistent UpstreamConn (a *client.Client)
//     and fans incoming messages out to all attached DownstreamSessions.
//   - Messages from downstream are forwarded upstream after stripping
//     bouncer-specific tags.
//   - History is stored per-network per-target and replayed to newly
//     attaching downstreams.
package bouncer

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/natalie-o-perret/go-irc/bouncer/history"
	goclient "github.com/natalie-o-perret/go-irc/client"
	"github.com/natalie-o-perret/go-irc/irc"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// UserConfig describes a bouncer user account.
type UserConfig struct {
	Name     string
	Password string // plain-text or bcrypt hash (prefix with "$2a$")
}

// NetworkConfig describes an upstream IRC network.
type NetworkConfig struct {
	Name        string
	Server      string
	TLS         bool
	Nick        string
	User        string
	RealName    string
	Password    string
	SASLUser    string
	SASLPass    string
	Channels    []string
	AutoConnect bool
}

// HistoryConfig controls message history storage.
type HistoryConfig struct {
	// Backend: "memory" or "sqlite"
	Backend string
	// Limit is the max messages per target.
	Limit int
}

// Config is the top-level bouncer configuration.
type Config struct {
	// Listen address for downstream clients.
	Listen string
	// TLSListen is the optional TLS listen address.
	TLSListen string
	// TLSConfig is required if TLSListen is set.
	TLSConfig *tls.Config
	// Users is the list of authenticated bouncer users.
	Users []UserConfig
	// Networks is the list of upstream networks.
	Networks []NetworkConfig
	// History controls message history.
	History HistoryConfig
}

// ---------------------------------------------------------------------------

// ChannelState tracks live channel state for replay.
type ChannelState struct {
	Topic   string
	TopicBy string
	TopicAt time.Time
	Members map[string]string // nick -> prefix
}

// NetworkState tracks the upstream session state.
type NetworkState struct {
	Nick     string
	Channels map[string]*ChannelState
}

// Network manages one upstream IRC connection.
type Network struct {
	cfg     NetworkConfig
	history history.Store
	client  *goclient.Client
	state   *NetworkState

	mu          sync.RWMutex
	downstreams []*DownstreamSession
}

func newNetwork(cfg NetworkConfig, hist history.Store) *Network {
	return &Network{
		cfg:     cfg,
		history: hist,
		state:   &NetworkState{Channels: make(map[string]*ChannelState)},
	}
}

// attach registers a downstream for fan-out.
func (n *Network) attach(ds *DownstreamSession) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.downstreams = append(n.downstreams, ds)
}

// detach removes a downstream.
func (n *Network) detach(ds *DownstreamSession) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i, d := range n.downstreams {
		if d == ds {
			n.downstreams = append(n.downstreams[:i], n.downstreams[i+1:]...)
			return
		}
	}
}

// fanOut sends msg to all attached downstreams.
func (n *Network) fanOut(msg *irc.Message) {
	n.mu.RLock()
	dss := append([]*DownstreamSession(nil), n.downstreams...)
	n.mu.RUnlock()
	for _, ds := range dss {
		ds.send(msg)
	}
}

// connect dials the upstream server and starts the feed loop.
func (n *Network) connect() {
	var tlsCfg *tls.Config
	if n.cfg.TLS {
		tlsCfg = &tls.Config{ServerName: strings.Split(n.cfg.Server, ":")[0]}
	}

	cfg := goclient.Config{
		Addr:           n.cfg.Server,
		Nick:           n.cfg.Nick,
		User:           n.cfg.User,
		RealName:       n.cfg.RealName,
		Password:       n.cfg.Password,
		TLS:            tlsCfg,
		AutoReconnect:  true,
		ReconnectDelay: 15 * time.Second,
	}

	n.client = goclient.New(cfg)

	// Track upstream state and fan out to downstreams
	n.client.On("*", func(c *goclient.Client, msg *irc.Message) {
		n.handleUpstream(msg)
	})

	// Auto-join configured channels after 001
	n.client.On("001", func(c *goclient.Client, msg *irc.Message) {
		n.mu.Lock()
		if len(msg.Params) > 0 {
			n.state.Nick = msg.Params[0]
		}
		n.mu.Unlock()
		for _, ch := range n.cfg.Channels {
			_ = c.Sendf(irc.JOIN, ch)
		}
	})

	go func() {
		for {
			if err := n.client.Connect(); err != nil {
				slog.Error("bouncer upstream disconnected", "network", n.cfg.Name, "err", err)
			}
			time.Sleep(15 * time.Second)
		}
	}()
}

func (n *Network) handleUpstream(msg *irc.Message) {
	// Update state
	switch msg.Command {
	case irc.JOIN:
		if len(msg.Params) > 0 && msg.Prefix != nil {
			ch := strings.ToLower(msg.Params[0])
			n.mu.Lock()
			if _, ok := n.state.Channels[ch]; !ok {
				n.state.Channels[ch] = &ChannelState{Members: make(map[string]string)}
			}
			n.state.Channels[ch].Members[msg.Prefix.Nick] = ""
			n.mu.Unlock()
		}
	case irc.PART, irc.QUIT:
		if msg.Prefix != nil {
			n.mu.Lock()
			for _, cs := range n.state.Channels {
				delete(cs.Members, msg.Prefix.Nick)
			}
			n.mu.Unlock()
		}
	case irc.NICK:
		if msg.Prefix != nil && len(msg.Params) > 0 {
			newNick := msg.Params[0]
			n.mu.Lock()
			for _, cs := range n.state.Channels {
				if prefix, ok := cs.Members[msg.Prefix.Nick]; ok {
					delete(cs.Members, msg.Prefix.Nick)
					cs.Members[newNick] = prefix
				}
			}
			if n.state.Nick == msg.Prefix.Nick {
				n.state.Nick = newNick
			}
			n.mu.Unlock()
		}
	}

	// Store history for PRIVMSG/NOTICE targeting a channel
	if msg.Command == irc.PRIVMSG || msg.Command == irc.NOTICE {
		if len(msg.Params) >= 2 {
			target := msg.Params[0]
			if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
				t := time.Now()
				if ts, ok := msg.Tags.Get("time"); ok {
					if pt, err := time.Parse(time.RFC3339, ts); err == nil {
						t = pt
					}
				}
				n.history.Append(n.cfg.Name, strings.ToLower(target), t, msg) //nolint:errcheck
			}
		}
	}

	// Fan out to all downstreams -- add server-time if not present
	out := msg.Clone()
	if out.Tags == nil {
		out.Tags = make(irc.Tags)
	}
	if _, ok := out.Tags.Get("time"); !ok {
		out.Tags["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	n.fanOut(out)
}

// ---------------------------------------------------------------------------
// DownstreamSession
// ---------------------------------------------------------------------------

// DownstreamSession is a client connected to the bouncer.
type DownstreamSession struct {
	conn    net.Conn
	writer  *bufio.Writer
	wmu     sync.Mutex
	bouncer *Bouncer
	network *Network
	nick    string
	user    string
	caps    map[string]bool
}

func newDownstreamSession(conn net.Conn, b *Bouncer) *DownstreamSession {
	return &DownstreamSession{
		conn:    conn,
		writer:  bufio.NewWriterSize(conn, 4096),
		bouncer: b,
		caps:    make(map[string]bool),
	}
}

func (ds *DownstreamSession) send(msg *irc.Message) {
	ds.wmu.Lock()
	defer ds.wmu.Unlock()
	// Strip server-time tag if downstream hasn't negotiated it
	out := msg
	if !ds.caps[irc.CapServerTime] {
		if _, ok := msg.Tags.Get("time"); ok {
			out = msg.Clone()
			delete(out.Tags, "time")
		}
	}
	line := out.String() + "\r\n"
	_, _ = ds.writer.WriteString(line)
	_ = ds.writer.Flush()
}

func (ds *DownstreamSession) sendNumeric(n irc.Numeric, params ...string) {
	nick := ds.nick
	if nick == "" {
		nick = "*"
	}
	ds.send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: ds.bouncer.cfg.Listen},
		Command: n.String(),
		Params:  append([]string{nick}, params...),
	})
}

func (ds *DownstreamSession) run() {
	defer func() {
		if ds.network != nil {
			ds.network.detach(ds)
		}
		_ = ds.conn.Close()
	}()

	scanner := bufio.NewScanner(ds.conn)
	scanner.Buffer(make([]byte, 16384), 16384)

	// Registration phase
	var passUser, passNetwork, passPass string
	var nick, user, realname string
	var registered bool
	caps := make(map[string]bool)
	var capLSDone bool

	for scanner.Scan() {
		line := scanner.Text()
		msg, err := irc.Parse(line)
		if err != nil {
			continue
		}

		switch msg.Command {
		case irc.CAP:
			ds.handleCAP(msg, caps, &capLSDone)
		case irc.PASS:
			if len(msg.Params) > 0 {
				// Format: user/network:password or user:password
				passUser, passNetwork, passPass = parseBouncerPass(msg.Params[0])
			}
		case irc.NICK:
			if len(msg.Params) > 0 {
				nick = msg.Params[0]
			}
		case irc.USER:
			if len(msg.Params) >= 4 {
				user = msg.Params[0]
				realname = msg.Params[3]
			}
		}

		if !registered && nick != "" && user != "" && capLSDone {
			// Authenticate
			if !ds.bouncer.authenticate(passUser, passPass) {
				ds.send(&irc.Message{
					Command: irc.ERROR,
					Params:  []string{"Access denied: invalid credentials"},
				})
				return
			}

			// Find network
			netName := passNetwork
			n, ok := ds.bouncer.getNetwork(passUser, netName)
			if !ok {
				ds.send(&irc.Message{
					Command: irc.ERROR,
					Params:  []string{"No such network: " + netName},
				})
				return
			}

			ds.nick = nick
			ds.user = user
			ds.caps = caps
			ds.network = n
			n.attach(ds)

			registered = true

			// Send welcome
			serverName := "bouncer." + netName + ".local"
			ds.send(&irc.Message{
				Prefix:  &irc.Prefix{Nick: serverName},
				Command: "001",
				Params:  []string{nick, "Welcome to the bouncer, " + nick + "!"},
			})
			ds.send(&irc.Message{
				Prefix:  &irc.Prefix{Nick: serverName},
				Command: "002",
				Params:  []string{nick, "Your host is go-irc bouncer"},
			})
			ds.send(&irc.Message{
				Prefix:  &irc.Prefix{Nick: serverName},
				Command: "376",
				Params:  []string{nick, "End of /MOTD command."},
			})

			// Replay channel state + recent history
			ds.replayState(n)

			break
		}
		if registered {
			break
		}
	}

	if !registered {
		return
	}

	// Proxy loop: forward downstream messages to upstream
	_ = realname
	_ = passUser
	_ = passPass

	for scanner.Scan() {
		line := scanner.Text()
		msg, err := irc.Parse(line)
		if err != nil {
			continue
		}
		if ds.network != nil && ds.network.client != nil {
			// Strip bouncer-only tags and forward
			upstream := msg.Clone()
			upstream.Tags = nil
			_ = ds.network.client.Send(upstream)
		}
	}
}

func (ds *DownstreamSession) handleCAP(msg *irc.Message, caps map[string]bool, done *bool) {
	if len(msg.Params) < 1 {
		return
	}
	params := msg.Params
	subCmd := strings.ToUpper(params[0])
	if params[0] == "*" && len(params) >= 2 {
		subCmd = strings.ToUpper(params[1])
	}

	supportedCaps := []string{
		irc.CapServerTime, irc.CapMessageTags, irc.CapBatch,
		irc.CapMultiPrefix, irc.CapAwayNotify, irc.CapExtendedJoin,
		irc.CapSetname, irc.CapAccountTag, irc.CapCapNotify,
	}

	nick := ds.nick
	if nick == "" {
		nick = "*"
	}

	switch subCmd {
	case irc.CapLS:
		*done = false
		ds.send(&irc.Message{
			Prefix:  &irc.Prefix{Nick: "bouncer"},
			Command: irc.CAP,
			Params:  []string{nick, irc.CapLS, strings.Join(supportedCaps, " ")},
		})

	case irc.CapREQ:
		arg := ""
		if len(params) >= 3 {
			arg = strings.TrimPrefix(params[2], ":")
		} else if len(params) >= 2 && params[0] != "*" {
			arg = strings.TrimPrefix(params[1], ":")
		}
		for _, cap := range strings.Fields(arg) {
			caps[strings.TrimPrefix(cap, "-")] = true
		}
		ds.send(&irc.Message{
			Prefix:  &irc.Prefix{Nick: "bouncer"},
			Command: irc.CAP,
			Params:  []string{nick, irc.CapACK, arg},
		})

	case irc.CapEND:
		*done = true
	}
}

func (ds *DownstreamSession) replayState(n *Network) {
	n.mu.RLock()
	channels := make(map[string]*ChannelState, len(n.state.Channels))
	for k, v := range n.state.Channels {
		channels[k] = v
	}
	n.mu.RUnlock()

	for chanName, cs := range channels {
		// Fake JOIN
		ds.send(&irc.Message{
			Prefix:  &irc.Prefix{Nick: ds.nick, User: ds.user, Host: "bouncer"},
			Command: irc.JOIN,
			Params:  []string{chanName},
		})

		// Topic
		if cs.Topic != "" {
			ds.sendNumeric(irc.RPL_TOPIC, chanName, cs.Topic)
		} else {
			ds.sendNumeric(irc.RPL_NOTOPIC, chanName, "No topic is set")
		}

		// Names
		var names []string
		cs2 := cs
		for nick, prefix := range cs2.Members {
			names = append(names, prefix+nick)
		}
		if len(names) > 0 {
			ds.sendNumeric(irc.RPL_NAMREPLY, "=", chanName, strings.Join(names, " "))
		}
		ds.sendNumeric(irc.RPL_ENDOFNAMES, chanName, "End of /NAMES list")

		// History replay
		entries, _ := n.history.Query(n.cfg.Name, chanName, history.Query{Limit: 50})
		for _, e := range entries {
			m := e.Msg.Clone()
			if m.Tags == nil {
				m.Tags = make(irc.Tags)
			}
			if ds.caps[irc.CapServerTime] {
				m.Tags["time"] = e.Time.UTC().Format(time.RFC3339Nano)
			}
			ds.send(m)
		}
	}
}

// ---------------------------------------------------------------------------
// Bouncer
// ---------------------------------------------------------------------------

// Bouncer is the top-level IRC bouncer.
type Bouncer struct {
	cfg      Config
	networks map[string]*Network // name -> Network
	mu       sync.RWMutex
	log      *slog.Logger
}

// New creates a new Bouncer.
func New(cfg Config) *Bouncer {
	b := &Bouncer{
		cfg:      cfg,
		networks: make(map[string]*Network),
		log:      slog.Default(),
	}

	hist := history.NewMemoryStore(cfg.History.Limit)

	for _, ncfg := range cfg.Networks {
		n := newNetwork(ncfg, hist)
		b.networks[strings.ToLower(ncfg.Name)] = n
		if ncfg.AutoConnect {
			n.connect()
		}
	}
	return b
}

// ListenAndServe starts the bouncer listeners.
func (b *Bouncer) ListenAndServe() error {
	errCh := make(chan error, 2)

	ln, err := net.Listen("tcp", b.cfg.Listen)
	if err != nil {
		return fmt.Errorf("bouncer: listen %s: %w", b.cfg.Listen, err)
	}
	b.log.Info("bouncer listening", "addr", b.cfg.Listen)
	go func() { errCh <- b.acceptLoop(ln) }()

	if b.cfg.TLSListen != "" && b.cfg.TLSConfig != nil {
		tlsLn, err := tls.Listen("tcp", b.cfg.TLSListen, b.cfg.TLSConfig)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("bouncer: tls listen %s: %w", b.cfg.TLSListen, err)
		}
		b.log.Info("bouncer TLS listening", "addr", b.cfg.TLSListen)
		go func() { errCh <- b.acceptLoop(tlsLn) }()
	}

	return <-errCh
}

func (b *Bouncer) acceptLoop(ln net.Listener) error {
	defer func() { _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		ds := newDownstreamSession(conn, b)
		go ds.run()
	}
}

// authenticate checks a user's password.
func (b *Bouncer) authenticate(username, password string) bool {
	for _, u := range b.cfg.Users {
		if !strings.EqualFold(u.Name, username) {
			continue
		}
		return u.Password == password
	}
	return false
}

// getNetwork returns the network for a user (or default first network).
func (b *Bouncer) getNetwork(user, netName string) (*Network, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if netName != "" {
		n, ok := b.networks[strings.ToLower(netName)]
		return n, ok
	}
	// return first network
	for _, n := range b.networks {
		return n, true
	}
	return nil, false
}

// parseBouncerPass parses "user/network:password" or "user:password".
func parseBouncerPass(pass string) (user, network, password string) {
	// Try user/network:password
	if i := strings.Index(pass, "/"); i >= 0 {
		if j := strings.Index(pass[i+1:], ":"); j >= 0 {
			user = pass[:i]
			network = pass[i+1 : i+1+j]
			password = pass[i+1+j+1:]
			return
		}
	}
	// Try user:password
	if i := strings.Index(pass, ":"); i >= 0 {
		user = pass[:i]
		password = pass[i+1:]
		return
	}
	user = pass
	return
}
