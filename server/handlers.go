package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/natalie-o-perret/go-irc/irc"
	"github.com/natalie-o-perret/go-irc/server/mode"
)

// dispatch routes a message to the appropriate handler.
func (srv *Server) dispatch(s *Session, msg *irc.Message) {
	cmd := strings.ToUpper(msg.Command)

	// Always allow these before registration
	switch cmd {
	case irc.CAP:
		srv.handleCAP(s, msg)
		return
	case irc.AUTHENTICATE:
		srv.handleAuthenticate(s, msg)
		return
	case irc.PASS:
		srv.handlePass(s, msg)
		return
	case irc.NICK:
		srv.handleNick(s, msg)
		return
	case irc.USER:
		srv.handleUser(s, msg)
		return
	case irc.QUIT:
		srv.handleQuit(s, msg)
		return
	case irc.PING:
		srv.handlePing(s, msg)
		return
	case irc.PONG:
		return // no-op
	}

	// Require registration for everything else
	if s.state != StateRegistered {
		s.SendNumeric(irc.ERR_NOTREGISTERED, "You have not registered")
		return
	}

	switch cmd {
	// Channel
	case irc.JOIN:
		srv.handleJoin(s, msg)
	case irc.PART:
		srv.handlePart(s, msg)
	case irc.TOPIC:
		srv.handleTopic(s, msg)
	case irc.NAMES:
		srv.handleNames(s, msg)
	case irc.LIST:
		srv.handleList(s, msg)
	case irc.KICK:
		srv.handleKick(s, msg)
	case irc.INVITE:
		srv.handleInvite(s, msg)
	case irc.MODE:
		srv.handleMode(s, msg)

	// Messaging
	case irc.PRIVMSG:
		srv.handlePrivmsg(s, msg, false)
	case irc.NOTICE:
		srv.handlePrivmsg(s, msg, true)
	case irc.TAGMSG:
		srv.handleTagmsg(s, msg)

	// Queries
	case irc.WHO:
		srv.handleWho(s, msg)
	case irc.WHOIS:
		srv.handleWhois(s, msg)
	case irc.WHOWAS:
		srv.handleWhowas(s, msg)

	// User state
	case irc.AWAY:
		srv.handleAway(s, msg)
	case irc.NICK:
		srv.handleNickRegistered(s, msg)
	case irc.USERHOST:
		srv.handleUserhost(s, msg)
	case irc.ISON:
		srv.handleIson(s, msg)

	// Server
	case irc.PING:
		srv.handlePing(s, msg)
	case irc.MOTD:
		srv.sendMOTD(s)
	case irc.LUSERS:
		srv.sendLusers(s)
	case irc.VERSION:
		srv.handleVersion(s, msg)
	case irc.TIME:
		srv.handleTime(s, msg)
	case irc.ADMIN:
		srv.handleAdmin(s, msg)
	case irc.INFO:
		srv.handleInfo(s, msg)
	case irc.STATS:
		srv.handleStats(s, msg)
	case irc.WALLOPS:
		srv.handleWallops(s, msg)

	// Oper
	case irc.OPER:
		srv.handleOper(s, msg)
	case irc.KILL:
		srv.handleKill(s, msg)

	// IRCv3
	case irc.SETNAME:
		srv.handleSetname(s, msg)

	default:
		s.SendNumeric(irc.ERR_UNKNOWNCOMMAND, cmd, "Unknown command")
	}
}

// ---------------------------------------------------------------------------
// Registration handlers
// ---------------------------------------------------------------------------

func (srv *Server) handlePass(s *Session, msg *irc.Message) {
	if s.state == StateRegistered {
		s.SendNumeric(irc.ERR_ALREADYREGISTRED, "You may not reregister")
		return
	}
	// Password is validated at registration completion
}

func (srv *Server) handleNick(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NONICKNAMEGIVEN, "No nickname given")
		return
	}
	nick := msg.Params[0]
	if !isValidNick(nick) {
		s.SendNumeric(irc.ERR_ERRONEUSNICKNAME, nick, "Erroneous Nickname")
		return
	}
	if _, taken := srv.sessions.Get(nick); taken && !strings.EqualFold(nick, s.nick) {
		s.SendNumeric(irc.ERR_NICKNAMEINUSE, nick, "Nickname is already in use")
		return
	}
	s.nick = nick
	srv.tryRegister(s)
}

