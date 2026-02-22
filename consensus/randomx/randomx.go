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


// Package randomx implements the RandomX proof-of-work consensus engine.
package randomx

/*
#cgo CFLAGS: -O3 -march=native
#cgo LDFLAGS: -lrandomx -lm -lstdc++

#include <stdlib.h>

// Forward declarations for RandomX C API
typedef struct randomx_cache randomx_cache;
typedef struct randomx_dataset randomx_dataset;
typedef struct randomx_vm randomx_vm;

// RandomX flags
typedef enum {
	RANDOMX_FLAG_DEFAULT = 0,
	RANDOMX_FLAG_LARGE_PAGES = 1,
	RANDOMX_FLAG_HARD_AES = 2,
	RANDOMX_FLAG_FULL_MEM = 4,
	RANDOMX_FLAG_JIT = 8,
	RANDOMX_FLAG_SECURE = 16,
	RANDOMX_FLAG_ARGON2_SSSE3 = 32,
	RANDOMX_FLAG_ARGON2_AVX2 = 64,
	RANDOMX_FLAG_ARGON2 = 96
} randomx_flags;

// RandomX C API functions
extern randomx_cache *randomx_alloc_cache(randomx_flags flags);
extern void randomx_init_cache(randomx_cache *cache, const void *key, size_t keySize);
extern void randomx_release_cache(randomx_cache* cache);

extern randomx_dataset *randomx_alloc_dataset(randomx_flags flags);
extern unsigned long randomx_dataset_item_count(void);
extern void randomx_init_dataset(randomx_dataset *dataset, randomx_cache *cache, unsigned long startItem, unsigned long itemCount);
extern void randomx_release_dataset(randomx_dataset *dataset);

extern randomx_vm *randomx_create_vm(randomx_flags flags, randomx_cache *cache, randomx_dataset *dataset);
extern void randomx_vm_set_cache(randomx_vm *machine, randomx_cache* cache);
extern void randomx_vm_set_dataset(randomx_vm *machine, randomx_dataset *dataset);
extern void randomx_destroy_vm(randomx_vm *machine);

extern void randomx_calculate_hash(randomx_vm *machine, const void *input, size_t inputSize, void *output);
extern void randomx_calculate_hash_first(randomx_vm *machine, const void *input, size_t inputSize);
extern void randomx_calculate_hash_next(randomx_vm *machine, const void *input, size_t inputSize, void *output);
*/
import "C"
import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	lru "github.com/ethereum/go-ethereum/common/lru"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
)

var (
	optimalFlagsOnce  sync.Once
	optimalFlagsValue = C.randomx_flags(C.RANDOMX_FLAG_DEFAULT | C.RANDOMX_FLAG_HARD_AES)
)

// RandomX is a consensus engine based on proof-of-work implementing the RandomX
// algorithm (CPU-friendly, ASIC-resistant, as used by Monero).
type RandomX struct {
	config *Config

	// Caching and dataset
	cache           *C.randomx_cache
	dataset         *C.randomx_dataset
	cacheKey        common.Hash
	cacheMutex      sync.RWMutex
	datasetDisabled atomic.Bool
	datasetJob      *datasetBuild

	// VM pool for parallel mining
	vmPool *VMPool

	// Remote mining support
	remote *remoteSealer

	// Hashrate tracking
	hashrate metrics.Meter

	// DoS protection
	recentBlocks *lru.Cache[common.Hash, bool]  // Cache of recently verified blocks to prevent re-verification attacks
	failCache    *lru.Cache[common.Hash, error] // Cache of recently failed verifications (hash -> error)
	verifyMutex  sync.Mutex                     // Protects verification metrics and throttling

	// Testing/development modes
	fakeFail  *uint64        // Block number which fails PoW check even in fake mode
	fakeDelay *time.Duration // Time delay to sleep for before returning from verify
	fakeFull  bool           // Accepts everything as valid
}

type datasetBuild struct {
	done chan struct{}
	err  atomic.Value // error
	seed common.Hash
}

func newDatasetBuild(seed common.Hash) *datasetBuild {
	b := &datasetBuild{done: make(chan struct{}), seed: seed}
	// Don't store nil in atomic.Value - it will panic
	// The error field starts as zero value (no error stored)
	// When Load() is called on an uninitialized atomic.Value, it returns nil
	return b
}

func (b *datasetBuild) setError(err error) {
	// Don't store nil in atomic.Value - it will panic
	// Only store actual errors
	if err != nil {
		b.err.Store(err)
	}
	// If err is nil, leave atomic.Value as zero value (Load returns nil)
}

