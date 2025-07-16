package heads_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"

	evmheads "github.com/smartcontractkit/chainlink-evm/pkg/heads"

	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"
	evmtypes "github.com/smartcontractkit/chainlink-evm/pkg/types"
	"github.com/smartcontractkit/chainlink-evm/pkg/utils"
	ubig "github.com/smartcontractkit/chainlink-evm/pkg/utils/big"
)

type trackerConfig struct {
	historyDepth uint32
}

func (h *trackerConfig) HistoryDepth() uint32 {
	return h.historyDepth
}

func (h *trackerConfig) SamplingInterval() time.Duration {
	return time.Duration(0)
}

func (h *trackerConfig) MaxBufferSize() uint32 {
	return uint32(0)
}

func (h *trackerConfig) FinalityTagBypass() bool {
	return false
}
func (h *trackerConfig) MaxAllowedFinalityDepth() uint32 {
	return 10000
}
func (h *trackerConfig) PersistenceEnabled() bool {
	return true
}
func (h *trackerConfig) PersistenceBatchSize() int64 {
	return 0
}

type config struct {
	safeBlockDepth                    uint32
	finalityDepth                     uint32
	blockEmissionIdleWarningThreshold time.Duration
	finalityTagEnabled                bool
	finalizedBlockOffset              uint32
}

func (c *config) SafeDepth() uint32     { return c.safeBlockDepth }
func (c *config) FinalityDepth() uint32 { return c.finalityDepth }
func (c *config) BlockEmissionIdleWarningThreshold() time.Duration {
	return c.blockEmissionIdleWarningThreshold
}

func (c *config) FinalityTagEnabled() bool {
	return c.finalityTagEnabled
}

func (c *config) FinalizedBlockOffset() uint32 {
	return c.finalizedBlockOffset
}

type saverOpts struct {
	headTrackerConfig *trackerConfig
}

func configureSaver(t *testing.T, opts saverOpts) (evmheads.HeadSaver, evmheads.ORM) {
	if opts.headTrackerConfig == nil {
		opts.headTrackerConfig = &trackerConfig{historyDepth: 6}
	}
	db := testutils.NewSqlxDB(t)
	lggr := logger.Test(t)
	htCfg := &config{finalityDepth: uint32(1)}
	orm := evmheads.NewORM(*testutils.FixtureChainID, db, 0)
	saver := evmheads.NewSaver(lggr, orm, htCfg, opts.headTrackerConfig)
	return saver, orm
}

func TestHeadSaver_Save(t *testing.T) {
	t.Parallel()

	saver, _ := configureSaver(t, saverOpts{})

	head := testutils.Head(1)
	err := saver.Save(tests.Context(t), head)
	require.NoError(t, err)

	latest, err := saver.LatestHeadFromDB(tests.Context(t))
	require.NoError(t, err)
	require.Equal(t, int64(1), latest.Number)

	latest = saver.LatestChain()
	require.NotNil(t, latest)
	require.Equal(t, int64(1), latest.Number)

	latest = saver.Chain(head.Hash)
	require.NotNil(t, latest)
	require.Equal(t, int64(1), latest.Number)
}

func TestHeadSaver_Load(t *testing.T) {
	t.Parallel()

	saver, orm := configureSaver(t, saverOpts{
		headTrackerConfig: &trackerConfig{historyDepth: 4},
	})

	// create chain
	// H0 <- H1 <- H2 <- H3 <- H4 <- H5
	//         \
	//           H2Uncle
	//
	newHead := func(num int, parent common.Hash) *evmtypes.Head {
		h := evmtypes.NewHead(big.NewInt(int64(num)), utils.NewHash(), parent, ubig.NewI(0))
		return &h
	}
	h0 := newHead(0, utils.NewHash())
	h1 := newHead(1, h0.Hash)
	h2 := newHead(2, h1.Hash)
	h3 := newHead(3, h2.Hash)
	h4 := newHead(4, h3.Hash)
	h5 := newHead(5, h4.Hash)
	h2Uncle := newHead(2, h1.Hash)

	allHeads := []*evmtypes.Head{h0, h1, h2, h2Uncle, h3, h4, h5}

	for _, h := range allHeads {
		err := orm.IdempotentInsertHead(tests.Context(t), h)
		require.NoError(t, err)
	}

	verifyLatestHead := func(latestHead *evmtypes.Head) {
		// latest head matches h5 and chain does not include h0
		require.NotNil(t, latestHead)
		require.Equal(t, int64(5), latestHead.Number)
		require.Equal(t, uint32(5), latestHead.ChainLength())
		require.Positive(t, latestHead.EarliestHeadInChain().BlockNumber())
	}

	// load all from [h5-historyDepth, h5]
	latestHead, err := saver.Load(tests.Context(t), h5.BlockNumber())
	require.NoError(t, err)
	// verify latest head loaded from db
	verifyLatestHead(latestHead)

	// verify latest head loaded from memory store
	latestHead = saver.LatestChain()
	require.NotNil(t, latestHead)
	verifyLatestHead(latestHead)

	// h2Uncle was loaded and has chain up to h1
	uncleChain := saver.Chain(h2Uncle.Hash)
	require.NotNil(t, uncleChain)
	require.Equal(t, uint32(2), uncleChain.ChainLength()) // h2Uncle -> h1
}
