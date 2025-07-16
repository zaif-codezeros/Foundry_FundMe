package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	pkgerrors "github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/smartcontractkit/chainlink-framework/multinode"

	"github.com/smartcontractkit/chainlink-evm/pkg/client"
	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"
	evmtypes "github.com/smartcontractkit/chainlink-evm/pkg/types"
	"github.com/smartcontractkit/chainlink-evm/pkg/utils"
)

func mustNewChainClient(t *testing.T, wsURL string, sendonlys ...url.URL) client.Client {
	return mustNewChainClientWithChainID(t, wsURL, testutils.FixtureChainID, sendonlys...)
}

func mustNewChainClientWithChainID(t *testing.T, wsURL string, chainID *big.Int, sendonlys ...url.URL) client.Client {
	ctx := tests.Context(t)
	cfg := client.TestNodePoolConfig{
		NodeSelectionMode: multinode.NodeSelectionModeRoundRobin,
	}
	c, err := client.NewChainClientWithTestNode(t, cfg, 0, cfg.NodeLeaseDuration, wsURL, nil, sendonlys, 42, chainID)
	require.NoError(t, err)
	require.NoError(t, c.Dial(ctx))
	require.Eventually(t, func() bool {
		for _, state := range c.NodeStates() {
			if state == "Alive" {
				return true
			}
		}
		return false
	}, time.Minute, time.Second, "no live nodes available")
	return c
}

func TestEthClient_TransactionReceipt(t *testing.T) {
	t.Parallel()

	txHash := "0xb903239f8543d04b5dc1ba6579132b143087c68db1b2168786408fcbce568238"

	mustReadResult := func(t *testing.T, file string) []byte {
		response, err := os.ReadFile(file)
		require.NoError(t, err)
		var resp struct {
			Result json.RawMessage `json:"result"`
		}
		err = json.Unmarshal(response, &resp)
		require.NoError(t, err)
		return resp.Result
	}

	t.Run("happy path", func(t *testing.T) {
		result := mustReadResult(t, "../testdata/jsonrpc/getTransactionReceipt.json")

		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			}
			if assert.Equal(t, "eth_getTransactionReceipt", method) && assert.True(t, params.IsArray()) &&
				assert.Equal(t, txHash, params.Array()[0].String()) {
				resp.Result = string(result)
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		hash := common.HexToHash(txHash)
		receipt, err := ethClient.TransactionReceipt(tests.Context(t), hash)
		require.NoError(t, err)
		assert.Equal(t, hash, receipt.TxHash)
		assert.Equal(t, big.NewInt(11), receipt.BlockNumber)
	})

	t.Run("no tx hash, returns ethereum.NotFound", func(t *testing.T) {
		result := mustReadResult(t, "../testdata/jsonrpc/getTransactionReceipt_notFound.json")
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			}
			if assert.Equal(t, "eth_getTransactionReceipt", method) && assert.True(t, params.IsArray()) &&
				assert.Equal(t, txHash, params.Array()[0].String()) {
				resp.Result = string(result)
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		hash := common.HexToHash(txHash)
		_, err := ethClient.TransactionReceipt(tests.Context(t), hash)
		require.Equal(t, ethereum.NotFound, pkgerrors.Cause(err))
	})
}

func TestEthClient_PendingNonceAt(t *testing.T) {
	t.Parallel()

	address := testutils.NewAddress()

	wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
		switch method {
		case "eth_subscribe":
			resp.Result = `"0x00"`
			resp.Notify = headResult
			return
		case "eth_unsubscribe":
			resp.Result = "true"
			return
		}
		if !assert.Equal(t, "eth_getTransactionCount", method) || !assert.True(t, params.IsArray()) {
			return
		}
		arr := params.Array()
		if assert.Equal(t, strings.ToLower(address.Hex()), strings.ToLower(arr[0].String())) &&
			assert.Equal(t, "pending", arr[1].String()) {
			resp.Result = `"0x100"`
		}
		return
	}).WSURL().String()

	ethClient := mustNewChainClient(t, wsURL)

	result, err := ethClient.PendingNonceAt(tests.Context(t), address)
	require.NoError(t, err)

	var expected uint64 = 256
	require.Equal(t, expected, result)
}

