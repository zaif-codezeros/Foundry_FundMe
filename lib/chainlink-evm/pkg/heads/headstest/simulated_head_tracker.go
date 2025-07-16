package headstest

import (
	"context"
	"errors"
	"math/big"

	evmclient "github.com/smartcontractkit/chainlink-evm/pkg/client"
	evmtypes "github.com/smartcontractkit/chainlink-evm/pkg/types"
)

// simulatedHeadTracker - simplified version of HeadTracker that works with simulated backed
type simulatedHeadTracker struct {
	ec             evmclient.Client
	useFinalityTag bool
	finalityDepth  int64
}

func NewSimulatedHeadTracker(ec evmclient.Client, useFinalityTag bool, finalityDepth int64) *simulatedHeadTracker {
	return &simulatedHeadTracker{
		ec:             ec,
		useFinalityTag: useFinalityTag,
		finalityDepth:  finalityDepth,
	}
}

func (ht *simulatedHeadTracker) LatestAndFinalizedBlock(ctx context.Context) (*evmtypes.Head, *evmtypes.Head, error) {
	latest, err := ht.ec.HeadByNumber(ctx, nil)
	if err != nil {
		return nil, nil, err
	}

	if latest == nil {
		return nil, nil, errors.New("expected latest block to be valid")
	}

	var finalizedBlock *evmtypes.Head
	if ht.useFinalityTag {
		finalizedBlock, err = ht.ec.LatestFinalizedBlock(ctx)
	} else {
		finalizedBlock, err = ht.ec.HeadByNumber(ctx, big.NewInt(max(latest.Number-ht.finalityDepth, 0)))
	}

	if err != nil {
		return nil, nil, errors.New("simulatedHeadTracker failed to get finalized block")
	}

	if finalizedBlock == nil {
		return nil, nil, errors.New("expected finalized block to be valid")
	}

	return latest, finalizedBlock, nil
}

func (ht *simulatedHeadTracker) LatestSafeBlock(ctx context.Context) (safe *evmtypes.Head, err error) {
	_, finalizedBlock, err := ht.LatestAndFinalizedBlock(ctx)
	if err != nil {
		return nil, errors.New("simulatedHeadTracker failed to get latest safe block")
	}
	return finalizedBlock, nil
}

func (ht *simulatedHeadTracker) LatestChain() *evmtypes.Head {
	return nil
}

func (ht *simulatedHeadTracker) HealthReport() map[string]error {
	return nil
}

func (ht *simulatedHeadTracker) Start(_ context.Context) error {
	return nil
}

func (ht *simulatedHeadTracker) Close() error {
	return nil
}

func (ht *simulatedHeadTracker) Backfill(_ context.Context, _ *evmtypes.Head) error {
	return errors.New("unimplemented")
}

func (ht *simulatedHeadTracker) Name() string {
	return "SimulatedHeadTracker"
}

func (ht *simulatedHeadTracker) Ready() error {
	return nil
}
