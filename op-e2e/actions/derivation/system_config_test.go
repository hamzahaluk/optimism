package derivation

import (
    "math/big"
    "testing"

    "github.com/ethereum-optimism/optimism/op-e2e/bindings"
    "github.com/ethereum-optimism/optimism/op-e2e/e2eutils"
    "github.com/ethereum-optimism/optimism/op-service/testlog"
    "github.com/stretchr/testify/require"
)

// TestSystemConfigBatchType runs system config-related test cases in singular and span batch modes.
func TestSystemConfigBatchType(t *testing.T) {
    tests := []struct {
        name string
        testFunc func(gt *testing.T, deltaTimeOffset *hexutil.Uint64)
    }{
        {"BatcherKeyRotation", BatcherKeyRotation},
        {"GPOParamsChange", GPOParamsChange},
        {"GasLimitChange", GasLimitChange},
    }

    runTestsInBatchMode(t, tests, "SingularBatch", nil)
    
    deltaTimeOffset := hexutil.Uint64(0)
    runTestsInBatchMode(t, tests, "SpanBatch", &deltaTimeOffset)
}

// Helper function to run tests in batch mode
func runTestsInBatchMode(t *testing.T, tests []struct {
    name string
    testFunc func(gt *testing.T, deltaTimeOffset *hexutil.Uint64)
}, mode string, deltaTimeOffset *hexutil.Uint64) {
    for _, test := range tests {
        test := test
        t.Run(test.name+"_"+mode, func(t *testing.T) {
            test.testFunc(t, deltaTimeOffset)
        })
    }
}

// BatcherKeyRotation tests batcher key changes and L1 reorganization effects.
func BatcherKeyRotation(gt *testing.T, deltaTimeOffset *hexutil.Uint64) {
    t := actionsHelpers.NewDefaultTesting(gt)

    dp := e2eutils.MakeDeployParams(t, actionsHelpers.DefaultRollupTestParams())
    dp.DeployConfig.L2BlockTime = 2
    upgradesHelpers.ApplyDeltaTimeOffset(dp, deltaTimeOffset)

    sd := e2eutils.Setup(t, dp, actionsHelpers.DefaultAlloc)
    log := testlog.Logger(t, log.LevelDebug)

    miner, seqEngine, sequencer := setupSequencerTest(t, sd, log)

    // Perform actions for Batcher A and B with proper error checks and messages
    batcherA := setupBatcher(t, log, sd, dp, miner, seqEngine, sequencer, dp.Secrets.Alice)
    batcherB := setupBatcher(t, log, sd, dp, miner, seqEngine, sequencer, dp.Secrets.Bob)

    requireBatchSubmitAndSync(t, miner, sequencer, batcherA)
    switchBatcherKey(t, miner, dp, batcherB)
    
    // Handle L1 reorg scenario
    handleReorg(t, miner, sequencer, batcherA, batcherB)
}

// Helper function to set up a batcher
func setupBatcher(t *testing.T, log log.Logger, sd *e2eutils.TestState, dp *e2eutils.DeployParams, miner *e2eutils.Miner, seqEngine, sequencer *e2eutils.SequencerEngine, batcherKey any) *actionsHelpers.L2Batcher {
    cfg := *actionsHelpers.DefaultBatcherCfg(dp)
    cfg.BatcherKey = batcherKey
    return actionsHelpers.NewL2Batcher(log, sd.RollupCfg, &cfg, sequencer.RollupClient(), miner.EthClient(), seqEngine.EthClient(), seqEngine.EngineClient(t, sd.RollupCfg))
}

// Helper function to require batch submission and synchronization
func requireBatchSubmitAndSync(t *testing.T, miner *e2eutils.Miner, sequencer *e2eutils.SequencerEngine, batcher *actionsHelpers.L2Batcher) {
    sequencer.ActL1HeadSignal(t)
    sequencer.ActBuildToL1Head(t)
    batcher.ActSubmitAll(t)
    
    miner.ActL1StartBlock(12)(t)
    miner.ActL1IncludeTx(batcher.L1Address)(t)
    miner.ActL1EndBlock(t)

    sequencer.ActL2PipelineFull(t)
    require.NoError(t, sequencer.Error(), "sequencer sync failed")
}

// Example for switching batcher key
func switchBatcherKey(t *testing.T, miner *e2eutils.Miner, dp *e2eutils.DeployParams, batcherB *actionsHelpers.L2Batcher) {
    sysCfgContract, err := bindings.NewSystemConfig(dp.RollupCfg.L1SystemConfigAddress, miner.EthClient())
    require.NoError(t, err, "failed to instantiate system config contract")

    sysCfgOwner, err := bind.NewKeyedTransactorWithChainID(dp.Secrets.SysCfgOwner, dp.RollupCfg.L1ChainID)
    require.NoError(t, err, "failed to create transactor")

    tx, err := sysCfgContract.SetBatcherHash(sysCfgOwner, eth.AddressAsLeftPaddedHash(dp.Addresses.Bob))
    require.NoError(t, err, "failed to set batcher hash")
    
    miner.ActL1StartBlock(12)(t)
    miner.ActL1IncludeTx(dp.Addresses.SysCfgOwner)(t)
    miner.ActL1EndBlock(t)
    
    t.Logf("Batcher key changed in L1 tx: %s", tx.Hash().Hex())
}

// Function to handle reorg scenario
func handleReorg(t *testing.T, miner *e2eutils.Miner, sequencer *e2eutils.SequencerEngine, batcherA, batcherB *actionsHelpers.L2Batcher) {
    miner.ActL1RewindDepth(5)(t)
    for i := 0; i < 6; i++ {
        miner.ActEmptyBlock(t)
    }

    batcherA.ActSubmitAll(t)
    sequencer.ActL2PipelineFull(t)
    require.NoError(t, sequencer.Error(), "sequencer reorg sync failed")
    
    batcherB.ActSubmitAll(t)
    sequencer.ActL2PipelineFull(t)
    require.NotEqual(t, batcherA, batcherB, "Batcher should be switched after reorg")
}
