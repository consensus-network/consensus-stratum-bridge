package consensusstratum

import (
	"context"
	"fmt"
	"time"

	"github.com/GRinvestPOOL/consensus-stratum-bridge/src/gostratum"
	"github.com/consensus-network/consensusd/app/appmessage"
	"github.com/consensus-network/consensusd/infrastructure/network/rpcclient"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type ConsensusApi struct {
	address       string
	blockWaitTime time.Duration
	logger        *zap.SugaredLogger
	consensusd      *rpcclient.RPCClient
	connected     bool
}

func NewConsensusApi(address string, blockWaitTime time.Duration, logger *zap.SugaredLogger) (*ConsensusApi, error) {
	client, err := rpcclient.NewRPCClient(address)
	if err != nil {
		return nil, err
	}

	return &ConsensusApi{
		address:       address,
		blockWaitTime: blockWaitTime,
		logger:        logger.With(zap.String("component", "consensusapi:"+address)),
		consensusd:      client,
		connected:     true,
	}, nil
}

func (ks *ConsensusApi) Start(ctx context.Context, blockCb func()) {
	ks.waitForSync(true)
	go ks.startBlockTemplateListener(ctx, blockCb)
	go ks.startStatsThread(ctx)
}

func (ks *ConsensusApi) startStatsThread(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	for {
		select {
		case <-ctx.Done():
			ks.logger.Warn("context cancelled, stopping stats thread")
			return
		case <-ticker.C:
			dagResponse, err := ks.consensusd.GetBlockDAGInfo()
			if err != nil {
				ks.logger.Warn("failed to get network hashrate from consensus, prom stats will be out of date", zap.Error(err))
				continue
			}
			response, err := ks.consensusd.EstimateNetworkHashesPerSecond(dagResponse.TipHashes[0], 1000)
			if err != nil {
				ks.logger.Warn("failed to get network hashrate from consensus, prom stats will be out of date", zap.Error(err))
				continue
			}
			RecordNetworkStats(response.NetworkHashesPerSecond, dagResponse.BlockCount, dagResponse.Difficulty)
		}
	}
}

func (ks *ConsensusApi) reconnect() error {
	if ks.consensusd != nil {
		return ks.consensusd.Reconnect()
	}

	client, err := rpcclient.NewRPCClient(ks.address)
	if err != nil {
		return err
	}
	ks.consensusd = client
	return nil
}

func (s *ConsensusApi) waitForSync(verbose bool) error {
	if verbose {
		s.logger.Info("checking consensusd sync state")
	}
	for {
		clientInfo, err := s.consensusd.GetInfo()
		if err != nil {
			return errors.Wrapf(err, "error fetching server info from consensusd @ %s", s.address)
		}
		if clientInfo.IsSynced {
			break
		}
		s.logger.Warn("Consensus is not synced, waiting for sync before starting bridge")
		time.Sleep(5 * time.Second)
	}
	if verbose {
		s.logger.Info("consensusd synced, starting server")
	}
	return nil
}

func (s *ConsensusApi) startBlockTemplateListener(ctx context.Context, blockReadyCb func()) {
	blockReadyChan := make(chan bool)
	err := s.consensusd.RegisterForNewBlockTemplateNotifications(func(_ *appmessage.NewBlockTemplateNotificationMessage) {
		blockReadyChan <- true
	})
	if err != nil {
		s.logger.Error("fatal: failed to register for block notifications from consensus")
	}

	ticker := time.NewTicker(s.blockWaitTime)
	for {
		if err := s.waitForSync(false); err != nil {
			s.logger.Error("error checking consensusd sync state, attempting reconnect: ", err)
			if err := s.reconnect(); err != nil {
				s.logger.Error("error reconnecting to consensusd, waiting before retry: ", err)
				time.Sleep(5 * time.Second)
			}
		}
		select {
		case <-ctx.Done():
			s.logger.Warn("context cancelled, stopping block update listener")
			return
		case <-blockReadyChan:
			blockReadyCb()
			ticker.Reset(s.blockWaitTime)
		case <-ticker.C: // timeout, manually check for new blocks
			blockReadyCb()
		}
	}
}

func (ks *ConsensusApi) GetBlockTemplate(
	client *gostratum.StratumContext) (*appmessage.GetBlockTemplateResponseMessage, error) {
	template, err := ks.consensusd.GetBlockTemplate(client.WalletAddr,
		fmt.Sprintf(`'%s' via consensus-network/consensus-stratum-bridge_%s`, client.RemoteApp, version))
	if err != nil {
		return nil, errors.Wrap(err, "failed fetching new block template from consensus")
	}
	return template, nil
}
