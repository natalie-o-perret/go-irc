// Package irc implements core IRC protocol types and parsing (RFC 1459, RFC
// 2812, IRCv3). It is pure logic with no I/O so every other package can import
// it without circular-dependency risk.
package irc

import (
	"bytes"
	"fmt"
	"strings"
)


// Tags holds IRCv3 message tags.  A missing value is represented as an empty
// string; a tag that is present without a value is also an empty string.
type Tags map[string]string

// Get returns the value of a tag and whether it existed.
func (t Tags) Get(key string) (string, bool) {
	if t == nil {
		return "", false
	}
	v, ok := t[key]
	return v, ok
}

// Prefix represents the optional prefix (source) of a message.
type Prefix struct {
	Nick string // nick or servername
	User string // username (empty for server prefix)
	Host string // hostname (empty when no @)
}

// IsServer returns true when the prefix looks like a servername (no nick!user).
func (p *Prefix) IsServer() bool { return p.User == "" && p.Host == "" }

// String formats a Prefix back into its wire form.
func (p *Prefix) String() string {
	if p == nil {
		return ""
	}
	if p.IsServer() {
		return p.Nick
	}
	if p.Host != "" {
		return p.Nick + "!" + p.User + "@" + p.Host
	}
	return p.Nick
}

// Message is a parsed IRC message.
type Message struct {
	Tags    Tags
	Prefix  *Prefix
	Command string
	Params  []string
}

// Trailing returns the last parameter if it exists, otherwise an empty string.
// Per RFC 1459 §2.3, the trailing parameter is simply the last param.
func (m *Message) Trailing() string {
	if len(m.Params) == 0 {
		return ""
	}
	return m.Params[len(m.Params)-1]
}

// Param returns the parameter at index i, or "" if out of range.
func (m *Message) Param(i int) string {
	if i < 0 || i >= len(m.Params) {
		return ""
	}
	return m.Params[i]
}

// Clone returns a deep copy of the message.
func (m *Message) Clone() *Message {
	out := &Message{
		Command: m.Command,
	}
	if m.Tags != nil {
		out.Tags = make(Tags, len(m.Tags))
		for k, v := range m.Tags {
			out.Tags[k] = v
		}
	}
	if m.Prefix != nil {
		p := *m.Prefix
		out.Prefix = &p
	}
	if m.Params != nil {
		out.Params = make([]string, len(m.Params))
		copy(out.Params, m.Params)
	}
	return out
}

// String formats a Message into its wire representation (without CRLF).
func (m *Message) String() string {
	return Format(m)
}

// Parse decodes a single raw IRC line (with or without CRLF) into a Message.
// It handles IRCv3 message tags, the optional :prefix, command, and parameters.
func Parse(raw string) (*Message, error) {
	raw = strings.TrimRight(raw, "\r\n")
	if raw == "" {
		return nil, fmt.Errorf("irc: empty message")
	}

	msg := &Message{}

	// --- tags (@key=value;key2=value2 ...) ---
	if raw[0] == '@' {
		idx := strings.IndexByte(raw, ' ')
		if idx < 0 {
			return nil, fmt.Errorf("irc: malformed message: no space after tags")
		}
		msg.Tags = parseTags(raw[1:idx])
		raw = strings.TrimLeft(raw[idx+1:], " ")
	}

	// --- prefix (:nick!user@host or :server) ---
	if len(raw) > 0 && raw[0] == ':' {
		idx := strings.IndexByte(raw, ' ')
		if idx < 0 {
			return nil, fmt.Errorf("irc: malformed message: no space after prefix")
		}
		msg.Prefix = parsePrefix(raw[1:idx])
		raw = strings.TrimLeft(raw[idx+1:], " ")
	}

	// --- command ---
	if raw == "" {
		return nil, fmt.Errorf("irc: empty command")
	}

	// --- params ---
	for raw != "" {
		if raw[0] == ':' {
			// trailing parameter — everything after the colon
			msg.Params = append(msg.Params, raw[1:])
			break
		}
		idx := strings.IndexByte(raw, ' ')
		if idx < 0 {
			msg.Params = append(msg.Params, raw)
			raw = ""
		} else {
			msg.Params = append(msg.Params, raw[:idx])
			raw = strings.TrimLeft(raw[idx+1:], " ")
		}
	}

	if len(msg.Params) == 0 {
		return nil, fmt.Errorf("irc: missing command")
	}
	msg.Command = strings.ToUpper(msg.Params[0])
	msg.Params = msg.Params[1:]

	return msg, nil
}

// Format encodes a Message to its wire representation (without CRLF).
func Format(m *Message) string {
	var buf bytes.Buffer

	// tags
	if len(m.Tags) > 0 {
		buf.WriteByte('@')
		first := true
		for k, v := range m.Tags {
			if !first {
				buf.WriteByte(';')
			}
			first = false
			buf.WriteString(k)
			if v != "" {
				buf.WriteByte('=')
				buf.WriteString(escapeTagValue(v))
			}
		}
		buf.WriteByte(' ')
	}

	// prefix
	if m.Prefix != nil {
		buf.WriteByte(':')
		buf.WriteString(m.Prefix.String())
		buf.WriteByte(' ')
	}

	// command
	buf.WriteString(m.Command)

	// params
	for i, p := range m.Params {
		buf.WriteByte(' ')
		// last param needs : if it contains spaces or starts with :
		if i == len(m.Params)-1 && (strings.ContainsAny(p, " :") || p == "") {
			buf.WriteByte(':')
		}
		buf.WriteString(p)
	}

	return buf.String()
}

// MustParse panics if the line cannot be parsed. Useful in tests.
func MustParse(raw string) *Message {
	m, err := Parse(raw)
	if err != nil {
		panic(err)
	}
	return m
}

// --- helpers -----------------------------------------------------------------

func parsePrefix(s string) *Prefix {
	p := &Prefix{}
	if at := strings.IndexByte(s, '@'); at >= 0 {
		p.Host = s[at+1:]
		s = s[:at]
	}
	if bang := strings.IndexByte(s, '!'); bang >= 0 {
		p.User = s[bang+1:]
		s = s[:bang]
	}
	p.Nick = s
	return p
}

func parseTags(s string) Tags {
	tags := make(Tags)
	for _, part := range strings.Split(s, ";") {
		if part == "" {
			continue
		}
		if eq := strings.IndexByte(part, '='); eq >= 0 {
			tags[part[:eq]] = unescapeTagValue(part[eq+1:])
		} else {
			tags[part] = ""
		}
	}
	return tags
}

// Tag value escaping as per IRCv3 message-tags spec.
var tagEscaper = strings.NewReplacer(
	";", `\:`,
	" ", `\s`,
	`\`, `\\`,
	"\r", `\r`,
	"\n", `\n`,
)

var tagUnescaper = strings.NewReplacer(
	`\:`, ";",
	`\s`, " ",
	`\\`, `\`,
	`\r`, "\r",
	`\n`, "\n",
)

func escapeTagValue(s string) string   { return tagEscaper.Replace(s) }
func unescapeTagValue(s string) string { return tagUnescaper.Replace(s) }
