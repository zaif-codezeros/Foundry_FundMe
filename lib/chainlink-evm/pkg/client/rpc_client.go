package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/google/uuid"
	pkgerrors "github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	commonassets "github.com/smartcontractkit/chainlink-common/pkg/assets"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-framework/metrics"
	"github.com/smartcontractkit/chainlink-framework/multinode"

	"github.com/smartcontractkit/chainlink-evm/pkg/assets"
	"github.com/smartcontractkit/chainlink-evm/pkg/config"
	"github.com/smartcontractkit/chainlink-evm/pkg/config/chaintype"
	evmtypes "github.com/smartcontractkit/chainlink-evm/pkg/types"
	"github.com/smartcontractkit/chainlink-evm/pkg/utils"
	ubig "github.com/smartcontractkit/chainlink-evm/pkg/utils/big"
)

var (
	promEVMPoolRPCNodeDials = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "evm_pool_rpc_node_dials_total",
		Help: "The total number of dials for the given RPC node",
	}, []string{"evmChainID", "nodeName"})
	promEVMPoolRPCNodeDialsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "evm_pool_rpc_node_dials_failed",
		Help: "The total number of failed dials for the given RPC node",
	}, []string{"evmChainID", "nodeName"})
	promEVMPoolRPCNodeDialsSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "evm_pool_rpc_node_dials_success",
		Help: "The total number of successful dials for the given RPC node",
	}, []string{"evmChainID", "nodeName"})

	promEVMPoolRPCNodeCalls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "evm_pool_rpc_node_calls_total",
		Help: "The approximate total number of RPC calls for the given RPC node",
	}, []string{"evmChainID", "nodeName"})
	promEVMPoolRPCNodeCallsFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "evm_pool_rpc_node_calls_failed",
		Help: "The approximate total number of failed RPC calls for the given RPC node",
	}, []string{"evmChainID", "nodeName"})
	promEVMPoolRPCNodeCallsSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "evm_pool_rpc_node_calls_success",
		Help: "The approximate total number of successful RPC calls for the given RPC node",
	}, []string{"evmChainID", "nodeName"})
	// Deprecated: Use github.com/smartcontractkit/chainlink-framework/metrics.RPCCallLatency instead.
	promEVMPoolRPCCallTiming = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "evm_pool_rpc_node_rpc_call_time",
		Help: "The duration of an RPC call in nanoseconds",
		Buckets: []float64{
			float64(50 * time.Millisecond),
			float64(100 * time.Millisecond),
			float64(200 * time.Millisecond),
			float64(500 * time.Millisecond),
			float64(1 * time.Second),
			float64(2 * time.Second),
			float64(4 * time.Second),
			float64(8 * time.Second),
		},
	}, []string{"evmChainID", "nodeName", "rpcHost", "isSendOnly", "success", "rpcCallName"})
)

const rpcSubscriptionMethodNewHeads = "newHeads"

type rawclient struct {
	rpc  *rpc.Client
	geth *ethclient.Client
	uri  url.URL
}

type RPCClient struct {
	cfg                        config.NodePool
	rpcLog                     logger.SugaredLogger
	name                       string
	id                         int
	chainID                    *big.Int
	tier                       multinode.NodeTier
	largePayloadRPCTimeout     time.Duration
	finalizedBlockPollInterval time.Duration
	newHeadsPollInterval       time.Duration
	rpcTimeout                 time.Duration
	chainType                  chaintype.ChainType
	clientErrors               config.ClientErrors

	ws   atomic.Pointer[rawclient]
	http atomic.Pointer[rawclient]

	*multinode.RPCClientBase[*evmtypes.Head]
}

var _ multinode.RPCClient[*big.Int, *evmtypes.Head] = (*RPCClient)(nil)
var _ multinode.SendTxRPCClient[*types.Transaction, struct{}] = (*RPCClient)(nil)

func NewRPCClient(
	cfg config.NodePool,
	lggr logger.Logger,
	wsuri *url.URL,
	httpuri *url.URL,
	name string,
	id int,
	chainID *big.Int,
	tier multinode.NodeTier,
	largePayloadRPCTimeout time.Duration,
	rpcTimeout time.Duration,
	chainType chaintype.ChainType,
) *RPCClient {
	r := &RPCClient{
		largePayloadRPCTimeout: largePayloadRPCTimeout,
		rpcTimeout:             rpcTimeout,
		chainType:              chainType,
		clientErrors:           cfg.Errors(),
	}
	r.cfg = cfg
	r.name = name
	r.id = id
	r.chainID = chainID
	r.tier = tier
	r.finalizedBlockPollInterval = cfg.FinalizedBlockPollInterval()
	r.newHeadsPollInterval = cfg.NewHeadsPollInterval()
	if wsuri != nil {
		r.ws.Store(&rawclient{uri: *wsuri})
	}
	if httpuri != nil {
		r.http.Store(&rawclient{uri: *httpuri})
	}
	lggr = logger.Named(lggr, "Client")
	lggr = logger.With(lggr,
		"clientTier", tier.String(),
		"clientName", name,
		"client", r.String(),
		"evmChainID", chainID,
	)
	r.rpcLog = logger.Sugared(lggr).Named("RPC")

	r.RPCClientBase = multinode.NewRPCClientBase[*evmtypes.Head](cfg, QueryTimeout, lggr, r.latestBlock, r.latestFinalizedBlock)
	return r
}