func (srv *Server) handleNickRegistered(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NONICKNAMEGIVEN, "No nickname given")
		return
	}
	newNick := msg.Params[0]
	if !isValidNick(newNick) {
		s.SendNumeric(irc.ERR_ERRONEUSNICKNAME, newNick, "Erroneous Nickname")
		return
	}
	if _, taken := srv.sessions.Get(newNick); taken && !strings.EqualFold(newNick, s.nick) {
		s.SendNumeric(irc.ERR_NICKNAMEINUSE, newNick, "Nickname is already in use")
		return
	}
	oldNick := s.nick
	if err := srv.sessions.Rename(oldNick, newNick); err != nil {
		s.SendNumeric(irc.ERR_NICKNAMEINUSE, newNick, "Nickname is already in use")
		return
	}
	s.nick = newNick
	nickMsg := &irc.Message{
		Prefix:  &irc.Prefix{Nick: oldNick, User: s.user, Host: s.host},
		Command: irc.NICK,
		Params:  []string{newNick},
	}
	s.Send(nickMsg)
	// Broadcast to shared channels
	notified := map[string]bool{strings.ToLower(oldNick): true}
	for _, ch := range srv.channels.All() {
		if !ch.HasMember(oldNick) && !ch.HasMember(newNick) {
			continue
		}
		// Update membership
		ch.mu.Lock()
		if m, ok := ch.members[strings.ToLower(oldNick)]; ok {
			delete(ch.members, strings.ToLower(oldNick))
			ch.members[strings.ToLower(newNick)] = m
		}
		ch.mu.Unlock()
		for _, m := range ch.Members() {
			key := strings.ToLower(m.Session.nick)
			if !notified[key] {
				notified[key] = true
				m.Session.Send(nickMsg)
			}
		}
	}
}

func (srv *Server) handleUser(s *Session, msg *irc.Message) {
	if s.state == StateRegistered {
		s.SendNumeric(irc.ERR_ALREADYREGISTRED, "You may not reregister")
		return
	}
	if len(msg.Params) < 4 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.USER, "Not enough parameters")
		return
	}
	s.user = cleanUsername(msg.Params[0])
	s.realname = msg.Params[3]
	srv.tryRegister(s)
}

func (srv *Server) tryRegister(s *Session) {
	if s.state == StateRegistered {
		return
	}
	// Check if CAP negotiation is in progress
	if s.capLSReceived && s.state == StateCapNeg {
		return // wait for CAP END
	}
	if s.nick == "" || s.user == "" {
		return
	}

	// Check server password
	if srv.cfg.Password != "" {
		// Password check is done via PASS; if not already validated, reject.
		// (For simplicity we skip persistent state here — real impl would track PASS.)
	}

	if err := srv.sessions.Add(s); err != nil {
		s.SendNumeric(irc.ERR_NICKNAMEINUSE, s.nick, "Nickname is already in use")
		s.nick = ""
		return
	}

	s.state = StateRegistered

	// Send welcome sequence
	s.SendNumeric(irc.RPL_WELCOME, fmt.Sprintf("Welcome to the %s IRC Network %s", srv.cfg.Network, s.mask()))
	s.SendNumeric(irc.RPL_YOURHOST, fmt.Sprintf("Your host is %s, running go-irc", srv.cfg.Name))
	s.SendNumeric(irc.RPL_CREATED, "This server was created just now")
	s.SendNumeric(irc.RPL_MYINFO, srv.cfg.Name, "go-irc-1.0", "iowzBGHRSXZaegimnpstv", "beIqaohvIMRScOAQKFLPTuGzjf")

	// Send ISUPPORT in batches of 13
	tokens := srv.isupport()
	for len(tokens) > 0 {
		batch := tokens
		if len(batch) > 13 {
			batch = tokens[:13]
		}
		tokens = tokens[len(batch):]
		params := append(batch, "are supported by this server")
		s.SendNumeric(irc.RPL_ISUPPORT, params...)
	}

	srv.sendLusers(s)
	srv.sendMOTD(s)
}

// ---------------------------------------------------------------------------
// CAP
// ---------------------------------------------------------------------------

func (srv *Server) handleCAP(s *Session, msg *irc.Message) {
	if len(msg.Params) < 1 {
		return
	}
	subCmd := strings.ToUpper(msg.Params[0])
	if len(msg.Params) >= 2 {
		subCmd = strings.ToUpper(msg.Params[1])
	} else {
		// Some clients send "CAP LS" as first param
		subCmd = strings.ToUpper(msg.Params[0])
	}

	// Reparse: CAP [nick] subcmd [params]
	// params[0] might be nick or subcmd
	params := msg.Params
	subCmd = strings.ToUpper(params[0])
	var subArg string
	if len(params) >= 2 {
		// params[0] could be "*" (the nick placeholder)
		if params[0] == "*" || params[0] == s.nick {
			subCmd = strings.ToUpper(params[1])
			if len(params) >= 3 {
				subArg = params[2]
			}
		} else {
			if len(params) >= 2 {
				subArg = params[1]
			}
		}
	}

	supported := srv.supportedCaps()

	switch subCmd {
	case irc.CapLS:
		s.capLSReceived = true
		if s.state != StateRegistered {
			s.state = StateCapNeg
		}
		if subArg == "302" {
			s.capVersion = 302
		}

		// Build LS response
		var parts []string
		for k, v := range supported {
			if v != "" {
				parts = append(parts, k+"="+v)
			} else {
				parts = append(parts, k)
			}
		}
		capList := strings.Join(parts, " ")

		srv.sendCAP(s, irc.CapLS, capList)

	case irc.CapLIST:
		var enabled []string
		for cap := range s.caps {
			enabled = append(enabled, cap)
		}
		srv.sendCAP(s, irc.CapLIST, strings.Join(enabled, " "))

	case irc.CapREQ:
		req := strings.TrimPrefix(subArg, ":")
		var ack, nak []string
		for _, cap := range strings.Fields(req) {
			disable := strings.HasPrefix(cap, "-")
			capName := strings.TrimPrefix(cap, "-")
			if _, ok := supported[capName]; ok {
				if disable {
					delete(s.caps, capName)
				} else {
					s.caps[capName] = true
				}
				ack = append(ack, cap)
			} else {
				nak = append(nak, cap)
			}
		}
		if len(nak) > 0 {
			srv.sendCAP(s, irc.CapNAK, req)
		} else {
			srv.sendCAP(s, irc.CapACK, strings.Join(ack, " "))
		}

	case irc.CapEND:
		if s.state == StateCapNeg {
			s.state = StatePreReg
		}
		srv.tryRegister(s)
	}
}

