// Package goirc is a comprehensive Go implementation of the IRC protocol,
// including a full-featured IRC server (ircd), IRC client library, IRC bouncer,
// and XDCC file transfer support.
//
// # Packages
//
// irc: Core IRC protocol types — message parsing/formatting per RFC 1459 +
// RFC 2812, IRCv3 message tags, mode change parsing, numeric reply codes, and
// capability name constants.
//
// client: High-level IRC client library with IRCv3 CAP negotiation, automatic
// SASL authentication, and a handler/mux system for dispatching incoming
// messages.
//
// client/sasl: SASL mechanisms: PLAIN, EXTERNAL, SCRAM-SHA-256, SCRAM-SHA-512.
//
// client/dcc: DCC (Direct Client-to-Client) file transfers — SEND/RECV with
// 4-byte big-endian ACKs, resume support, and passive DCC (port 0 + token).
//
// client/xdcc: XDCC file serving and downloading — pack list parsing in
// iroffer/SysReset format, XDCC SEND request handling, and a Bot type for
// serving packs.
//
// server: Full RFC 2812 + IRCv3 IRC server with channel and user mode
// management, WHO/WHOIS/WHOWAS, OPER/KILL, IRCv3 cap negotiation, and
// server-time tags.
//
// server/mode: Mode set types and validators for channel and user modes.
//
// bouncer: Multi-upstream multi-downstream IRC bouncer with per-network
// history storage and playback, similar in spirit to soju and ZNC.
//
// bouncer/history: Pluggable message history storage — in-memory ring buffer
// backend included.
//
// config: TOML configuration loading with validation and sensible defaults for
// both ircd and the bouncer.
//
// internal/ringbuf: Generic, thread-safe, fixed-capacity circular buffer used
// by the history subsystem.
package goirc