func (r *RPCClient) ClientVersion(ctx context.Context) (version string, err error) {
	err = r.CallContext(ctx, &version, "web3_clientVersion")
	if err != nil {
		return "", fmt.Errorf("fetching client version failed: %w", err)
	}
	r.rpcLog.Debugf("client version: %s", version)
	return version, nil
}

func (r *RPCClient) Dial(callerCtx context.Context) error {
	ctx, cancel, _ := r.AcquireQueryCtx(callerCtx, r.rpcTimeout)
	defer cancel()

	ws := r.ws.Load()
	http := r.http.Load()
	if ws == nil && http == nil {
		return errors.New("cannot dial rpc client when both ws and http info are missing")
	}

	promEVMPoolRPCNodeDials.WithLabelValues(r.chainID.String(), r.name).Inc()
	lggr := r.rpcLog
	if ws != nil {
		lggr = lggr.With("wsuri", ws.uri.Redacted())
		wsrpc, err := rpc.DialWebsocket(ctx, ws.uri.String(), "")
		if err != nil {
			promEVMPoolRPCNodeDialsFailed.WithLabelValues(r.chainID.String(), r.name).Inc()
			return r.wrapRPCClientError(pkgerrors.Wrapf(err, "error while dialing websocket: %v", ws.uri.Redacted()))
		}

		r.ws.Store(&rawclient{uri: ws.uri, rpc: wsrpc, geth: ethclient.NewClient(wsrpc)})
	}

	if http != nil {
		lggr = lggr.With("httpuri", http.uri.Redacted())
		if err := r.DialHTTP(); err != nil {
			return err
		}
	}

	lggr.Debugw("RPC dial: evmclient.Client#dial")
	promEVMPoolRPCNodeDialsSuccess.WithLabelValues(r.chainID.String(), r.name).Inc()
	return nil
}

// DialHTTP doesn't actually make any external HTTP calls
// It can only return error if the URL is malformed.
func (r *RPCClient) DialHTTP() error {
	http := r.http.Load()
	promEVMPoolRPCNodeDials.WithLabelValues(r.chainID.String(), r.name).Inc()
	lggr := r.rpcLog.With("httpuri", http.uri.Redacted())
	lggr.Debugw("RPC dial: evmclient.Client#dial")

	var httprpc *rpc.Client
	httprpc, err := rpc.DialHTTP(http.uri.String())
	if err != nil {
		promEVMPoolRPCNodeDialsFailed.WithLabelValues(r.chainID.String(), r.name).Inc()
		return r.wrapRPCClientError(pkgerrors.Wrapf(err, "error while dialing HTTP: %v", http.uri.Redacted()))
	}

	http.rpc = httprpc
	http.geth = ethclient.NewClient(httprpc)

	promEVMPoolRPCNodeDialsSuccess.WithLabelValues(r.chainID.String(), r.name).Inc()

	return nil
}

func (r *RPCClient) Close() {
	defer func() {
		ws := r.ws.Load()
		if ws != nil && ws.rpc != nil {
			ws.rpc.Close()
		}
	}()
	r.RPCClientBase.Close()
}

func (r *RPCClient) String() string {
	s := fmt.Sprintf("(%s)%s", r.tier.String(), r.name)
	ws := r.ws.Load()
	if ws != nil {
		s = s + ":" + ws.uri.Redacted()
	}
	http := r.http.Load()
	if http != nil {
		s = s + ":" + http.uri.Redacted()
	}
	return s
}

func (r *RPCClient) logResult(
	lggr logger.Logger,
	err error,
	callDuration time.Duration,
	rpcDomain,
	callName string,
	results ...interface{},
) {
	lggr = logger.With(lggr, "duration", callDuration, "rpcDomain", rpcDomain, "callName", callName)
	promEVMPoolRPCNodeCalls.WithLabelValues(r.chainID.String(), r.name).Inc()
	if err == nil {
		promEVMPoolRPCNodeCallsSuccess.WithLabelValues(r.chainID.String(), r.name).Inc()
		logger.Sugared(lggr).Tracew(fmt.Sprintf("evmclient.Client#%s RPC call success", callName), results...)
	} else {
		promEVMPoolRPCNodeCallsFailed.WithLabelValues(r.chainID.String(), r.name).Inc()
		lggr.Debugw(
			fmt.Sprintf("evmclient.Client#%s RPC call failure", callName),
			append(results, "err", err)...,
		)
	}

	metrics.RPCCallLatency.
		WithLabelValues(
			metrics.EVM,                    // chain family
			r.chainID.String(),             // chain id
			rpcDomain,                      // rpc url
			"false",                        // is send only
			strconv.FormatBool(err == nil), // is successful
			callName,                       // rpc call name
		).
		Observe(float64(callDuration))

	// TODO: Remove deprecated metric
	promEVMPoolRPCCallTiming.
		WithLabelValues(
			r.chainID.String(),             // chain id
			r.name,                         // RPCClient name
			rpcDomain,                      // rpc domain
			"false",                        // is send only
			strconv.FormatBool(err == nil), // is successful
			callName,                       // rpc call name
		).
		Observe(float64(callDuration))
}

