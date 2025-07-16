package logpoller

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	pkgerrors "github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	commontypes "github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-evm/pkg/client/clienttest"
	"github.com/smartcontractkit/chainlink-evm/pkg/heads/headstest"
	"github.com/smartcontractkit/chainlink-evm/pkg/logpoller/internal/log_emitter"
	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"
	evmtypes "github.com/smartcontractkit/chainlink-evm/pkg/types"
	"github.com/smartcontractkit/chainlink-evm/pkg/utils"
)

var (
	EmitterABI, _ = abi.JSON(strings.NewReader(log_emitter.LogEmitterABI))
)

// Validate that filters stored in log_filters_table match the filters stored in memory
func validateFiltersTable(t *testing.T, lp *logPoller, orm ORM) {
	ctx := testutils.Context(t)
	filters, err := orm.LoadFilters(ctx)
	require.NoError(t, err)
	require.Equal(t, len(filters), len(lp.filters))
	for name, dbFilter := range filters {
		dbFilter := dbFilter
		memFilter, ok := lp.filters[name]
		require.True(t, ok)
		assert.Truef(t, memFilter.Contains(&dbFilter),
			"in-memory Filter %s is missing some addresses or events from db Filter table", name)
		assert.Truef(t, dbFilter.Contains(&memFilter), "db Filter table %s is missing some addresses or events from in-memory Filter", name)
	}
}

func TestLogPoller_RegisterFilter(t *testing.T) {
	t.Parallel()
	a1 := common.HexToAddress("0x2ab9a2dc53736b361b72d900cdf9f78f9406fbbb")
	a2 := common.HexToAddress("0x2ab9a2dc53736b361b72d900cdf9f78f9406fbbc")

	lggr, observedLogs := logger.TestObserved(t, zapcore.WarnLevel)
	chainID := testutils.NewRandomEVMChainID()
	db := testutils.NewSqlxDB(t)
	ctx := testutils.Context(t)

	orm := NewORM(chainID, db, lggr)

	// Set up a test chain with a log emitting contract deployed.
	lpOpts := Opts{
		PollPeriod:               time.Hour,
		BackfillBatchSize:        1,
		RPCBatchSize:             2,
		KeepFinalizedBlocksDepth: 1000,
	}
	lp := NewLogPoller(orm, nil, lggr, nil, lpOpts)

	// We expect a zero Filter if nothing registered yet.
	f := lp.Filter(nil, nil, nil)
	require.Len(t, f.Addresses, 1)
	assert.Equal(t, common.HexToAddress("0x0000000000000000000000000000000000000000"), f.Addresses[0])

	err := lp.RegisterFilter(ctx, Filter{Name: "Emitter Log 1", EventSigs: []common.Hash{EmitterABI.Events["Log1"].ID}, Addresses: []common.Address{a1}})
	require.NoError(t, err)
	assert.Equal(t, []common.Address{a1}, lp.Filter(nil, nil, nil).Addresses)
	assert.Equal(t, [][]common.Hash{{EmitterABI.Events["Log1"].ID}}, lp.Filter(nil, nil, nil).Topics)
	validateFiltersTable(t, lp, orm)

	// Should de-dupe EventSigs
	err = lp.RegisterFilter(ctx, Filter{Name: "Emitter Log 1 + 2", EventSigs: []common.Hash{EmitterABI.Events["Log1"].ID, EmitterABI.Events["Log2"].ID}, Addresses: []common.Address{a2}})
	require.NoError(t, err)
	assert.Equal(t, []common.Address{a1, a2}, lp.Filter(nil, nil, nil).Addresses)
	assert.Equal(t, [][]common.Hash{{EmitterABI.Events["Log1"].ID, EmitterABI.Events["Log2"].ID}}, lp.Filter(nil, nil, nil).Topics)
	validateFiltersTable(t, lp, orm)

	// Should de-dupe Addresses
	err = lp.RegisterFilter(ctx, Filter{Name: "Emitter Log 1 + 2 dupe", EventSigs: []common.Hash{EmitterABI.Events["Log1"].ID, EmitterABI.Events["Log2"].ID}, Addresses: []common.Address{a2}})
	require.NoError(t, err)
	assert.Equal(t, []common.Address{a1, a2}, lp.Filter(nil, nil, nil).Addresses)
	assert.Equal(t, [][]common.Hash{{EmitterABI.Events["Log1"].ID, EmitterABI.Events["Log2"].ID}}, lp.Filter(nil, nil, nil).Topics)
	validateFiltersTable(t, lp, orm)

	// Address required.
	err = lp.RegisterFilter(ctx, Filter{Name: "no address", EventSigs: []common.Hash{EmitterABI.Events["Log1"].ID}})
	require.Error(t, err)
	// Event required
	err = lp.RegisterFilter(ctx, Filter{Name: "No event", Addresses: []common.Address{a1}})
	require.Error(t, err)
	validateFiltersTable(t, lp, orm)

	// Removing non-existence Filter should log error but return nil
	err = lp.UnregisterFilter(ctx, "Filter doesn't exist")
	require.NoError(t, err)
	require.Equal(t, 1, observedLogs.Len())
	require.Contains(t, observedLogs.TakeAll()[0].Entry.Message, "not found")

	// Check that all filters are still there
	_, ok := lp.filters["Emitter Log 1"]
	require.True(t, ok, "'Emitter Log 1 Filter' missing")
	_, ok = lp.filters["Emitter Log 1 + 2"]
	require.True(t, ok, "'Emitter Log 1 + 2' Filter missing")
	_, ok = lp.filters["Emitter Log 1 + 2 dupe"]
	require.True(t, ok, "'Emitter Log 1 + 2 dupe' Filter missing")

	// Removing an existing Filter should remove it from both memory and db
	err = lp.UnregisterFilter(ctx, "Emitter Log 1 + 2")
	require.NoError(t, err)
	_, ok = lp.filters["Emitter Log 1 + 2"]
	require.False(t, ok, "'Emitter Log 1 Filter' should have been removed by UnregisterFilter()")
	require.Len(t, lp.filters, 2)
	validateFiltersTable(t, lp, orm)

	err = lp.UnregisterFilter(ctx, "Emitter Log 1 + 2 dupe")
	require.NoError(t, err)
	err = lp.UnregisterFilter(ctx, "Emitter Log 1")
	require.NoError(t, err)
	assert.Empty(t, lp.filters)
	filters, err := lp.orm.LoadFilters(ctx)
	require.NoError(t, err)
	assert.Empty(t, filters)

	// Make sure cache was invalidated
	assert.Len(t, lp.Filter(nil, nil, nil).Addresses, 1)
	assert.Equal(t, lp.Filter(nil, nil, nil).Addresses[0], common.HexToAddress("0x0000000000000000000000000000000000000000"))
	assert.Len(t, lp.Filter(nil, nil, nil).Topics, 1)
	assert.Empty(t, lp.Filter(nil, nil, nil).Topics[0])
}

