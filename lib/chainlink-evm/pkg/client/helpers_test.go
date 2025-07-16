package client

import (
	"fmt"
	"math/big"
	"net/url"
	"sync"
	"testing"
	"time"

	pkgerrors "github.com/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-framework/metrics"
	"github.com/smartcontractkit/chainlink-framework/multinode"
	client "github.com/smartcontractkit/chainlink-framework/multinode"
	"github.com/smartcontractkit/chainlink-framework/multinode/mocks"

	"github.com/smartcontractkit/chainlink-evm/pkg/config"
	"github.com/smartcontractkit/chainlink-evm/pkg/config/toml"
	evmtypes "github.com/smartcontractkit/chainlink-evm/pkg/types"
)

type TestClientErrors struct {
	nonceTooLow                       string
	nonceTooHigh                      string
	replacementTransactionUnderpriced string
	limitReached                      string
	transactionAlreadyInMempool       string
	terminallyUnderpriced             string
	insufficientEth                   string
	txFeeExceedsCap                   string
	l2FeeTooLow                       string
	l2FeeTooHigh                      string
	l2Full                            string
	transactionAlreadyMined           string
	fatal                             string
	serviceUnavailable                string
	tooManyResults                    string
	missingBlocks                     string
}

func NewTestClientErrors() TestClientErrors {
	return TestClientErrors{
		nonceTooLow:                       "client error nonce too low",
		nonceTooHigh:                      "client error nonce too high",
		replacementTransactionUnderpriced: "client error replacement underpriced",
		limitReached:                      "client error limit reached",
		transactionAlreadyInMempool:       "client error transaction already in mempool",
		terminallyUnderpriced:             "client error terminally underpriced",
		insufficientEth:                   "client error insufficient eth",
		txFeeExceedsCap:                   "client error tx fee exceeds cap",
		l2FeeTooLow:                       "client error l2 fee too low",
		l2FeeTooHigh:                      "client error l2 fee too high",
		l2Full:                            "client error l2 full",
		transactionAlreadyMined:           "client error transaction already mined",
		fatal:                             "client error fatal",
		serviceUnavailable:                "client error service unavailable",
		tooManyResults:                    "client error too many results",
		missingBlocks:                     "client error missing blocks",
	}
}

func (c *TestClientErrors) NonceTooLow() string  { return c.nonceTooLow }
func (c *TestClientErrors) NonceTooHigh() string { return c.nonceTooHigh }

func (c *TestClientErrors) ReplacementTransactionUnderpriced() string {
	return c.replacementTransactionUnderpriced
}

func (c *TestClientErrors) LimitReached() string { return c.limitReached }

func (c *TestClientErrors) TransactionAlreadyInMempool() string {
	return c.transactionAlreadyInMempool
}

func (c *TestClientErrors) TerminallyUnderpriced() string   { return c.terminallyUnderpriced }
func (c *TestClientErrors) InsufficientEth() string         { return c.insufficientEth }
func (c *TestClientErrors) TxFeeExceedsCap() string         { return c.txFeeExceedsCap }
func (c *TestClientErrors) L2FeeTooLow() string             { return c.l2FeeTooLow }
func (c *TestClientErrors) L2FeeTooHigh() string            { return c.l2FeeTooHigh }
func (c *TestClientErrors) L2Full() string                  { return c.l2Full }
func (c *TestClientErrors) TransactionAlreadyMined() string { return c.transactionAlreadyMined }
func (c *TestClientErrors) Fatal() string                   { return c.fatal }
func (c *TestClientErrors) ServiceUnavailable() string      { return c.serviceUnavailable }
func (c *TestClientErrors) TooManyResults() string          { return c.tooManyResults }
func (c *TestClientErrors) MissingBlocks() string           { return c.missingBlocks }