func (b *datasetBuild) error() error {
	if v := b.err.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (b *datasetBuild) ready() bool {
	select {
	case <-b.done:
		return true
	default:
		return false
	}
}

// Config are the configuration parameters of the RandomX consensus engine.
type Config struct {
	// CacheDir is the directory for storing the RandomX cache/dataset
	CacheDir string

	// PowMode defines the mining mode (normal, test, fake, etc.)
	PowMode Mode

	// LightMode forces RandomX to operate without the 2 GiB dataset.
	// This keeps memory usage low (useful for tests or constrained
	// environments) at the cost of significantly reduced hash rate.
	LightMode bool
}

// Mode defines the type of PoW mode
type Mode uint

const (
	ModeNormal Mode = iota
	ModeTest
	ModeFake
	ModeFullFake
)

// VMPool manages a pool of RandomX VMs for parallel mining
type VMPool struct {
	vms      []*C.randomx_vm
	mu       sync.Mutex
	cache    *C.randomx_cache
	dataset  *C.randomx_dataset
	flags    C.randomx_flags
	poolSize int
}

// sealWork wraps a seal block with relative result channel.
type sealWork struct {
	errc chan error
	res  chan [4]string
}

// mineResult wraps the pow solution parameters for the specified block.
type mineResult struct {
	nonce     types.BlockNonce
	mixDigest common.Hash
	hash      common.Hash

	errc chan error
}

// hashrate wraps the hash rate submitted by the remote sealer.
type hashrate struct {
	id   common.Hash
	ping time.Time
	rate uint64

	done chan struct{}
}

// sealTask wraps a seal block with relative result channel and chain reader.
type sealTask struct {
	block    *types.Block
	results  chan<- *types.Block
	chain    consensus.ChainHeaderReader
	sealHash common.Hash
}

// remoteSealer wraps the actual sealing work and listens for work requests and
// returns work solutions.
type remoteSealer struct {
	randomx     *RandomX
	chain       consensus.ChainHeaderReader
	works       map[common.Hash]*sealTask
	rates       map[common.Hash]hashrate
	currentTask *sealTask
	currentWork [4]string
	notifyCtx   []chan [4]string // Notification channels for new work
	reqWG       sync.WaitGroup   // Tracks remote sealing threads
	mutex       sync.Mutex

	fetchWorkCh  chan *sealWork
	submitWorkCh chan *mineResult
	submitRateCh chan *hashrate
	fetchRateCh  chan chan uint64
	requestExit  chan struct{}
	exitCh       chan struct{}
	startCh      chan struct{}
	cancelCh     chan common.Hash
	workCh       chan *sealTask
}

// New creates a full-featured RandomX consensus engine with the given configuration.
func New(config *Config) *RandomX {
	if config == nil {
		config = &Config{
			PowMode: ModeNormal,
		}
	}

	// Initialize DoS protection caches
	recentBlocks := lru.NewCache[common.Hash, bool](1024) // Cache 1024 recent block hashes
	failCache := lru.NewCache[common.Hash, error](256)    // Cache 256 recent failures

	randomx := &RandomX{
		config:       config,
		hashrate:     *metrics.NewMeter(),
		recentBlocks: recentBlocks,
		failCache:    failCache,
	}
	randomx.remote = startRemoteSealer(randomx)

	log.Info("RandomX DoS protection enabled", "blockCache", 1024, "failCache", 256)

	return randomx
}

// startRemoteSealer starts the remote sealer goroutine.
func startRemoteSealer(randomx *RandomX) *remoteSealer {
	sealer := &remoteSealer{
		randomx:      randomx,
		works:        make(map[common.Hash]*sealTask),
		rates:        make(map[common.Hash]hashrate),
		fetchWorkCh:  make(chan *sealWork),
		submitWorkCh: make(chan *mineResult),
		submitRateCh: make(chan *hashrate),
		fetchRateCh:  make(chan chan uint64),
		requestExit:  make(chan struct{}),
		exitCh:       make(chan struct{}),
		startCh:      make(chan struct{}),
		cancelCh:     make(chan common.Hash, 16),
		workCh:       make(chan *sealTask),
	}
	go sealer.loop(randomx)
	return sealer
}

// NewFaker creates a RandomX consensus engine with a fake PoW scheme that accepts
// all blocks' seal as valid, though they still have to conform to the Ethereum
// consensus rules.
func NewFaker() *RandomX {
	return &RandomX{
		fakeFull: false,
	}
}

// NewFakeFailer creates a RandomX consensus engine with a fake PoW scheme that
// accepts all blocks as valid apart from the single one specified, though they
// still have to conform to the Ethereum consensus rules.
func NewFakeFailer(fail uint64) *RandomX {
	return &RandomX{
		fakeFail: &fail,
	}
}

// NewFakeDelayer creates a RandomX consensus engine with a fake PoW scheme that
// accepts all blocks as valid, but delays verifications by some time, though
// they still have to conform to the Ethereum consensus rules.
func NewFakeDelayer(delay time.Duration) *RandomX {
	return &RandomX{
		fakeDelay: &delay,
	}
}

// NewFullFaker creates a RandomX consensus engine with a full fake scheme that
// accepts all blocks as valid, without checking any consensus rules whatsoever.
func NewFullFaker() *RandomX {
	return &RandomX{
		fakeFull: true,
	}
}

// getOptimalFlags returns the best RandomX flags for this system with fallback
func getOptimalFlags() C.randomx_flags {
	optimalFlagsOnce.Do(func() {
		// Try optimal flags: JIT + HardAES + Large Pages (best performance)
		optimal := C.randomx_flags(C.RANDOMX_FLAG_JIT | C.RANDOMX_FLAG_HARD_AES | C.RANDOMX_FLAG_LARGE_PAGES)

		testCache := C.randomx_alloc_cache(optimal)
		if testCache != nil {
			C.randomx_release_cache(testCache)
			optimalFlagsValue = optimal
			log.Info("RandomX using optimal flags", "jit", true, "hugepages", true, "hardAES", true)
			return
		}

		// Fallback 1: JIT + HardAES (no huge pages)
		fallback1 := C.randomx_flags(C.RANDOMX_FLAG_JIT | C.RANDOMX_FLAG_HARD_AES)
		testCache = C.randomx_alloc_cache(fallback1)
		if testCache != nil {
			C.randomx_release_cache(testCache)
			optimalFlagsValue = fallback1
			log.Warn("RandomX using JIT without huge pages (performance -30%)", "jit", true, "hugepages", false)
			return
		}

		// Fallback 2: HardAES only (no JIT, no huge pages) - slowest but stable
		optimalFlagsValue = C.randomx_flags(C.RANDOMX_FLAG_DEFAULT | C.RANDOMX_FLAG_HARD_AES)
		log.Warn("RandomX using interpreted mode (performance -10-15×)", "jit", false, "hugepages", false,
			"hint", "Enable huge pages: sudo sysctl -w vm.nr_hugepages=1280")
	})
	return optimalFlagsValue
}

func withFullMemory(flags C.randomx_flags) C.randomx_flags {
	return flags | C.randomx_flags(C.RANDOMX_FLAG_FULL_MEM)
}

func flagsForDataset(dataset *C.randomx_dataset) C.randomx_flags {
	flags := getOptimalFlags()
	if dataset != nil {
		flags = withFullMemory(flags)
	}
	return flags
}

// initCache initializes the RandomX cache with the given key
func (randomx *RandomX) shouldUseDataset() bool {
	if randomx.config == nil {
		return true
	}
	if randomx.config.LightMode {
		return false
	}
	return randomx.config.PowMode == ModeNormal
}

func (randomx *RandomX) ensureDatasetLocked(flags C.randomx_flags) error {
	if randomx.cache == nil {
		return errors.New("randomx: cache must be initialised before dataset")
	}

	datasetFlags := withFullMemory(flags)

	// Reset the disabled flag since we're about to (re)attempt full dataset mode.
	randomx.datasetDisabled.Store(false)

	if randomx.dataset == nil {
		log.Info("Allocating RandomX dataset (full mode)")
		randomx.dataset = C.randomx_alloc_dataset(datasetFlags)
		if randomx.dataset == nil {
			return errors.New("randomx: failed to allocate dataset")
		}
	}

	randomx.startDatasetBuildLocked()
	return nil
}

func (randomx *RandomX) initCache(key common.Hash) error {
	randomx.cacheMutex.Lock()
	defer randomx.cacheMutex.Unlock()

	// Check if cache is already initialized with the same key
	if randomx.cache != nil && randomx.cacheKey == key {
		return nil
	}

	// Release old cache if exists
	if randomx.cache != nil {
		C.randomx_release_cache(randomx.cache)
		randomx.cache = nil
	}

	// Get optimal flags with automatic fallback
	flags := getOptimalFlags()

	// Allocate and initialize cache
	randomx.cache = C.randomx_alloc_cache(flags)
	if randomx.cache == nil {
		return errors.New("randomx: failed to allocate cache")
	}

	keyPtr := (*C.char)(unsafe.Pointer(&key[0]))
	C.randomx_init_cache(randomx.cache, unsafe.Pointer(keyPtr), C.size_t(len(key)))
	randomx.cacheKey = key

	// CRITICAL: Update VMPool cache references when epoch changes
	// Without this, VMs in the pool would use stale cache from previous epoch
	if randomx.vmPool != nil {
		randomx.vmPool.UpdateCache(randomx.cache)
		log.Debug("Updated VMPool cache references for new epoch", "seed", key.Hex()[:16])
	}

	if randomx.shouldUseDataset() {
		if randomx.datasetDisabled.Load() {
			if job := randomx.datasetJob; job == nil || job.seed != key {
				// Retry on new epoch or when no build is running for this seed.
				randomx.datasetDisabled.Store(false)
			}
		}
		if !randomx.datasetDisabled.Load() {
			if err := randomx.ensureDatasetLocked(flags); err != nil {
				log.Warn("RandomX dataset unavailable, continuing in light mode", "err", err)
				randomx.datasetDisabled.Store(true)
			}
		}
	}

	return nil
}

func (randomx *RandomX) startDatasetBuildLocked() {
	dataset := randomx.dataset
	cache := randomx.cache
	if dataset == nil || cache == nil {
		return
	}

	seed := randomx.cacheKey

	if existing := randomx.datasetJob; existing != nil {
		switch {
		case existing.seed == seed && !existing.ready():
			// Build already in progress for this seed.
			return
		case existing.seed == seed && existing.ready() && existing.error() == nil:
			// Dataset already initialised for this seed.
			return
		case !existing.ready():
			// A previous build (for a different seed) is still running; let it finish.
			log.Debug("RandomX dataset build already running", "seed", existing.seed.Hex())
			return
		}
	}

	job := newDatasetBuild(seed)
	randomx.datasetJob = job

	go randomx.buildDataset(job, dataset, cache, seed)
}

func (randomx *RandomX) buildDataset(job *datasetBuild, dataset *C.randomx_dataset, cache *C.randomx_cache, seed common.Hash) {
	defer close(job.done)

	if dataset == nil || cache == nil {
		err := errors.New("randomx: dataset build prerequisites missing")
		job.setError(err)
		randomx.datasetDisabled.Store(true)
		log.Warn("RandomX dataset build aborted", "err", err)
		return
	}

	// Warn if GOMAXPROCS is too low for RandomX dataset initialization
	gomaxprocs := runtime.GOMAXPROCS(0)
	if gomaxprocs == 1 {
		log.Warn("RandomX dataset initialization with GOMAXPROCS=1 may cause instability",
			"gomaxprocs", gomaxprocs,
			"recommendation", "Remove GOMAXPROCS=1 or set to at least 2 for stable RandomX operation")
	}

	itemCount := C.randomx_dataset_item_count()
	start := time.Now()
	log.Info("Initializing RandomX dataset in background", "items", uint64(itemCount), "seed", seed.Hex())

	// Build dataset with timeout protection
	// Note: We can't cancel the C call, but we can detect if it hangs
	buildDone := make(chan struct{})
	buildTimeout := 15 * time.Minute // Reasonable timeout for dataset initialization

	// Initialize dataset in chunks to avoid threading conflicts with GOMAXPROCS=1
	// This prevents segfaults when RandomX C library tries to spawn multiple threads
	go func() {
		// CRITICAL: Lock this goroutine to its OS thread
		// The RandomX C library creates pthreads internally for parallel dataset init.
		// Without this, Go's scheduler might move the goroutine between OS threads,
		// causing the C library's threads to lose their execution context, leading to SIGSEGV.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		defer func() {
			if r := recover(); r != nil {
				log.Error("RandomX dataset build panic", "error", r)
				job.setError(fmt.Errorf("panic during dataset build: %v", r))
				randomx.datasetDisabled.Store(true)
			}
		}()

		// Validate pointers before calling C functions
		if dataset == nil || cache == nil {
			err := errors.New("randomx: dataset or cache pointer became nil")
			job.setError(err)
			randomx.datasetDisabled.Store(true)
			log.Error("RandomX dataset build failed", "err", err)
			return
		}

		// Use single-threaded initialization to avoid conflicts with GOMAXPROCS=1
		// Initialize in one chunk with proper thread safety
		const numChunks = 1
		chunkSize := itemCount / numChunks

		for i := C.ulong(0); i < numChunks; i++ {
			startItem := i * chunkSize
			count := chunkSize
			if i == numChunks-1 {
				// Last chunk gets remainder
				count = itemCount - startItem
			}

			// Final validation before C call
			if dataset == nil || cache == nil {
				err := errors.New("randomx: pointers invalidated during initialization")
				job.setError(err)
				randomx.datasetDisabled.Store(true)
				log.Error("RandomX dataset build aborted", "err", err)
				return
			}

			// This C call internally creates multiple pthreads for parallel initialization
			// It's safe now because we're locked to an OS thread
			C.randomx_init_dataset(dataset, cache, startItem, count)
		}
		close(buildDone)
	}()

	select {
	case <-buildDone:
		// Build completed successfully
		job.setError(nil)
		randomx.datasetDisabled.Store(false)
		log.Info("RandomX dataset ready", "seed", seed.Hex(), "duration", time.Since(start))
	case <-time.After(buildTimeout):
		// Build timed out - this indicates a serious problem
		err := fmt.Errorf("randomx: dataset build timeout after %v", buildTimeout)
		job.setError(err)
		randomx.datasetDisabled.Store(true)
		log.Error("RandomX dataset build timeout - dataset disabled", "timeout", buildTimeout, "seed", seed.Hex())
		// Note: C call continues in background, but we mark it as failed
		// This prevents goroutine leak of the main build goroutine
	}
}