func TestLogPoller_ConvertLogs(t *testing.T) {
	t.Parallel()
	lggr := logger.Test(t)

	topics := []common.Hash{EmitterABI.Events["Log1"].ID}

	var cases = []struct {
		name     string
		logs     []types.Log
		blocks   []Block
		expected int
	}{
		{"SingleBlock",
			[]types.Log{{Topics: topics}, {Topics: topics}},
			[]Block{{BlockTimestamp: time.Now()}},
			2},
		{"BlockList",
			[]types.Log{{Topics: topics}, {Topics: topics}, {Topics: topics}},
			[]Block{{BlockTimestamp: time.Now()}},
			3},
		{"EmptyList",
			[]types.Log{},
			[]Block{},
			0},
		{"TooManyBlocks",
			[]types.Log{{}},
			[]Block{{}, {}},
			0},
		{"TooFewBlocks",
			[]types.Log{{}, {}, {}},
			[]Block{{}, {}},
			0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logs := convertLogs(c.logs, c.blocks, lggr, big.NewInt(53))
			require.Len(t, logs, c.expected)
			for i := 0; i < c.expected; i++ {
				if len(c.blocks) == 1 {
					assert.Equal(t, c.blocks[0].BlockTimestamp, logs[i].BlockTimestamp)
				} else {
					assert.Equal(t, logs[i].BlockTimestamp, c.blocks[i].BlockTimestamp)
				}
			}
		})
	}
}

func TestFilterName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "a - b:c:d", FilterName("a", "b", "c", "d"))
	assert.Equal(t, "empty args test", FilterName("empty args test"))
}

func TestLogPoller_BackupPollerStartup(t *testing.T) {
	t.Parallel()

	t.Run("assert backup poller (safe tag < finalized < latest)", func(t *testing.T) {
		latestBlock := int64(4)
		const finalityDepth = 2
		head := &evmtypes.Head{Number: latestBlock}
		finalizedHead := &evmtypes.Head{Number: latestBlock - finalityDepth}
		safeHead := &evmtypes.Head{Number: latestBlock - finalityDepth - 1} // forcing safe head to be lower than finalized head
		expectedSafeBlockNumber := finalizedHead.Number
		assertBackupPollerStartup(t, head, finalizedHead, safeHead, finalityDepth, expectedSafeBlockNumber)
	})

	t.Run("assert backup poller (finalized < safe tag < latest)", func(t *testing.T) {
		latestBlock := int64(4)
		const finalityDepth = 2
		head := &evmtypes.Head{Number: latestBlock}
		safeHead := &evmtypes.Head{Number: latestBlock - 1} // forcing safe head to be lower than latest head but higher than finalized head
		finalizedHead := &evmtypes.Head{Number: latestBlock - finalityDepth}
		expectedSafeBlockNumber := safeHead.Number
		assertBackupPollerStartup(t, head, finalizedHead, safeHead, finalityDepth, expectedSafeBlockNumber)
	})
}