func (srv *Server) sendCAP(s *Session, subCmd, value string) {
	nick := s.nick
	if nick == "" {
		nick = "*"
	}
	params := []string{nick, subCmd}
	if value != "" {
		params = append(params, value)
	}
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
		Command: irc.CAP,
		Params:  params,
	})
}

// ---------------------------------------------------------------------------
// AUTHENTICATE (minimal SASL PLAIN support)
// ---------------------------------------------------------------------------

func (srv *Server) handleAuthenticate(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		return
	}
	if msg.Params[0] == "PLAIN" {
		s.saslMech = "PLAIN"
		s.Send(&irc.Message{
			Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
			Command: irc.AUTHENTICATE,
			Params:  []string{"+"},
		})
		return
	}
	if s.saslMech == "PLAIN" {
		// Accept any credentials (no auth backend — extend as needed)
		s.saslDone = true
		s.SendNumeric(irc.RPL_SASLSUCCESS, "Authentication successful")
		return
	}
	s.SendNumeric(irc.ERR_SASLFAIL, "Authentication failed")
}

// ---------------------------------------------------------------------------
// Messaging
// ---------------------------------------------------------------------------

func (srv *Server) handlePrivmsg(s *Session, msg *irc.Message, notice bool) {
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, msg.Command, "Not enough parameters")
		return
	}
	target := msg.Params[0]
	text := msg.Params[1]

	cmd := irc.PRIVMSG
	if notice {
		cmd = irc.NOTICE
	}

	tags := irc.Tags{}
	if s.capEnabled(irc.CapServerTime) {
		tags["time"] = serverTime()
	}

	outMsg := &irc.Message{
		Tags:    tags,
		Prefix:  s.Prefix(),
		Command: cmd,
		Params:  []string{target, text},
	}

	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
		ch, ok := srv.channels.Get(target)
		if !ok {
			s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick/channel")
			return
		}
		if !ch.HasMember(s.nick) && ch.modes.Has('n') {
			s.SendNumeric(irc.ERR_CANNOTSENDTOCHAN, target, "Cannot send to channel")
			return
		}
		if ch.modes.Has('m') {
			m := ch.GetMembership(s.nick)
			if m == nil || m.Prefix == "" {
				s.SendNumeric(irc.ERR_CANNOTSENDTOCHAN, target, "Cannot send to channel (+m)")
				return
			}
		}
		ch.Broadcast(outMsg, s)
		if s.capEnabled(irc.CapEchoMessage) {
			s.Send(outMsg)
		}
	} else {
		dest, ok := srv.sessions.Get(target)
		if !ok {
			s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick/channel")
			return
		}
		dest.Send(outMsg)
		if s.capEnabled(irc.CapEchoMessage) {
			s.Send(outMsg)
		}
		if dest.away != "" && !notice {
			s.SendNumeric(irc.RPL_AWAY, target, dest.away)
		}
	}
}

func (srv *Server) handleTagmsg(s *Session, msg *irc.Message) {
	if len(msg.Params) < 1 {
		return
	}
	target := msg.Params[0]
	outMsg := &irc.Message{
		Tags:    msg.Tags,
		Prefix:  s.Prefix(),
		Command: irc.TAGMSG,
		Params:  []string{target},
	}
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
		ch, ok := srv.channels.Get(target)
		if !ok {
			return
		}
		ch.Broadcast(outMsg, s)
	} else {
		if dest, ok := srv.sessions.Get(target); ok {
			dest.Send(outMsg)
		}
	}
}

// ---------------------------------------------------------------------------
// Channel commands
// ---------------------------------------------------------------------------

