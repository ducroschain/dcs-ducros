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
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

type mockChainReader struct {
	headers map[uint64]*types.Header
	config  *params.ChainConfig
}

func newMockChainReader(config *params.ChainConfig) *mockChainReader {
	return &mockChainReader{
		headers: make(map[uint64]*types.Header),
		config:  config,
	}
}

func (m *mockChainReader) Config() *params.ChainConfig { return m.config }
func (m *mockChainReader) GetHeader(hash common.Hash, number uint64) *types.Header {
	return m.headers[number]
}
func (m *mockChainReader) GetHeaderByNumber(number uint64) *types.Header  { return m.headers[number] }
func (m *mockChainReader) GetHeaderByHash(hash common.Hash) *types.Header { return nil }
func (m *mockChainReader) GetTd(hash common.Hash, number uint64) *big.Int { return big.NewInt(0) }
func (m *mockChainReader) addHeader(header *types.Header)                 { m.headers[header.Number.Uint64()] = header }

func TestLWMABasic(t *testing.T) {
	config := &params.ChainConfig{
		ChainID:        big.NewInt(33669),
		HomesteadBlock: big.NewInt(0),
		RandomX:        &params.RandomXConfig{},
	}

	chain := newMockChainReader(config)
	genesis := &types.Header{
		Number:     big.NewInt(0),
		Time:       1000,
		Difficulty: big.NewInt(1000),
		ParentHash: common.Hash{},
	}
	chain.addHeader(genesis)

	var parent = genesis
	for i := uint64(1); i <= LWMAWindowSize+10; i++ {
		header := &types.Header{
			Number:     big.NewInt(int64(i)),
			Time:       parent.Time + LWMATargetBlockTime,
			Difficulty: parent.Difficulty,
			ParentHash: parent.Hash(),
		}
		chain.addHeader(header)
		parent = header
	}

	nextTime := parent.Time + LWMATargetBlockTime
	difficulty := CalcDifficultyLWMA(chain, nextTime, parent)

	expectedDiff := parent.Difficulty
	ratio := new(big.Rat).SetFrac(difficulty, expectedDiff)
	ratioFloat, _ := ratio.Float64()

	if ratioFloat < 0.9 || ratioFloat > 1.1 {
		t.Errorf("Difficulty changed too much: got %v, expected ~%v (ratio: %.2f)",
			difficulty, expectedDiff, ratioFloat)
	}

	t.Logf("LWMA test passed: parent diff=%v, next diff=%v, ratio=%.3f",
		parent.Difficulty, difficulty, ratioFloat)
}

func TestShouldUseLWMA(t *testing.T) {
	tests := []struct {
		name        string
		config      *params.ChainConfig
		blockNumber *big.Int
		expected    bool
	}{
		{
			name:        "RandomX without activation block",
			config:      &params.ChainConfig{RandomX: &params.RandomXConfig{}},
			blockNumber: big.NewInt(1),
			expected:    true,
		},
		{
			name: "RandomX with activation block (before)",
			config: &params.ChainConfig{
				RandomX: &params.RandomXConfig{LWMAActivationBlock: big.NewInt(100)},
			},
			blockNumber: big.NewInt(50),
			expected:    false,
		},
		{
			name: "RandomX with activation block (at activation)",
			config: &params.ChainConfig{
				RandomX: &params.RandomXConfig{LWMAActivationBlock: big.NewInt(100)},
			},
			blockNumber: big.NewInt(100),
			expected:    true,
		},
		{
			name:        "No RandomX config",
			config:      &params.ChainConfig{RandomX: nil},
			blockNumber: big.NewInt(100),
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldUseLWMA(tt.config, tt.blockNumber)
			if result != tt.expected {
				t.Errorf("ShouldUseLWMA() = %v, want %v", result, tt.expected)
			}
		})
	}
}
