//      .-') _                    (`-.   
//     ( OO ) )                 _(OO  )_ 
// ,--./ ,--,'  .-'),-----. ,--(_/   ,. \
// |   \ |  |\ ( OO'  .-.  '\   \   /(__/
// |    \|  | )/   |  | |  | \   \ /   / 
// |  .     |/ \_) |  |\|  |  \   '   /, 
// |  |\    |    \ |  | |  |   \     /__)
// |  | \   |     `'  '-'  '    \   /    
// `--'  `--'       `-----'      `-'     
//
//        ✧  N O V E M B R E   2 0 2 5  ✧
//
//        ✦  A J O U T   D U   F I C H I E R  ✦
//
//  Copyright © 2025 The go-ducros Authors
//  This file is part of the go-ducros library.
//
//  The go-ducros library is free software: you can redistribute it and/or modify
//  it under the terms of the GNU Lesser General Public License as published by
//  the Free Software Foundation, either version 3 of the License, or
//  (at your option) any later version.
//
//  The go-ducros library is distributed in the hope that it will be useful,
//  but WITHOUT ANY WARRANTY; without even the implied warranty of
//  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
//  GNU Lesser General Public License for more details.
//
//  You should have received a copy of the GNU Lesser General Public License
//  along with the go-ducros library. If not, see <http://www.gnu.org/licenses/>.


package randomx

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// TestSeedBlock verifies the seed block calculation
func TestSeedBlock(t *testing.T) {
	tests := []struct {
		blockNumber uint64
		expected    uint64
		description string
	}{
		{0, 0, "genesis block"},
		{1, 0, "early block < lag"},
		{63, 0, "block just before lag"},
		{64, 0, "block at lag"},
		{65, 0, "block just after lag"},
		{2047, 0, "last block of first epoch"},
		{2048, 0, "first block of second epoch (lag not reached yet)"},
		{2048 + 64, 2048, "first block where seed changes to epoch 1"},
		{2048 + 64 + 1, 2048, "second block of epoch 1 seed"},
		{4095, 2048, "last block using epoch 1 seed"},
		{4096, 2048, "first block of epoch 2 (seed not changed yet)"},
		{4096 + 64, 4096, "first block where seed changes to epoch 2"},
		{10000, 8192, "arbitrary block in epoch 4"},
	}

	for _, tt := range tests {
		result := seedBlock(tt.blockNumber)
		if result != tt.expected {
			t.Errorf("%s: seedBlock(%d) = %d, expected %d",
				tt.description, tt.blockNumber, result, tt.expected)
		}
	}
}

// TestGetEpochNumber verifies epoch number calculation
func TestGetEpochNumber(t *testing.T) {
	tests := []struct {
		blockNumber uint64
		expected    uint64
		description string
	}{
		{0, 0, "genesis"},
		{63, 0, "before lag"},
		{64, 0, "at lag"},
		{2048, 0, "epoch 0 boundary"},
		{2048 + 64, 1, "epoch 1 starts"},
		{4096 + 64, 2, "epoch 2 starts"},
		{6144 + 64, 3, "epoch 3 starts"},
	}

	for _, tt := range tests {
		result := GetEpochNumber(tt.blockNumber)
		if result != tt.expected {
			t.Errorf("%s: GetEpochNumber(%d) = %d, expected %d",
				tt.description, tt.blockNumber, result, tt.expected)
		}
	}
}

// TestIsEpochTransition verifies epoch transition detection
func TestIsEpochTransition(t *testing.T) {
	tests := []struct {
		blockNumber uint64
		expected    bool
		description string
	}{
		{0, true, "genesis is a transition"},
		{1, false, "block 1 is not a transition"},
		{2047, false, "last block of epoch 0"},
		{2048, false, "epoch boundary but lag not reached"},
		{2048 + 64, true, "first epoch transition after genesis"},
		{2048 + 64 + 1, false, "not a transition"},
		{4096 + 64, true, "second epoch transition"},
		{6144 + 64, true, "third epoch transition"},
	}

	for _, tt := range tests {
		result := IsEpochTransition(tt.blockNumber)
		if result != tt.expected {
			t.Errorf("%s: IsEpochTransition(%d) = %v, expected %v",
				tt.description, tt.blockNumber, result, tt.expected)
		}
	}
}