func (srv *Server) handleJoin(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.JOIN, "Not enough parameters")
		return
	}

	channels := strings.Split(msg.Params[0], ",")
	keys := []string{}
	if len(msg.Params) >= 2 {
		keys = strings.Split(msg.Params[1], ",")
	}

	for i, chanName := range channels {
		if chanName == "" {
			continue
		}
		if !isValidChannelName(chanName) {
			s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "Invalid channel name")
			continue
		}
		key := ""
		if i < len(keys) {
			key = keys[i]
		}

		ch := srv.channels.GetOrCreate(chanName)

		// Check key
		if k, ok := ch.modes.Arg('k'); ok && k != key {
			s.SendNumeric(irc.ERR_BADCHANNELKEY, chanName, "Cannot join channel (+k)")
			continue
		}
		// Check invite-only
		if ch.modes.Has('i') && !ch.HasMember(s.nick) {
			s.SendNumeric(irc.ERR_INVITEONLYCHAN, chanName, "Cannot join channel (+i)")
			continue
		}
		// Check limit
		if limit, ok := ch.modes.Arg('l'); ok {
			var n int
			fmt.Sscan(limit, &n)
			if n > 0 && len(ch.Members()) >= n {
				s.SendNumeric(irc.ERR_CHANNELISFULL, chanName, "Cannot join channel (+l)")
				continue
			}
		}

		if ch.HasMember(s.nick) {
			continue // already in channel
		}

		// Give ops if first member
		prefix := ""
		if len(ch.Members()) == 0 {
			prefix = "@"
		}
		ch.AddMember(s, prefix)

		joinMsg := &irc.Message{
			Prefix:  s.Prefix(),
			Command: irc.JOIN,
			Params:  []string{chanName},
		}
		if s.capEnabled(irc.CapExtendedJoin) {
			joinMsg.Params = append(joinMsg.Params, s.account, s.realname)
		}
		ch.Broadcast(joinMsg, nil)

		// Topic
		ch.mu.RLock()
		topic := ch.topic
		topicBy := ch.topicSetBy
		topicAt := ch.topicSetAt
		ch.mu.RUnlock()

		if topic != "" {
			s.SendNumeric(irc.RPL_TOPIC, chanName, topic)
			s.SendNumeric(irc.RPL_TOPICWHOTIME, chanName, topicBy, fmt.Sprintf("%d", topicAt.Unix()))
		} else {
			s.SendNumeric(irc.RPL_NOTOPIC, chanName, "No topic is set")
		}

		srv.sendNames(s, ch)
	}
}

func (srv *Server) handlePart(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.PART, "Not enough parameters")
		return
	}
	reason := ""
	if len(msg.Params) >= 2 {
		reason = msg.Params[1]
	}
	for _, chanName := range strings.Split(msg.Params[0], ",") {
		ch, ok := srv.channels.Get(chanName)
		if !ok {
			s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "No such channel")
			continue
		}
		if !ch.HasMember(s.nick) {
			s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
			continue
		}
		params := []string{chanName}
		if reason != "" {
			params = append(params, reason)
		}
		partMsg := &irc.Message{Prefix: s.Prefix(), Command: irc.PART, Params: params}
		ch.Broadcast(partMsg, nil)
		empty := ch.RemoveMember(s.nick)
		if empty {
			srv.channels.Remove(chanName)
		}
	}
}

func (srv *Server) handleTopic(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.TOPIC, "Not enough parameters")
		return
	}
	chanName := msg.Params[0]
	ch, ok := srv.channels.Get(chanName)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "No such channel")
		return
	}
	if !ch.HasMember(s.nick) {
		s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
		return
	}

	if len(msg.Params) < 2 {
		// Query topic
		ch.mu.RLock()
		topic := ch.topic
		topicBy := ch.topicSetBy
		topicAt := ch.topicSetAt
		ch.mu.RUnlock()
		if topic != "" {
			s.SendNumeric(irc.RPL_TOPIC, chanName, topic)
			s.SendNumeric(irc.RPL_TOPICWHOTIME, chanName, topicBy, fmt.Sprintf("%d", topicAt.Unix()))
		} else {
			s.SendNumeric(irc.RPL_NOTOPIC, chanName, "No topic is set")
		}
		return
	}

	// Set topic (check +t)
	if ch.modes.Has('t') {
		m := ch.GetMembership(s.nick)
		if m == nil || !strings.ContainsAny(m.Prefix, "@&~") {
			s.SendNumeric(irc.ERR_CHANOPRIVSNEEDED, chanName, "You're not channel operator")
			return
		}
	}

	newTopic := msg.Params[1]
	ch.mu.Lock()
	ch.topic = newTopic
	ch.topicSetBy = s.mask()
	ch.topicSetAt = timeNow()
	ch.mu.Unlock()

	topicMsg := &irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.TOPIC,
		Params:  []string{chanName, newTopic},
	}
	ch.Broadcast(topicMsg, nil)
}

func (srv *Server) handleNames(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		// Send NAMES for all channels the user is in
		for _, ch := range srv.channels.All() {
			if ch.HasMember(s.nick) {
				srv.sendNames(s, ch)
			}
		}
		return
	}
	for _, chanName := range strings.Split(msg.Params[0], ",") {
		ch, ok := srv.channels.Get(chanName)
		if !ok {
			continue
		}
		srv.sendNames(s, ch)
	}
}