type TestNodePoolConfig struct {
	NodePollFailureThreshold       uint32
	NodePollInterval               time.Duration
	NodeSelectionMode              string
	NodeSyncThreshold              uint32
	NodeLeaseDuration              time.Duration
	NodeIsSyncingEnabledVal        bool
	NodeFinalizedBlockPollInterval time.Duration
	NodeErrors                     config.ClientErrors
	EnforceRepeatableReadVal       bool
	NodeDeathDeclarationDelay      time.Duration
	NodeNewHeadsPollInterval       time.Duration
}

func (tc TestNodePoolConfig) PollFailureThreshold() uint32 { return tc.NodePollFailureThreshold }
func (tc TestNodePoolConfig) PollInterval() time.Duration  { return tc.NodePollInterval }
func (tc TestNodePoolConfig) SelectionMode() string        { return tc.NodeSelectionMode }
func (tc TestNodePoolConfig) SyncThreshold() uint32        { return tc.NodeSyncThreshold }
func (tc TestNodePoolConfig) LeaseDuration() time.Duration {
	return tc.NodeLeaseDuration
}

func (tc TestNodePoolConfig) NodeIsSyncingEnabled() bool {
	return tc.NodeIsSyncingEnabledVal
}

func (tc TestNodePoolConfig) FinalizedBlockPollInterval() time.Duration {
	return tc.NodeFinalizedBlockPollInterval
}

func (tc TestNodePoolConfig) NewHeadsPollInterval() time.Duration {
	return tc.NodeNewHeadsPollInterval
}

func (tc TestNodePoolConfig) VerifyChainID() bool {
	return true
}

func (tc TestNodePoolConfig) Errors() config.ClientErrors {
	return tc.NodeErrors
}

func (tc TestNodePoolConfig) EnforceRepeatableRead() bool {
	return tc.EnforceRepeatableReadVal
}

func (tc TestNodePoolConfig) DeathDeclarationDelay() time.Duration {
	return tc.NodeDeathDeclarationDelay
}

func NewChainClientWithTestNode(
	t *testing.T,
	nodeCfg multinode.NodeConfig,
	noNewHeadsThreshold time.Duration,
	leaseDuration time.Duration,
	rpcUrl string,
	rpcHTTPURL *url.URL,
	sendonlyRPCURLs []url.URL,
	id int,
	chainID *big.Int,
) (Client, error) {
	parsed, err := url.ParseRequestURI(rpcUrl)
	if err != nil {
		return nil, err
	}

	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return nil, pkgerrors.Errorf("ethereum url scheme must be websocket: %s", parsed.String())
	}

	multiNodeMetrics, err := metrics.NewGenericMultiNodeMetrics("EVM Test", chainID.String())
	require.NoError(t, err)

	lggr := logger.Test(t)
	nodePoolCfg := TestNodePoolConfig{
		NodeFinalizedBlockPollInterval: 1 * time.Second,
	}
	rpc := NewRPCClient(nodePoolCfg, lggr, parsed, rpcHTTPURL, "eth-primary-rpc-0", id, chainID, multinode.Primary, client.QueryTimeout, client.QueryTimeout, "")

	n := multinode.NewNode[*big.Int, *evmtypes.Head, *RPCClient](
		nodeCfg, mocks.ChainConfig{NoNewHeadsThresholdVal: noNewHeadsThreshold}, lggr, multiNodeMetrics, parsed, rpcHTTPURL, "eth-primary-node-0", id, chainID, 1, rpc, "EVM")
	primaries := []multinode.Node[*big.Int, *RPCClient]{n}

	sendonlys := make([]multinode.SendOnlyNode[*big.Int, *RPCClient], len(sendonlyRPCURLs))
	for i, u := range sendonlyRPCURLs {
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, pkgerrors.Errorf("sendonly ethereum rpc url scheme must be http(s): %s", u.String())
		}
		rpc := NewRPCClient(nodePoolCfg, lggr, nil, &sendonlyRPCURLs[i], fmt.Sprintf("eth-sendonly-rpc-%d", i), id, chainID, multinode.Secondary, client.QueryTimeout, client.QueryTimeout, "")
		s := multinode.NewSendOnlyNode[*big.Int, *RPCClient](
			lggr, multiNodeMetrics, u, fmt.Sprintf("eth-sendonly-%d", i), chainID, rpc)
		sendonlys[i] = s
	}

	clientErrors := NewTestClientErrors()
	c := NewChainClient(lggr, multiNodeMetrics, nodeCfg.SelectionMode(), leaseDuration, primaries, sendonlys, chainID, &clientErrors, 0, "")
	t.Cleanup(c.Close)
	return c, nil
}

