package main

import (
	"fmt"

	event_interface "github.com/crypto-com/chain-indexing/appinterface/event"
	eventhandler_interface "github.com/crypto-com/chain-indexing/appinterface/eventhandler"
	"github.com/crypto-com/chain-indexing/appinterface/rdb"
	"github.com/crypto-com/chain-indexing/entity/event"
	projection_entity "github.com/crypto-com/chain-indexing/entity/projection"
	applogger "github.com/crypto-com/chain-indexing/internal/logger"
	event_usecase "github.com/crypto-com/chain-indexing/usecase/event"
	"github.com/crypto-com/chain-indexing/usecase/parser"
)

type IndexService struct {
	logger      applogger.Logger
	rdbConn     rdb.Conn
	projections []projection_entity.Projection

	systemMode               string
	accountAddressPrefix     string
	consNodeAddressPrefix    string
	bondingDenom             string
	windowSize               int
	tendermintHTTPRPCURL     string
	insecureTendermintClient bool
	strictGenesisParsing     bool
	initBlockHeight          int64
}

// NewIndexService creates a new server instance for polling and indexing
func NewIndexService(
	logger applogger.Logger,
	rdbConn rdb.Conn,
	config *Config,
	projections []projection_entity.Projection,
) *IndexService {
	return &IndexService{
		logger:      logger,
		rdbConn:     rdbConn,
		projections: projections,

		systemMode:               config.System.Mode,
		consNodeAddressPrefix:    config.Blockchain.ConNodeAddressPrefix,
		accountAddressPrefix:     config.Blockchain.AccountAddressPrefix,
		bondingDenom:             config.Blockchain.BondingDenom,
		initBlockHeight:          config.Blockchain.InitBlockHeight,
		windowSize:               config.Sync.WindowSize,
		tendermintHTTPRPCURL:     config.Tendermint.HTTPRPCUrl,
		insecureTendermintClient: config.Tendermint.Insecure,
		strictGenesisParsing:     config.Tendermint.StrictGenesisParsing,
	}
}

func (service *IndexService) Run() error {
	// run polling tendermint manager, update view tables directly
	infoManager := NewInfoManager(
		service.logger,
		service.rdbConn,
		service.tendermintHTTPRPCURL,
		service.insecureTendermintClient,
		service.strictGenesisParsing,
	)
	infoManager.Run()

	switch service.systemMode {
	case SYSTEM_MODE_EVENT_STORE:
		return service.RunEventStoreMode()
	case SYSTEM_MODE_TENDERMINT_DIRECT:
		return service.RunTendermintDirectMode()
	default:
		return fmt.Errorf("unsupported system mode: %s", service.systemMode)
	}
}

func (service *IndexService) RunEventStoreMode() error {
	eventRegistry := event.NewRegistry()
	event_usecase.RegisterEvents(eventRegistry)
	eventStore := event_interface.NewRDbStore(service.rdbConn.ToHandle(), eventRegistry)

	projectionManager := projection_entity.NewStoreBasedManager(service.logger, eventStore)

	for _, projection := range service.projections {
		if err := projectionManager.RegisterProjection(projection); err != nil {
			return fmt.Errorf("error registering projection `%s` to manager %v", projection.Id(), err)
		}
	}
	// projectionManager.RunInBackground()

	eventStoreHandler := eventhandler_interface.NewRDbEventStoreHandler(
		service.logger,
		service.rdbConn,
		eventRegistry,
	)
	txDecoder := parser.NewTxDecoder()
	syncManager := NewSyncManager(
		SyncManagerParams{
			Logger:    service.logger,
			RDbConn:   service.rdbConn,
			TxDecoder: txDecoder,
			Config: SyncManagerConfig{
				WindowSize:               service.windowSize,
				TendermintRPCUrl:         service.tendermintHTTPRPCURL,
				InsecureTendermintClient: service.insecureTendermintClient,
				StrictGenesisParsing:     service.strictGenesisParsing,
				initBlockHeight:          service.initBlockHeight,
				AccountAddressPrefix:     service.accountAddressPrefix,
				StakingDenom:             service.bondingDenom,
			},
		},
		eventStoreHandler,
	)
	if err := syncManager.Run(); err != nil {
		return fmt.Errorf("error running sync manager %v", err)
	}

	return nil
}

func (service *IndexService) RunTendermintDirectMode() error {
	txDecoder := parser.NewTxDecoder()

	for i := range service.projections {
		go func(projection projection_entity.Projection) {
			syncManager := NewSyncManager(SyncManagerParams{
				Logger: service.logger.WithFields(applogger.LogFields{
					"projection": projection.Id(),
				}),
				RDbConn:   service.rdbConn,
				TxDecoder: txDecoder,
				Config: SyncManagerConfig{
					WindowSize:               service.windowSize,
					TendermintRPCUrl:         service.tendermintHTTPRPCURL,
					InsecureTendermintClient: service.insecureTendermintClient,
					AccountAddressPrefix:     service.accountAddressPrefix,
					StakingDenom:             service.bondingDenom,
				},
			}, eventhandler_interface.NewProjectionHandler(service.logger, projection))
			if err := syncManager.Run(); err != nil {
				panic(fmt.Sprintf("error running sync manager %v", err))
			}
		}(service.projections[i])
	}
	select {}
}