func (randomx *RandomX) datasetReadyLocked() *C.randomx_dataset {
	if randomx.datasetDisabled.Load() {
		return nil
	}

	dataset := randomx.dataset
	if dataset == nil {
		return nil
	}

	job := randomx.datasetJob
	if job == nil {
		return dataset
	}

	if !job.ready() {
		return nil
	}

	if err := job.error(); err != nil {
		log.Warn("RandomX dataset disabled after build failure", "err", err)
		randomx.datasetDisabled.Store(true)
		return nil
	}

	return dataset
}

// NewVMPool creates a new pool of RandomX VMs for parallel mining
func NewVMPool(cache *C.randomx_cache, dataset *C.randomx_dataset, flags C.randomx_flags, size int) *VMPool {
	pool := &VMPool{
		vms:      make([]*C.randomx_vm, 0, size),
		cache:    cache,
		dataset:  dataset,
		flags:    flags,
		poolSize: size,
	}

	// Pre-allocate VMs
	for i := 0; i < size; i++ {
		vm := C.randomx_create_vm(flags, cache, dataset)
		if vm != nil {
			pool.vms = append(pool.vms, vm)
		}
	}

	return pool
}

// Get retrieves a VM from the pool
func (p *VMPool) Get() *C.randomx_vm {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.vms) == 0 {
		// Create new VM if pool is empty
		return C.randomx_create_vm(p.flags, p.cache, p.dataset)
	}

	vm := p.vms[len(p.vms)-1]
	p.vms = p.vms[:len(p.vms)-1]
	return vm
}

