package irc

import (
	"strings"
)

// ModeChange represents a single mode application (+/-<char> [arg]).
type ModeChange struct {
	Add  bool
	Mode rune
	Arg  string
}

// ParseModeString parses a mode string and its arguments into a slice of
// ModeChanges.  args is the list of parameters that follow the mode string.
// The function consumes args left-to-right for modes that require arguments.
//
// Channel modes that take arguments when set (+k, +l, +o, +h, +v, +b, +e, +I):
// handled by the caller via a ModeValidator — this function just yields the
// raw changes.
func ParseModeString(modes string, args []string) []ModeChange {
	var changes []ModeChange
	add := true
	argIdx := 0

	for _, ch := range modes {
		switch ch {
		case '+':
			add = true
		case '-':
			add = false
		default:
			mc := ModeChange{Add: add, Mode: ch}
			if argIdx < len(args) && modeNeedsArg(ch, add) {
				mc.Arg = args[argIdx]
				argIdx++
			}
			changes = append(changes, mc)
		}
	}
	return changes
}

// modeNeedsArg returns true for modes that consume a parameter.
// This is a conservative default; servers override via ModeValidator.
func modeNeedsArg(m rune, add bool) bool {
	switch m {
	case 'o', 'O', 'h', 'v', 'b', 'e', 'I', 'q', 'a', 'k':
		return true
	case 'l':
		return add
	}
	return false
}

// FormatModeChanges serialises a slice of ModeChanges back into a mode string
// and associated arguments, merging consecutive same-direction changes.
func FormatModeChanges(changes []ModeChange) (modeStr string, args []string) {
	if len(changes) == 0 {
		return "", nil
	}

	var sb strings.Builder
	add := changes[0].Add
	if add {
		sb.WriteByte('+')
	} else {
		sb.WriteByte('-')
	}

	for _, mc := range changes {
		if mc.Add != add {
			add = mc.Add
			if add {
				sb.WriteByte('+')
			} else {
				sb.WriteByte('-')
			}
		}
		sb.WriteRune(mc.Mode)
		if mc.Arg != "" {
			args = append(args, mc.Arg)
		}
	}
	return sb.String(), args
}