func (r *RPCClient) getRPCDomain() string {
	http := r.http.Load()
	if http != nil {
		return http.uri.Host
	}
	return r.ws.Load().uri.Host
}

func (r *RPCClient) isChainType(chainType chaintype.ChainType) bool {
	return r.chainType == chainType
}

// RPC wrappers

// CallContext implementation
func (r *RPCClient) CallContext(ctx context.Context, result interface{}, method string, args ...interface{}) error {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.largePayloadRPCTimeout)
	defer cancel()
	lggr := r.newRqLggr().With(
		"method", method,
		"args", args,
	)

	lggr.Debug("RPC call: evmclient.Client#CallContext")
	start := time.Now()
	var err error

	if http != nil {
		err = r.wrapHTTP(http.rpc.CallContext(ctx, result, method, args...))
	} else {
		err = r.wrapWS(ws.rpc.CallContext(ctx, result, method, args...))
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "CallContext")

	return err
}

func (r *RPCClient) BatchCallContext(rootCtx context.Context, b []rpc.BatchElem) error {
	// Astar's finality tags provide weaker finality guarantees than we require.
	// Fetch latest finalized block using Astar's custom requests and populate it after batch request completes
	var astarRawLatestFinalizedBlock json.RawMessage
	var requestedFinalizedBlock bool
	if r.chainType == chaintype.ChainAstar {
		for _, el := range b {
			if el.Method == "eth_getLogs" {
				r.rpcLog.Critical("evmclient.BatchCallContext: eth_getLogs is not supported")
				return errors.New("evmclient.BatchCallContext: eth_getLogs is not supported")
			}
			if !isRequestingFinalizedBlock(el) {
				continue
			}

			requestedFinalizedBlock = true
			err := r.astarLatestFinalizedBlock(rootCtx, &astarRawLatestFinalizedBlock)
			if err != nil {
				return fmt.Errorf("failed to get astar latest finalized block: %w", err)
			}

			break
		}
	}

	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(rootCtx, r.largePayloadRPCTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("nBatchElems", len(b), "batchElems", b)

	lggr.Trace("RPC call: evmclient.Client#BatchCallContext")
	start := time.Now()
	var err error

	if http != nil {
		err = r.wrapHTTP(http.rpc.BatchCallContext(ctx, b))
	} else {
		err = r.wrapWS(ws.rpc.BatchCallContext(ctx, b))
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "BatchCallContext")
	if err != nil {
		return err
	}

	if r.chainType == chaintype.ChainAstar && requestedFinalizedBlock {
		// populate requested finalized block with correct value
		for _, el := range b {
			if !isRequestingFinalizedBlock(el) {
				continue
			}

			el.Error = nil
			err = json.Unmarshal(astarRawLatestFinalizedBlock, el.Result)
			if err != nil {
				el.Error = fmt.Errorf("failed to unmarshal astar finalized block into provided struct: %w", err)
			}
		}
	}

	return nil
}

func isRequestingFinalizedBlock(el rpc.BatchElem) bool {
	isGetBlock := el.Method == "eth_getBlockByNumber" && len(el.Args) > 0
	if !isGetBlock {
		return false
	}

	if el.Args[0] == rpc.FinalizedBlockNumber {
		return true
	}

	switch arg := el.Args[0].(type) {
	case string:
		return arg == rpc.FinalizedBlockNumber.String()
	case fmt.Stringer:
		return arg.String() == rpc.FinalizedBlockNumber.String()
	default:
		return false
	}
}

// SubscribeToHeads implements custom SubscribeToheads method to override the RPCClientBase
// with added ws support.
func (r *RPCClient) SubscribeToHeads(ctx context.Context) (ch <-chan *evmtypes.Head, sub multinode.Subscription, err error) {
	ctx, cancel, chStopInFlight, ws, _ := r.acquireQueryCtx(ctx, r.rpcTimeout)
	defer cancel()
	args := []interface{}{rpcSubscriptionMethodNewHeads}
	start := time.Now()
	lggr := r.newRqLggr().With("args", args)

	// if new head based on http polling is enabled, we will replace it for WS newHead subscription
	if r.newHeadsPollInterval > 0 {
		lggr.Debugf("Polling new heads over http")
		return r.RPCClientBase.SubscribeToHeads(ctx)
	}

	if ws == nil {
		return nil, nil, errors.New("SubscribeNewHead is not allowed without ws url")
	}

	lggr.Debug("RPC call: evmclient.Client#EthSubscribe")
	defer func() {
		duration := time.Since(start)
		r.logResult(lggr, err, duration, r.getRPCDomain(), "EthSubscribe")
		err = r.wrapWS(err)
	}()

	channel := make(chan *evmtypes.Head)
	forwarder := newSubForwarder(channel, func(head *evmtypes.Head) (*evmtypes.Head, error) {
		head.EVMChainID = ubig.New(r.chainID)
		r.OnNewHead(ctx, chStopInFlight, head)
		return head, nil
	}, r.wrapRPCClientError)

	err = forwarder.start(ws.rpc.EthSubscribe(ctx, forwarder.srcCh, args...))
	if err != nil {
		return nil, nil, err
	}

	sub, err = r.RegisterSub(forwarder, chStopInFlight)
	if err != nil {
		return nil, nil, err
	}

	return channel, sub, err
}

// GethClient wrappers

func (r *RPCClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (receipt *evmtypes.Receipt, err error) {
	err = r.CallContext(ctx, &receipt, "eth_getTransactionReceipt", txHash, false)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		err = r.wrapRPCClientError(ethereum.NotFound)
		return
	}
	return
}

func (r *RPCClient) TransactionReceiptGeth(ctx context.Context, txHash common.Hash) (receipt *types.Receipt, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("txHash", txHash)

	lggr.Debug("RPC call: evmclient.Client#TransactionReceipt")

	start := time.Now()
	if http != nil {
		receipt, err = http.geth.TransactionReceipt(ctx, txHash)
		err = r.wrapHTTP(err)
	} else {
		receipt, err = ws.geth.TransactionReceipt(ctx, txHash)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "TransactionReceipt",
		"receipt", receipt,
	)

	return
}
func (r *RPCClient) TransactionByHash(ctx context.Context, txHash common.Hash) (tx *types.Transaction, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("txHash", txHash)

	lggr.Debug("RPC call: evmclient.Client#TransactionByHash")

	start := time.Now()
	if http != nil {
		tx, _, err = http.geth.TransactionByHash(ctx, txHash)
		err = r.wrapHTTP(err)
	} else {
		tx, _, err = ws.geth.TransactionByHash(ctx, txHash)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "TransactionByHash",
		"receipt", tx,
	)

	return
}

func (r *RPCClient) HeaderByNumber(ctx context.Context, number *big.Int) (header *types.Header, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("number", number)

	lggr.Debug("RPC call: evmclient.Client#HeaderByNumber")
	start := time.Now()
	if http != nil {
		header, err = http.geth.HeaderByNumber(ctx, number)
		err = r.wrapHTTP(err)
	} else {
		header, err = ws.geth.HeaderByNumber(ctx, number)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "HeaderByNumber", "header", header)

	return
}

func (r *RPCClient) HeaderByHash(ctx context.Context, hash common.Hash) (header *types.Header, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("hash", hash)

	lggr.Debug("RPC call: evmclient.Client#HeaderByHash")
	start := time.Now()
	if http != nil {
		header, err = http.geth.HeaderByHash(ctx, hash)
		err = r.wrapHTTP(err)
	} else {
		header, err = ws.geth.HeaderByHash(ctx, hash)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "HeaderByHash",
		"header", header,
	)

	return
}

func (r *RPCClient) LatestSafeBlock(ctx context.Context) (head *evmtypes.Head, err error) {
	ctx, cancel, _, _, _ := r.acquireQueryCtx(ctx, r.rpcTimeout)
	defer cancel()
	err = r.ethGetBlockByNumber(ctx, rpc.SafeBlockNumber.String(), &head)
	if err != nil {
		return
	}

	if head == nil {
		err = r.wrapRPCClientError(ethereum.NotFound)
		return
	}

	head.EVMChainID = ubig.New(r.chainID)

	return
}

func (r *RPCClient) latestBlock(ctx context.Context) (head *evmtypes.Head, err error) {
	return r.BlockByNumber(ctx, nil)
}

func (r *RPCClient) latestFinalizedBlock(ctx context.Context) (head *evmtypes.Head, err error) {
	// capture chStopInFlight to ensure we are not updating chainInfo with observations related to previous life cycle
	ctx, cancel, _, _, _ := r.acquireQueryCtx(ctx, r.rpcTimeout)
	defer cancel()
	if r.chainType == chaintype.ChainAstar {
		// astar's finality tags provide weaker guarantee. Use their custom request to request latest finalized block
		err = r.astarLatestFinalizedBlock(ctx, &head)
	} else {
		err = r.ethGetBlockByNumber(ctx, rpc.FinalizedBlockNumber.String(), &head)
	}

	if err != nil {
		return
	}

	if head == nil {
		err = r.wrapRPCClientError(ethereum.NotFound)
		return
	}
	head.EVMChainID = ubig.New(r.chainID)
	return
}

func (r *RPCClient) astarLatestFinalizedBlock(ctx context.Context, result interface{}) (err error) {
	var hashResult string
	err = r.CallContext(ctx, &hashResult, "chain_getFinalizedHead")
	if err != nil {
		return fmt.Errorf("failed to get astar latest finalized hash: %w", err)
	}

	var astarHead struct {
		Number *hexutil.Big `json:"number"`
	}
	err = r.CallContext(ctx, &astarHead, "chain_getHeader", hashResult, false)
	if err != nil {
		return fmt.Errorf("failed to get astar head by hash: %w", err)
	}

	if astarHead.Number == nil {
		return r.wrapRPCClientError(errors.New("expected non empty head number of finalized block"))
	}

	err = r.ethGetBlockByNumber(ctx, astarHead.Number.String(), result)
	if err != nil {
		return fmt.Errorf("failed to get astar finalized block: %w", err)
	}

	return nil
}

func (r *RPCClient) BlockByNumber(ctx context.Context, number *big.Int) (head *evmtypes.Head, err error) {
	ctx, cancel, chStopInFlight, _, _ := r.acquireQueryCtx(ctx, r.rpcTimeout)
	defer cancel()
	hexNumber := ToBlockNumArg(number)
	err = r.ethGetBlockByNumber(ctx, hexNumber, &head)
	if err != nil {
		return
	}

	if head == nil {
		err = r.wrapRPCClientError(ethereum.NotFound)
		return
	}

	head.EVMChainID = ubig.New(r.chainID)

	if hexNumber == rpc.LatestBlockNumber.String() {
		r.OnNewHead(ctx, chStopInFlight, head)
	}

	return
}

func (r *RPCClient) ethGetBlockByNumber(ctx context.Context, number string, result interface{}) (err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	const method = "eth_getBlockByNumber"
	args := []interface{}{number, false}
	lggr := r.newRqLggr().With(
		"method", method,
		"args", args,
	)

	lggr.Debug("RPC call: evmclient.Client#CallContext")
	start := time.Now()
	if http != nil {
		err = r.wrapHTTP(http.rpc.CallContext(ctx, result, method, args...))
	} else {
		err = r.wrapWS(ws.rpc.CallContext(ctx, result, method, args...))
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "CallContext")
	return err
}

func (r *RPCClient) BlockByHash(ctx context.Context, hash common.Hash) (head *evmtypes.Head, err error) {
	err = r.CallContext(ctx, &head, "eth_getBlockByHash", hash.Hex(), false)
	if err != nil {
		return nil, err
	}
	if head == nil {
		err = r.wrapRPCClientError(ethereum.NotFound)
		return
	}
	head.EVMChainID = ubig.New(r.chainID)
	return
}

func (r *RPCClient) BlockByHashGeth(ctx context.Context, hash common.Hash) (block *types.Block, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("hash", hash)

	lggr.Debug("RPC call: evmclient.Client#BlockByHash")
	start := time.Now()
	if http != nil {
		block, err = http.geth.BlockByHash(ctx, hash)
		err = r.wrapHTTP(err)
	} else {
		block, err = ws.geth.BlockByHash(ctx, hash)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "BlockByHash",
		"block", block,
	)

	return
}

func (r *RPCClient) BlockByNumberGeth(ctx context.Context, number *big.Int) (block *types.Block, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("number", number)

	lggr.Debug("RPC call: evmclient.Client#BlockByNumber")
	start := time.Now()
	if http != nil {
		block, err = http.geth.BlockByNumber(ctx, number)
		err = r.wrapHTTP(err)
	} else {
		block, err = ws.geth.BlockByNumber(ctx, number)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "BlockByNumber",
		"block", block,
	)

	return
}

func (r *RPCClient) SendTransaction(ctx context.Context, tx *types.Transaction) (struct{}, multinode.SendTxReturnCode, error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.largePayloadRPCTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("tx", tx)

	lggr.Debug("RPC call: evmclient.Client#SendTransaction")
	start := time.Now()
	var err error

	if r.isChainType(chaintype.ChainTron) {
		err = errors.New("SendTransaction not implemented for Tron, this should never be called")
		return struct{}{}, multinode.Fatal, err
	}

	if http != nil {
		err = r.wrapHTTP(http.geth.SendTransaction(ctx, tx))
	} else {
		err = r.wrapWS(ws.geth.SendTransaction(ctx, tx))
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "SendTransaction")

	return struct{}{}, ClassifySendError(err, r.clientErrors, logger.Sugared(logger.Nop()), tx, common.Address{}, r.chainType.IsL2()), err
}

func (r *RPCClient) SimulateTransaction(ctx context.Context, tx *types.Transaction) error {
	// Not Implemented
	return pkgerrors.New("SimulateTransaction not implemented")
}

func (r *RPCClient) SendEmptyTransaction(
	ctx context.Context,
	newTxAttempt func(nonce evmtypes.Nonce, feeLimit uint32, fee *assets.Wei, fromAddress common.Address) (attempt any, err error),
	nonce evmtypes.Nonce,
	gasLimit uint32,
	fee *assets.Wei,
	fromAddress common.Address,
) (txhash string, err error) {
	// Not Implemented
	return "", pkgerrors.New("SendEmptyTransaction not implemented")
}

// PendingSequenceAt returns one higher than the highest nonce from both mempool and mined transactions
func (r *RPCClient) PendingSequenceAt(ctx context.Context, account common.Address) (nonce evmtypes.Nonce, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("account", account)

	lggr.Debug("RPC call: evmclient.Client#PendingNonceAt")
	start := time.Now()
	var n uint64

	// Tron doesn't have the concept of nonces, this shouldn't be called but just in case we'll return an error
	if r.isChainType(chaintype.ChainTron) {
		err = errors.New("tron does not support eth_getTransactionCount")
		return
	}

	if http != nil {
		n, err = http.geth.PendingNonceAt(ctx, account)
		nonce = evmtypes.Nonce(int64(n))
		err = r.wrapHTTP(err)
	} else {
		n, err = ws.geth.PendingNonceAt(ctx, account)
		nonce = evmtypes.Nonce(int64(n))
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "PendingNonceAt",
		"nonce", nonce,
	)

	return
}

// NonceAt is a bit of a misnomer. You might expect it to return the highest
// mined nonce at the given block number, but it actually returns the total
// transaction count which is the highest mined nonce + 1
func (r *RPCClient) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (nonce uint64, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("account", account, "blockNumber", blockNumber)

	lggr.Debug("RPC call: evmclient.Client#NonceAt")
	start := time.Now()

	// Tron doesn't have the concept of nonces, this shouldn't be called but just in case we'll return an error
	if r.isChainType(chaintype.ChainTron) {
		err = errors.New("tron does not support eth_getTransactionCount")
		return
	}

	if http != nil {
		nonce, err = http.geth.NonceAt(ctx, account, blockNumber)
		err = r.wrapHTTP(err)
	} else {
		nonce, err = ws.geth.NonceAt(ctx, account, blockNumber)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "NonceAt",
		"nonce", nonce,
	)

	return
}

func (r *RPCClient) PendingCodeAt(ctx context.Context, account common.Address) (code []byte, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("account", account)

	lggr.Debug("RPC call: evmclient.Client#PendingCodeAt")
	start := time.Now()
	if http != nil {
		code, err = http.geth.PendingCodeAt(ctx, account)
		err = r.wrapHTTP(err)
	} else {
		code, err = ws.geth.PendingCodeAt(ctx, account)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "PendingCodeAt",
		"code", code,
	)

	return
}

func (r *RPCClient) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) (code []byte, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("account", account, "blockNumber", blockNumber)

	lggr.Debug("RPC call: evmclient.Client#CodeAt")
	start := time.Now()
	if http != nil {
		code, err = http.geth.CodeAt(ctx, account, blockNumber)
		err = r.wrapHTTP(err)
	} else {
		code, err = ws.geth.CodeAt(ctx, account, blockNumber)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "CodeAt",
		"code", code,
	)

	return
}