// Put returns a VM to the pool
func (p *VMPool) Put(vm *C.randomx_vm) {
	if vm == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.vms) < p.poolSize {
		p.vms = append(p.vms, vm)
	} else {
		// Pool is full, destroy the VM
		C.randomx_destroy_vm(vm)
	}
}

// Close destroys all VMs in the pool
func (p *VMPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, vm := range p.vms {
		if vm != nil {
			C.randomx_destroy_vm(vm)
		}
	}
	p.vms = nil
}

// UpdateCache updates the cache for all VMs in the pool
// CRITICAL: Must be called when epoch changes to prevent VMs from using stale cache
func (p *VMPool) UpdateCache(newCache *C.randomx_cache) {
	if newCache == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Update pool's cache reference
	p.cache = newCache

	// Update cache for all existing VMs in the pool
	for _, vm := range p.vms {
		if vm != nil {
			C.randomx_vm_set_cache(vm, newCache)
		}
	}
}

// Close closes the RandomX engine and cleans up resources.
func (randomx *RandomX) Close() error {
	randomx.cacheMutex.Lock()
	defer randomx.cacheMutex.Unlock()

	if randomx.vmPool != nil {
		randomx.vmPool.Close()
		randomx.vmPool = nil
	}

	if randomx.dataset != nil {
		C.randomx_release_dataset(randomx.dataset)
		randomx.dataset = nil
	}

	if randomx.cache != nil {
		C.randomx_release_cache(randomx.cache)
		randomx.cache = nil
	}

	return nil
}