func assertBackupPollerStartup(t *testing.T, head *evmtypes.Head, finalizedHead *evmtypes.Head, safeHead *evmtypes.Head, finalityDepth int64, expectedSafeBlockNumber int64) {
	addr := common.HexToAddress("0x2ab9a2dc53736b361b72d900cdf9f78f9406fbbc")
	lggr, observedLogs := logger.TestObserved(t, zapcore.WarnLevel)
	chainID := testutils.FixtureChainID
	db := testutils.NewSqlxDB(t)
	orm := NewORM(chainID, db, lggr)

	events := []common.Hash{EmitterABI.Events["Log1"].ID}
	log1 := types.Log{
		Index:       0,
		BlockHash:   common.Hash{},
		BlockNumber: uint64(head.Number), //nolint:gosec // G115
		Topics:      events,
		Address:     addr,
		TxHash:      common.HexToHash("0x1234"),
		Data:        EvmWord(uint64(300)).Bytes(),
	}

	ec := clienttest.NewClient(t)
	ec.On("FilterLogs", mock.Anything, mock.Anything).Return([]types.Log{log1}, nil)
	ec.On("ConfiguredChainID").Return(chainID, nil)

	headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
	headTracker.On("LatestAndFinalizedBlock", mock.Anything).Return(head, finalizedHead, nil)
	headTracker.On("LatestSafeBlock", mock.Anything).Return(safeHead, nil)

	ctx := testutils.Context(t)
	lpOpts := Opts{
		PollPeriod:               time.Hour,
		FinalityDepth:            finalityDepth,
		BackfillBatchSize:        3,
		RPCBatchSize:             2,
		KeepFinalizedBlocksDepth: 1000,
		BackupPollerBlockDelay:   0,
	}
	lp := NewLogPoller(orm, ec, lggr, headTracker, lpOpts)
	require.NoError(t, lp.BackupPollAndSaveLogs(ctx))
	assert.Equal(t, int64(0), lp.backupPollerNextBlock)
	assert.Equal(t, 1, observedLogs.FilterMessageSnippet("ran before first successful log poller run").Len())

	lp.PollAndSaveLogs(ctx, head.Number)

	lastProcessed, err := lp.orm.SelectLatestBlock(ctx)
	require.NoError(t, err)
	require.Equal(t, head.Number, lastProcessed.BlockNumber)
	require.Equal(t, expectedSafeBlockNumber, lastProcessed.SafeBlockNumber)

	require.NoError(t, lp.BackupPollAndSaveLogs(ctx))
	assert.Equal(t, int64(2), lp.backupPollerNextBlock)
}

func mockBatchCallContext(t *testing.T, ec *clienttest.Client) {
	mockBatchCallContextWithHead(t, ec, newHeadVal)
}

func newHeadVal(num int64) evmtypes.Head {
	return evmtypes.Head{
		Number:     num,
		Hash:       common.BigToHash(big.NewInt(num)),
		ParentHash: common.BigToHash(big.NewInt(num - 1)),
	}
}

func newHead(num int64) *evmtypes.Head {
	r := newHeadVal(num)
	return &r
}

func mockBatchCallContextWithHead(t *testing.T, ec *clienttest.Client, newHead func(num int64) evmtypes.Head) {
	ec.On("BatchCallContext", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		elems := args.Get(1).([]rpc.BatchElem)
		for _, e := range elems {
			var num int64
			block := e.Args[0].(string)
			switch block {
			case "latest":
				num = 8
			case "finalized":
				num = 5
			default:
				n, err := hexutil.DecodeUint64(block)
				require.NoError(t, err)
				num = int64(n)
			}
			result := e.Result.(*evmtypes.Head)
			*result = newHead(num)
		}
	})
}