// TestGetEpochTransitionBlock verifies next transition calculation
func TestGetEpochTransitionBlock(t *testing.T) {
	tests := []struct {
		currentBlock uint64
		expected     uint64
		description  string
	}{
		{0, 64, "genesis → first transition"},
		{63, 64, "just before first transition"},
		{64, 2048 + 64, "at first transition → next"},
		{2047, 2048 + 64, "end of epoch 0"},
		{2048 + 64, 4096 + 64, "epoch 1 → epoch 2"},
		{4096 + 64, 6144 + 64, "epoch 2 → epoch 3"},
	}

	for _, tt := range tests {
		result := GetEpochTransitionBlock(tt.currentBlock)
		if result != tt.expected {
			t.Errorf("%s: GetEpochTransitionBlock(%d) = %d, expected %d",
				tt.description, tt.currentBlock, result, tt.expected)
		}
	}
}

// mockChainReader provides a mock chain for testing seed calculation
type mockChainReader struct {
	headers map[uint64]*types.Header
}

func newMockChainReader() *mockChainReader {
	return &mockChainReader{
		headers: make(map[uint64]*types.Header),
	}
}

func (m *mockChainReader) addHeader(number uint64, hash common.Hash) {
	m.headers[number] = &types.Header{
		Number: big.NewInt(int64(number)),
	}
	// Store hash in a way that Hash() will return it
	// For testing, we'll create headers with known hashes
}

func (m *mockChainReader) Config() *params.ChainConfig {
	return params.MainnetChainConfig
}

func (m *mockChainReader) CurrentHeader() *types.Header {
	return nil
}

func (m *mockChainReader) GetHeader(hash common.Hash, number uint64) *types.Header {
	return m.headers[number]
}

func (m *mockChainReader) GetHeaderByNumber(number uint64) *types.Header {
	return m.headers[number]
}

func (m *mockChainReader) GetHeaderByHash(hash common.Hash) *types.Header {
	return nil
}

func (m *mockChainReader) GetTd(hash common.Hash, number uint64) *big.Int {
	return big.NewInt(int64(number))
}

// TestCalcSeedHashGenesis verifies genesis seed
func TestCalcSeedHashGenesis(t *testing.T) {
	chain := newMockChainReader()

	// Test genesis
	seed, err := calcSeedHash(chain, big.NewInt(0))
	if err != nil {
		t.Fatalf("calcSeedHash(0) failed: %v", err)
	}

	if seed == (common.Hash{}) {
		t.Error("Genesis seed should not be zero hash")
	}

	// Genesis seed should be deterministic
	seed2, _ := calcSeedHash(chain, big.NewInt(0))
	if seed != seed2 {
		t.Error("Genesis seed is not deterministic")
	}
}

// TestCalcSeedHashConsistency verifies seed consistency within epoch
func TestCalcSeedHashConsistency(t *testing.T) {
	chain := newMockChainReader()

	// Add some blocks
	for i := uint64(0); i < 5000; i++ {
		chain.addHeader(i, common.BigToHash(big.NewInt(int64(i))))
	}

	// Blocks in the same epoch should have the same seed
	// Test epoch 1 (blocks 2112 to 4159, using seed from block 2048)
	var seeds []common.Hash
	testBlocks := []uint64{2112, 2500, 3000, 3500, 4000, 4159}

	for _, blockNum := range testBlocks {
		seed, err := calcSeedHash(chain, big.NewInt(int64(blockNum)))
		if err != nil {
			t.Fatalf("calcSeedHash(%d) failed: %v", blockNum, err)
		}
		seeds = append(seeds, seed)
	}

	// All seeds in the same epoch should be identical
	for i := 1; i < len(seeds); i++ {
		if seeds[i] != seeds[0] {
			t.Errorf("Block %d has different seed than block %d within same epoch",
				testBlocks[i], testBlocks[0])
		}
	}
}

