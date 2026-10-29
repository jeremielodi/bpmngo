// Package ids owns BpmnGo's global and operational identifier domains.
package ids

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

type EngineKey = int64
type GlobalID = uuid.UUID

const NilEngineKey EngineKey = 0

func NewGlobalID() GlobalID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Errorf("generate UUIDv7: %w", err))
	}
	return id
}

const (
	sequenceBits = 12
	nodeBits     = 10
	maxSequence  = int64(1<<sequenceBits - 1)
	maxNode      = int64(1<<nodeBits - 1)
	timeShift    = sequenceBits + nodeBits
)

var engineEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Generator creates positive, time-ordered 63-bit keys without a database
// round trip: 41 timestamp bits, 10 node bits and 12 sequence bits.
type Generator struct {
	mu       sync.Mutex
	node     int64
	lastMS   int64
	sequence int64
	now      func() time.Time
}

func NewGenerator(node int64) (*Generator, error) {
	if node < 0 || node > maxNode {
		return nil, fmt.Errorf("ENGINE_NODE_ID must be between 0 and %d", maxNode)
	}
	return &Generator{node: node, lastMS: -1, now: time.Now}, nil
}

func (g *Generator) Next() EngineKey {
	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		nowMS := g.now().UTC().Sub(engineEpoch).Milliseconds()
		if nowMS < 0 {
			panic("system clock is before the BpmnGo engine-key epoch")
		}
		if nowMS < g.lastMS {
			nowMS = g.lastMS
		}
		if nowMS == g.lastMS {
			if g.sequence < maxSequence {
				g.sequence++
				return (nowMS << timeShift) | (g.node << sequenceBits) | g.sequence
			}
			time.Sleep(time.Millisecond)
			continue
		}
		g.lastMS = nowMS
		g.sequence = 0
		return (nowMS << timeShift) | (g.node << sequenceBits)
	}
}

var (
	defaultOnce sync.Once
	defaultGen  *Generator
)

func NewEngineKey() EngineKey {
	defaultOnce.Do(func() {
		node := int64(0)
		if raw := os.Getenv("ENGINE_NODE_ID"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				panic(fmt.Errorf("parse ENGINE_NODE_ID: %w", err))
			}
			node = parsed
		}
		var err error
		defaultGen, err = NewGenerator(node)
		if err != nil {
			panic(err)
		}
	})
	return defaultGen.Next()
}