func (r *RPCClient) EstimateGas(ctx context.Context, c interface{}) (gas uint64, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.largePayloadRPCTimeout)
	defer cancel()
	call := c.(ethereum.CallMsg)
	lggr := r.newRqLggr().With("call", call)

	lggr.Debug("RPC call: evmclient.Client#EstimateGas")
	start := time.Now()

	if r.isChainType(chaintype.ChainTron) {
		err = r.wrapHTTP(http.rpc.CallContext(ctx, &gas, "eth_estimateGas", r.prepareCallArgs(call)))
		return
	}

	if http != nil {
		gas, err = http.geth.EstimateGas(ctx, call)
		err = r.wrapHTTP(err)
	} else {
		gas, err = ws.geth.EstimateGas(ctx, call)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "EstimateGas",
		"gas", gas,
	)

	return
}

func (r *RPCClient) SuggestGasPrice(ctx context.Context) (price *big.Int, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr()

	lggr.Debug("RPC call: evmclient.Client#SuggestGasPrice")
	start := time.Now()
	if http != nil {
		price, err = http.geth.SuggestGasPrice(ctx)
		err = r.wrapHTTP(err)
	} else {
		price, err = ws.geth.SuggestGasPrice(ctx)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "SuggestGasPrice",
		"price", price,
	)

	return
}

