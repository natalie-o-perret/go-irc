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
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strconv"
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

const chatHistoryLimit = 50

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
	Queries  map[string]bool
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
		state:   &NetworkState{Channels: make(map[string]*ChannelState), Queries: make(map[string]bool)},
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

	// Store channel and private-message history.
	if msg.Command == irc.PRIVMSG || msg.Command == irc.NOTICE {
		if len(msg.Params) >= 2 {
			target := strings.ToLower(msg.Params[0])
			store := strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&")
			if !store && msg.Prefix != nil {
				n.mu.Lock()
				if strings.EqualFold(target, n.state.Nick) {
					target = strings.ToLower(msg.Prefix.Nick)
				} else if strings.EqualFold(msg.Prefix.Nick, n.state.Nick) {
					store = true
				}
				if target != "" {
					n.state.Queries[target] = true
					store = true
				}
				n.mu.Unlock()
			}
			if store {
				t := time.Now()
				if ts, ok := msg.Tags.Get("time"); ok {
					if pt, err := time.Parse(time.RFC3339, ts); err == nil {
						t = pt
					}
				}
				n.history.Append(n.cfg.Name, target, t, msg) //nolint:errcheck
			}
		}
	}

	// Fan out to all downstreams -- add server-time if not present
	out := msg.Clone()
	if out.Tags == nil {
		out.Tags = make(irc.Tags)
	}
	if _, ok := out.Tags.Get("time"); !ok {
		out.Tags["time"] = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	n.fanOut(out)
}

// ---------------------------------------------------------------------------
// DownstreamSession
// ---------------------------------------------------------------------------

