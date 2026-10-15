// Package events turns raw output into typed, classified events.
package events

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// IDGen produces ULIDs: 48-bit millisecond time + 80 random bits, encoded in
// Crockford base32. IDs from one generator are strictly increasing, even
// within the same millisecond.
type IDGen struct {
	mu      sync.Mutex
	lastMS  uint64
	lastRnd [10]byte
}

// New returns a ULID for time t.
func (g *IDGen) New(t time.Time) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms := uint64(t.UnixMilli())
	if ms <= g.lastMS {
		// Same (or earlier) millisecond: increment the previous randomness
		// so ordering is preserved.
		ms = g.lastMS
		for i := len(g.lastRnd) - 1; i >= 0; i-- {
			g.lastRnd[i]++
			if g.lastRnd[i] != 0 {
				break
			}
		}
	} else {
		if _, err := rand.Read(g.lastRnd[:]); err != nil {
			panic("events: crypto/rand failed: " + err.Error())
		}
		g.lastMS = ms
	}
	var b [16]byte
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ms)
	copy(b[:6], ts[2:])
	copy(b[6:], g.lastRnd[:])
	return encode(b)
}

func encode(b [16]byte) string {
	// 128 bits → 26 base32 chars; the first char carries 3 bits.
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	var out [26]byte
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&0x1f]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