func (r *RPCClient) CallContract(ctx context.Context, msg interface{}, blockNumber *big.Int) (val []byte, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.largePayloadRPCTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("callMsg", msg, "blockNumber", blockNumber)
	message := msg.(ethereum.CallMsg)

	lggr.Debug("RPC call: evmclient.Client#CallContract")
	start := time.Now()
	var hex hexutil.Bytes
	if http != nil {
		err = http.rpc.CallContext(ctx, &hex, "eth_call", r.prepareCallArgs(message), ToBackwardCompatibleBlockNumArg(blockNumber))
		err = r.wrapHTTP(err)
	} else {
		err = ws.rpc.CallContext(ctx, &hex, "eth_call", r.prepareCallArgs(message), ToBackwardCompatibleBlockNumArg(blockNumber))
		err = r.wrapWS(err)
	}
	if err == nil {
		val = hex
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "CallContract",
		"val", val,
	)

	return
}

func (r *RPCClient) PendingCallContract(ctx context.Context, msg interface{}) (val []byte, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.largePayloadRPCTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("callMsg", msg)
	message := msg.(ethereum.CallMsg)

	lggr.Debug("RPC call: evmclient.Client#PendingCallContract")
	start := time.Now()
	var hex hexutil.Bytes
	if http != nil {
		err = http.rpc.CallContext(ctx, &hex, "eth_call", r.prepareCallArgs(message), "pending")
		err = r.wrapHTTP(err)
	} else {
		err = ws.rpc.CallContext(ctx, &hex, "eth_call", r.prepareCallArgs(message), "pending")
		err = r.wrapWS(err)
	}
	if err == nil {
		val = hex
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "PendingCallContract",
		"val", val,
	)

	return
}