func TestLogPoller_Replay(t *testing.T) {
	t.Parallel()
	addr := common.HexToAddress("0x2ab9a2dc53736b361b72d900cdf9f78f9406fbbc")

	lggr, observedLogs := logger.TestObserved(t, zapcore.ErrorLevel)
	chainID := testutils.FixtureChainID
	db := testutils.NewSqlxDB(t)
	orm := NewORM(chainID, db, lggr)

	var head atomic.Pointer[evmtypes.Head]
	head.Store(newHead(4))

	events := []common.Hash{EmitterABI.Events["Log1"].ID}
	log1 := types.Log{
		Index:       0,
		BlockHash:   common.BigToHash(big.NewInt(head.Load().Number)),
		BlockNumber: uint64(head.Load().Number),
		Topics:      events,
		Address:     addr,
		TxHash:      common.HexToHash("0x1234"),
		Data:        EvmWord(uint64(300)).Bytes(),
	}

	ec := clienttest.NewClient(t)
	ec.EXPECT().HeadByHash(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, hash common.Hash) (*evmtypes.Head, error) {
		return &evmtypes.Head{Number: hash.Big().Int64(), Hash: hash}, nil
	}).Maybe()
	ec.On("HeadByNumber", mock.Anything, mock.Anything).Return(func(context.Context, *big.Int) (*evmtypes.Head, error) {
		return head.Load(), nil
	})
	ec.On("FilterLogs", mock.Anything, mock.Anything).Return([]types.Log{log1}, nil).Once()
	ec.On("ConfiguredChainID").Return(chainID, nil)

	lpOpts := Opts{
		PollPeriod:               time.Second,
		FinalityDepth:            3,
		BackfillBatchSize:        3,
		RPCBatchSize:             3,
		KeepFinalizedBlocksDepth: 20,
		BackupPollerBlockDelay:   0,
	}
	headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)

	headTracker.On("LatestAndFinalizedBlock", mock.Anything).Return(func(ctx context.Context) (*evmtypes.Head, *evmtypes.Head, error) {
		h := head.Load()
		finalized := newHead(h.Number - lpOpts.FinalityDepth)
		return h, finalized, nil
	})
	safe := newHead(5)
	headTracker.EXPECT().LatestSafeBlock(mock.Anything).Return(safe, nil)
	lp := NewLogPoller(orm, ec, lggr, headTracker, lpOpts)

	{
		ctx := testutils.Context(t)
		// process 1 log in block 3
		lp.PollAndSaveLogs(ctx, 4)
		latest, err := lp.LatestBlock(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(4), latest.BlockNumber)
		require.Equal(t, int64(1), latest.FinalizedBlockNumber)
	}

	t.Run("abort before replayStart received", func(t *testing.T) {
		// Replay() should abort immediately if caller's context is cancelled before request signal is read
		cancelCtx, cancel := context.WithCancel(testutils.Context(t))
		cancel()
		err := lp.Replay(cancelCtx, 3)
		assert.ErrorIs(t, err, ErrReplayRequestAborted)
	})

	recvStartReplay := func(ctx context.Context, block int64) {
		select {
		case fromBlock := <-lp.replayStart:
			assert.Equal(t, block, fromBlock)
		case <-ctx.Done():
			assert.NoError(t, ctx.Err(), "Timed out waiting to receive replay request from lp.replayStart")
		}
	}

	// Replay() should return error code received from replayComplete
	t.Run("returns error code on replay complete", func(t *testing.T) {
		ctx := testutils.Context(t)
		ec.On("FilterLogs", mock.Anything, mock.Anything).Return([]types.Log{log1}, nil).Once()
		mockBatchCallContext(t, ec)
		anyErr := pkgerrors.New("any error")
		done := make(chan struct{})
		go func() {
			defer close(done)
			recvStartReplay(ctx, 2)
			lp.replayComplete <- anyErr
		}()
		assert.ErrorIs(t, lp.Replay(ctx, 1), anyErr)
		<-done
	})

	// Replay() should return ErrReplayInProgress if caller's context is cancelled after replay has begun
	t.Run("late abort returns ErrReplayInProgress", func(t *testing.T) {
		cancelCtx, cancel := context.WithTimeout(testutils.Context(t), time.Second) // Intentionally abort replay after 1s
		done := make(chan struct{})
		go func() {
			defer close(done)
			recvStartReplay(cancelCtx, 4)
			cancel()
		}()
		assert.ErrorIs(t, lp.Replay(cancelCtx, 4), ErrReplayInProgress)
		<-done
		lp.replayComplete <- nil
		lp.wg.Wait()
	})

	// Main lp.run() loop shouldn't get stuck if client aborts
	t.Run("client abort doesnt hang run loop", func(t *testing.T) {
		ctx := testutils.Context(t)
		lp.backupPollerNextBlock = 0

		pass := make(chan struct{})
		cancelled := make(chan struct{})

		rctx, rcancel := context.WithCancel(testutils.Context(t))
		var wg sync.WaitGroup
		defer func() { wg.Wait() }()
		ec.On("FilterLogs", mock.Anything, mock.Anything).Once().Return([]types.Log{log1}, nil).Run(func(args mock.Arguments) {
			head.Store(&evmtypes.Head{Number: 4})
			wg.Add(1)
			go func() {
				defer wg.Done()
				assert.ErrorIs(t, lp.Replay(rctx, 4), ErrReplayInProgress)
				close(cancelled)
			}()
		})
		ec.On("FilterLogs", mock.Anything, mock.Anything).Once().Return([]types.Log{log1}, nil).Run(func(args mock.Arguments) {
			rcancel()
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case lp.replayStart <- 4:
					close(pass)
				case <-ctx.Done():
					return
				}
			}()
			// We cannot return until we're sure that Replay() received the cancellation signal,
			// otherwise replayComplete<- might be sent first
			<-cancelled
		})

		ec.On("FilterLogs", mock.Anything, mock.Anything).Return([]types.Log{log1}, nil).Maybe() // in case task gets delayed by >= 100ms

		head.Store(newHead(5))
		t.Cleanup(lp.reset)
		servicetest.Run(t, lp)

		select {
		case <-ctx.Done():
			t.Errorf("timed out waiting for lp.run() to respond to second replay event")
		case <-pass:
		}
	})

	// remove Maybe expectation from prior subtest, as it will override all expected calls in future subtests
	ec.On("FilterLogs", mock.Anything, mock.Anything).Unset()

	// run() should abort if log poller shuts down while replay is in progress
	t.Run("shutdown during replay", func(t *testing.T) {
		ctx := testutils.Context(t)
		lp.backupPollerNextBlock = 0

		pass := make(chan struct{})
		done := make(chan struct{})
		defer func() { <-done }()

		ec.On("FilterLogs", mock.Anything, mock.Anything).Once().Return([]types.Log{log1}, nil).Run(func(args mock.Arguments) {
			go func() {
				defer close(done)

				head.Store(newHead(4)) // Restore latest block to 4, so this matches the fromBlock requested
				select {
				case lp.replayStart <- 4:
				case <-ctx.Done():
				}
			}()
		})
		ec.On("FilterLogs", mock.Anything, mock.Anything).Once().Return([]types.Log{log1}, nil).Run(func(args mock.Arguments) {
			go func() {
				assert.NoError(t, lp.Close())

				// prevent double close
				lp.reset()
				assert.NoError(t, lp.Start(ctx))

				close(pass)
			}()
		})
		ec.On("FilterLogs", mock.Anything, mock.Anything).Return([]types.Log{log1}, nil)

		t.Cleanup(lp.reset)
		head.Store(newHead(6)) // Latest block must be > lastProcessed in order for SaveAndPollLogs() to call FilterLogs()
		servicetest.Run(t, lp)

		select {
		case <-ctx.Done():
			t.Error("timed out waiting for lp.run() to respond to shutdown event during replay")
		case <-pass:
		}
	})

	// ReplayAsync should return as soon as replayStart is received
	t.Run("ReplayAsync success", func(t *testing.T) {
		t.Cleanup(lp.reset)

		head.Store(&evmtypes.Head{Number: 5})
		ec.On("FilterLogs", mock.Anything, mock.Anything).Return([]types.Log{log1}, nil)
		mockBatchCallContext(t, ec)
		servicetest.Run(t, lp)

		lp.ReplayAsync(1)

		recvStartReplay(testutils.Context(t), 4)
	})

	t.Run("ReplayAsync error", func(t *testing.T) {
		ctx := testutils.Context(t)
		t.Cleanup(lp.reset)
		servicetest.Run(t, lp)
		head.Store(&evmtypes.Head{Number: 4})

		anyErr := pkgerrors.New("async error")
		observedLogs.TakeAll()

		lp.ReplayAsync(4)
		recvStartReplay(testutils.Context(t), 4)

		select {
		case lp.replayComplete <- anyErr:
			time.Sleep(2 * time.Second)
		case <-ctx.Done():
			t.Error("timed out waiting to send replaceComplete")
		}
		require.Equal(t, 1, observedLogs.Len())
		assert.Equal(t, observedLogs.All()[0].Message, anyErr.Error())
	})

	t.Run("run regular replay when there are not blocks in db", func(t *testing.T) {
		ctx := testutils.Context(t)
		err := lp.orm.DeleteLogsAndBlocksAfter(ctx, 0)
		require.NoError(t, err)

		lp.ReplayAsync(1)
		recvStartReplay(testutils.Context(t), 1)
	})

	t.Run("run only backfill when everything is finalized", func(t *testing.T) {
		ctx := testutils.Context(t)
		err := lp.orm.DeleteLogsAndBlocksAfter(ctx, 0)
		require.NoError(t, err)

		h := head.Load()
		err = lp.orm.InsertBlock(ctx, common.BigToHash(big.NewInt(h.Number)), h.Number, h.Timestamp, h.Number, h.Number)
		require.NoError(t, err)

		ec.On("FilterLogs", mock.Anything, mock.Anything).Return([]types.Log{log1}, nil)
		mockBatchCallContext(t, ec)

		err = lp.Replay(ctx, 1)
		require.NoError(t, err)
	})
}

