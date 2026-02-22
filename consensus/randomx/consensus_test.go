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
	crand "crypto/rand"
	"encoding/binary"
	"math/big"
	"math/rand"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

func randSlice(min, max uint32) []byte {
	var b = make([]byte, 4)
	crand.Read(b)
	a := binary.LittleEndian.Uint32(b)
	size := min + a%(max-min)
	out := make([]byte, size)
	crand.Read(out)
	return out
}

func TestDifficultyCalculators(t *testing.T) {
	for i := 0; i < 5000; i++ {
		// 1 to 300 seconds diff
		var timeDelta = uint64(1 + rand.Uint32()%3000)
		diffBig := new(big.Int).SetBytes(randSlice(2, 10))
		if diffBig.Cmp(params.MinimumDifficulty) < 0 {
			diffBig.Set(params.MinimumDifficulty)
		}
		header := &types.Header{
			Difficulty: diffBig,
			Number:     new(big.Int).SetUint64(rand.Uint64() % 50_000_000),
			Time:       rand.Uint64() - timeDelta,
		}
		if rand.Uint32()&1 == 0 {
			header.UncleHash = types.EmptyUncleHash
		}
		bombDelay := new(big.Int).SetUint64(rand.Uint64() % 50_000_000)
		for i, pair := range []struct {
			bigFn  func(time uint64, parent *types.Header) *big.Int
			u256Fn func(time uint64, parent *types.Header) *big.Int
		}{
			{FrontierDifficultyCalculator, CalcDifficultyFrontierU256},
			{HomesteadDifficultyCalculator, CalcDifficultyHomesteadU256},
			{DynamicDifficultyCalculator(bombDelay), MakeDifficultyCalculatorU256(bombDelay)},
		} {
			time := header.Time + timeDelta
			want := pair.bigFn(time, header)
			have := pair.u256Fn(time, header)
			if want.BitLen() > 256 {
				continue
			}
			if want.Cmp(have) != 0 {
				t.Fatalf("pair %d: want %x have %x\nparent.Number: %x\np.Time: %x\nc.Time: %x\nBombdelay: %v\n", i, want, have,
					header.Number, header.Time, time, bombDelay)
			}
		}
	}
}

func BenchmarkDifficultyCalculator(b *testing.B) {
	x1 := makeDifficultyCalculator(big.NewInt(1000000))
	x2 := MakeDifficultyCalculatorU256(big.NewInt(1000000))
	h := &types.Header{
		ParentHash: common.Hash{},
		UncleHash:  types.EmptyUncleHash,
		Difficulty: big.NewInt(0xffffff),
		Number:     big.NewInt(500000),
		Time:       1000000,
	}
	b.Run("big-frontier", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			calcDifficultyFrontier(1000014, h)
		}
	})
	b.Run("u256-frontier", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			CalcDifficultyFrontierU256(1000014, h)
		}
	})
	b.Run("big-homestead", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			calcDifficultyHomestead(1000014, h)
		}
	})
	b.Run("u256-homestead", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			CalcDifficultyHomesteadU256(1000014, h)
		}
	})
	b.Run("big-generic", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			x1(1000014, h)
		}
	})
	b.Run("u256-generic", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			x2(1000014, h)
		}
	})
}

func TestCalcDifficultyFrontier(t *testing.T) {
	config := &params.ChainConfig{
		ChainID:        big.NewInt(1),
		HomesteadBlock: big.NewInt(1150000),
	}

	tests := []struct {
		parentTime       uint64
		parentDifficulty *big.Int
		currentTime      uint64
		expected         *big.Int
	}{
		// Test case 1: Fast block (should increase difficulty)
		{1000, big.NewInt(131072), 1010, big.NewInt(131136)},
		// Test case 2: Slow block (should decrease difficulty)
		{1000, big.NewInt(131072), 1020, big.NewInt(131008)},
	}

	for i, test := range tests {
		parent := &types.Header{
			Number:     big.NewInt(1000),
			Time:       test.parentTime,
			Difficulty: test.parentDifficulty,
		}
		diff := CalcDifficulty(config, test.currentTime, parent)
		if diff.Cmp(test.expected) != 0 {
			t.Errorf("test %d: expected %v, got %v", i, test.expected, diff)
		}
	}
}

func TestRandomXEngine(t *testing.T) {
	// Test creating different types of RandomX engines
	t.Run("New", func(t *testing.T) {
		engine := New(&Config{PowMode: ModeNormal, LightMode: true})
		if engine == nil {
			t.Fatal("expected non-nil engine")
		}
		defer engine.Close()
	})

	t.Run("NewFaker", func(t *testing.T) {
		engine := NewFaker()
		if engine == nil {
			t.Fatal("expected non-nil engine")
		}
		if engine.fakeFull {
			t.Error("NewFaker should not be full fake")
		}
	})

	t.Run("NewFullFaker", func(t *testing.T) {
		engine := NewFullFaker()
		if engine == nil {
			t.Fatal("expected non-nil engine")
		}
		if !engine.fakeFull {
			t.Error("NewFullFaker should be full fake")
		}
	})

	t.Run("NewFakeFailer", func(t *testing.T) {
		failBlock := uint64(100)
		engine := NewFakeFailer(failBlock)
		if engine == nil {
			t.Fatal("expected non-nil engine")
		}
		if engine.fakeFail == nil || *engine.fakeFail != failBlock {
			t.Errorf("expected fakeFail to be %d", failBlock)
		}
	})
}
