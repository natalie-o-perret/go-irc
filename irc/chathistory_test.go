package irc_test

import (
	"slices"
	"testing"
	"time"

	"github.com/natalie-o-perret/go-irc/irc"
)

func TestSelectChatHistory(t *testing.T) {
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	var entries []irc.ChatHistoryEntry
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		entries = append(entries, irc.ChatHistoryEntry{
			Time: base.Add(time.Duration(i) * time.Second),
			Msg:  &irc.Message{Tags: irc.Tags{"msgid": id}},
		})
	}

	tests := []struct {
		name      string
		command   string
		selectors []string
		limit     int
		want      []string
	}{
		{name: "latest", command: "LATEST", selectors: []string{"*"}, limit: 2, want: []string{"d", "e"}},
		{name: "before", command: "BEFORE", selectors: []string{"msgid=d"}, limit: 2, want: []string{"b", "c"}},
		{name: "after", command: "AFTER", selectors: []string{"msgid=b"}, limit: 2, want: []string{"c", "d"}},
		{name: "around", command: "AROUND", selectors: []string{"msgid=c"}, limit: 4, want: []string{"a", "b", "d", "e"}},
		{name: "between", command: "BETWEEN", selectors: []string{"msgid=a", "msgid=e"}, limit: 5, want: []string{"b", "c", "d"}},
		{name: "between reverse", command: "BETWEEN", selectors: []string{"msgid=e", "msgid=a"}, limit: 2, want: []string{"c", "d"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := irc.SelectChatHistory(entries, tt.command, tt.selectors, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, len(got))
			for i, entry := range got {
				ids[i] = entry.Msg.Tags["msgid"]
			}
			if !slices.Equal(ids, tt.want) {
				t.Fatalf("got %v want %v", ids, tt.want)
			}
		})
	}
}