func (r *RPCClient) LatestBlockHeight(ctx context.Context) (*big.Int, error) {
	var height big.Int
	h, err := r.BlockNumber(ctx)
	return height.SetUint64(h), err
}

func (r *RPCClient) BlockNumber(ctx context.Context) (height uint64, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr()

	lggr.Debug("RPC call: evmclient.Client#BlockNumber")
	start := time.Now()
	if http != nil {
		height, err = http.geth.BlockNumber(ctx)
		err = r.wrapHTTP(err)
	} else {
		height, err = ws.geth.BlockNumber(ctx)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "BlockNumber",
		"height", height,
	)

	return
}

func (r *RPCClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (balance *big.Int, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("account", account.Hex(), "blockNumber", blockNumber)

	lggr.Debug("RPC call: evmclient.Client#BalanceAt")
	start := time.Now()
	if http != nil {
		balance, err = http.geth.BalanceAt(ctx, account, blockNumber)
		err = r.wrapHTTP(err)
	} else {
		balance, err = ws.geth.BalanceAt(ctx, account, blockNumber)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "BalanceAt",
		"balance", balance,
	)

	return
}

func (r *RPCClient) FeeHistory(ctx context.Context, blockCount uint64, lastBlock *big.Int, rewardPercentiles []float64) (feeHistory *ethereum.FeeHistory, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("blockCount", blockCount, "rewardPercentiles", rewardPercentiles)

	lggr.Debug("RPC call: evmclient.Client#FeeHistory")
	start := time.Now()
	if http != nil {
		feeHistory, err = http.geth.FeeHistory(ctx, blockCount, lastBlock, rewardPercentiles)
		err = r.wrapHTTP(err)
	} else {
		feeHistory, err = ws.geth.FeeHistory(ctx, blockCount, lastBlock, rewardPercentiles)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "FeeHistory",
		"feeHistory", feeHistory,
	)

	return
}

// CallArgs represents the data used to call the balance method of a contract.
// "To" is the address of the ERC contract. "Data" is the message sent
// to the contract. "From" is the sender address.
type CallArgs struct {
	From common.Address `json:"from"`
	To   common.Address `json:"to"`
	Data hexutil.Bytes  `json:"data"`
}

// TokenBalance returns the balance of the given address for the token contract address.
func (r *RPCClient) TokenBalance(ctx context.Context, address common.Address, contractAddress common.Address) (*big.Int, error) {
	result := ""
	numLinkBigInt := new(big.Int)
	functionSelector := evmtypes.HexToFunctionSelector(BALANCE_OF_ADDRESS_FUNCTION_SELECTOR) // balanceOf(address)
	data := utils.ConcatBytes(functionSelector.Bytes(), common.LeftPadBytes(address.Bytes(), utils.EVMWordByteLen))
	args := CallArgs{
		To:   contractAddress,
		Data: data,
	}
	err := r.CallContext(ctx, &result, "eth_call", args, "latest")
	if err != nil {
		return numLinkBigInt, err
	}
	if _, ok := numLinkBigInt.SetString(result, 0); !ok {
		return nil, r.wrapRPCClientError(fmt.Errorf("failed to parse int: %s", result))
	}
	return numLinkBigInt, nil
}

// LINKBalance returns the balance of LINK at the given address
func (r *RPCClient) LINKBalance(ctx context.Context, address common.Address, linkAddress common.Address) (*commonassets.Link, error) {
	balance, err := r.TokenBalance(ctx, address, linkAddress)
	if err != nil {
		return commonassets.NewLinkFromJuels(0), err
	}
	return (*commonassets.Link)(balance), nil
}

func (r *RPCClient) FilterEvents(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	return r.FilterLogs(ctx, q)
}

func (r *RPCClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) (l []types.Log, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr().With("q", q)

	lggr.Debug("RPC call: evmclient.Client#FilterLogs")
	start := time.Now()
	if http != nil {
		l, err = http.geth.FilterLogs(ctx, q)
		err = r.wrapHTTP(err)
	} else {
		l, err = ws.geth.FilterLogs(ctx, q)
		err = r.wrapWS(err)
	}

	if err == nil {
		err = r.makeLogsValid(l)
	}
	duration := time.Since(start)
	r.logResult(lggr, err, duration, r.getRPCDomain(), "FilterLogs",
		"log", l,
	)

	return
}

func (r *RPCClient) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (_ ethereum.Subscription, err error) {
	ctx, cancel, chStopInFlight, ws, _ := r.acquireQueryCtx(ctx, r.rpcTimeout)
	defer cancel()
	if ws == nil {
		return nil, errors.New("SubscribeFilterLogs is not allowed without ws url")
	}
	lggr := r.newRqLggr().With("q", q)

	lggr.Debug("RPC call: evmclient.Client#SubscribeFilterLogs")
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		r.logResult(lggr, err, duration, r.getRPCDomain(), "SubscribeFilterLogs")
		err = r.wrapWS(err)
	}()
	sub := newSubForwarder(ch, r.makeLogValid, r.wrapRPCClientError)
	err = sub.start(ws.geth.SubscribeFilterLogs(ctx, q, sub.srcCh))
	if err != nil {
		return
	}

	managedSub, err := r.RegisterSub(sub, chStopInFlight)
	if err != nil {
		return
	}

	return managedSub, nil
}