func TestEthClient_BalanceAt(t *testing.T) {
	t.Parallel()

	largeBalance, _ := big.NewInt(0).SetString("100000000000000000000", 10)
	address := testutils.NewAddress()

	cases := []struct {
		name    string
		balance *big.Int
	}{
		{"basic", big.NewInt(256)},
		{"larger than signed 64 bit integer", largeBalance},
	}

	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
				switch method {
				case "eth_subscribe":
					resp.Result = `"0x00"`
					resp.Notify = headResult
					return
				case "eth_unsubscribe":
					resp.Result = "true"
					return
				}
				if assert.Equal(t, "eth_getBalance", method) && assert.True(t, params.IsArray()) &&
					assert.Equal(t, strings.ToLower(address.Hex()), strings.ToLower(params.Array()[0].String())) {
					resp.Result = `"` + hexutil.EncodeBig(test.balance) + `"`
				}
				return
			}).WSURL().String()

			ethClient := mustNewChainClient(t, wsURL)

			result, err := ethClient.BalanceAt(tests.Context(t), address, nil)
			require.NoError(t, err)
			assert.Equal(t, test.balance, result)
		})
	}
}

func TestEthClient_LatestBlockHeight(t *testing.T) {
	t.Parallel()

	wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
		switch method {
		case "eth_subscribe":
			resp.Result = `"0x00"`
			resp.Notify = headResult
			return
		case "eth_unsubscribe":
			resp.Result = "true"
			return
		}
		if !assert.Equal(t, "eth_blockNumber", method) {
			return
		}
		resp.Result = `"0x100"`
		return
	}).WSURL().String()

	ethClient := mustNewChainClient(t, wsURL)

	result, err := ethClient.LatestBlockHeight(tests.Context(t))
	require.NoError(t, err)
	require.Equal(t, big.NewInt(256), result)
}

func TestEthClient_GetERC20Balance(t *testing.T) {
	t.Parallel()
	ctx := tests.Context(t)

	expectedBig, _ := big.NewInt(0).SetString("100000000000000000000000000000000000000", 10)

	cases := []struct {
		name    string
		balance *big.Int
	}{
		{"small", big.NewInt(256)},
		{"big", expectedBig},
	}

	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			contractAddress := testutils.NewAddress()
			userAddress := testutils.NewAddress()
			functionSelector := evmtypes.HexToFunctionSelector(client.BALANCE_OF_ADDRESS_FUNCTION_SELECTOR) // balanceOf(address)
			txData := utils.ConcatBytes(functionSelector.Bytes(), common.LeftPadBytes(userAddress.Bytes(), utils.EVMWordByteLen))

			wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
				switch method {
				case "eth_subscribe":
					resp.Result = `"0x00"`
					resp.Notify = headResult
					return
				case "eth_unsubscribe":
					resp.Result = "true"
					return
				}
				if !assert.Equal(t, "eth_call", method) || !assert.True(t, params.IsArray()) {
					return
				}
				arr := params.Array()
				callArgs := arr[0]
				if assert.True(t, callArgs.IsObject()) &&
					assert.Equal(t, strings.ToLower(contractAddress.Hex()), callArgs.Get("to").String()) &&
					assert.Equal(t, hexutil.Encode(txData), callArgs.Get("data").String()) &&
					assert.Equal(t, "latest", arr[1].String()) {
					resp.Result = `"` + hexutil.EncodeBig(test.balance) + `"`
				}
				return
			}).WSURL().String()

			ethClient := mustNewChainClient(t, wsURL)

			result, err := ethClient.TokenBalance(ctx, userAddress, contractAddress)
			require.NoError(t, err)
			assert.Equal(t, test.balance, result)
		})
	}
}

