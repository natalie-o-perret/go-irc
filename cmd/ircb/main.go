// Command ircb is an IRC bouncer that multiplexes upstream connections to
// multiple downstream clients with message history and playback.
//
// Usage:
//
//	ircb [-config path/to/config.toml]
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	bouncerpkg "github.com/natalie-o-perret/go-irc/bouncer"
	"github.com/natalie-o-perret/go-irc/config"
)

func main() {
	cfgPath := flag.String("config", "ircb.toml", "Path to TOML config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ircb: %v\n", err)
		os.Exit(1)
	}
	if cfg.Bouncer == nil {
		fmt.Fprintln(os.Stderr, "ircb: [bouncer] section missing from config")
		os.Exit(1)
	}

	bc := cfg.Bouncer

	users := make([]bouncerpkg.UserConfig, len(bc.Users))
	for i, u := range bc.Users {
		users[i] = bouncerpkg.UserConfig{Name: u.Name, Password: u.Password}
	}

	networks := make([]bouncerpkg.NetworkConfig, len(bc.Networks))
	for i, n := range bc.Networks {
		networks[i] = bouncerpkg.NetworkConfig{
			Name:        n.Name,
			Server:      n.Server,
			TLS:         n.TLS,
			Nick:        n.Nick,
			User:        n.User,
			RealName:    n.RealName,
			Password:    n.Password,
			SASLUser:    n.SASLUser,
			SASLPass:    n.SASLPass,
			Channels:    n.Channels,
			AutoConnect: n.AutoConnect,
		}
	}

	b := bouncerpkg.New(bouncerpkg.Config{
		Listen:   bc.Listen,
		Users:    users,
		Networks: networks,
		History: bouncerpkg.HistoryConfig{
			Backend: bc.History.Backend,
			Limit:   bc.History.Limit,
		},
	})

	slog.Info("starting ircb bouncer", "listen", bc.Listen)
	if err := b.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "ircb: %v\n", err)
		os.Exit(1)
	}
}

