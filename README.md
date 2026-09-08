# go-irc

[![CI](https://github.com/natalie-o-perret/go-irc/actions/workflows/ci.yml/badge.svg)](https://github.com/natalie-o-perret/go-irc/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/natalie-o-perret/go-irc.svg)](https://pkg.go.dev/github.com/natalie-o-perret/go-irc)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Contributing](https://img.shields.io/badge/contributing-guide-blue)](CONTRIBUTING.md)

A client, server, and bouncer implementation for IRC in Go.

> Full IRC protocol client & server library - IRC bouncer - XDCC file transfers - IRCv3 - pure Go - no CGo.

## Features

| Layer                   | What you get                                                                                        |
|-------------------------|-----------------------------------------------------------------------------------------------------|
| **`irc/`**              | RFC 1459 + RFC 2812 message parsing/formatting, IRCv3 message tags, mode change parsing             |
| **`client/`**           | Full IRC client with IRCv3 CAP negotiation, SASL (PLAIN / EXTERNAL / SCRAM-SHA-256 / SCRAM-SHA-512) |
| **`client/dcc/`**       | DCC SEND / RECV with 4-byte ACKs, resume, passive DCC (port 0 + token)                              |
| **`client/xdcc/`**      | XDCC pack list parsing (iroffer/SysReset formats), `XDCC SEND #n`, bot pack serving                 |
| **`client/sasl/`**      | SASL PLAIN, EXTERNAL, SCRAM-SHA-256, SCRAM-SHA-512                                                  |
| **`server/`**           | Full ircd: channel modes, user modes, WHOIS/WHO/WHOWAS, OPER/KILL, WALLOPS, IRCv3                   |
| **`server/mode/`**      | Channel and user mode set management with list modes (+b/+e/+I)                                     |
| **`bouncer/`**          | Multi-upstream multi-downstream IRC bouncer with history replay and cap bridging                    |
| **`bouncer/history/`**  | In-memory (ring-buffer) history store, queryable by time window                                     |
| **`config/`**           | TOML config loading with validation and defaults                                                    |
| **`internal/ringbuf/`** | Generic thread-safe ring buffer                                                                     |

### IRCv3 capabilities advertised by the server

`server-time` · `message-tags` · `batch` · `draft/chathistory` · `echo-message` ·
`multi-prefix` · `away-notify` · `extended-join` · `setname` · `cap-notify` · `invite-notify`

The client also supports SASL PLAIN, EXTERNAL, SCRAM-SHA-256, and SCRAM-SHA-512 when the upstream server advertises them.

## Comparison with existing frameworks

This project combines protocol primitives, client, server, bouncer, and XDCC tooling in one repository. The table below
shows how `go-irc` stacks up against the most commonly used Go IRC libraries and tools.

| Project                                              | Protocol parsing | IRC client | IRC server       | Bouncer          | DCC / XDCC | SASL                               | IRCv3 caps | Pure Go  |
|------------------------------------------------------|------------------|------------|------------------|------------------|------------|------------------------------------|------------|----------|
| **go-irc** (this repo)                               | yes              | yes        | yes              | yes              | yes        | PLAIN, EXTERNAL, SCRAM-SHA-256/512 | selected   | yes      |
| [`go-ircevent`](https://github.com/thoj/go-ircevent) | partial          | yes        | no               | no               | no         | no                                 | limited    | yes      |
| [`girc`](https://github.com/lrstanley/girc)          | yes              | yes        | no               | no               | no         | PLAIN                              | moderate   | yes      |
| [`Ergo (ergo)`](https://github.com/ergochat/ergo)    | yes              | no         | yes (production) | no               | no         | several                            | extensive  | yes      |
| [`soju`](https://codeberg.org/emersion/soju)         | yes              | no         | no               | yes (production) | no         | several                            | extensive  | yes      |
| [`ZNC`](https://znc.in)                              | yes              | no         | no               | yes (production) | no         | several                            | limited    | no (C++) |

### Notes on each alternative

**[`go-ircevent`](https://github.com/thoj/go-ircevent)** is a well-known event-driven IRC client. It offers a simple
callback-based API and handles basic IRC connection management, but has no server, bouncer, DCC transfer, or SASL
support beyond the basics.

**[`girc`](https://github.com/lrstanley/girc)** is a modern, ergonomic IRC client library with a focus on extensibility
and clean API design. It handles IRCv3 CAP negotiation and some SASL mechanisms, but is limited to the client layer.
There is no server, bouncer, or file-transfer support.

**[Ergo (`ergochat/ergo`)](https://github.com/ergochat/ergo)** is a production-grade IRC server implementing many modern
IRCv3 extensions and targeting real-world deployments. It is a server binary rather than a reusable library, so
embedding or extending it requires more effort. It has no client or DCC/XDCC components.

**[`soju`](https://codeberg.org/emersion/soju)** is a production IRC bouncer written in Go, maintained by the IRCv3
working group contributors. It is feature-rich and battle-tested as a standalone service but is not designed to be
embedded as a library. It has no IRC server or file-transfer support.

**[ZNC](https://znc.in)** is the most widely deployed IRC bouncer. It is written in C++ and highly extensible through a
module system. `go-irc` trades the ecosystem maturity of ZNC for a pure-Go embeddable bouncer you can use directly in
your application.

If you only need one layer, a specialized project is often the best fit. If you want one Go module that spans the full
IRC stack (protocol primitives, client, server, bouncer, DCC/XDCC), `go-irc` is designed for that use case.

## Binaries

| Binary     | Description                  |
|------------|------------------------------|
| `cmd/ircd` | Standalone IRC server        |
| `cmd/ircb` | IRC bouncer (ZNC/soju-style) |
| `cmd/ircx` | XDCC downloader CLI          |

### Build

```bash
go build ./cmd/ircd   # -> ./ircd
go build ./cmd/ircb   # -> ./ircb
go build ./cmd/ircx   # -> ./ircx
```

### Quick-start: ircd

```bash
./ircd                            # runs on :6667 with built-in defaults
./ircd -config ircd.toml          # use config file
```

Example `ircd.toml`:

```toml
[server]
name    = "irc.example.com"
network = "ExampleNet"
listen  = ":6667"
motd    = "Welcome to ExampleNet!\nHave fun."

[[server.oper]]
name     = "admin"
password = "$2a$10$..."   # bcrypt hash of your oper password
```

### Quick-start: bouncer

```bash
./ircb -config ircb.toml
```

Clients connect with:

```
/server localhost 6668
/pass myuser/libera:mypassword
```

Example `ircb.toml`:

```toml
[bouncer]
listen = ":6668"

[[bouncer.user]]
name     = "myuser"
password = "mypassword"

[[bouncer.network]]
name         = "libera"
server       = "irc.libera.chat:6667"
nick         = "mynick"
user         = "myuser"
realname     = "My Name"
channels     = ["#go", "#linux"]
auto_connect = true

[bouncer.history]
backend = "memory"
limit   = 500
```

### Quick-start: ircx (XDCC downloader)

```bash
# Download pack #42 from a bot
./ircx -server irc.rizon.net:6667 -nick mynick -channel "#channel" \
       -bot "XDCC_Bot" -pack 42 -dest ./downloads

# List all available packs from a bot
./ircx -server irc.rizon.net:6667 -nick mynick \
       -bot "XDCC_Bot" -list
```

## Library usage

### Parsing IRC messages

```go
import "github.com/natalie-o-perret/go-irc/irc"

msg, err := irc.Parse(":nick!user@host PRIVMSG #go :Hello world")
fmt.Println(msg.Command) // PRIVMSG
fmt.Println(msg.Prefix.Nick) // nick
fmt.Println(msg.Trailing()) // Hello world
fmt.Println(irc.Format(msg)) // back to wire format
```

### Connecting a client

```go
import (
"github.com/natalie-o-perret/go-irc/client"
"github.com/natalie-o-perret/go-irc/client/sasl"
"github.com/natalie-o-perret/go-irc/irc"
)

c := client.New(client.Config{
Addr:     "irc.libera.chat:6667",
Nick:     "mynick",
User:     "mynick",
RealName: "My Name",
SASL:     &sasl.Plain{Username: "mynick", Password: "hunter2"},
})

c.On("001", func (cl *client.Client, msg *irc.Message) {
cl.Sendf(irc.JOIN, "#go")
})

c.On(irc.PRIVMSG, func (cl *client.Client, msg *irc.Message) {
fmt.Printf("<%s> %s\n", msg.Prefix.Nick, msg.Trailing())
})

if err := c.Connect(); err != nil {
log.Fatal(err)
}
```

### XDCC download

```go
import (
"github.com/natalie-o-perret/go-irc/client"
"github.com/natalie-o-perret/go-irc/client/dcc"
"github.com/natalie-o-perret/go-irc/irc"
)

manager := dcc.NewManager()

c := client.New(client.Config{Addr: "irc.rizon.net:6667", Nick: "mynick"})

c.On("001", func (cl *client.Client, _ *irc.Message) {
cl.Sendf(irc.PRIVMSG, "XDCC_Bot", "XDCC SEND #1")
})

c.On(irc.PRIVMSG, func (cl *client.Client, msg *irc.Message) {
cmd, args, ok := dcc.DecodeCTCP(msg.Trailing())
if !ok || cmd != "DCC" {
return
}
req, _ := dcc.ParseSendRequest(args[5:]) // strip "SEND "
sess, _ := manager.Receive(req, "./downloads")
go func () {
if err := sess.Wait(); err != nil {
log.Println("transfer error:", err)
}
fmt.Println("done:", sess.Filename)
}()
})
```

### Embedding the server

```go
import "github.com/natalie-o-perret/go-irc/server"

srv := server.New(server.Config{
Name:    "irc.local",
Network: "LocalNet",
Listen:  ":6667",
MOTD:    "Hello!",
})
log.Fatal(srv.ListenAndServe())
```

## Architecture

```
go-irc/
├── irc/                    # Pure protocol: message parsing, numerics, caps, modes
├── internal/ringbuf/       # Generic thread-safe ring buffer
├── client/                 # IRC client library
│   ├── sasl/               #   PLAIN, EXTERNAL, SCRAM-SHA-256/512
│   ├── dcc/                #   DCC SEND/RECV with resume + passive DCC
│   └── xdcc/               #   XDCC pack lists, downloading, bot serving
├── server/                 # IRC server (ircd)
│   └── mode/               #   Channel / user mode sets
├── bouncer/                # IRC bouncer
│   └── history/            #   History storage (memory ring buffer)
├── config/                 # TOML config loader
└── cmd/
    ├── ircd/               # IRC server binary
    ├── ircb/               # IRC bouncer binary
    └── ircx/               # XDCC CLI downloader
```

### Bouncer internals

```
                ┌─────────────────────────────┐
                │          Bouncer             │
                │                             │
  IRC client -->│  DownstreamSession          │
  IRC client -->│  DownstreamSession   ------->  UpstreamSession --> libera.chat
  IRC client -->│  DownstreamSession          │    (client.Client)
                │                             │
                │  history.Store (memory)     │
                └─────────────────────────────┘
```

- Each downstream authenticates with `PASS user/network:password`
- The bouncer keeps the upstream connection alive even when all downstreams disconnect
- New downstreams receive an instant channel state replay (JOIN + NAMES + TOPIC) plus the last 50 messages per channel
- IRCv3 `server-time` tags are added to replayed messages

## Dependencies

| Package                      | Purpose                                           |
|------------------------------|---------------------------------------------------|
| `github.com/BurntSushi/toml` | TOML config parsing                               |
| `golang.org/x/crypto`        | bcrypt (oper passwords) + PBKDF2 (SCRAM SASL)     |
| `modernc.org/sqlite`         | Optional SQLite history backend (pure Go, no CGo) |

## License

MIT - see [LICENSE](LICENSE).