func TestReceipt_UnmarshalEmptyBlockHash(t *testing.T) {
	t.Parallel()

	input := `{
        "transactionHash": "0x444172bef57ad978655171a8af2cfd89baa02a97fcb773067aef7794d6913374",
        "gasUsed": "0x1",
        "cumulativeGasUsed": "0x1",
        "logs": [],
        "logsBloom": "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
        "blockNumber": "0x8bf99b",
        "blockHash": null
    }`

	var receipt types.Receipt
	err := json.Unmarshal([]byte(input), &receipt)
	require.NoError(t, err)
}

func TestEthClient_LatestSafeBlock(t *testing.T) {
	t.Parallel()

	expectedBlockNum := big.NewInt(123)
	expectedBlockHash := "0xabc123abc123abc123abc123abc123abc123abc123abc123abc123abc123abc1"
	expectedChainID := testutils.FixtureChainID

	rpcResp := `{
		"difficulty":"0x1",
		"extraData":"0x",
		"gasLimit":"0x1",
		"gasUsed":"0x0",
		"hash":"` + expectedBlockHash + `",
		"logsBloom":"0x0",
		"miner":"0x0000000000000000000000000000000000000000",
		"mixHash":"0x0",
		"nonce":"0x0",
		"number":"0x7b",
		"parentHash":"0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d",
		"receiptsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
		"sha3Uncles":"0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
		"size":"0x1",
		"stateRoot":"0xc7b01007a10da045eacb90385887dd0c38fcb5db7393006bdde24b93873c334b",
		"timestamp":"0x58318da2",
		"totalDifficulty":"0x1",
		"transactions":[],
		"transactionsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
		"uncles":[]
	}`

	wsURL := testutils.NewWSServer(t, expectedChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
		switch method {
		case "eth_subscribe":
			resp.Result = `"0x00"`
			resp.Notify = headResult
			return
		case "eth_unsubscribe":
			resp.Result = "true"
			return
		}
		if method == "eth_getBlockByNumber" && params.IsArray() {
			arr := params.Array()
			if arr[0].String() == "safe" {
				resp.Result = rpcResp
			}
		}
		return
	}).WSURL().String()

	ethClient := mustNewChainClient(t, wsURL)

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	result, err := ethClient.LatestSafeBlock(ctx)
	require.NoError(t, err)
	require.Equal(t, expectedBlockHash, result.Hash.Hex())
	require.Equal(t, expectedBlockNum.Int64(), result.Number)
	require.Zero(t, expectedChainID.Cmp(result.EVMChainID.ToInt()))
}