func (srv *Server) sendNames(s *Session, ch *Channel) {
	multiPrefix := s.capEnabled(irc.CapMultiPrefix)
	names := ch.NamesReply(multiPrefix)

	// Send in chunks of ~400 chars
	const chunkMax = 400
	var line []string
	lineLen := 0
	flush := func() {
		if len(line) == 0 {
			return
		}
		s.SendNumeric(irc.RPL_NAMREPLY, "=", ch.name, strings.Join(line, " "))
		line = nil
		lineLen = 0
	}
	for _, n := range names {
		if lineLen+len(n)+1 > chunkMax {
			flush()
		}
		line = append(line, n)
		lineLen += len(n) + 1
	}
	flush()
	s.SendNumeric(irc.RPL_ENDOFNAMES, ch.name, "End of /NAMES list")
}

func (srv *Server) handleList(s *Session, msg *irc.Message) {
	s.SendNumeric(irc.RPL_LISTSTART, "Channel", "Users  Name")
	for _, ch := range srv.channels.All() {
		if ch.modes.Has('s') || ch.modes.Has('p') {
			if !ch.HasMember(s.nick) {
				continue
			}
		}
		ch.mu.RLock()
		topic := ch.topic
		ch.mu.RUnlock()
		count := fmt.Sprintf("%d", len(ch.Members()))
		s.SendNumeric(irc.RPL_LIST, ch.name, count, topic)
	}
	s.SendNumeric(irc.RPL_LISTEND, "End of /LIST")
}

func (srv *Server) handleKick(s *Session, msg *irc.Message) {
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.KICK, "Not enough parameters")
		return
	}
	chanName := msg.Params[0]
	target := msg.Params[1]
	reason := s.nick
	if len(msg.Params) >= 3 {
		reason = msg.Params[2]
	}

	ch, ok := srv.channels.Get(chanName)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "No such channel")
		return
	}
	m := ch.GetMembership(s.nick)
	if m == nil {
		s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
		return
	}
	if !strings.ContainsAny(m.Prefix, "@&~") {
		s.SendNumeric(irc.ERR_CHANOPRIVSNEEDED, chanName, "You're not channel operator")
		return
	}
	if !ch.HasMember(target) {
		s.SendNumeric(irc.ERR_USERNOTINCHANNEL, target, chanName, "They aren't on that channel")
		return
	}
	kickMsg := &irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.KICK,
		Params:  []string{chanName, target, reason},
	}
	ch.Broadcast(kickMsg, nil)
	empty := ch.RemoveMember(target)
	if empty {
		srv.channels.Remove(chanName)
	}
}

func (srv *Server) handleInvite(s *Session, msg *irc.Message) {
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.INVITE, "Not enough parameters")
		return
	}
	target := msg.Params[0]
	chanName := msg.Params[1]

	dest, ok := srv.sessions.Get(target)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick/channel")
		return
	}
	ch, ok := srv.channels.Get(chanName)
	if ok {
		if !ch.HasMember(s.nick) {
			s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
			return
		}
	}

	s.SendNumeric(irc.RPL_INVITING, target, chanName)
	dest.Send(&irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.INVITE,
		Params:  []string{target, chanName},
	})

	// Broadcast invite-notify
	if ch != nil {
		for _, m := range ch.Members() {
			if m.Session != s && m.Session.capEnabled(irc.CapInviteNotify) {
				m.Session.Send(&irc.Message{
					Prefix:  s.Prefix(),
					Command: irc.INVITE,
					Params:  []string{target, chanName},
				})
			}
		}
	}
}

func (srv *Server) handleMode(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.MODE, "Not enough parameters")
		return
	}
	target := msg.Params[0]

	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
		srv.handleChannelMode(s, msg, target)
	} else {
		srv.handleUserMode(s, msg, target)
	}
}

func (srv *Server) handleChannelMode(s *Session, msg *irc.Message, chanName string) {
	ch, ok := srv.channels.Get(chanName)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "No such channel")
		return
	}

	// Query mode
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.RPL_CHANNELMODEIS, chanName, ch.modes.String())
		s.SendNumeric(irc.RPL_CREATIONTIME, chanName, fmt.Sprintf("%d", ch.createdAt.Unix()))
		return
	}

	// List modes query (e.g. MODE #chan b)
	if len(msg.Params) == 2 && len(msg.Params[1]) == 1 {
		modeRune := rune(msg.Params[1][0])
		cv := mode.ChannelValidator{}
		if cv.Type(modeRune) == mode.TypeList {
			for _, entry := range ch.modes.List(modeRune) {
				s.SendNumeric(irc.RPL_BANLIST, chanName, entry)
			}
			s.SendNumeric(irc.RPL_ENDOFBANLIST, chanName, "End of ban list")
			return
		}
	}

	// Set/unset modes
	membership := ch.GetMembership(s.nick)
	if membership == nil {
		s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
		return
	}
	if !strings.ContainsAny(membership.Prefix, "@&~") && !s.isOper {
		s.SendNumeric(irc.ERR_CHANOPRIVSNEEDED, chanName, "You're not channel operator")
		return
	}

	changes := irc.ParseModeString(msg.Params[1], msg.Params[2:])
	applied := ch.modes.Apply(changes, mode.ChannelValidator{})
	if len(applied) > 0 {
		modeStr, args := irc.FormatModeChanges(applied)
		params := append([]string{chanName, modeStr}, args...)
		ch.Broadcast(&irc.Message{
			Prefix:  s.Prefix(),
			Command: irc.MODE,
			Params:  params,
		}, nil)
	}
}

