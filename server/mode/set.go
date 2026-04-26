// Package mode provides IRC channel and user mode management.
package mode

import (
	"strings"
	"sync"

	"github.com/natalie-o-perret/go-irc/irc"
)

// Set stores the current mode state for a channel or user.
type Set struct {
	mu    sync.RWMutex
	flags map[rune]bool
	args  map[rune]string   // modes with a single argument (e.g. +k, +l)
	lists map[rune][]string // list modes (e.g. +b ban list)
}

// New creates an empty mode Set.
func New() *Set {
	return &Set{
		flags: make(map[rune]bool),
		args:  make(map[rune]string),
		lists: make(map[rune][]string),
	}
}

// Clone returns a deep copy of the Set.
func (s *Set) Clone() *Set {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := New()
	for k, v := range s.flags {
		out.flags[k] = v
	}
	for k, v := range s.args {
		out.args[k] = v
	}
	for k, v := range s.lists {
		cp := make([]string, len(v))
		copy(cp, v)
		out.lists[k] = cp
	}
	return out
}

// Has returns true if a simple flag mode is set.
func (s *Set) Has(m rune) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.flags[m]
}

// Arg returns the argument for an argument-bearing mode.
func (s *Set) Arg(m rune) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.args[m]
	return v, ok
}

// List returns entries in a list mode.
func (s *Set) List(m rune) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.lists[m]...)
}

// String returns the mode string (e.g. "+imn+k secret").
func (s *Set) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sb strings.Builder
	var args []string
	sb.WriteByte('+')
	for m := range s.flags {
		sb.WriteRune(m)
	}
	for m, v := range s.args {
		sb.WriteRune(m)
		args = append(args, v)
	}
	if len(args) > 0 {
		sb.WriteByte(' ')
		sb.WriteString(strings.Join(args, " "))
	}
	return sb.String()
}

// ModeValidator validates mode arguments and classifies modes by type.
type ModeValidator interface {
	// Type returns the mode type for the given mode rune.
	Type(m rune) ModeType
	// ValidateArg validates the argument for a mode and returns the
	// canonical form, or an error string if invalid.
	ValidateArg(m rune, add bool, arg string) (string, error)
}

// ModeType classifies how a mode consumes parameters.
type ModeType int

const (
	// TypeFlag has no parameter (e.g. +m, +n, +i, +s, +p, +t).
	TypeFlag ModeType = iota
	// TypeArgAlways always takes a parameter when set or unset (e.g. +o, +v, +b).
	TypeArgAlways
	// TypeArgSet takes a parameter only when setting (e.g. +l, +k).
	TypeArgSet
	// TypeArgUnset takes a parameter only when unsetting (rare).
	TypeArgUnset
	// TypeList is a list mode (e.g. +b ban list, +e except, +I invite).
	TypeList
	// TypeUnknown is returned for unrecognised mode characters.
	TypeUnknown
)

// Apply applies a slice of irc.ModeChanges to the Set using a ModeValidator.
// Returns the list of changes actually applied.
func (s *Set) Apply(changes []irc.ModeChange, v ModeValidator) []irc.ModeChange {
	s.mu.Lock()
	defer s.mu.Unlock()

	var applied []irc.ModeChange
	for _, mc := range changes {
		typ := v.Type(mc.Mode)
		switch typ {
		case TypeFlag:
			if mc.Add {
				if !s.flags[mc.Mode] {
					s.flags[mc.Mode] = true
					applied = append(applied, mc)
				}
			} else {
				if s.flags[mc.Mode] {
					delete(s.flags, mc.Mode)
					applied = append(applied, mc)
				}
			}

		case TypeArgAlways, TypeArgSet:
			if mc.Add {
				canonical, err := v.ValidateArg(mc.Mode, true, mc.Arg)
				if err != nil {
					continue
				}
				s.args[mc.Mode] = canonical
				applied = append(applied, irc.ModeChange{Add: true, Mode: mc.Mode, Arg: canonical})
			} else {
				if _, ok := s.args[mc.Mode]; ok {
					delete(s.args, mc.Mode)
					applied = append(applied, mc)
				}
			}

		case TypeList:
			if mc.Add {
				canonical, err := v.ValidateArg(mc.Mode, true, mc.Arg)
				if err != nil {
					continue
				}
				// avoid duplicates
				found := false
				for _, e := range s.lists[mc.Mode] {
					if e == canonical {
						found = true
						break
					}
				}
				if !found {
					s.lists[mc.Mode] = append(s.lists[mc.Mode], canonical)
					applied = append(applied, irc.ModeChange{Add: true, Mode: mc.Mode, Arg: canonical})
				}
			} else {
				list := s.lists[mc.Mode]
				for i, e := range list {
					if e == mc.Arg {
						s.lists[mc.Mode] = append(list[:i], list[i+1:]...)
						applied = append(applied, mc)
						break
					}
				}
			}
		}
	}
	return applied
}

// ---------------------------------------------------------------------------
// Default validators
// ---------------------------------------------------------------------------

// ChannelValidator implements ModeValidator for standard channel modes.
type ChannelValidator struct{}

func (ChannelValidator) Type(m rune) ModeType {
	switch m {
	case 'i', 'm', 'n', 's', 'p', 't', 'r', 'z', 'c', 'C', 'S', 'G', 'N', 'u', 'f', 'j', 'Q', 'K', 'L', 'P', 'O', 'A', 'R', 'T':
		return TypeFlag
	case 'k':
		return TypeArgSet
	case 'l':
		return TypeArgSet
	case 'o', 'h', 'v', 'a', 'q':
		return TypeArgAlways
	case 'b', 'e', 'I':
		return TypeList
	}
	return TypeUnknown
}

func (ChannelValidator) ValidateArg(_ rune, _ bool, arg string) (string, error) {
	return arg, nil
}

// UserValidator implements ModeValidator for user modes.
type UserValidator struct{}

func (UserValidator) Type(m rune) ModeType {
	switch m {
	case 'i', 'o', 'w', 'r', 'x', 'z', 'W', 'g', 'B':
		return TypeFlag
	}
	return TypeUnknown
}

func (UserValidator) ValidateArg(_ rune, _ bool, arg string) (string, error) {
	return arg, nil
}

