# go-irc

A high-quality, feature-complete IRC implementation in Go.

> Full IRC protocol client & server library — IRC bouncer — XDCC file transfers
> — IRCv3 — pure Go — no CGo.

---

## Features

| Layer | What you get |
|-------|-------------|
| **`irc/`** | RFC 1459 + RFC 2812 message parsing/formatting, IRCv3 message tags, mode change parsing |
| **`client/`** | Full IRC client with IRCv3 CAP negotiation, SASL (PLAIN / EXTERNAL / SCRAM-SHA-256 / SCRAM-SHA-512) |
| **`client/dcc/`** | DCC SEND / RECV with 4-byte ACKs, resume, passive DCC (port 0 + token) |
| **`client/xdcc/`** | XDCC pack list parsing (iroffer/SysReset formats), `XDCC SEND #n`, bot pack serving |
| **`client/sasl/`** | SASL PLAIN, EXTERNAL, SCRAM-SHA-256, SCRAM-SHA-512 |
| **`server/`** | Full ircd: channel modes, user modes, WHOIS/WHO/WHOWAS, OPER/KILL, WALLOPS, IRCv3 |
| **`server/mode/`** | Channel and user mode set management with list modes (+b/+e/+I) |
| **`bouncer/`** | Multi-upstream multi-downstream IRC bouncer with history replay and cap bridging |
| **`bouncer/history/`** | In-memory (ring-buffer) history store, queryable by time window |
| **`config/`** | TOML config loading with validation and defaults |
| **`internal/ringbuf/`** | Generic thread-safe ring buffer |

### IRCv3 capabilities supported

`server-time` · `message-tags` · `batch` · `labeled-response` · `echo-message` ·
`multi-prefix` · `away-notify` · `extended-join` · `chghost` · `setname` ·
`account-tag` · `cap-notify` · `userhost-in-names` · `invite-notify` ·
`account-notify` · `sasl`

---

## Binaries

| Binary | Description |
|--------|-------------|
| `cmd/ircd` | Standalone IRC server |
| `cmd/ircb` | IRC bouncer (ZNC/soju-style) |
| `cmd/ircx` | XDCC downloader CLI |

### Build

```bash
go build ./cmd/ircd   # → ./ircd
go build ./cmd/ircb   # → ./ircb
go build ./cmd/ircx   # → ./ircx
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

---

## Library usage

### Parsing IRC messages

```go
import "github.com/natalie-o-perret/go-irc/irc"

msg, err := irc.Parse(":nick!user@host PRIVMSG #go :Hello world")
fmt.Println(msg.Command)          // PRIVMSG
fmt.Println(msg.Prefix.Nick)      // nick
fmt.Println(msg.Trailing())       // Hello world
fmt.Println(irc.Format(msg))      // back to wire format
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

c.On("001", func(cl *client.Client, msg *irc.Message) {
    cl.Sendf(irc.JOIN, "#go")
})

c.On(irc.PRIVMSG, func(cl *client.Client, msg *irc.Message) {
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

c.On("001", func(cl *client.Client, _ *irc.Message) {
    cl.Sendf(irc.PRIVMSG, "XDCC_Bot", "XDCC SEND #1")
})

c.On(irc.PRIVMSG, func(cl *client.Client, msg *irc.Message) {
    cmd, args, ok := dcc.DecodeCTCP(msg.Trailing())
    if !ok || cmd != "DCC" {
        return
    }
    req, _ := dcc.ParseSendRequest(args[5:]) // strip "SEND "
    sess, _ := manager.Receive(req, "./downloads")
    go func() {
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

---

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
  IRC client ──▶│  DownstreamSession          │
  IRC client ──▶│  DownstreamSession   ──────▶│  UpstreamSession ──▶ libera.chat
  IRC client ──▶│  DownstreamSession          │    (client.Client)
                │                             │
                │  history.Store (memory)     │
                └─────────────────────────────┘
```

- Each downstream authenticates with `PASS user/network:password`
- The bouncer keeps the upstream connection alive even when all downstreams disconnect
- New downstreams receive an instant channel state replay (JOIN + NAMES + TOPIC) plus the last 50 messages per channel
- IRCv3 `server-time` tags are added to replayed messages

---

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/BurntSushi/toml` | TOML config parsing |
| `golang.org/x/crypto` | bcrypt (oper passwords) + PBKDF2 (SCRAM SASL) |
| `modernc.org/sqlite` | Optional SQLite history backend (pure Go, no CGo) |

---

## License

MIT — see [LICENSE](LICENSE).