func (lp *logPoller) reset() {
	lp.StateMachine = services.StateMachine{}
	lp.stopCh = make(chan struct{})
}

func Test_latestBlockAndFinalityDepth(t *testing.T) {
	lggr := logger.Test(t)

	lpOpts := Opts{
		PollPeriod:               time.Hour,
		BackfillBatchSize:        3,
		RPCBatchSize:             3,
		KeepFinalizedBlocksDepth: 20,
	}

	t.Run("headTracker returns an error", func(t *testing.T) {
		headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
		const expectedError = "finalized block is not available yet"
		headTracker.On("LatestAndFinalizedBlock", mock.Anything).Return(&evmtypes.Head{}, &evmtypes.Head{}, errors.New(expectedError))

		lp := NewLogPoller(nil, nil, lggr, headTracker, lpOpts)
		_, _, err := lp.latestBlocks(t.Context())
		require.ErrorContains(t, err, expectedError)
	})
	t.Run("headTracker returns valid chain", func(t *testing.T) {
		headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
		finalizedBlock := &evmtypes.Head{Number: 2}
		finalizedBlock.IsFinalized.Store(true)
		head := &evmtypes.Head{Number: 10}
		headTracker.On("LatestAndFinalizedBlock", mock.Anything).Return(head, finalizedBlock, nil)

		lp := NewLogPoller(nil, nil, lggr, headTracker, lpOpts)
		latestBlock, finalizedBlockNumber, err := lp.latestBlocks(t.Context())
		require.NoError(t, err)
		require.NotNil(t, latestBlock)
		assert.Equal(t, head.BlockNumber(), latestBlock.BlockNumber())
		assert.Equal(t, finalizedBlock.Number, finalizedBlockNumber)
	})
}