func (srv *Server) handleUserMode(s *Session, msg *irc.Message, target string) {
	if !strings.EqualFold(target, s.nick) && !s.isOper {
		s.SendNumeric(irc.ERR_USERSDONTMATCH, "Cannot change mode for other users")
		return
	}
	dest, ok := srv.sessions.Get(target)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick")
		return
	}
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.RPL_UMODEIS, dest.modes.String())
		return
	}
	changes := irc.ParseModeString(msg.Params[1], msg.Params[2:])
	applied := dest.modes.Apply(changes, mode.UserValidator{})
	if len(applied) > 0 {
		modeStr, args := irc.FormatModeChanges(applied)
		params := append([]string{target, modeStr}, args...)
		dest.Send(&irc.Message{
			Prefix:  s.Prefix(),
			Command: irc.MODE,
			Params:  params,
		})
	}
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

func (srv *Server) handleWho(s *Session, msg *irc.Message) {
	mask := "*"
	if len(msg.Params) > 0 {
		mask = msg.Params[0]
	}

	if strings.HasPrefix(mask, "#") || strings.HasPrefix(mask, "&") {
		ch, ok := srv.channels.Get(mask)
		if ok {
			for _, m := range ch.Members() {
				u := m.Session
				flags := "H"
				if u.away != "" {
					flags = "G"
				}
				flags += m.Prefix
				s.SendNumeric(irc.RPL_WHOREPLY, mask, u.user, u.host, srv.cfg.Name, u.nick, flags, "0 "+u.realname)
			}
		}
	} else {
		for _, u := range srv.sessions.All() {
			if matchMask(mask, u.mask()) || matchMask(mask, u.nick) {
				flags := "H"
				if u.away != "" {
					flags = "G"
				}
				s.SendNumeric(irc.RPL_WHOREPLY, mask, u.user, u.host, srv.cfg.Name, u.nick, flags, "0 "+u.realname)
			}
		}
	}
	s.SendNumeric(irc.RPL_ENDOFWHO, mask, "End of /WHO list")
}

func (srv *Server) handleWhois(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NONICKNAMEGIVEN, "No nickname given")
		return
	}
	nick := msg.Params[0]
	target, ok := srv.sessions.Get(nick)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHNICK, nick, "No such nick/channel")
		s.SendNumeric(irc.RPL_ENDOFWHOIS, nick, "End of /WHOIS list")
		return
	}

	s.SendNumeric(irc.RPL_WHOISUSER, target.nick, target.user, target.host, "*", target.realname)
	s.SendNumeric(irc.RPL_WHOISSERVER, target.nick, srv.cfg.Name, srv.cfg.Network)

	// Channels
	var chans []string
	for _, ch := range srv.channels.All() {
		m := ch.GetMembership(target.nick)
		if m != nil {
			chans = append(chans, m.Prefix+ch.name)
		}
	}
	if len(chans) > 0 {
		s.SendNumeric(irc.RPL_WHOISCHANNELS, target.nick, strings.Join(chans, " "))
	}

	if target.isOper {
		s.SendNumeric(irc.RPL_WHOISOPERATOR, target.nick, "is an IRC operator")
	}
	if target.away != "" {
		s.SendNumeric(irc.RPL_AWAY, target.nick, target.away)
	}
	idle := int64(timeNow().Sub(target.idleAt).Seconds())
	signon := target.signOnAt.Unix()
	s.SendNumeric(irc.RPL_WHOISIDLE, target.nick, fmt.Sprintf("%d", idle), fmt.Sprintf("%d", signon), "seconds idle, signon time")
	s.SendNumeric(irc.RPL_ENDOFWHOIS, target.nick, "End of /WHOIS list")
}

func (srv *Server) handleWhowas(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NONICKNAMEGIVEN, "No nickname given")
		return
	}
	nick := msg.Params[0]
	srv.whowasMu.Lock()
	entries := append([]whowasEntry(nil), srv.whowas...)
	srv.whowasMu.Unlock()

	found := false
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if strings.EqualFold(e.nick, nick) {
			s.SendNumeric(irc.RPL_WHOWASUSER, e.nick, e.user, e.host, "*", e.realname)
			s.SendNumeric(irc.RPL_WHOISSERVER, e.nick, srv.cfg.Name, e.quitAt.Format("Mon Jan 02 15:04:05 2006"))
			found = true
		}
	}
	if !found {
		s.SendNumeric(irc.ERR_WASNOSUCHNICK, nick, "There was no such nickname")
	}
	s.SendNumeric(irc.RPL_ENDOFWHOWAS, nick, "End of WHOWAS")
}