func TestEthClient_HeaderByNumber(t *testing.T) {
	t.Parallel()

	expectedBlockNum := big.NewInt(1)
	expectedBlockHash := "0x41800b5c3f1717687d85fc9018faac0a6e90b39deaa0b99e7fe4fe796ddeb26a"

	cases := []struct {
		name                  string
		expectedRequestBlock  *big.Int
		expectedResponseBlock int64
		error                 error
		rpcResp               string
	}{
		{"happy geth", expectedBlockNum, expectedBlockNum.Int64(), nil,
			`{"difficulty":"0xf3a00","extraData":"0xd883010503846765746887676f312e372e318664617277696e","gasLimit":"0xffc001","gasUsed":"0x0","hash":"0x41800b5c3f1717687d85fc9018faac0a6e90b39deaa0b99e7fe4fe796ddeb26a","logsBloom":"0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000","miner":"0xd1aeb42885a43b72b518182ef893125814811048","mixHash":"0x0f98b15f1a4901a7e9204f3c500a7bd527b3fb2c3340e12176a44b83e414a69e","nonce":"0x0ece08ea8c49dfd9","number":"0x1","parentHash":"0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d","receiptsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","sha3Uncles":"0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347","size":"0x218","stateRoot":"0xc7b01007a10da045eacb90385887dd0c38fcb5db7393006bdde24b93873c334b","timestamp":"0x58318da2","totalDifficulty":"0x1f3a00","transactions":[],"transactionsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","uncles":[]}`},
		{"happy parity", expectedBlockNum, expectedBlockNum.Int64(), nil,
			`{"author":"0xd1aeb42885a43b72b518182ef893125814811048","difficulty":"0xf3a00","extraData":"0xd883010503846765746887676f312e372e318664617277696e","gasLimit":"0xffc001","gasUsed":"0x0","hash":"0x41800b5c3f1717687d85fc9018faac0a6e90b39deaa0b99e7fe4fe796ddeb26a","logsBloom":"0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000","miner":"0xd1aeb42885a43b72b518182ef893125814811048","mixHash":"0x0f98b15f1a4901a7e9204f3c500a7bd527b3fb2c3340e12176a44b83e414a69e","nonce":"0x0ece08ea8c49dfd9","number":"0x1","parentHash":"0x41941023680923e0fe4d74a34bdac8141f2540e3ae90623718e47d66d1ca4a2d","receiptsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","sealFields":["0xa00f98b15f1a4901a7e9204f3c500a7bd527b3fb2c3340e12176a44b83e414a69e","0x880ece08ea8c49dfd9"],"sha3Uncles":"0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347","size":"0x218","stateRoot":"0xc7b01007a10da045eacb90385887dd0c38fcb5db7393006bdde24b93873c334b","timestamp":"0x58318da2","totalDifficulty":"0x1f3a00","transactions":[],"transactionsRoot":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","uncles":[]}`},
		{"missing header", expectedBlockNum, 0, errors.New("RPCClient returned error (eth-primary-rpc-0): not found"),
			`null`},
	}

	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
				switch method {
				case "eth_subscribe":
					resp.Result = `"0x00"`
					resp.Notify = headResult
					return
				case "eth_unsubscribe":
					resp.Result = "true"
					return
				}
				if !assert.Equal(t, "eth_getBlockByNumber", method) || !assert.True(t, params.IsArray()) {
					return
				}
				arr := params.Array()
				blockNumStr := arr[0].String()
				var blockNum hexutil.Big
				err := blockNum.UnmarshalText([]byte(blockNumStr))
				if assert.NoError(t, err) && assert.Equal(t, test.expectedRequestBlock, blockNum.ToInt()) &&
					assert.False(t, arr[1].Bool()) {
					resp.Result = test.rpcResp
				}
				return
			}).WSURL().String()

			ethClient := mustNewChainClient(t, wsURL)

			ctx, cancel := context.WithTimeout(tests.Context(t), 5*time.Second)
			result, err := ethClient.HeadByNumber(ctx, expectedBlockNum)
			if test.error != nil {
				require.EqualError(t, err, test.error.Error())
			} else {
				require.NoError(t, err)
				require.Equal(t, expectedBlockHash, result.Hash.Hex())
				require.Equal(t, test.expectedResponseBlock, result.Number)
				require.Zero(t, testutils.FixtureChainID.Cmp(result.EVMChainID.ToInt()))
			}
			cancel()
		})
	}
}

func TestEthClient_SendTransaction_NoSecondaryURL(t *testing.T) {
	t.Parallel()

	tx := testutils.NewLegacyTransaction(uint64(42), testutils.NewAddress(), big.NewInt(142), 242, big.NewInt(342), []byte{1, 2, 3})

	wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
		switch method {
		case "eth_subscribe":
			resp.Result = `"0x00"`
			resp.Notify = headResult
			return
		case "eth_unsubscribe":
			resp.Result = "true"
			return
		}
		if !assert.Equal(t, "eth_sendRawTransaction", method) {
			return
		}
		resp.Result = `"` + tx.Hash().Hex() + `"`
		return
	}).WSURL().String()

	ethClient := mustNewChainClient(t, wsURL)

	err := ethClient.SendTransaction(tests.Context(t), tx)
	assert.NoError(t, err)
}