func Test_latestSafeBlocks(t *testing.T) {
	lggr := logger.Test(t)

	lpOpts := Opts{
		PollPeriod:               time.Hour,
		BackfillBatchSize:        3,
		RPCBatchSize:             3,
		KeepFinalizedBlocksDepth: 20,
	}

	t.Run("headTracker returns an error", func(t *testing.T) {
		headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
		const expectedError = "safe block is not available yet"
		headTracker.On("LatestSafeBlock", mock.Anything).Return(&evmtypes.Head{}, errors.New(expectedError))

		lp := NewLogPoller(nil, nil, lggr, headTracker, lpOpts)
		_, err := lp.latestSafeBlock(t.Context(), 0)
		require.ErrorContains(t, err, expectedError)
	})
	t.Run("headTracker returns valid chain", func(t *testing.T) {
		headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
		safeBlock := &evmtypes.Head{Number: 2}
		headTracker.On("LatestSafeBlock", mock.Anything).Return(safeBlock, nil)

		lp := NewLogPoller(nil, nil, lggr, headTracker, lpOpts)
		safeBlockNumber, err := lp.latestSafeBlock(t.Context(), 1)
		require.NoError(t, err)
		assert.Equal(t, safeBlock.Number, safeBlockNumber)
	})
	t.Run("headTracker returns valid chain but safe is lower than finalized", func(t *testing.T) {
		headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
		safeBlock := &evmtypes.Head{Number: 2}
		headTracker.On("LatestSafeBlock", mock.Anything).Return(safeBlock, nil)
		latestFinalizedBlockNumber := int64(3)

		lp := NewLogPoller(nil, nil, lggr, headTracker, lpOpts)
		safeBlockNumber, err := lp.latestSafeBlock(t.Context(), latestFinalizedBlockNumber)
		require.NoError(t, err)
		assert.Equal(t, latestFinalizedBlockNumber, safeBlockNumber)
	})
}