// hashRandomX calculates the RandomX hash for the given input
func hashRandomX(vm *C.randomx_vm, input []byte) common.Hash {
	var hash common.Hash
	inputPtr := (*C.char)(unsafe.Pointer(&input[0]))
	hashPtr := unsafe.Pointer(&hash[0])

	C.randomx_calculate_hash(vm, unsafe.Pointer(inputPtr), C.size_t(len(input)), hashPtr)
	return hash
}

// verifyRandomX checks whether the given hash and nonce satisfy the PoW difficulty
// CRITICAL: RandomX/Monero uses LITTLE-ENDIAN hash interpretation
func verifyRandomX(hash common.Hash, difficulty *big.Int) bool {
	// The hash must be less than or equal to the target difficulty
	// target = 2^256 / difficulty
	target := new(big.Int).Div(maxUint256, difficulty)

	// CRITICAL: RandomX uses LITTLE-ENDIAN byte order
	// Reverse the hash bytes before comparison
	reversedHash := make([]byte, 32)
	for i := 0; i < 32; i++ {
		reversedHash[i] = hash[31-i]
	}

	hashInt := new(big.Int).SetBytes(reversedHash)
	return hashInt.Cmp(target) <= 0
}

// maxUint256 is the maximum value representable by a uint256
var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 256), common.Big1)

// verifyPoWWithCache verifies the proof-of-work using the provided cache
// This function handles all C-related operations and is called from consensus.go
// Implements rx-eth-v1 format for compatibility with xmrig RandomX mining
func verifyPoWWithCache(cache *C.randomx_cache, dataset *C.randomx_dataset, sealHash common.Hash, header *types.Header) error {
	if cache == nil {
		return errors.New("randomx cache not initialized")
	}

	// Create VM for verification with optimal flags (same as cache)
	flags := flagsForDataset(dataset)
	vm := C.randomx_create_vm(flags, cache, dataset)
	if vm == nil {
		return errors.New("failed to create RandomX VM for verification")
	}
	defer C.randomx_destroy_vm(vm)

	// rx-eth-v1 Format Verification
	// ===============================
	// The stratum-proxy sends miners a 43-byte blob:
	//   blob = headerHash(32) || extraNonce4(4) || const3(3) || nonce4_placeholder(4)
	//
	// Miners (xmrig) fill in nonce4 and compute: hash = RandomX(blob)
	//
	// The proxy combines: nonce64 = (extraNonce4 << 32) | minerNonce4
	// and submits to geth with the combined nonce64.
	//
	// For verification, geth must reconstruct the EXACT same 43-byte preimage
	// that xmrig hashed. The headerHash in the blob is the SealHash computed
	// BEFORE any modifications, so we must use the unmodified header.

	// 1. Extract extraNonce4 from high 32 bits of nonce64
	nonce64 := header.Nonce.Uint64()
	extraNonce4 := uint32(nonce64 >> 32)

	// 2. Extract minerNonce4 from low 32 bits of nonce64
	minerNonce4 := uint32(nonce64 & 0xFFFFFFFF)

	// 3. Reconstruct rx-eth-v1 preimage (43 bytes total)
	hashInput := make([]byte, 43)

	// Byte 0-31: SealHash (keccak256 of RLP-encoded header without nonce/mixdigest)
	copy(hashInput[0:32], sealHash[:])

	// Byte 32-35: extraNonce4 (4 bytes, little-endian)
	binary.LittleEndian.PutUint32(hashInput[32:36], extraNonce4)

	// Byte 36-38: Constant padding (3 bytes of zeros)
	// Already zero from make()

	// Byte 39-42: minerNonce4 (4 bytes, little-endian)
	binary.LittleEndian.PutUint32(hashInput[39:43], minerNonce4)

	// DEBUG: Log detailed verification info
	log.Info("RandomX verification",
		"sealHash", sealHash.Hex(),
		"nonce64", fmt.Sprintf("0x%016x", nonce64),
		"extraNonce", fmt.Sprintf("0x%08x", extraNonce4),
		"minerNonce", fmt.Sprintf("0x%08x", minerNonce4),
		"preimage", hexutil.Encode(hashInput),
		"expectedMix", header.MixDigest.Hex())

	// 4. Calculate RandomX hash using the reconstructed preimage
	hash := hashRandomX(vm, hashInput)

	log.Info("RandomX hash computed", "computedHash", hash.Hex())

	// 5. Verify that the calculated hash matches the MixDigest
	if hash != header.MixDigest {
		return fmt.Errorf("invalid mix digest: computed %s != header %s (extraNonce=%08x minerNonce=%08x)",
			hash.Hex(), header.MixDigest.Hex(), extraNonce4, minerNonce4)
	}

	// 6. Verify that the hash satisfies the difficulty requirement
	if !verifyRandomX(hash, header.Difficulty) {
		return fmt.Errorf("invalid proof-of-work: hash %s does not meet difficulty %s",
			hash.Hex(), header.Difficulty.String())
	}

	return nil
}