func TestEthClient_SendTransaction_WithSecondaryURLs(t *testing.T) {
	t.Parallel()

	tx := testutils.NewLegacyTransaction(uint64(42), testutils.NewAddress(), big.NewInt(142), 242, big.NewInt(342), []byte{1, 2, 3})

	wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
		switch method {
		case "eth_subscribe":
			resp.Result = `"0x00"`
			resp.Notify = headResult
			return
		case "eth_unsubscribe":
			resp.Result = "true"
			return
		case "eth_sendRawTransaction":
			resp.Result = `"` + tx.Hash().Hex() + `"`
		}
		return
	}).WSURL().String()

	rpcSrv := rpc.NewServer()
	t.Cleanup(rpcSrv.Stop)
	service := sendTxService{chainID: testutils.FixtureChainID}
	err := rpcSrv.RegisterName("eth", &service)
	require.NoError(t, err)
	ts := httptest.NewServer(rpcSrv)
	t.Cleanup(ts.Close)

	sendonlyURL, err := url.Parse(ts.URL)
	require.NoError(t, err)

	ethClient := mustNewChainClient(t, wsURL, *sendonlyURL, *sendonlyURL)

	err = ethClient.SendTransaction(tests.Context(t), tx)
	require.NoError(t, err)

	// Unfortunately it's a bit tricky to test this, since there is no
	// synchronization. We have to rely on timing instead.
	require.Eventually(t, func() bool { return service.sentCount.Load() == int32(2) }, tests.WaitTimeout(t), 500*time.Millisecond)
}

type sendTxService struct {
	chainID   *big.Int
	sentCount atomic.Int32
}

func (x *sendTxService) ChainID(ctx context.Context) (*hexutil.Big, error) {
	return (*hexutil.Big)(x.chainID), nil
}

func (x *sendTxService) SendRawTransaction(ctx context.Context, signRawTx hexutil.Bytes) error {
	x.sentCount.Add(1)
	return nil
}