func Test_FetchBlocks(t *testing.T) {
	lggr := logger.Test(t)
	chainID := testutils.FixtureChainID
	db := testutils.NewSqlxDB(t)
	orm := NewORM(chainID, db, lggr)
	ctx := testutils.Context(t)

	lpOpts := Opts{
		PollPeriod:               time.Hour,
		BackfillBatchSize:        2,
		RPCBatchSize:             2,
		KeepFinalizedBlocksDepth: 50,
		FinalityDepth:            3,
	}

	ec := clienttest.NewClient(t)
	mockBatchCallContext(t, ec) // This will return 5 for "finalized" and 8 for "latest"

	cases := []struct {
		name            string
		blocksRequested []uint64
		chainReference  *Block
		expectedErr     error
	}{
		{
			"All blocks are finalized from RPC's perspective, no reference",
			[]uint64{2, 5, 3, 4},
			nil,
			nil,
		},
		{
			"RPC's latest finalized is lower than request, no reference",
			[]uint64{8, 2},
			nil,
			errors.New("received unfinalized block 8 while expecting finalized block (latestFinalizedBlockNumber = 5)"),
		},
		{
			"All blocks are finalized, but chain reference does not match",
			[]uint64{2, 5, 3, 4},
			&Block{BlockNumber: 1, BlockHash: common.BigToHash(big.NewInt(2))},
			errors.New("expected RPC's finalized block hash at hegiht 1 to be 0x0000000000000000000000000000000000000000000000000000000000000002 but got 0x0000000000000000000000000000000000000000000000000000000000000001: finality violated"),
		},
		{
			"All blocks are finalized and chain reference matches",
			[]uint64{2, 5, 3, 4},
			&Block{BlockNumber: 1, BlockHash: common.BigToHash(big.NewInt(1))},
			nil,
		},
	}

	lp := NewLogPoller(orm, ec, lggr, nil, lpOpts)
	for _, tc := range cases {
		for _, lp.useFinalityTag = range []bool{false, true} {
			blockValidationReq := latestBlock
			if lp.useFinalityTag {
				blockValidationReq = finalizedBlock
			}
			t.Run(fmt.Sprintf("%s where useFinalityTag=%t", tc.name, lp.useFinalityTag), func(t *testing.T) {
				blocks, err := lp.fetchBlocks(ctx, tc.blocksRequested, blockValidationReq, tc.chainReference)
				if tc.expectedErr != nil {
					require.Equal(t, err.Error(), tc.expectedErr.Error())
					return // PASS
				}
				require.NoError(t, err)
				for _, blockRequested := range tc.blocksRequested {
					assert.Equal(t, blockRequested, uint64(blocks[blockRequested].Number)) //nolint:gosec // G115
				}
			})
		}
	}
}

