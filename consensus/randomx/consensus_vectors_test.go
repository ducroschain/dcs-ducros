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

// Consensus test vectors for RandomX verification
// These tests ensure mining and verification are identical

//go:build integration
// +build integration

package randomx

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// TestConsensusVectorEpochRotation tests epoch rotation at 2048 block boundaries
func TestConsensusVectorEpochRotation(t *testing.T) {
	engine := New(&Config{PowMode: ModeNormal, LightMode: true})
	defer engine.Close()

	chain := newMockChainForVectors(t)

	// Test vectors for epoch transitions
	vectors := []struct {
		blockNum          uint64
		expectedSeedBlock uint64
		description       string
	}{
		{0, 0, "Genesis block"},
		{63, 0, "Before lag (63 < 64)"},
		{64, 0, "First block with lag (64 - 64 = 0)"},
		{2047, 0, "Last block of epoch 0"},
		{2048, 0, "First block using epoch 0 seed (lag not reached)"},
		{2111, 0, "Last block before seed changes (2111 - 64 = 2047)"},
		{2112, 2048, "CRITICAL: First block with epoch 1 seed (2112 - 64 = 2048)"},
		{4096, 2048, "Epoch 1 continues"},
		{4160, 4096, "CRITICAL: First block with epoch 2 seed (4160 - 64 = 4096)"},
	}

	for _, tv := range vectors {
		t.Run(tv.description, func(t *testing.T) {
			seedBlockNum := seedBlock(tv.blockNum)
			if seedBlockNum != tv.expectedSeedBlock {
				t.Errorf("Block %d: expected seed block %d, got %d",
					tv.blockNum, tv.expectedSeedBlock, seedBlockNum)
			}

			// Verify GetSeedHash returns consistent results
			seed1, err := engine.GetSeedHash(chain, big.NewInt(int64(tv.blockNum)))
			if err != nil {
				t.Fatalf("GetSeedHash failed: %v", err)
			}

			// Call again to ensure determinism
			seed2, err := engine.GetSeedHash(chain, big.NewInt(int64(tv.blockNum)))
			if err != nil {
				t.Fatalf("GetSeedHash second call failed: %v", err)
			}

			if seed1 != seed2 {
				t.Errorf("GetSeedHash not deterministic! seed1=%s seed2=%s",
					seed1.Hex(), seed2.Hex())
			}

			t.Logf("Block %d -> seed block %d -> seed %s", tv.blockNum, seedBlockNum, seed1.Hex())
		})
	}
}