// DownstreamSession is a client connected to the bouncer.
type DownstreamSession struct {
	conn       net.Conn
	writer     *bufio.Writer
	wmu        sync.Mutex
	bouncer    *Bouncer
	network    *Network
	nick       string
	user       string
	caps       map[string]bool
	capVersion int
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
	if msg.Command == irc.TAGMSG && !ds.caps[irc.CapMessageTags] {
		return
	}
	ds.wmu.Lock()
	defer ds.wmu.Unlock()
	out := msg
	if len(msg.Tags) > 0 {
		out = msg.Clone()
		for tag := range out.Tags {
			cap := irc.CapMessageTags
			switch tag {
			case "time":
				cap = irc.CapServerTime
			case "batch":
				cap = irc.CapBatch
			case "draft/chathistory-end":
				cap = irc.CapChatHistory
			}
			if !ds.caps[cap] {
				delete(out.Tags, tag)
			}
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
	capDone := true

	for scanner.Scan() {
		line := scanner.Text()
		if irc.InputTooLong(line) {
			ds.sendNumeric(irc.ERR_INPUTTOOLONG, "Input line was too long")
			continue
		}
		msg, err := irc.Parse(line)
		if err != nil {
			continue
		}

		switch msg.Command {
		case irc.CAP:
			ds.handleCAP(msg, caps, &capDone)
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

		if !registered && nick != "" && user != "" && capDone {
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
			ds.sendNumeric(irc.RPL_ISUPPORT, "CHATHISTORY=50", "MSGREFTYPES=timestamp,msgid", "are supported by this server")
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
		if irc.InputTooLong(line) {
			ds.sendNumeric(irc.ERR_INPUTTOOLONG, "Input line was too long")
			continue
		}
		msg, err := irc.Parse(line)
		if err != nil {
			continue
		}
		if msg.Command == irc.CAP {
			done := true
			ds.handleCAP(msg, ds.caps, &done)
			continue
		}
		if msg.Command == irc.CHATHISTORY {
			ds.handleChatHistory(msg)
			continue
		}
		if ds.network != nil && ds.network.client != nil {
			upstream := msg.Clone()
			upstream.Tags = make(irc.Tags)
			if ds.caps[irc.CapMessageTags] {
				for tag, value := range msg.Tags {
					if strings.HasPrefix(tag, "+") {
						upstream.Tags[tag] = value
					}
				}
			}
			_ = ds.network.client.Send(upstream)
		}
	}
}

func (ds *DownstreamSession) handleCAP(msg *irc.Message, caps map[string]bool, done *bool) {
	if len(msg.Params) < 1 {
		ds.sendNumeric(irc.ERR_INVALIDCAPCMD, "*", "Invalid CAP command")
		return
	}
	subCmd := strings.ToUpper(msg.Params[0])
	arg := ""
	if len(msg.Params) >= 2 {
		arg = msg.Params[1]
	}

	supportedCaps := []string{
		irc.CapServerTime, irc.CapMessageTags, irc.CapBatch, irc.CapChatHistory,
		irc.CapMultiPrefix, irc.CapAwayNotify, irc.CapExtendedJoin,
		irc.CapSetname, irc.CapCapNotify,
	}

	nick := ds.nick
	if nick == "" {
		nick = "*"
	}

	switch subCmd {
	case irc.CapLS:
		*done = false
		if version, err := strconv.Atoi(arg); err == nil && version >= 302 {
			ds.capVersion = version
			caps[irc.CapCapNotify] = true
		}
		ds.send(&irc.Message{
			Prefix:  &irc.Prefix{Nick: "bouncer"},
			Command: irc.CAP,
			Params:  []string{nick, irc.CapLS, strings.Join(supportedCaps, " ")},
		})

	case irc.CapREQ:
		*done = false
		arg = strings.TrimPrefix(arg, ":")
		for _, requested := range strings.Fields(arg) {
			name := strings.TrimPrefix(requested, "-")
			if !slices.Contains(supportedCaps, name) {
				ds.send(&irc.Message{
					Prefix:  &irc.Prefix{Nick: "bouncer"},
					Command: irc.CAP,
					Params:  []string{nick, irc.CapNAK, arg},
				})
				return
			}
		}
		for _, requested := range strings.Fields(arg) {
			name := strings.TrimPrefix(requested, "-")
			if strings.HasPrefix(requested, "-") && (name != irc.CapCapNotify || ds.capVersion < 302) {
				delete(caps, name)
			} else if !strings.HasPrefix(requested, "-") {
				caps[name] = true
			}
		}
		ds.send(&irc.Message{
			Prefix:  &irc.Prefix{Nick: "bouncer"},
			Command: irc.CAP,
			Params:  []string{nick, irc.CapACK, arg},
		})

	case irc.CapLIST:
		var enabled []string
		for name := range caps {
			if caps[name] {
				enabled = append(enabled, name)
			}
		}
		slices.Sort(enabled)
		ds.send(&irc.Message{
			Prefix:  &irc.Prefix{Nick: "bouncer"},
			Command: irc.CAP,
			Params:  []string{nick, irc.CapLIST, strings.Join(enabled, " ")},
		})

	case irc.CapEND:
		*done = true

	default:
		ds.sendNumeric(irc.ERR_INVALIDCAPCMD, subCmd, "Invalid CAP command")
	}
}

func (ds *DownstreamSession) handleChatHistory(msg *irc.Message) {
	if !ds.caps[irc.CapChatHistory] {
		ds.sendNumeric(irc.ERR_UNKNOWNCOMMAND, irc.CHATHISTORY, "Unknown command")
		return
	}
	subcommand := strings.ToUpper(msg.Param(0))
	if subcommand == "TARGETS" {
		ds.handleChatHistoryTargets(msg)
		return
	}
	expected := 4
	if subcommand == "BETWEEN" {
		expected = 5
	}
	if len(msg.Params) != expected {
		ds.sendChatHistoryFail("INVALID_PARAMS", msg.Param(0), "Invalid parameters")
		return
	}

	target := strings.ToLower(msg.Params[1])
	limit, err := strconv.Atoi(msg.Params[len(msg.Params)-1])
	if err != nil || limit <= 0 {
		ds.sendChatHistoryFail("INVALID_PARAMS", msg.Params[0], "Invalid limit")
		return
	}
	if limit > chatHistoryLimit {
		limit = chatHistoryLimit
	}
	if ds.network == nil {
		ds.sendChatHistoryFail("INVALID_TARGET", msg.Params[0], "Messages could not be retrieved")
		return
	}
	ds.network.mu.RLock()
	_, channelOK := ds.network.state.Channels[target]
	queryOK := ds.network.state.Queries[target]
	ds.network.mu.RUnlock()
	if !channelOK && !queryOK {
		ds.sendChatHistoryFail("INVALID_TARGET", msg.Params[0], "Messages could not be retrieved")
		return
	}

	stored, err := ds.network.history.Query(ds.network.cfg.Name, target, history.Query{Limit: int(^uint(0) >> 1)})
	if err != nil {
		ds.sendChatHistoryFail("MESSAGE_ERROR", msg.Params[0], "Messages could not be retrieved")
		return
	}
	entries := make([]irc.ChatHistoryEntry, len(stored))
	for i, entry := range stored {
		entries[i] = irc.ChatHistoryEntry{Time: entry.Time, Msg: entry.Msg}
	}
	entries, complete, err := irc.SelectChatHistory(entries, subcommand, msg.Params[2:len(msg.Params)-1], limit)
	if err != nil {
		code := "INVALID_PARAMS"
		if strings.Contains(err.Error(), "unsupported") {
			code = "INVALID_MSGREFTYPE"
		}
		ds.sendChatHistoryFail(code, msg.Params[0], err.Error())
		return
	}
	ds.sendHistory(target, entries, complete)
}

func (ds *DownstreamSession) handleChatHistoryTargets(msg *irc.Message) {
	if len(msg.Params) != 4 || ds.network == nil {
		ds.sendChatHistoryFail("INVALID_PARAMS", msg.Param(0), "Invalid parameters")
		return
	}
	first, err := time.Parse(irc.ServerTimeLayout, strings.TrimPrefix(msg.Params[1], "timestamp="))
	if err != nil || !strings.HasPrefix(msg.Params[1], "timestamp=") {
		ds.sendChatHistoryFail("INVALID_PARAMS", msg.Params[0], "Invalid timestamp")
		return
	}
	second, err := time.Parse(irc.ServerTimeLayout, strings.TrimPrefix(msg.Params[2], "timestamp="))
	if err != nil || !strings.HasPrefix(msg.Params[2], "timestamp=") {
		ds.sendChatHistoryFail("INVALID_PARAMS", msg.Params[0], "Invalid timestamp")
		return
	}
	limit, err := strconv.Atoi(msg.Params[3])
	if err != nil || limit <= 0 {
		ds.sendChatHistoryFail("INVALID_PARAMS", msg.Params[0], "Invalid limit")
		return
	}
	if first.After(second) {
		first, second = second, first
	}
	ds.network.mu.RLock()
	var names []string
	for name := range ds.network.state.Channels {
		names = append(names, name)
	}
	for name := range ds.network.state.Queries {
		names = append(names, name)
	}
	ds.network.mu.RUnlock()
	var targets []irc.ChatHistoryEntry
	for _, name := range names {
		entries, err := ds.network.history.Query(ds.network.cfg.Name, name, history.Query{Limit: 1})
		if err != nil || len(entries) == 0 {
			continue
		}
		latest := entries[0]
		if latest.Time.After(first) && latest.Time.Before(second) {
			targets = append(targets, irc.ChatHistoryEntry{
				Time: latest.Time,
				Msg:  &irc.Message{Command: irc.CHATHISTORY, Params: []string{"TARGETS", name, latest.Time.UTC().Format(irc.ServerTimeLayout)}},
			})
		}
	}
	slices.SortFunc(targets, func(a, b irc.ChatHistoryEntry) int { return b.Time.Compare(a.Time) })
	complete := len(targets) <= limit
	if len(targets) > limit {
		targets = targets[:limit]
	}
	ds.sendHistoryBatch("draft/chathistory-targets", "", targets, complete)
}

func (ds *DownstreamSession) sendChatHistoryFail(code, context, text string) {
	ds.send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: "bouncer"},
		Command: irc.FAIL,
		Params:  []string{irc.CHATHISTORY, code, context, text},
	})
}

func (ds *DownstreamSession) sendHistory(target string, entries []irc.ChatHistoryEntry, complete bool) {
	ds.sendHistoryBatch("chathistory", target, entries, complete)
}

func (ds *DownstreamSession) sendHistoryBatch(batchType, target string, entries []irc.ChatHistoryEntry, complete bool) {
	batchID := ""
	if ds.caps[irc.CapBatch] {
		batchID = rand.Text()
		tags := irc.Tags{}
		if complete {
			tags["draft/chathistory-end"] = ""
		}
		params := []string{"+" + batchID, batchType}
		if target != "" {
			params = append(params, target)
		}
		ds.send(&irc.Message{
			Tags:    tags,
			Prefix:  &irc.Prefix{Nick: "bouncer"},
			Command: irc.BATCH,
			Params:  params,
		})
	}
	for _, entry := range entries {
		msg := entry.Msg.Clone()
		if msg.Tags == nil {
			msg.Tags = make(irc.Tags)
		}
		msg.Tags["time"] = entry.Time.UTC().Format(irc.ServerTimeLayout)
		if batchID != "" {
			msg.Tags["batch"] = batchID
		}
		ds.send(msg)
	}
	if batchID != "" {
		ds.send(&irc.Message{
			Prefix:  &irc.Prefix{Nick: "bouncer"},
			Command: irc.BATCH,
			Params:  []string{"-" + batchID},
		})
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

		if ds.caps[irc.CapChatHistory] {
			continue
		}
		// History replay for clients without CHATHISTORY support.
		stored, _ := n.history.Query(n.cfg.Name, chanName, history.Query{Limit: chatHistoryLimit})
		entries := make([]irc.ChatHistoryEntry, len(stored))
		for i, entry := range stored {
			entries[i] = irc.ChatHistoryEntry{Time: entry.Time, Msg: entry.Msg}
		}
		ds.sendHistory(chanName, entries, true)
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