func TestEthClient_SendTransactionReturnCode(t *testing.T) {
	t.Parallel()

	fromAddress := testutils.NewAddress()
	tx := testutils.NewLegacyTransaction(uint64(42), testutils.NewAddress(), big.NewInt(142), 242, big.NewInt(342), []byte{1, 2, 3})

	t.Run("returns Fatal error type when error message is fatal", func(t *testing.T) {
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			case "eth_sendRawTransaction":
				resp.Result = `"` + tx.Hash().Hex() + `"`
				resp.Error.Message = "invalid sender"
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		errType, err := ethClient.SendTransactionReturnCode(tests.Context(t), tx, fromAddress)
		assert.Error(t, err)
		assert.Equal(t, multinode.Fatal, errType)
	})

	t.Run("returns TransactionAlreadyKnown error type when error message is nonce too low", func(t *testing.T) {
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			case "eth_sendRawTransaction":
				resp.Result = `"` + tx.Hash().Hex() + `"`
				resp.Error.Message = "nonce too low"
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		errType, err := ethClient.SendTransactionReturnCode(tests.Context(t), tx, fromAddress)
		assert.Error(t, err)
		assert.Equal(t, multinode.TransactionAlreadyKnown, errType)
	})

	t.Run("returns Successful error type when there is no error message", func(t *testing.T) {
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			case "eth_sendRawTransaction":
				resp.Result = `"` + tx.Hash().Hex() + `"`
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		errType, err := ethClient.SendTransactionReturnCode(tests.Context(t), tx, fromAddress)
		assert.NoError(t, err)
		assert.Equal(t, multinode.Successful, errType)
	})

	t.Run("returns Underpriced error type when transaction is terminally underpriced", func(t *testing.T) {
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			case "eth_sendRawTransaction":
				resp.Result = `"` + tx.Hash().Hex() + `"`
				resp.Error.Message = "transaction underpriced"
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		errType, err := ethClient.SendTransactionReturnCode(tests.Context(t), tx, fromAddress)
		assert.Error(t, err)
		assert.Equal(t, multinode.Underpriced, errType)
	})

	t.Run("returns Unsupported error type when error message is queue full", func(t *testing.T) {
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			case "eth_sendRawTransaction":
				resp.Result = `"` + tx.Hash().Hex() + `"`
				resp.Error.Message = "queue full"
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		errType, err := ethClient.SendTransactionReturnCode(tests.Context(t), tx, fromAddress)
		assert.Error(t, err)
		assert.Equal(t, multinode.Unsupported, errType)
	})

	t.Run("returns Retryable error type when there is a transaction gap", func(t *testing.T) {
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			case "eth_sendRawTransaction":
				resp.Result = `"` + tx.Hash().Hex() + `"`
				resp.Error.Message = "NonceGap"
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		errType, err := ethClient.SendTransactionReturnCode(tests.Context(t), tx, fromAddress)
		assert.Error(t, err)
		assert.Equal(t, multinode.Retryable, errType)
	})

	t.Run("returns InsufficientFunds error type when the sender address doesn't have enough funds", func(t *testing.T) {
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			case "eth_sendRawTransaction":
				resp.Result = `"` + tx.Hash().Hex() + `"`
				resp.Error.Message = "insufficient funds for transfer"
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		errType, err := ethClient.SendTransactionReturnCode(tests.Context(t), tx, fromAddress)
		assert.Error(t, err)
		assert.Equal(t, multinode.InsufficientFunds, errType)
	})

	t.Run("returns ExceedsFeeCap error type when gas price is too high for the node", func(t *testing.T) {
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			case "eth_sendRawTransaction":
				resp.Result = `"` + tx.Hash().Hex() + `"`
				resp.Error.Message = "Transaction fee cap exceeded"
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		errType, err := ethClient.SendTransactionReturnCode(tests.Context(t), tx, fromAddress)
		assert.Error(t, err)
		assert.Equal(t, multinode.ExceedsMaxFee, errType)
	})

	t.Run("returns Unknown error type when the error can't be categorized", func(t *testing.T) {
		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			case "eth_sendRawTransaction":
				resp.Result = `"` + tx.Hash().Hex() + `"`
				resp.Error.Message = "some random error"
			}
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		errType, err := ethClient.SendTransactionReturnCode(tests.Context(t), tx, fromAddress)
		assert.Error(t, err)
		assert.Equal(t, multinode.Unknown, errType)
	})
}

func TestEthClient_SubscribeNewHead(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(tests.Context(t), tests.WaitTimeout(t))
	defer cancel()

	chainId := big.NewInt(123456)
	wsURL := testutils.NewWSServer(t, chainId, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
		if method == "eth_unsubscribe" {
			resp.Result = "true"
			return
		}
		assert.Equal(t, "eth_subscribe", method)
		if assert.True(t, params.IsArray()) && assert.Equal(t, "newHeads", params.Array()[0].String()) {
			resp.Result = `"0x00"`
			resp.Notify = headResult
		}
		return
	}).WSURL().String()

	ethClient := mustNewChainClientWithChainID(t, wsURL, chainId)

	headCh, sub, err := ethClient.SubscribeToHeads(ctx)
	require.NoError(t, err)

	select {
	case err := <-sub.Err():
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case h := <-headCh:
		require.NotNil(t, h.EVMChainID)
		require.Zero(t, chainId.Cmp(h.EVMChainID.ToInt()))
	}
	sub.Unsubscribe()
}