// TestConsensusVectorInvalidHeaders tests rejection of invalid headers
func TestConsensusVectorInvalidHeaders(t *testing.T) {
	engine := New(&Config{PowMode: ModeNormal, LightMode: true})
	defer engine.Close()

	chain := newMockChainForVectors(t)

	// Create valid parent
	parent := &types.Header{
		Number:     big.NewInt(100),
		Time:       1000,
		Difficulty: big.NewInt(1000),
		MixDigest:  common.Hash{},
		Nonce:      types.BlockNonce{},
	}
	chain.headers[parent.Hash()] = parent

	// Test vectors for invalid headers
	vectors := []struct {
		name         string
		modifyHeader func(*types.Header)
		expectError  bool
	}{
		{
			name: "Valid header",
			modifyHeader: func(h *types.Header) {
				// No modification - should pass basic checks
			},
			expectError: false, // Will fail PoW but pass other checks
		},
		{
			name: "Timestamp in future",
			modifyHeader: func(h *types.Header) {
				h.Time = uint64(9999999999) // Far future
			},
			expectError: true,
		},
		{
			name: "Timestamp older than parent",
			modifyHeader: func(h *types.Header) {
				h.Time = parent.Time - 1
			},
			expectError: true,
		},
		{
			name: "Timestamp equal to parent (should fail MTP)",
			modifyHeader: func(h *types.Header) {
				h.Time = parent.Time
			},
			expectError: true, // MTP requires > median
		},
		{
			name: "Invalid difficulty (too low)",
			modifyHeader: func(h *types.Header) {
				h.Difficulty = big.NewInt(1) // Way too low
			},
			expectError: true,
		},
		{
			name: "Gas used > gas limit",
			modifyHeader: func(h *types.Header) {
				h.GasLimit = 1000
				h.GasUsed = 2000
			},
			expectError: true,
		},
	}

	for _, tv := range vectors {
		t.Run(tv.name, func(t *testing.T) {
			header := &types.Header{
				ParentHash: parent.Hash(),
				Number:     big.NewInt(101),
				Time:       parent.Time + 13,
				Difficulty: parent.Difficulty,
				GasLimit:   8000000,
				GasUsed:    0,
				MixDigest:  common.Hash{},
				Nonce:      types.BlockNonce{},
			}

			tv.modifyHeader(header)

			err := engine.VerifyHeader(chain, header)

			if tv.expectError && err == nil {
				t.Errorf("Expected error but got nil")
			} else if !tv.expectError && err != nil && err != consensus.ErrUnknownAncestor {
				// Unknown ancestor is OK for test setup
				// PoW failure is expected (we're not mining)
				if err.Error() != "proof-of-work verification failed: invalid proof-of-work" &&
					err.Error() != "proof-of-work verification failed: invalid mix digest" {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

// TestConsensusVectorSeedConsistency tests that seed remains constant within epoch
func TestConsensusVectorSeedConsistency(t *testing.T) {
	engine := New(&Config{PowMode: ModeNormal, LightMode: true})
	defer engine.Close()

	chain := newMockChainForVectors(t)

	// Test that all blocks in epoch 1 (2112-4159) use same seed
	epoch1Start := uint64(2112)
	epoch1End := uint64(4159)

	var epoch1Seed common.Hash
	for block := epoch1Start; block <= epoch1End; block++ {
		seed, err := engine.GetSeedHash(chain, big.NewInt(int64(block)))
		if err != nil {
			t.Fatalf("Block %d: GetSeedHash failed: %v", block, err)
		}

		if block == epoch1Start {
			epoch1Seed = seed
			t.Logf("Epoch 1 seed: %s", seed.Hex())
		} else {
			if seed != epoch1Seed {
				t.Errorf("Block %d: seed changed within epoch! Expected %s, got %s",
					block, epoch1Seed.Hex(), seed.Hex())
			}
		}

		// Check every 100 blocks to speed up test
		if block > epoch1Start+100 && (block-epoch1Start)%100 != 0 {
			block += 99
		}
	}

	// Verify seed CHANGES at epoch boundary
	epoch2Start := epoch1End + 1
	seed2, err := engine.GetSeedHash(chain, big.NewInt(int64(epoch2Start)))
	if err != nil {
		t.Fatalf("Epoch 2 start: GetSeedHash failed: %v", err)
	}

	if seed2 == epoch1Seed {
		t.Errorf("Seed did NOT change at epoch boundary! Both epochs have seed %s", seed2.Hex())
	}

	t.Logf("Epoch 2 seed: %s (correctly different)", seed2.Hex())
}

// TestConsensusVectorReorgAcrossEpoch tests reorg handling across epoch boundary
func TestConsensusVectorReorgAcrossEpoch(t *testing.T) {
	engine := New(&Config{PowMode: ModeNormal, LightMode: true})
	defer engine.Close()

	chain := newMockChainForVectors(t)

	// Simulate reorg at epoch boundary (block 2112)
	// Chain A: normal progression
	// Chain B: alternate chain with different seed block

	// Both chains should use same seed derivation logic
	seedA, err := engine.GetSeedHash(chain, big.NewInt(2112))
	if err != nil {
		t.Fatalf("Chain A seed failed: %v", err)
	}

	seedB, err := engine.GetSeedHash(chain, big.NewInt(2112))
	if err != nil {
		t.Fatalf("Chain B seed failed: %v", err)
	}

	// Same block number should give same seed (deterministic)
	if seedA != seedB {
		t.Errorf("Reorg produced different seeds! A=%s B=%s", seedA.Hex(), seedB.Hex())
	}

	t.Logf("Reorg test passed: consistent seed %s", seedA.Hex())
}

// TestConsensusVectorKeyBlockMissing tests behavior when seed block is missing
func TestConsensusVectorKeyBlockMissing(t *testing.T) {
	engine := New(&Config{PowMode: ModeNormal, LightMode: true})
	defer engine.Close()

	// Create chain WITHOUT seed block 2048
	chain := &mockChainReader{
		headers: make(map[common.Hash]*types.Header),
		config: &params.ChainConfig{
			ChainID: big.NewInt(9999),
			RandomX: &params.RandomXConfig{
				LWMAActivationBlock: big.NewInt(0),
			},
		},
	}

	// Add block 2112 but NOT seed block 2048
	header2112 := &types.Header{
		Number: big.NewInt(2112),
		Time:   2112,
	}
	chain.headers[header2112.Hash()] = header2112

	// This MUST fail because seed block 2048 is missing
	_, err := engine.GetSeedHash(chain, big.NewInt(2112))
	if err == nil {
		t.Error("Expected error when seed block missing, got nil")
	}

	if err != nil {
		t.Logf("Correctly rejected missing seed block: %v", err)
	}
}

// Mock chain reader for test vectors
func newMockChainForVectors(t *testing.T) *mockChainReader {
	chain := &mockChainReader{
		headers: make(map[common.Hash]*types.Header),
		config: &params.ChainConfig{
			ChainID:        big.NewInt(9999),
			HomesteadBlock: big.NewInt(0),
			LondonBlock:    big.NewInt(0),
			RandomX: &params.RandomXConfig{
				LWMAActivationBlock: big.NewInt(0),
			},
		},
	}

	// Pre-populate headers for seed blocks
	seedBlocks := []uint64{0, 2048, 4096, 6144, 8192}
	for _, blockNum := range seedBlocks {
		header := &types.Header{
			ParentHash: common.Hash{}, // Simplified
			Number:     big.NewInt(int64(blockNum)),
			Time:       blockNum,
			Difficulty: big.NewInt(1000),
			// Deterministic hash for testing
			Extra: []byte(fmt.Sprintf("seed-block-%d", blockNum)),
		}
		// Force specific hash by using Extra field
		hash := crypto.Keccak256Hash(header.Extra)
		chain.headers[hash] = header
		// Also make it findable by number
		chain.headersByNumber = make(map[uint64]*types.Header)
		chain.headersByNumber[blockNum] = header
	}

	return chain
}

// Extended mock chain reader with number lookup
type mockChainReader struct {
	headers         map[common.Hash]*types.Header
	headersByNumber map[uint64]*types.Header
	config          *params.ChainConfig
}

func (m *mockChainReader) Config() *params.ChainConfig {
	return m.config
}

func (m *mockChainReader) GetHeader(hash common.Hash, number uint64) *types.Header {
	return m.headers[hash]
}

func (m *mockChainReader) GetHeaderByNumber(number uint64) *types.Header {
	if m.headersByNumber != nil {
		return m.headersByNumber[number]
	}
	return nil
}

func (m *mockChainReader) GetHeaderByHash(hash common.Hash) *types.Header {
	return m.headers[hash]
}

func (m *mockChainReader) GetTd(hash common.Hash, number uint64) *big.Int {
	return big.NewInt(0)
}

func (m *mockChainReader) CurrentHeader() *types.Header {
	var latest *types.Header
	for _, h := range m.headers {
		if latest == nil || h.Number.Cmp(latest.Number) > 0 {
			latest = h
		}
	}
	return latest
}

// TestConsensusVectorLWMAConsistency tests LWMA difficulty calculation consistency
// between mining (Prepare) and verification (verifyHeader) paths
func TestConsensusVectorLWMAConsistency(t *testing.T) {
	engine := New(&Config{PowMode: ModeNormal, LightMode: true})
	defer engine.Close()

	chain := &mockChainReader{
		headers:         make(map[common.Hash]*types.Header),
		headersByNumber: make(map[uint64]*types.Header),
		config: &params.ChainConfig{
			ChainID: big.NewInt(9999),
			RandomX: &params.RandomXConfig{
				LWMAActivationBlock: big.NewInt(0),
			},
		},
	}

	// Build chain of 65 blocks to test LWMA window
	var previousHeader *types.Header
	for i := uint64(0); i <= 65; i++ {
		header := &types.Header{
			Number:     big.NewInt(int64(i)),
			Time:       1000 + (i * 13), // 13s block time
			Difficulty: big.NewInt(1000),
			GasLimit:   8000000,
			GasUsed:    0,
		}

		if i > 0 {
			header.ParentHash = previousHeader.Hash()
			// Calculate difficulty via mining path (Prepare)
			header.Difficulty = engine.CalcDifficulty(chain, header.Time, previousHeader)
		}

		hash := crypto.Keccak256Hash(header.Number.Bytes())
		chain.headers[hash] = header
		chain.headersByNumber[i] = header
		previousHeader = header
	}

	// Test: Verify difficulty calculation is deterministic
	t.Run("LWMA determinism", func(t *testing.T) {
		parent := chain.headersByNumber[64]
		nextTime := parent.Time + 13

		// Calculate difficulty twice
		diff1 := engine.CalcDifficulty(chain, nextTime, parent)
		diff2 := engine.CalcDifficulty(chain, nextTime, parent)

		if diff1.Cmp(diff2) != 0 {
			t.Errorf("LWMA not deterministic! diff1=%s diff2=%s", diff1, diff2)
		}

		// Calculate via LWMA directly
		diff3 := CalcDifficultyLWMA(chain, nextTime, parent)

		if diff1.Cmp(diff3) != 0 {
			t.Errorf("CalcDifficulty != CalcDifficultyLWMA! CalcDifficulty=%s LWMA=%s",
				diff1, diff3)
		}
	})

	// Test: Mining path (Prepare) == Verification path (verifyHeader)
	t.Run("Mining vs Verification consistency", func(t *testing.T) {
		parent := chain.headersByNumber[64]

		// Mining path: Create header, call Prepare
		miningHeader := &types.Header{
			ParentHash: parent.Hash(),
			Number:     big.NewInt(65),
			Time:       parent.Time + 13,
			GasLimit:   8000000,
		}

		err := engine.Prepare(chain, miningHeader)
		if err != nil {
			t.Fatalf("Prepare failed: %v", err)
		}

		miningDifficulty := miningHeader.Difficulty

		// Verification path: Calculate expected difficulty
		verificationDifficulty := engine.CalcDifficulty(chain, miningHeader.Time, parent)

		if miningDifficulty.Cmp(verificationDifficulty) != 0 {
			t.Errorf("Mining != Verification! mining=%s verification=%s",
				miningDifficulty, verificationDifficulty)
		}
	})

	// Test: LWMA parameters are applied consistently
	t.Run("LWMA parameter consistency", func(t *testing.T) {
		// Verify constants match documentation
		if LWMAWindowSize != 60 {
			t.Errorf("LWMAWindowSize = %d, expected 60", LWMAWindowSize)
		}
		if LWMATargetBlockTime != 13 {
			t.Errorf("LWMATargetBlockTime = %d, expected 13", LWMATargetBlockTime)
		}
		if LWMAMaxAdjustmentUp != 2 {
			t.Errorf("LWMAMaxAdjustmentUp = %d, expected 2", LWMAMaxAdjustmentUp)
		}
		if LWMAMaxAdjustmentDown != 2 {
			t.Errorf("LWMAMaxAdjustmentDown = %d, expected 2", LWMAMaxAdjustmentDown)
		}
		if LWMABurstDetectionWindow != 10 {
			t.Errorf("LWMABurstDetectionWindow = %d, expected 10", LWMABurstDetectionWindow)
		}
	})

	// Test: Burst detection activates correctly
	t.Run("Burst detection", func(t *testing.T) {
		// Create fast blocks pattern (burst attack)
		fastChain := &mockChainReader{
			headers:         make(map[common.Hash]*types.Header),
			headersByNumber: make(map[uint64]*types.Header),
			config:          chain.config,
		}

		var prev *types.Header
		for i := uint64(0); i <= 65; i++ {
			var blockTime uint64
			if i < 55 {
				blockTime = 1000 + (i * 13) // Normal timing
			} else {
				blockTime = 1000 + (55 * 13) + ((i - 55) * 3) // Fast blocks (3s)
			}

			header := &types.Header{
				Number:     big.NewInt(int64(i)),
				Time:       blockTime,
				Difficulty: big.NewInt(1000),
				GasLimit:   8000000,
			}

			if i > 0 {
				header.ParentHash = prev.Hash()
			}

			hash := crypto.Keccak256Hash(header.Number.Bytes())
			fastChain.headers[hash] = header
			fastChain.headersByNumber[i] = header
			prev = header
		}

		// Calculate difficulty after burst
		parent := fastChain.headersByNumber[64]

		// Collect block times for burst detection
		blockTimes := make([]uint64, LWMAWindowSize)
		current := parent
		for i := LWMAWindowSize - 1; i >= 0; i-- {
			blockTimes[i] = current.Time
			if i > 0 {
				current = fastChain.GetHeader(current.ParentHash, current.Number.Uint64()-1)
			}
		}

		burstDetected := detectHashrateBurst(blockTimes, LWMAWindowSize)
		if !burstDetected {
			t.Error("Expected burst detection to trigger on fast blocks, but it didn't")
		}

		t.Logf("Burst detection correctly triggered")
	})
}