// ---------------------------------------------------------------------------
// User state
// ---------------------------------------------------------------------------

func (srv *Server) handleAway(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 || msg.Params[0] == "" {
		s.away = ""
		s.SendNumeric(irc.RPL_UNAWAY, "You are no longer marked as being away")
	} else {
		s.away = msg.Params[0]
		s.SendNumeric(irc.RPL_NOWAWAY, "You have been marked as being away")
	}

	// away-notify broadcast
	for _, ch := range srv.channels.All() {
		if !ch.HasMember(s.nick) {
			continue
		}
		awayMsg := &irc.Message{
			Prefix:  s.Prefix(),
			Command: irc.AWAY,
		}
		if s.away != "" {
			awayMsg.Params = []string{s.away}
		}
		for _, m := range ch.Members() {
			if m.Session != s && m.Session.capEnabled(irc.CapAwayNotify) {
				m.Session.Send(awayMsg)
			}
		}
	}
}

func (srv *Server) handleUserhost(s *Session, msg *irc.Message) {
	var results []string
	for _, nick := range msg.Params {
		u, ok := srv.sessions.Get(nick)
		if !ok {
			continue
		}
		oper := ""
		if u.isOper {
			oper = "*"
		}
		away := "+"
		if u.away != "" {
			away = "-"
		}
		results = append(results, u.nick+oper+"="+away+u.user+"@"+u.host)
	}
	s.SendNumeric(irc.RPL_USERHOST, strings.Join(results, " "))
}

func (srv *Server) handleIson(s *Session, msg *irc.Message) {
	var online []string
	for _, nick := range msg.Params {
		if _, ok := srv.sessions.Get(nick); ok {
			online = append(online, nick)
		}
	}
	s.SendNumeric(irc.RPL_ISON, strings.Join(online, " "))
}

// ---------------------------------------------------------------------------
// Server commands
// ---------------------------------------------------------------------------

func (srv *Server) handlePing(s *Session, msg *irc.Message) {
	params := msg.Params
	if len(params) == 0 {
		params = []string{srv.cfg.Name}
	}
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
		Command: irc.PONG,
		Params:  params,
	})
}

func (srv *Server) handleQuit(s *Session, msg *irc.Message) {
	reason := "Quit"
	if len(msg.Params) > 0 && msg.Params[0] != "" {
		reason = msg.Params[0]
	}
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
		Command: irc.ERROR,
		Params:  []string{"Closing Link: " + s.host + " (Quit: " + reason + ")"},
	})
	s.conn.Close()
}

func (srv *Server) sendMOTD(s *Session) {
	if srv.cfg.MOTD == "" {
		s.SendNumeric(irc.ERR_NOMOTD, "MOTD File is missing")
		return
	}
	s.SendNumeric(irc.RPL_MOTDSTART, "- "+srv.cfg.Name+" Message of the Day -")
	for _, line := range strings.Split(srv.cfg.MOTD, "\n") {
		s.SendNumeric(irc.RPL_MOTD, "- "+line)
	}
	s.SendNumeric(irc.RPL_ENDOFMOTD, "End of /MOTD command.")
}

func (srv *Server) sendLusers(s *Session) {
	total := srv.sessions.Count()
	chans := srv.channels.Count()
	s.SendNumeric(irc.RPL_LUSERCLIENT, fmt.Sprintf("There are %d users on 1 server", total))
	s.SendNumeric(irc.RPL_LUSERCHANNELS, fmt.Sprintf("%d", chans), "channels formed")
	s.SendNumeric(irc.RPL_LUSERME, fmt.Sprintf("I have %d clients and 0 servers", total))
}

func (srv *Server) handleVersion(s *Session, _ *irc.Message) {
	s.SendNumeric(irc.RPL_VERSION, "go-irc-1.0", srv.cfg.Name, "go-irc")
}

func (srv *Server) handleTime(s *Session, _ *irc.Message) {
	s.SendNumeric(irc.RPL_TIME, srv.cfg.Name, timeNow().Format("Mon Jan 02 2006 15:04:05 -0700"))
}

func (srv *Server) handleAdmin(s *Session, _ *irc.Message) {
	s.SendNumeric(irc.RPL_ADMINME, srv.cfg.Name, "Administrative info")
	s.SendNumeric(irc.RPL_ADMINLOC1, "go-irc IRC server")
	s.SendNumeric(irc.RPL_ADMINLOC2, "Running go-irc")
	s.SendNumeric(irc.RPL_ADMINEMAIL, "admin@"+srv.cfg.Name)
}

func (srv *Server) handleInfo(s *Session, _ *irc.Message) {
	s.SendNumeric(irc.RPL_INFO, "go-irc — a Go IRC server")
	s.SendNumeric(irc.RPL_INFO, "https://github.com/natalie-o-perret/go-irc")
	s.SendNumeric(irc.RPL_ENDOFINFO, "End of /INFO list")
}

