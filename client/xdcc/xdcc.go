// Package xdcc provides XDCC file transfer support (pack lists, requesting,
// and serving packs) on top of the DCC package.
package xdcc

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/natalie-o-perret/go-irc/client/dcc"
)

// Pack represents a single XDCC pack.
type Pack struct {
	Number      int
	Filename    string
	Size        int64  // bytes; -1 if unknown
	Gets        int    // total downloads
	CRC32       uint32 // optional checksum
	Description string // optional human-readable description
}

// PackList is a list of XDCC packs.
type PackList []Pack

// ByNumber returns the Pack with the given number, if it exists.
func (pl PackList) ByNumber(n int) (Pack, bool) {
	for _, p := range pl {
		if p.Number == n {
			return p, true
		}
	}
	return Pack{}, false
}

// Common XDCC LIST line patterns emitted by popular bots.
var (
	// SysReset/iroffer-ng: #1  10x  [12.3M]  filename.mkv
	reIroffer = regexp.MustCompile(`#(\d+)\s+(\d+)x\s+\[([^\]]+)\]\s+(.+)`)
)

// ParseListNotice attempts to parse a single XDCC LIST NOTICE line.
// Returns nil if the line is not a recognised pack line.
func ParseListNotice(line string) *Pack {
	line = strings.TrimSpace(line)

	if m := reIroffer.FindStringSubmatch(line); m != nil {
		num, _ := strconv.Atoi(m[1])
		gets, _ := strconv.Atoi(m[2])
		size := parseSize(m[3])
		return &Pack{
			Number:   num,
			Gets:     gets,
			Size:     size,
			Filename: strings.TrimSpace(m[4]),
		}
	}

	return nil
}

func parseSize(s string) int64 {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "G"):
		mult = 1 << 30
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "M"):
		mult = 1 << 20
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "K"):
		mult = 1 << 10
		s = s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return -1
	}
	return int64(f * float64(mult))
}

// ---------------------------------------------------------------------------
// Bot — serves packs via XDCC
// ---------------------------------------------------------------------------

// SlotLimit is the default maximum concurrent transfers per bot.
const SlotLimit = 5

// Bot manages an XDCC pack list and handles incoming XDCC requests.
type Bot struct {
	mu       sync.Mutex
	packs    PackList
	manager  *dcc.Manager
	maxSlots int
	// active: key is pack number, value slice is session IDs for that pack
	active map[int][]uint32
	// listenAddr is passed to dcc.Manager.Send
	listenAddr string
}

// NewBot creates a new XDCC bot.
func NewBot(manager *dcc.Manager, listenAddr string, maxSlots int) *Bot {
	if maxSlots <= 0 {
		maxSlots = SlotLimit
	}
	return &Bot{
		manager:    manager,
		maxSlots:   maxSlots,
		active:     make(map[int][]uint32),
		listenAddr: listenAddr,
	}
}

// AddPack adds a pack to the bot's list.
func (b *Bot) AddPack(p Pack) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.packs = append(b.packs, p)
}

// RemovePack removes a pack from the list by number.
func (b *Bot) RemovePack(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, p := range b.packs {
		if p.Number == n {
			b.packs = append(b.packs[:i], b.packs[i+1:]...)
			return true
		}
	}
	return false
}

// Packs returns a copy of the current pack list.
func (b *Bot) Packs() PackList {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(PackList, len(b.packs))
	copy(out, b.packs)
	return out
}

// ListMessage returns the XDCC LIST output as a slice of NOTICE lines.
func (b *Bot) ListMessage() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := make([]string, 0, len(b.packs)+2)
	lines = append(lines, fmt.Sprintf("Packs: %d  Slots: %d/%d", len(b.packs), b.activeSlots(), b.maxSlots))
	for _, p := range b.packs {
		sizeStr := formatSize(p.Size)
		lines = append(lines, fmt.Sprintf("#%-4d %4dx  [%6s]  %s", p.Number, p.Gets, sizeStr, p.Filename))
	}
	return lines
}

// RequestPack initiates a DCC SEND for the given pack.  Returns the session
// and the CTCP body to send to the requesting nick.
func (b *Bot) RequestPack(packNum int) (*dcc.Session, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.activeSlots() >= b.maxSlots {
		return nil, "", fmt.Errorf("xdcc: no free slots (%d/%d)", b.activeSlots(), b.maxSlots)
	}

	pack, ok := b.packs.ByNumber(packNum)
	if !ok {
		return nil, "", fmt.Errorf("xdcc: pack #%d not found", packNum)
	}

	sess, ctcp, err := b.manager.Send(pack.Filename, b.listenAddr)
	if err != nil {
		return nil, "", fmt.Errorf("xdcc: start send: %w", err)
	}

	b.active[packNum] = append(b.active[packNum], sess.ID)

	// Track completion to clean up slot
	go func() {
		_ = sess.Wait()
		b.mu.Lock()
		ids := b.active[packNum]
		for i, id := range ids {
			if id == sess.ID {
				b.active[packNum] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}()

	return sess, ctcp, nil
}

func (b *Bot) activeSlots() int {
	total := 0
	for _, ids := range b.active {
		total += len(ids)
	}
	return total
}

// FormatPackSize returns a human-readable size string for a pack.
func FormatPackSize(size int64) string {
	return formatSize(size)
}

func formatSize(size int64) string {
	if size < 0 {
		return "???"
	}
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(size)/(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%dB", size)
	}
}