func TestEthClient_BatchCallContext(t *testing.T) {
	t.Parallel()

	t.Run("batch requests return errors", func(t *testing.T) {
		rpcError := errors.New("something went wrong")
		b := []rpc.BatchElem{
			{
				Method: "eth_call",
				Args:   []interface{}{0},
				Result: "",
			},
			{
				Method: "eth_call",
				Args:   []interface{}{1},
				Result: "",
			},
		}

		wsURL := testutils.NewWSServer(t, testutils.FixtureChainID, func(method string, params gjson.Result) (resp testutils.JSONRPCResponse) {
			switch method {
			case "eth_subscribe":
				resp.Result = `"0x00"`
				resp.Notify = headResult
				return
			case "eth_unsubscribe":
				resp.Result = "true"
				return
			}
			require.Equal(t, "eth_call", method)
			resp.Error.Code = -1
			resp.Error.Message = rpcError.Error()
			resp.Result = ""
			return
		}).WSURL().String()

		ethClient := mustNewChainClient(t, wsURL)

		err := ethClient.BatchCallContext(context.Background(), b)
		require.NoError(t, err)
		for _, elem := range b {
			require.Equal(t, elem.Error.Error(), rpcError.Error())
		}
	})
}

func TestEthClient_ErroringClient(t *testing.T) {
	t.Parallel()
	ctx := tests.Context(t)

	// Empty node means there are no active nodes to select from, causing client to always return error.
	erroringClient := client.NewChainClientWithEmptyNode(t, multinode.NodeSelectionModeRoundRobin, time.Second*0, time.Second*0, testutils.FixtureChainID)

	_, err := erroringClient.BalanceAt(ctx, common.Address{}, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	err = erroringClient.BatchCallContext(ctx, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	err = erroringClient.BatchCallContextAll(ctx, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.BlockByHash(ctx, common.Hash{})
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.BlockByNumber(ctx, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	err = erroringClient.CallContext(ctx, nil, "")
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.CallContract(ctx, ethereum.CallMsg{}, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	id := erroringClient.ConfiguredChainID()
	require.Equal(t, id, big.NewInt(0))

	_, err = erroringClient.CodeAt(ctx, common.Address{}, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	id = erroringClient.ConfiguredChainID()
	require.Equal(t, id, testutils.FixtureChainID)

	err = erroringClient.Dial(ctx)
	require.ErrorContains(t, err, "no available nodes for chain")

	_, err = erroringClient.EstimateGas(ctx, ethereum.CallMsg{})
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.FilterLogs(ctx, ethereum.FilterQuery{})
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.HeaderByHash(ctx, common.Hash{})
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.HeaderByNumber(ctx, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.HeadByHash(ctx, common.Hash{})
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.HeadByNumber(ctx, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.LINKBalance(ctx, common.Address{}, common.Address{})
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.LatestBlockHeight(ctx)
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.PendingCodeAt(ctx, common.Address{})
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.PendingNonceAt(ctx, common.Address{})
	require.Equal(t, multinode.ErrNodeError, err)

	txSenderNotStarted := errors.New("TransactionSender not started")
	err = erroringClient.SendTransaction(ctx, nil)
	require.Equal(t, txSenderNotStarted, err)

	tx := testutils.NewLegacyTransaction(uint64(42), testutils.NewAddress(), big.NewInt(142), 242, big.NewInt(342), []byte{1, 2, 3})
	code, err := erroringClient.SendTransactionReturnCode(ctx, tx, common.Address{})
	require.Equal(t, multinode.Unknown, code)
	require.Equal(t, txSenderNotStarted, err)

	_, err = erroringClient.NonceAt(ctx, common.Address{}, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.SubscribeFilterLogs(ctx, ethereum.FilterQuery{}, nil)
	require.Equal(t, multinode.ErrNodeError, err)

	_, _, err = erroringClient.SubscribeToHeads(ctx)
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.SuggestGasPrice(ctx)
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.SuggestGasTipCap(ctx)
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.TokenBalance(ctx, common.Address{}, common.Address{})
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.TransactionByHash(ctx, common.Hash{})
	require.Equal(t, multinode.ErrNodeError, err)

	_, err = erroringClient.TransactionReceipt(ctx, common.Hash{})
	require.Equal(t, multinode.ErrNodeError, err)
}

const headResult = client.HeadResult