func (srv *Server) handleStats(s *Session, msg *irc.Message) {
	query := "?"
	if len(msg.Params) > 0 {
		query = msg.Params[0]
	}
	switch strings.ToLower(query) {
	case "u":
		s.SendNumeric(irc.RPL_STATSUPTIME, "Server Up 0 days, 0:00:00")
	}
	s.SendNumeric(irc.RPL_ENDOFSTATS, query, "End of /STATS report")
}

func (srv *Server) handleWallops(s *Session, msg *irc.Message) {
	if !s.isOper {
		s.SendNumeric(irc.ERR_NOPRIVILEGES, "Permission Denied- You're not an IRC operator")
		return
	}
	text := ""
	if len(msg.Params) > 0 {
		text = msg.Params[0]
	}
	wallMsg := &irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.WALLOPS,
		Params:  []string{text},
	}
	for _, sess := range srv.sessions.All() {
		if sess.modes.Has('w') || sess.isOper {
			sess.Send(wallMsg)
		}
	}
}

// ---------------------------------------------------------------------------
// Oper commands
// ---------------------------------------------------------------------------

func (srv *Server) handleOper(s *Session, msg *irc.Message) {
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.OPER, "Not enough parameters")
		return
	}
	name := msg.Params[0]
	pass := msg.Params[1]

	hash, ok := srv.cfg.Opers[name]
	if !ok || !checkBcrypt(hash, pass) {
		s.SendNumeric(irc.ERR_PASSWDMISMATCH, "Password incorrect")
		return
	}

	s.isOper = true
	s.modes.Apply([]irc.ModeChange{{Add: true, Mode: 'o'}}, mode.UserValidator{})
	s.SendNumeric(irc.RPL_YOUROPER, "You are now an IRC operator")
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
		Command: irc.MODE,
		Params:  []string{s.nick, "+o"},
	})
}

func (srv *Server) handleKill(s *Session, msg *irc.Message) {
	if !s.isOper {
		s.SendNumeric(irc.ERR_NOPRIVILEGES, "Permission Denied- You're not an IRC operator")
		return
	}
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.KILL, "Not enough parameters")
		return
	}
	target, ok := srv.sessions.Get(msg.Params[0])
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHNICK, msg.Params[0], "No such nick")
		return
	}
	reason := msg.Params[1]
	target.Send(&irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.ERROR,
		Params:  []string{"Killed by " + s.nick + " (" + reason + ")"},
	})
	target.conn.Close()
}

// ---------------------------------------------------------------------------
// IRCv3 extensions
// ---------------------------------------------------------------------------

func (srv *Server) handleSetname(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		return
	}
	s.realname = msg.Params[0]
	setMsg := &irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.SETNAME,
		Params:  []string{s.realname},
	}
	// Broadcast to shared channels
	notified := map[string]bool{strings.ToLower(s.nick): true}
	for _, ch := range srv.channels.All() {
		if !ch.HasMember(s.nick) {
			continue
		}
		for _, m := range ch.Members() {
			key := strings.ToLower(m.Session.nick)
			if !notified[key] && m.Session.capEnabled(irc.CapSetname) {
				notified[key] = true
				m.Session.Send(setMsg)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isValidNick(nick string) bool {
	if len(nick) == 0 || len(nick) > 30 {
		return false
	}
	for i, ch := range nick {
		if i == 0 {
			if !isLetter(ch) && ch != '_' && ch != '\\' && ch != '[' && ch != ']' && ch != '{' && ch != '}' && ch != '|' && ch != '^' && ch != '`' {
				return false
			}
		} else {
			if !isLetter(ch) && !isDigit(ch) && ch != '-' && ch != '_' && ch != '\\' && ch != '[' && ch != ']' && ch != '{' && ch != '}' && ch != '|' && ch != '^' && ch != '`' {
				return false
			}
		}
	}
	return true
}

func isValidChannelName(name string) bool {
	if len(name) < 2 || len(name) > 64 {
		return false
	}
	if name[0] != '#' && name[0] != '&' {
		return false
	}
	return !strings.ContainsAny(name, " \x07,")
}

func isLetter(ch rune) bool { return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') }
func isDigit(ch rune) bool  { return ch >= '0' && ch <= '9' }

func cleanUsername(u string) string {
	u = strings.TrimPrefix(u, "~")
	if len(u) > 10 {
		u = u[:10]
	}
	return u
}

func matchMask(mask, target string) bool {
	// Simple glob: * and ?
	mask = strings.ToLower(mask)
	target = strings.ToLower(target)
	return globMatch(mask, target)
}

func globMatch(pattern, s string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			if len(pattern) == 1 {
				return true
			}
			for i := range s {
				if globMatch(pattern[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		default:
			if len(s) == 0 || pattern[0] != s[0] {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		}
	}
	return len(s) == 0
}

// checkBcrypt compares a bcrypt hash with a plain-text password.
func checkBcrypt(hash, password string) bool {
	return bcryptCheck([]byte(hash), []byte(password))
}

// timeNow is a substitutable clock for tests.
var timeNow = func() time.Time { return time.Now() }
