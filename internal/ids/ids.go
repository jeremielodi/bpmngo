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

// ConfiguredNode returns ENGINE_NODE_ID, and false when it is not set.
func ConfiguredNode() (int64, bool, error) {
	raw := os.Getenv("ENGINE_NODE_ID")
	if raw == "" {
		return 0, false, nil
	}
	node, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse ENGINE_NODE_ID: %w", err)
	}
	if node < 0 || node > maxNode {
		return 0, false, fmt.Errorf("ENGINE_NODE_ID must be between 0 and %d", maxNode)
	}
	return node, true, nil
}

// Node is the node number of this process's keys. Call it only once the
// node is settled (after SetNode, when the boot claims one).
func Node() int64 {
	ensureGenerator()
	return defaultGen.node
}

// ensureGenerator makes sure the process's generator exists.
func ensureGenerator() {
	defaultOnce.Do(func() {
		node, _, err := ConfiguredNode()
		if err != nil {
			panic(err)
		}
		defaultGen, err = NewGenerator(node)
		if err != nil {
			panic(err)
		}
	})
}

// MaxNode is the highest node number a key can carry.
func MaxNode() int64 { return maxNode }

// SetNode fixes the node number of this process's keys, claimed at boot
// when ENGINE_NODE_ID is not set: instances sharing a database must never
// share a node, or two of them would issue the same keys in the same
// millisecond. It must run before the first key is issued.
func SetNode(node int64) error {
	if node < 0 || node > maxNode {
		return fmt.Errorf("engine node %d out of range 0..%d", node, maxNode)
	}
	issued := true
	defaultOnce.Do(func() { issued = false })
	if issued && defaultGen != nil {
		return fmt.Errorf("engine node set after keys were issued")
	}
	defaultGen, _ = NewGenerator(node)
	return nil
}

func NewEngineKey() EngineKey {
	ensureGenerator()
	return defaultGen.Next()
}