// Seal generates a new sealing request for the given input block and pushes
// the result into the given channel.
//
// Note, the method returns immediately and will send the result async. More
// than one result may also be returned depending on the consensus algorithm.
func (randomx *RandomX) Seal(chain consensus.ChainHeaderReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
	log.Info("RandomX Seal called", "block", block.NumberU64(), "difficulty", block.Difficulty())

	// If we're running a fake PoW, simply return a 0 nonce immediately
	if randomx.fakeFull || randomx.config != nil && randomx.config.PowMode == ModeFake {
		log.Debug("Using fake PoW mode")
		header := block.Header()
		header.Nonce = types.BlockNonce{}
		header.MixDigest = common.Hash{}
		select {
		case results <- block.WithSeal(header):
		default:
		}
		return nil
	}

	// If we're running a failed PoW, return error
	if randomx.fakeFail != nil && *randomx.fakeFail == block.NumberU64() {
		return errors.New("randomx: invalid proof-of-work")
	}

	// Pre-compute the seal hash for this block. It will be reused both for
	// local mining and for coordinating remote miners.
	sealHash := randomx.SealHash(block.Header())

	// If we have a remote sealer, send work to it but continue with the
	// local mining flow. Remote miners operate concurrently and may submit
	// solutions via the RPC API while the built-in miner keeps searching.
	if randomx.remote != nil {
		task := &sealTask{block: block, results: results, chain: chain, sealHash: sealHash}
		select {
		case randomx.remote.workCh <- task:
			log.Info("Work sent to remote sealer", "block", block.NumberU64())
		case <-stop:
			log.Info("Mining stopped before sending work to remote sealer")
			return nil
		}
	}

	// Perform local mining
	header := block.Header()

	// Calculate the RandomX seed for this block's epoch
	seedHash, err := randomx.GetSeedHash(chain, header.Number)
	if err != nil {
		log.Error("Failed to calculate RandomX seed", "err", err)
		return err
	}

	// Initialize RandomX cache with epoch seed
	// Cache is reused for all blocks in the same epoch (2048 blocks)
	log.Debug("Initializing RandomX cache", "seedHash", seedHash.Hex(), "blockNumber", header.Number)
	if err := randomx.initCache(seedHash); err != nil {
		log.Error("Failed to initialize RandomX cache", "err", err)
		return err
	}

	// Create a runner and the multiple search threads it directs
	abort := make(chan struct{})
	found := make(chan *types.Block)

	// Start mining goroutine
	log.Info("Starting RandomX mining goroutine")
	go func() {
		defer close(abort)
		randomx.mine(block, found, abort, stop)
	}()

	// Wait for result or stop signal
	select {
	case result := <-found:
		log.Info("Solution found!", "block", result.NumberU64())
		if randomx.remote != nil {
			randomx.remote.cancel(sealHash)
		}
		// Solution found, push to results
		select {
		case results <- result:
			log.Debug("Result sent to results channel")
		default:
			log.Warn("Results channel full, dropping result")
		}
	case <-stop:
		log.Info("Mining aborted via stop channel")
		// Mining aborted externally
		close(abort)
		if randomx.remote != nil {
			randomx.remote.cancel(sealHash)
		}
	}

	log.Debug("Seal function returning")
	return nil
}