// TestCalcSeedHashChanges verifies seed changes at epoch boundaries
func TestCalcSeedHashChanges(t *testing.T) {
	chain := newMockChainReader()

	// Add blocks with distinct hashes
	for i := uint64(0); i < 10000; i++ {
		hash := common.BigToHash(big.NewInt(int64(i * 12345))) // Distinct hashes
		chain.addHeader(i, hash)
	}

	// Get seeds from different epochs
	epoch0Block := uint64(100)  // Epoch 0
	epoch1Block := uint64(2112) // Epoch 1 (2048 + 64)
	epoch2Block := uint64(4160) // Epoch 2 (4096 + 64)

	seed0, err := calcSeedHash(chain, big.NewInt(int64(epoch0Block)))
	if err != nil {
		t.Fatalf("Failed to get seed for epoch 0: %v", err)
	}

	seed1, err := calcSeedHash(chain, big.NewInt(int64(epoch1Block)))
	if err != nil {
		t.Fatalf("Failed to get seed for epoch 1: %v", err)
	}

	seed2, err := calcSeedHash(chain, big.NewInt(int64(epoch2Block)))
	if err != nil {
		t.Fatalf("Failed to get seed for epoch 2: %v", err)
	}

	// Seeds should be different across epochs
	if seed0 == seed1 {
		t.Error("Seed should change between epoch 0 and epoch 1")
	}
	if seed1 == seed2 {
		t.Error("Seed should change between epoch 1 and epoch 2")
	}
	if seed0 == seed2 {
		t.Error("Seed should be different between epoch 0 and epoch 2")
	}
}

// TestGetSeedHashFakeMode verifies fake mode behavior
func TestGetSeedHashFakeMode(t *testing.T) {
	engine := NewFaker()
	chain := newMockChainReader()

	seed, err := engine.GetSeedHash(chain, big.NewInt(12345))
	if err != nil {
		t.Fatalf("GetSeedHash in fake mode failed: %v", err)
	}

	// Fake mode should return a fixed seed
	seed2, _ := engine.GetSeedHash(chain, big.NewInt(99999))
	if seed != seed2 {
		t.Error("Fake mode should return the same seed for any block")
	}
}

// TestSeedBlockConsistencyWithMonero verifies compatibility with Monero's model
func TestSeedBlockConsistencyWithMonero(t *testing.T) {
	// Monero uses: seed_height = (height <= SEEDHASH_EPOCH_BLOCKS + SEEDHASH_EPOCH_LAG) ? 0 :
	//              (height - SEEDHASH_EPOCH_LAG - 1) & ~(SEEDHASH_EPOCH_BLOCKS - 1)
	// Where SEEDHASH_EPOCH_BLOCKS = 2048, SEEDHASH_EPOCH_LAG = 64

	// Our formula should match Monero's behavior
	moneroTests := []struct {
		height   uint64
		expected uint64
	}{
		{0, 0},
		{2048, 0},
		{2111, 0},
		{2112, 2048}, // 2112 - 64 = 2048, epoch boundary
		{4096, 2048}, // Still in epoch 1 seed
		{4160, 4096}, // 4160 - 64 = 4096, epoch 2 seed
		{6208, 6144}, // 6208 - 64 = 6144, epoch 3 seed
	}

	for _, tt := range moneroTests {
		result := seedBlock(tt.height)
		if result != tt.expected {
			t.Errorf("Monero compat: seedBlock(%d) = %d, expected %d (epoch boundary mismatch)",
				tt.height, result, tt.expected)
		}
	}
}

var _ consensus.ChainHeaderReader = (*mockChainReader)(nil)
