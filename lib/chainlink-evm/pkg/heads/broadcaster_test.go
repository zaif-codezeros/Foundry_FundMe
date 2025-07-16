package heads_test

import (
	"context"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/mailbox/mailboxtest"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/smartcontractkit/chainlink-evm/pkg/config/configtest"

	"github.com/smartcontractkit/chainlink-framework/chains/heads"

	"github.com/smartcontractkit/chainlink-evm/pkg/client/clienttest"
	"github.com/smartcontractkit/chainlink-evm/pkg/config/toml"
	evmheads "github.com/smartcontractkit/chainlink-evm/pkg/heads"
	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"
	evmtypes "github.com/smartcontractkit/chainlink-evm/pkg/types"
	"github.com/smartcontractkit/chainlink-evm/pkg/utils"
	"github.com/smartcontractkit/chainlink-evm/pkg/utils/big"
)

func waitHeadBroadcasterToStart(t *testing.T, hb evmheads.Broadcaster) {
	t.Helper()

	subscriber := &CountingHeadTrackable{}
	_, unsubscribe := hb.Subscribe(subscriber)
	defer unsubscribe()

	hb.BroadcastNewLongestChain(testutils.Head(1))
	g := gomega.NewWithT(t)
	g.Eventually(subscriber.OnNewLongestChainCount).Should(gomega.Equal(int32(1)))
}

func TestHeadBroadcaster_Subscribe(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	evmCfg := configtest.NewChainScopedConfig(t, func(c *toml.EVMConfig) {
		c.HeadTracker.SamplingInterval = &commonconfig.Duration{}
	})
	db := testutils.NewSqlxDB(t)
	logger := logger.Test(t)

	sub := clienttest.NewSubscription(t)
	ethClient := clienttest.NewClientWithDefaultChainID(t)

	chchHeaders := make(chan chan<- *evmtypes.Head, 1)
	chHead := make(chan *evmtypes.Head)
	ethClient.On("SubscribeToHeads", mock.Anything).
		Run(func(args mock.Arguments) {
			chchHeaders <- chHead
		}).
		Return((<-chan *evmtypes.Head)(chHead), sub, nil)

	h := testutils.Head(1)
	ethClient.On("HeadByNumber", mock.Anything, mock.Anything).Return(h, nil)

	sub.On("Unsubscribe").Return()
	sub.On("Err").Return(nil)

	checker1 := &CountingHeadTrackable{}
	checker2 := &CountingHeadTrackable{}

	orm := evmheads.NewORM(*ethClient.ConfiguredChainID(), db, 0)
	hs := evmheads.NewSaver(logger, orm, evmCfg.EVM(), evmCfg.EVM().HeadTracker())
	mailMon := mailboxtest.NewMonitor(t)
	servicetest.Run(t, mailMon)
	hb := evmheads.NewBroadcaster(logger)
	servicetest.Run(t, hb)
	ht := evmheads.NewTracker(logger, ethClient, evmCfg.EVM(), evmCfg.EVM().HeadTracker(), hb, hs, mailMon)
	servicetest.Run(t, ht)

	latest1, unsubscribe1 := hb.Subscribe(checker1)
	// "latest head" is nil here because we didn't receive any yet
	assert.Equal(t, (*evmtypes.Head)(nil), latest1)

	headers := <-chchHeaders
	headers <- h
	g.Eventually(checker1.OnNewLongestChainCount).Should(gomega.Equal(int32(1)))

	latest2, _ := hb.Subscribe(checker2)
	// "latest head" is set here to the most recent head received
	assert.NotNil(t, latest2)
	assert.Equal(t, h.Number, latest2.Number)

	unsubscribe1()

	h2 := &evmtypes.Head{Number: 2, Hash: utils.NewHash(), ParentHash: h.Hash, EVMChainID: big.New(testutils.FixtureChainID)}
	h2.Parent.Store(h)
	headers <- h2
	g.Eventually(checker2.OnNewLongestChainCount).Should(gomega.Equal(int32(1)))
}

func TestHeadBroadcaster_BroadcastNewLongestChain(t *testing.T) {
	t.Parallel()
	g := gomega.NewWithT(t)

	lggr := logger.Test(t)
	broadcaster := evmheads.NewBroadcaster(lggr)

	err := broadcaster.Start(tests.Context(t))
	require.NoError(t, err)

	waitHeadBroadcasterToStart(t, broadcaster)

	subscriber1 := &CountingHeadTrackable{}
	subscriber2 := &CountingHeadTrackable{}
	_, unsubscribe1 := broadcaster.Subscribe(subscriber1)
	_, unsubscribe2 := broadcaster.Subscribe(subscriber2)

	broadcaster.BroadcastNewLongestChain(testutils.Head(1))
	g.Eventually(subscriber1.OnNewLongestChainCount).Should(gomega.Equal(int32(1)))

	unsubscribe1()

	broadcaster.BroadcastNewLongestChain(testutils.Head(2))
	g.Eventually(subscriber2.OnNewLongestChainCount).Should(gomega.Equal(int32(2)))

	unsubscribe2()

	subscriber3 := &CountingHeadTrackable{}
	_, unsubscribe3 := broadcaster.Subscribe(subscriber3)
	broadcaster.BroadcastNewLongestChain(testutils.Head(1))
	g.Eventually(subscriber3.OnNewLongestChainCount).Should(gomega.Equal(int32(1)))

	unsubscribe3()

	// no subscribers - shall do nothing
	broadcaster.BroadcastNewLongestChain(testutils.Head(0))

	err = broadcaster.Close()
	require.NoError(t, err)

	require.Equal(t, int32(1), subscriber3.OnNewLongestChainCount())
}

func TestHeadBroadcaster_TrackableCallbackTimeout(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	broadcaster := evmheads.NewBroadcaster(lggr)

	err := broadcaster.Start(tests.Context(t))
	require.NoError(t, err)

	waitHeadBroadcasterToStart(t, broadcaster)

	slowAwaiter := testutils.NewAwaiter()
	fastAwaiter := testutils.NewAwaiter()
	slow := &sleepySubscriber{awaiter: slowAwaiter, delay: heads.TrackableCallbackTimeout * 2}
	fast := &sleepySubscriber{awaiter: fastAwaiter, delay: heads.TrackableCallbackTimeout / 2}
	_, unsubscribe1 := broadcaster.Subscribe(slow)
	_, unsubscribe2 := broadcaster.Subscribe(fast)

	broadcaster.BroadcastNewLongestChain(testutils.Head(1))
	slowAwaiter.AwaitOrFail(t, tests.WaitTimeout(t))
	fastAwaiter.AwaitOrFail(t, tests.WaitTimeout(t))

	require.True(t, slow.contextDone)
	require.False(t, fast.contextDone)

	unsubscribe1()
	unsubscribe2()

	err = broadcaster.Close()
	require.NoError(t, err)
}

type sleepySubscriber struct {
	awaiter     testutils.Awaiter
	delay       time.Duration
	contextDone bool
}

func (ss *sleepySubscriber) OnNewLongestChain(ctx context.Context, head *evmtypes.Head) {
	time.Sleep(ss.delay)
	select {
	case <-ctx.Done():
		ss.contextDone = true
	default:
	}
	ss.awaiter.ItHappened()
}