func NewChainClientWithEmptyNode(
	t *testing.T,
	selectionMode string,
	leaseDuration time.Duration,
	noNewHeadsThreshold time.Duration,
	chainID *big.Int,
) Client {
	lggr := logger.Test(t)

	multiNodeMetrics, err := metrics.NewGenericMultiNodeMetrics("EVM Test", chainID.String())
	require.NoError(t, err)

	c := NewChainClient(lggr, multiNodeMetrics, selectionMode, leaseDuration, nil, nil, chainID, nil, 0, "")
	t.Cleanup(c.Close)
	return c
}

func NewChainClientWithMockedRpc(
	t *testing.T,
	selectionMode string,
	leaseDuration time.Duration,
	noNewHeadsThreshold time.Duration,
	chainID *big.Int,
	rpc *RPCClient,
) Client {
	lggr := logger.Test(t)

	cfg := TestNodePoolConfig{
		NodeSelectionMode: multinode.NodeSelectionModeRoundRobin,
	}
	parsed, _ := url.ParseRequestURI("ws://test")

	multiNodeMetrics, err := metrics.NewGenericMultiNodeMetrics("EVM Test", chainID.String())
	require.NoError(t, err)

	n := multinode.NewNode[*big.Int, *evmtypes.Head, *RPCClient](
		cfg, mocks.ChainConfig{NoNewHeadsThresholdVal: noNewHeadsThreshold}, lggr, multiNodeMetrics, parsed, nil, "eth-primary-node-0", 1, chainID, 1, rpc, "EVM")
	primaries := []multinode.Node[*big.Int, *RPCClient]{n}
	clientErrors := NewTestClientErrors()
	c := NewChainClient(lggr, multiNodeMetrics, selectionMode, leaseDuration, primaries, nil, chainID, &clientErrors, 0, "")
	t.Cleanup(c.Close)
	return c
}

const HeadResult = `{"difficulty":"0xf3a00","extraData":"0xd883010503846765746887676f312e372e318664617277696e","gasLimit":"0xffc001","gasUsed":"0x0","hash":"0x41800b5c3f1717687d85fc9018faac0a6e90b39deaa0b99e7fe4fe796ddeb26a","logsBloom":"0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000","miner":"0xd1aeb42885a43b72b518182ef893125814811048","mixHash":"0x0f98b15f1a4901a7e9204f3c500a7bd527b3fb2c3340e12176a44b83e414a69e","nonce":"0x0ece08ea8c49dfd9","number":"0x1","parentHash":"0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d","receiptsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","sha3Uncles":"0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347","size":"0x218","stateRoot":"0xc7b01007a10da045eacb90385887dd0c38fcb5db7393006bdde24b93873c334b","timestamp":"0x58318da2","totalDifficulty":"0x1f3a00","transactions":[],"transactionsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","uncles":[]}`

type mockSubscription struct {
	unsubscribed bool
	Errors       chan error
	unsub        sync.Once
}

func NewMockSubscription() *mockSubscription {
	return &mockSubscription{Errors: make(chan error)}
}

func (mes *mockSubscription) Err() <-chan error { return mes.Errors }

func (mes *mockSubscription) Unsubscribe() {
	mes.unsub.Do(func() {
		mes.unsubscribed = true
		close(mes.Errors)
	})
}

func ParseTestNodeConfigs(nodes []NodeConfig) ([]*toml.Node, error) {
	return parseNodeConfigs(nodes)
}