func Test_PollAndSaveLogs_BackfillFinalityViolation(t *testing.T) {
	t.Parallel()

	db := testutils.NewSqlxDB(t)
	lpOpts := Opts{
		PollPeriod:               time.Second,
		FinalityDepth:            3,
		BackfillBatchSize:        3,
		RPCBatchSize:             3,
		KeepFinalizedBlocksDepth: 20,
		BackupPollerBlockDelay:   0,
	}
	t.Run("Finalized DB block is not present in RPC's chain", func(t *testing.T) {
		lggr, _ := logger.TestObserved(t, zapcore.ErrorLevel)
		orm := NewORM(testutils.NewRandomEVMChainID(), db, lggr)
		headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
		finalized := newHead(5)
		latest := newHead(16)
		headTracker.EXPECT().LatestAndFinalizedBlock(mock.Anything).RunAndReturn(func(ctx context.Context) (*evmtypes.Head, *evmtypes.Head, error) {
			return latest, finalized, nil
		}).Once()
		headTracker.EXPECT().LatestSafeBlock(mock.Anything).Return(finalized, nil).Once()
		ec := clienttest.NewClient(t)
		ec.EXPECT().HeadByNumber(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, number *big.Int) (*evmtypes.Head, error) {
			return newHead(number.Int64()), nil
		})
		ec.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{{BlockNumber: 5}}, nil).Once()
		mockBatchCallContext(t, ec)
		// insert finalized block with different hash than in RPC
		require.NoError(t, orm.InsertBlock(t.Context(), common.HexToHash("0x123"), 2, time.Unix(10, 0), 2, 2))
		lp := NewLogPoller(orm, ec, lggr, headTracker, lpOpts)
		lp.PollAndSaveLogs(t.Context(), 4)
		require.ErrorIs(t, lp.HealthReport()[lp.Name()], commontypes.ErrFinalityViolated)
	})
	t.Run("RPCs contradict each other and return different finalized blocks", func(t *testing.T) {
		lggr, _ := logger.TestObserved(t, zapcore.ErrorLevel)
		orm := NewORM(testutils.NewRandomEVMChainID(), db, lggr)
		headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
		finalized := newHead(5)
		latest := newHead(16)
		headTracker.EXPECT().LatestAndFinalizedBlock(mock.Anything).Return(latest, finalized, nil).Once()
		headTracker.EXPECT().LatestSafeBlock(mock.Anything).Return(finalized, nil).Once()
		ec := clienttest.NewClient(t)
		ec.EXPECT().HeadByNumber(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, number *big.Int) (*evmtypes.Head, error) {
			return newHead(number.Int64()), nil
		})
		ec.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{{BlockNumber: 5}}, nil).Once()
		mockBatchCallContextWithHead(t, ec, func(num int64) evmtypes.Head {
			// return new hash for every call
			return evmtypes.Head{Number: num, Hash: utils.NewHash()}
		})
		lp := NewLogPoller(orm, ec, lggr, headTracker, lpOpts)
		lp.PollAndSaveLogs(t.Context(), 4)
		require.ErrorIs(t, lp.HealthReport()[lp.Name()], commontypes.ErrFinalityViolated)
	})
	t.Run("Log's hash does not match block's", func(t *testing.T) {
		lggr, _ := logger.TestObserved(t, zapcore.ErrorLevel)
		orm := NewORM(testutils.NewRandomEVMChainID(), db, lggr)
		headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
		finalized := newHead(5)
		latest := newHead(16)
		headTracker.EXPECT().LatestAndFinalizedBlock(mock.Anything).Return(latest, finalized, nil).Once()
		headTracker.EXPECT().LatestSafeBlock(mock.Anything).Return(finalized, nil).Once()
		ec := clienttest.NewClient(t)
		ec.EXPECT().HeadByNumber(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, number *big.Int) (*evmtypes.Head, error) {
			return newHead(number.Int64()), nil
		})
		ec.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{{BlockNumber: 5, BlockHash: common.HexToHash("0x123")}}, nil).Once()
		mockBatchCallContext(t, ec)
		lp := NewLogPoller(orm, ec, lggr, headTracker, lpOpts)
		lp.PollAndSaveLogs(t.Context(), 4)
		require.ErrorIs(t, lp.HealthReport()[lp.Name()], commontypes.ErrFinalityViolated)
	})
	t.Run("Happy path", func(t *testing.T) {
		lggr, _ := logger.TestObserved(t, zapcore.ErrorLevel)
		chainID := testutils.NewRandomEVMChainID()
		orm := NewORM(chainID, db, lggr)
		headTracker := headstest.NewTracker[*evmtypes.Head, common.Hash](t)
		finalized := newHead(5)
		latest := newHead(16)
		headTracker.EXPECT().LatestAndFinalizedBlock(mock.Anything).Return(latest, finalized, nil).Once()
		headTracker.EXPECT().LatestSafeBlock(mock.Anything).Return(finalized, nil).Once()
		ec := clienttest.NewClient(t)
		ec.EXPECT().ConfiguredChainID().Return(chainID)
		ec.EXPECT().HeadByNumber(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, number *big.Int) (*evmtypes.Head, error) {
			return newHead(number.Int64()), nil
		})
		ec.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]types.Log{{BlockNumber: 5, BlockHash: common.BigToHash(big.NewInt(5)), Topics: []common.Hash{{}}}}, nil).Once()
		mockBatchCallContext(t, ec)
		lp := NewLogPoller(orm, ec, lggr, headTracker, lpOpts)
		lp.PollAndSaveLogs(t.Context(), 4)
		require.NoError(t, lp.HealthReport()[lp.Name()])
	})
}

func benchmarkFilter(b *testing.B, nFilters, nAddresses, nEvents int) {
	lggr := logger.Test(b)
	lpOpts := Opts{
		PollPeriod:               time.Hour,
		FinalityDepth:            2,
		BackfillBatchSize:        3,
		RPCBatchSize:             2,
		KeepFinalizedBlocksDepth: 1000,
	}
	lp := NewLogPoller(nil, nil, lggr, nil, lpOpts)
	for i := 0; i < nFilters; i++ {
		var addresses []common.Address
		var events []common.Hash
		for j := 0; j < nAddresses; j++ {
			addresses = append(addresses, common.BigToAddress(big.NewInt(int64(j+1))))
		}
		for j := 0; j < nEvents; j++ {
			events = append(events, common.BigToHash(big.NewInt(int64(j+1))))
		}
		err := lp.RegisterFilter(testutils.Context(b), Filter{Name: "my Filter", EventSigs: events, Addresses: addresses})
		require.NoError(b, err)
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		lp.Filter(nil, nil, nil)
	}
}

func BenchmarkFilter10_1(b *testing.B) {
	benchmarkFilter(b, 10, 1, 1)
}
func BenchmarkFilter100_10(b *testing.B) {
	benchmarkFilter(b, 100, 10, 10)
}
func BenchmarkFilter1000_100(b *testing.B) {
	benchmarkFilter(b, 1000, 100, 100)
}