// mine is the actual mining loop that searches for a valid nonce
func (randomx *RandomX) mine(block *types.Block, found chan<- *types.Block, abort <-chan struct{}, stop <-chan struct{}) {
	header := block.Header()
	target := new(big.Int).Div(maxUint256, header.Difficulty)

	log.Info("RandomX mine starting", "block", block.NumberU64(), "difficulty", header.Difficulty, "target", target.String())

	// CRITICAL: Lock cache for entire mining duration to prevent rotation
	// If cache is rotated (epoch change), the VM would reference freed memory
	randomx.cacheMutex.RLock()
	defer randomx.cacheMutex.RUnlock()

	cache := randomx.cache
	if cache == nil {
		log.Error("RandomX cache is nil!")
		return
	}

	dataset := randomx.datasetReadyLocked()
	if dataset == nil {
		log.Debug("RandomX dataset not ready, mining in light mode")
	}

	log.Debug("Creating RandomX VM for mining")
	// Create VM with optimal flags (JIT + hugepages if available)
	// VM holds a reference to cache/dataset, so they must remain valid for VM lifetime
	flags := flagsForDataset(dataset)
	vm := C.randomx_create_vm(flags, cache, dataset)
	if vm == nil {
		log.Error("Failed to create RandomX VM!")
		return
	}
	defer C.randomx_destroy_vm(vm)
	log.Info("RandomX VM created, starting nonce search...")

	// Prepare the header for hashing (without nonce)
	sealHash := randomx.SealHash(header)

	// Start nonce search using rx-eth-v1 format
	// For local mining, use high 32 bits as extraNonce and low 32 bits as minerNonce
	var (
		nonce64   = uint64(time.Now().UnixNano())
		attempts  = uint64(0)
		hashInput = make([]byte, 43) // rx-eth-v1: 32+4+3+4 bytes
	)

	// Copy seal hash (bytes 0-31)
	copy(hashInput[:32], sealHash[:])

	// Mining loop
	for {
		select {
		case <-abort:
			log.Debug("Mining aborted", "attempts", attempts)
			return
		case <-stop:
			log.Debug("Mining stopped", "attempts", attempts)
			return
		default:
			// Extract extraNonce4 (high 32 bits) and minerNonce4 (low 32 bits)
			extraNonce4 := uint32(nonce64 >> 32)
			minerNonce4 := uint32(nonce64 & 0xFFFFFFFF)

			// Build rx-eth-v1 preimage (43 bytes)
			// Bytes 0-31: sealHash (already copied above)
			// Bytes 32-35: extraNonce4 (LE)
			binary.LittleEndian.PutUint32(hashInput[32:36], extraNonce4)
			// Bytes 36-38: const padding (already zero from make())
			// Bytes 39-42: minerNonce4 (LE)
			binary.LittleEndian.PutUint32(hashInput[39:43], minerNonce4)

			// Calculate RandomX hash
			hash := hashRandomX(vm, hashInput)

			// Check if we found a valid solution
			// CRITICAL: RandomX uses LITTLE-ENDIAN byte order
			reversedHash := make([]byte, 32)
			for i := 0; i < 32; i++ {
				reversedHash[i] = hash[31-i]
			}
			hashInt := new(big.Int).SetBytes(reversedHash)
			if hashInt.Cmp(target) <= 0 {
				// Found valid nonce!
				log.Info("✅ Found valid nonce!", "block", block.NumberU64(),
					"nonce64", fmt.Sprintf("%016x", nonce64),
					"extraNonce", fmt.Sprintf("%08x", extraNonce4),
					"minerNonce", fmt.Sprintf("%08x", minerNonce4),
					"attempts", attempts, "hash", hash.Hex())

				newHeader := types.CopyHeader(header)
				newHeader.Nonce = types.EncodeNonce(nonce64)
				newHeader.MixDigest = hash

				// No need to store extraNonce in header.Extra
				// It's already encoded in the nonce (high 32 bits)

				select {
				case found <- block.WithSeal(newHeader):
					log.Debug("Sealed block sent to found channel")
					return
				case <-abort:
					log.Warn("Aborted while trying to send found block")
					return
				case <-stop:
					log.Warn("Stopped while trying to send found block")
					return
				}
			}

			// Increment nonce
			nonce64++
			attempts++

			// Log progress every 100000 attempts
			if attempts%100000 == 0 {
				log.Debug("Mining progress", "attempts", attempts, "nonce64", fmt.Sprintf("%016x", nonce64))
			}

			// Check abort every 1024 attempts
			if attempts%1024 == 0 {
				select {
				case <-abort:
					return
				case <-stop:
					return
				default:
				}
			}
		}
	}
}