func (r *RPCClient) SuggestGasTipCap(ctx context.Context) (tipCap *big.Int, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr()

	lggr.Debug("RPC call: evmclient.Client#SuggestGasTipCap")
	start := time.Now()
	if http != nil {
		tipCap, err = http.geth.SuggestGasTipCap(ctx)
		err = r.wrapHTTP(err)
	} else {
		tipCap, err = ws.geth.SuggestGasTipCap(ctx)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "SuggestGasTipCap",
		"tipCap", tipCap,
	)

	return
}

// Returns the ChainID according to the geth client. This is useful for functions like verify()
// the common node.
func (r *RPCClient) ChainID(ctx context.Context) (chainID *big.Int, err error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()

	if http != nil {
		chainID, err = http.geth.ChainID(ctx)
		err = r.wrapHTTP(err)
	} else {
		chainID, err = ws.geth.ChainID(ctx)
		err = r.wrapWS(err)
	}
	return
}

// newRqLggr generates a new logger with a unique request ID
func (r *RPCClient) newRqLggr() logger.SugaredLogger {
	return r.rpcLog.With("requestID", uuid.New())
}

func (r *RPCClient) wrapRPCClientError(err error) error {
	// simple add msg to the error without adding new stack trace
	return pkgerrors.WithMessage(err, r.rpcClientErrorPrefix())
}

