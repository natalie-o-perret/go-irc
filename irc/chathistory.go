package irc

import (
	"fmt"
	"strings"
	"time"
)

const ServerTimeLayout = "2006-01-02T15:04:05.000Z"

// ChatHistoryEntry is a message available for history selection.
type ChatHistoryEntry struct {
	Time time.Time
	Msg  *Message
}

type messageReference struct {
	time  time.Time
	msgid string
	index int
	found bool
}

// SelectChatHistory applies an IRCv3 history subcommand to ascending entries.
func SelectChatHistory(entries []ChatHistoryEntry, subcommand string, selectors []string, limit int) ([]ChatHistoryEntry, bool, error) {
	if limit <= 0 {
		return nil, false, fmt.Errorf("invalid limit")
	}
	subcommand = strings.ToUpper(subcommand)
	if len(selectors) != 1 && (subcommand != "BETWEEN" || len(selectors) != 2) {
		return nil, false, fmt.Errorf("invalid selectors")
	}
	if subcommand == "LATEST" && selectors[0] == "*" {
		return latest(entries, limit)
	}
	if selectors[0] == "*" {
		return nil, false, fmt.Errorf("invalid selector")
	}
	first, err := parseMessageReference(entries, selectors[0])
	if err != nil {
		return nil, false, err
	}
	if !first.found {
		return nil, true, nil
	}

	switch subcommand {
	case "LATEST", "AFTER":
		matches := filterHistory(entries, func(entry ChatHistoryEntry, index int) bool { return afterReference(entry, index, first) })
		if subcommand == "LATEST" {
			return latest(matches, limit)
		}
		return earliest(matches, limit)
	case "BEFORE":
		return latest(filterHistory(entries, func(entry ChatHistoryEntry, index int) bool { return beforeReference(entry, index, first) }), limit)
	case "AROUND":
		before := filterHistory(entries, func(entry ChatHistoryEntry, index int) bool { return beforeReference(entry, index, first) })
		after := filterHistory(entries, func(entry ChatHistoryEntry, index int) bool { return afterReference(entry, index, first) })
		left := min(len(before), limit/2)
		right := min(len(after), limit-left)
		left = min(len(before), limit-right)
		result := append([]ChatHistoryEntry(nil), before[len(before)-left:]...)
		result = append(result, after[:right]...)
		return result, len(before)+len(after) <= limit, nil
	case "BETWEEN":
		second, err := parseMessageReference(entries, selectors[1])
		if err != nil {
			return nil, false, err
		}
		if !second.found {
			return nil, true, nil
		}
		forward := first.time.Before(second.time) || (first.time.Equal(second.time) && first.msgid != "" && second.msgid != "" && first.index < second.index)
		if forward {
			matches := filterHistory(entries, func(entry ChatHistoryEntry, index int) bool {
				return afterReference(entry, index, first) && beforeReference(entry, index, second)
			})
			return earliest(matches, limit)
		}
		matches := filterHistory(entries, func(entry ChatHistoryEntry, index int) bool {
			return beforeReference(entry, index, first) && afterReference(entry, index, second)
		})
		return latest(matches, limit)
	default:
		return nil, false, fmt.Errorf("unknown subcommand")
	}
}

func parseMessageReference(entries []ChatHistoryEntry, selector string) (messageReference, error) {
	if strings.HasPrefix(selector, "timestamp=") {
		value := strings.TrimPrefix(selector, "timestamp=")
		t, err := time.Parse(ServerTimeLayout, value)
		if err != nil {
			return messageReference{}, fmt.Errorf("invalid timestamp")
		}
		return messageReference{time: t, found: true}, nil
	}
	if strings.HasPrefix(selector, "msgid=") {
		id := strings.TrimPrefix(selector, "msgid=")
		if id == "" {
			return messageReference{}, fmt.Errorf("invalid message ID")
		}
		for i, entry := range entries {
			if entry.Msg.Tags["msgid"] == id {
				return messageReference{time: entry.Time, msgid: id, index: i, found: true}, nil
			}
		}
		return messageReference{}, nil
	}
	return messageReference{}, fmt.Errorf("unsupported message reference")
}

func beforeReference(entry ChatHistoryEntry, index int, ref messageReference) bool {
	if ref.msgid != "" {
		return index < ref.index
	}
	return entry.Time.Before(ref.time)
}

func afterReference(entry ChatHistoryEntry, index int, ref messageReference) bool {
	if ref.msgid != "" {
		return index > ref.index
	}
	return entry.Time.After(ref.time)
}

func filterHistory(entries []ChatHistoryEntry, keep func(ChatHistoryEntry, int) bool) []ChatHistoryEntry {
	result := make([]ChatHistoryEntry, 0, len(entries))
	for i, entry := range entries {
		if keep(entry, i) {
			result = append(result, entry)
		}
	}
	return result
}

func latest(entries []ChatHistoryEntry, limit int) ([]ChatHistoryEntry, bool, error) {
	complete := len(entries) <= limit
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, complete, nil
}

func earliest(entries []ChatHistoryEntry, limit int) ([]ChatHistoryEntry, bool, error) {
	complete := len(entries) <= limit
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, complete, nil
}