// loop is the main event loop for the remote sealer.
func (s *remoteSealer) loop(randomx *RandomX) {
	defer func() {
		close(s.exitCh)
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.startCh:
			// Start notification, do nothing
		case work := <-s.workCh:
			if work == nil || work.block == nil {
				continue
			}

			s.mutex.Lock()

			if s.currentTask != nil && work.block.ParentHash() != s.currentTask.block.ParentHash() {
				s.mutex.Unlock()
				continue
			}

			if work.chain != nil {
				s.chain = work.chain
			}

			s.currentTask = work
			s.currentWork = s.makeWork(work.block)
			s.currentWork[0] = work.sealHash.Hex()
			s.works[work.sealHash] = work

			for _, ch := range s.notifyCtx {
				select {
				case ch <- s.currentWork:
				default:
				}
			}
			s.mutex.Unlock()

		case req := <-s.fetchWorkCh:
			// Fetch current work
			s.mutex.Lock()
			if s.currentTask == nil {
				s.mutex.Unlock()
				req.errc <- errNoMiningWork
				continue
			}
			req.res <- s.currentWork
			s.mutex.Unlock()

		case result := <-s.submitWorkCh:
			s.mutex.Lock()

			task := s.works[result.hash]
			if task == nil {
				s.mutex.Unlock()
				log.Warn("Work submitted but not found", "hash", result.hash)
				result.errc <- errInvalidSealResult
				continue
			}

			block := task.block
			resultsCh := task.results
			chain := task.chain
			if chain == nil {
				chain = s.chain
			}
			s.mutex.Unlock()

			if chain == nil {
				log.Error("Chain reference not available for PoW verification")
				result.errc <- errInvalidSealResult
				continue
			}

			header := types.CopyHeader(block.Header())
			header.Nonce = result.nonce
			header.MixDigest = result.mixDigest

			nonce64 := header.Nonce.Uint64()
			extraNonce4 := uint32(nonce64 >> 32)
			minerNonce4 := uint32(nonce64 & 0xFFFFFFFF)

			log.Debug("Remote work submitted", "nonce64", fmt.Sprintf("%016x", nonce64),
				"extraNonce", fmt.Sprintf("%08x", extraNonce4),
				"minerNonce", fmt.Sprintf("%08x", minerNonce4),
				"hash", result.hash.Hex())

			if err := randomx.verifyPoW(chain, header); err != nil {
				log.Warn("Invalid proof-of-work submitted", "err", err)
				result.errc <- errInvalidSealResult
				continue
			}

			sealed := block.WithSeal(header)

			s.mutex.Lock()
			delete(s.works, result.hash)
			if s.currentTask != nil && s.currentTask.sealHash == result.hash {
				s.currentTask = nil
				s.currentWork = [4]string{}
			}
			s.mutex.Unlock()

			if resultsCh != nil {
				select {
				case resultsCh <- sealed:
				default:
					log.Warn("Remote result channel full, dropping sealed block", "hash", result.hash)
				}
			}

			result.errc <- nil

		case req := <-s.submitRateCh:
			// Submit hashrate from remote miner
			s.mutex.Lock()
			s.rates[req.id] = hashrate{
				id:   req.id,
				ping: time.Now(),
				rate: req.rate,
				done: req.done,
			}
			s.mutex.Unlock()
			close(req.done)

		case req := <-s.fetchRateCh:
			// Fetch aggregate hashrate
			s.mutex.Lock()
			var total uint64
			for id, rate := range s.rates {
				// Remove stale hashrate reports (>10s old)
				if time.Since(rate.ping) > 10*time.Second {
					delete(s.rates, id)
					continue
				}
				total += rate.rate
			}
			s.mutex.Unlock()
			req <- total

		case hash := <-s.cancelCh:
			s.mutex.Lock()
			delete(s.works, hash)
			if s.currentTask != nil && s.currentTask.sealHash == hash {
				s.currentTask = nil
				s.currentWork = [4]string{}
			}
			s.mutex.Unlock()

		case <-ticker.C:
			// Clean up stale work
			s.mutex.Lock()
			if s.currentTask != nil && len(s.works) > 0 {
				currentNumber := s.currentTask.block.NumberU64()
				for hash, task := range s.works {
					if task.block.NumberU64()+10 < currentNumber {
						delete(s.works, hash)
					}
				}
			}
			s.mutex.Unlock()

		case <-s.requestExit:
			return
		}
	}
}

func (s *remoteSealer) cancel(hash common.Hash) {
	select {
	case s.cancelCh <- hash:
	default:
	}
}

// makeWork creates a work package for the given block.
func (s *remoteSealer) makeWork(block *types.Block) [4]string {
	hash := s.randomx.SealHash(block.Header())

	// Calculate the epoch-based seed hash for RandomX
	var seedHash common.Hash
	if s.chain != nil {
		seed, err := s.randomx.GetSeedHash(s.chain, block.Header().Number)
		if err == nil {
			seedHash = seed
		} else {
			log.Warn("Failed to get seed hash, using zero", "err", err)
			seedHash = common.Hash{}
		}
	} else {
		// Fallback: use parent hash (legacy behavior)
		seedHash = block.ParentHash()
	}

	// Calculate target = 2^256 / difficulty (mining boundary condition)
	target := new(big.Int).Div(maxUint256, block.Difficulty())

	return [4]string{
		hash.Hex(),                               // [0] Header hash (SealHash) - what to hash
		seedHash.Hex(),                           // [1] Seed hash (epoch-based RandomX seed) - RandomX key
		common.BytesToHash(target.Bytes()).Hex(), // [2] Target boundary (2^256/difficulty)
		hexutil.EncodeBig(block.Number()),        // [3] Block number
	}
}