func (r *RPCClient) rpcClientErrorPrefix() string {
	return fmt.Sprintf("RPCClient returned error (%s)", r.name)
}

// PrepareCallArgs prepares the call arguments for RPC calls with chain-specific handling
func (r *RPCClient) prepareCallArgs(msg ethereum.CallMsg) interface{} {
	return toBackwardCompatibleCallArgWithChainTypeSupport(msg, r.chainType)
}

func wrapCallError(err error, tp string) error {
	if err == nil {
		return nil
	}
	if pkgerrors.Cause(err).Error() == "context deadline exceeded" {
		err = pkgerrors.Wrap(err, "remote node timed out")
	}
	return pkgerrors.Wrapf(err, "%s call failed", tp)
}

func (r *RPCClient) wrapWS(err error) error {
	err = wrapCallError(err, fmt.Sprintf("%s websocket (%s)", r.tier.String(), r.ws.Load().uri.Redacted()))
	return r.wrapRPCClientError(err)
}

func (r *RPCClient) wrapHTTP(err error) error {
	err = wrapCallError(err, fmt.Sprintf("%s http (%s)", r.tier.String(), r.http.Load().uri.Redacted()))
	err = r.wrapRPCClientError(err)
	if err != nil {
		r.rpcLog.Debugw("Call failed", "err", err)
	} else {
		r.rpcLog.Trace("Call succeeded")
	}
	return err
}

// makeLiveQueryCtxAndSafeGetClients wraps makeQueryCtx
func (r *RPCClient) makeLiveQueryCtxAndSafeGetClients(parentCtx context.Context, timeout time.Duration) (ctx context.Context, cancel context.CancelFunc,
	ws *rawclient, http *rawclient) {
	ctx, cancel, _, ws, http = r.acquireQueryCtx(parentCtx, timeout)
	return
}

func (r *RPCClient) acquireQueryCtx(parentCtx context.Context, timeout time.Duration) (ctx context.Context, cancel context.CancelFunc,
	chStopInFlight chan struct{}, ws *rawclient, http *rawclient) {
	ctx, cancel, chStopInFlight = r.AcquireQueryCtx(parentCtx, timeout)
	if loadedWs := r.ws.Load(); loadedWs != nil {
		cp := *loadedWs
		ws = &cp
	}
	if loadedHttp := r.http.Load(); loadedHttp != nil {
		cp := *loadedHttp
		http = &cp
	}
	return
}

func (r *RPCClient) IsSyncing(ctx context.Context) (bool, error) {
	ctx, cancel, ws, http := r.makeLiveQueryCtxAndSafeGetClients(ctx, r.rpcTimeout)
	defer cancel()
	lggr := r.newRqLggr()

	lggr.Debug("RPC call: evmclient.Client#SyncProgress")
	var syncProgress *ethereum.SyncProgress
	start := time.Now()
	var err error

	if http != nil {
		syncProgress, err = http.geth.SyncProgress(ctx)
		err = r.wrapHTTP(err)
	} else {
		syncProgress, err = ws.geth.SyncProgress(ctx)
		err = r.wrapWS(err)
	}
	duration := time.Since(start)

	r.logResult(lggr, err, duration, r.getRPCDomain(), "BlockNumber",
		"syncProgress", syncProgress,
	)

	return syncProgress != nil, nil
}

func (r *RPCClient) Name() string {
	return r.name
}

func ToBlockNumArg(number *big.Int) string {
	if number == nil {
		return "latest"
	}
	return hexutil.EncodeBig(number)
}

func (r *RPCClient) makeLogsValid(logs []types.Log) error {

	switch r.chainType {
	case chaintype.ChainSei, chaintype.ChainHedera, chaintype.ChainRootstock:
		// Sei, Rootstock and Hedera does not have unique log index position in the block.
	default:
		return nil
	}

	for i := range logs {
		var err error
		logs[i], err = r.makeLogValid(logs[i])
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *RPCClient) makeLogValid(log types.Log) (types.Log, error) {
	switch r.chainType {
	case chaintype.ChainSei, chaintype.ChainHedera, chaintype.ChainRootstock:
		// Sei, Rootstock and Hedera does not have unique log index position in the block.
	default:
		return log, nil
	}

	if log.TxIndex > math.MaxUint32 {
		return types.Log{}, fmt.Errorf("TxIndex of tx %s exceeds max supported value of %d", log.TxHash, math.MaxUint32)
	}

	if log.Index > math.MaxUint32 {
		return types.Log{}, fmt.Errorf("log's index %d of tx %s exceeds max supported value of %d", log.Index, log.TxHash, math.MaxUint32)
	}

	// it's safe as we have a build guard to guarantee 64-bit system
	newIndex := uint64(log.TxIndex<<32) | uint64(log.Index)
	log.Index = uint(newIndex)
	return log, nil
}
