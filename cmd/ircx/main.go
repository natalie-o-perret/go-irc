// Command ircx is an XDCC file downloader CLI.
//
// Usage:
//
//	ircx -server irc.rizon.net:6667 -nick mynick -channel "#channelname" -bot "XDCC_Bot" -pack 42 -dest ./downloads
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/natalie-o-perret/go-irc/client"
	"github.com/natalie-o-perret/go-irc/client/dcc"
	"github.com/natalie-o-perret/go-irc/client/xdcc"
	"github.com/natalie-o-perret/go-irc/irc"
)

// Build-time variables injected by goreleaser / go build -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	server := flag.String("server", "", "IRC server address (host:port)")
	nick := flag.String("nick", "go-ircx", "Nick to use")
	channel := flag.String("channel", "", "Channel to join (optional)")
	bot := flag.String("bot", "", "XDCC bot nick")
	pack := flag.Int("pack", 0, "Pack number to request")
	dest := flag.String("dest", ".", "Download destination directory")
	list := flag.Bool("list", false, "Request XDCC LIST instead of downloading")
	showVer := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Printf("ircx %s (%s) built %s\n", version, commit, date)
		return
	}

	if *server == "" {
		fmt.Fprintln(os.Stderr, "ircx: -server required")
		os.Exit(1)
	}
	if *bot == "" && !*list {
		fmt.Fprintln(os.Stderr, "ircx: -bot required")
		os.Exit(1)
	}

	manager := dcc.NewManager()
	var activeSess *dcc.Session

	c := client.New(client.Config{
		Addr:     *server,
		Nick:     *nick,
		User:     "ircx",
		RealName: "go-irc XDCC client",
	})

	// After welcome, join channel and request pack
	c.On("001", func(cl *client.Client, msg *irc.Message) {
		if *channel != "" {
			cl.Sendf(irc.JOIN, *channel)
		}
		// Wait a moment for joins to settle
		time.AfterFunc(2*time.Second, func() {
			if *list {
				slog.Info("requesting XDCC list", "bot", *bot)
				cl.Sendf(irc.PRIVMSG, *bot, "\x01DCC LIST\x01")
			} else if *pack > 0 {
				slog.Info("requesting XDCC pack", "bot", *bot, "pack", *pack)
				cl.Sendf(irc.PRIVMSG, *bot, fmt.Sprintf("XDCC SEND #%d", *pack))
			}
		})
	})

	// Handle XDCC LIST reply (NOTICEs from the bot)
	c.On(irc.NOTICE, func(cl *client.Client, msg *irc.Message) {
		if *list && msg.Prefix != nil && msg.Prefix.Nick == *bot {
			text := msg.Trailing()
			p := xdcc.ParseListNotice(text)
			if p != nil {
				fmt.Printf("#%-4d  %-40s  %s\n", p.Number, p.Filename, xdcc.FormatPackSize(p.Size))
			}
		}
	})

	// Handle DCC SEND offers
	c.On(irc.PRIVMSG, func(cl *client.Client, msg *irc.Message) {
		if msg.Prefix == nil {
			return
		}
		text := msg.Trailing()
		cmd, args, ok := dcc.DecodeCTCP(text)
		if !ok || cmd != "DCC" {
			return
		}
		parts := splitWords(args)
		if len(parts) < 1 {
			return
		}
		if parts[0] != "SEND" {
			return
		}
		req, err := dcc.ParseSendRequest(args[len("SEND "):])
		if err != nil {
			slog.Error("DCC SEND parse error", "err", err)
			return
		}

		slog.Info("DCC SEND offer", "filename", req.Filename, "size", req.Size, "ip", req.IP, "port", req.Port)

		sess, err := manager.Receive(req, *dest)
		if err != nil {
			slog.Error("DCC receive error", "err", err)
			return
		}
		activeSess = sess

		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for range ticker.C {
				transferred, total := sess.Progress()
				state := dcc.TransferState(sess.State.Load())
				if total > 0 {
					pct := float64(transferred) * 100 / float64(total)
					fmt.Printf("\r  %s: %.1f%% (%d / %d bytes)    ", sess.Filename, pct, transferred, total)
				}
				if state == dcc.StateCompleted || state == dcc.StateFailed || state == dcc.StateCancelled {
					fmt.Println()
					break
				}
			}
		}()

		if err := sess.Wait(); err != nil {
			slog.Error("transfer failed", "err", err)
		} else {
			slog.Info("transfer complete", "filename", sess.Filename, "dest", *dest)
		}

		cl.Disconnect("thanks!")
	})

	_ = activeSess

	slog.Info("connecting", "server", *server, "nick", *nick)
	if err := c.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "ircx: %v\n", err)
		os.Exit(1)
	}
}

func splitWords(s string) []string {
	var words []string
	inQuote := false
	start := 0
	for i, ch := range s {
		switch {
		case ch == '"':
			inQuote = !inQuote
		case ch == ' ' && !inQuote:
			if i > start {
				words = append(words, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		words = append(words, s[start:])
	}
	return words
}
