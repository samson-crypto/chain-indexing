package main

import (
	"fmt"
	"os"

	"time"

	"github.com/crypto-com/chainindex/infrastructure"
	"github.com/crypto-com/chainindex/infrastructure/pg"
	applogger "github.com/crypto-com/chainindex/internal/logger"
)

type IndexServer struct {
	TendermintHTTPRPCURL string
	RdbConn              *pg.PgxConn
	logger               applogger.Logger
}

// NewIndexServer creates a new server instance for polling and indexing
func NewIndexServer(config *FileConfig) (*IndexServer, error) {
	logger := infrastructure.NewZerologLoggerWithColor(os.Stdout)

	pgxConnPool, err := SetupRdbConn(config, logger)
	if err != nil {
		return nil, fmt.Errorf("error setting up DB connection %v", err)
	}

	return &IndexServer{
		TendermintHTTPRPCURL: config.Tendermint.HTTPRPCURL,
		RdbConn:              pgxConnPool,
		logger:               logger,
	}, nil
}

func SetupRdbConn(config *FileConfig, logger applogger.Logger) (*pg.PgxConn, error) {
	var pgxConnPool *pg.PgxConn
	var err error

	// Parse duration strings to duration
	maxConnLifeTime, err := time.ParseDuration(config.Postgres.MaxConnLifeTime)
	if err != nil {
		return nil, fmt.Errorf("error parsing MaxConnLifeTime string to duration %v", err)
	}
	maxConnIdleTime, err := time.ParseDuration(config.Postgres.MaxConnIdleTime)
	if err != nil {
		return nil, fmt.Errorf("error parsing MaxConnIdleTime string to duration %v", err)
	}
	healthCheckInterval, err := time.ParseDuration(config.Postgres.HealthCheckInterval)
	if err != nil {
		return nil, fmt.Errorf("error parsing HealthCheckInterval string to duration %v", err)
	}

	for pgxConnPool == nil {
		pgxConnPool, err = pg.NewPgxConnPool(&pg.PgxConnPoolConfig{
			ConnConfig: pg.ConnConfig{
				Host:          config.Database.Host,
				Port:          config.Database.Port,
				MaybeUsername: &config.Database.Username,
				MaybePassword: &config.Database.Password,
				Database:      config.Database.Name,
				SSL:           config.Database.SSL,
			},
			MaybeMaxConns:          &config.Postgres.MaxConns,
			MaybeMinConns:          &config.Postgres.MinConns,
			MaybeMaxConnLifeTime:   &maxConnLifeTime,
			MaybeMaxConnIdleTime:   &maxConnIdleTime,
			MaybeHealthCheckPeriod: &healthCheckInterval,
		}, logger)

		if err != nil {
			logger.Errorf("Cannot setup connection to database, will retry in 5 seconds, error %v", err)
			<-time.After(5 * time.Second)
		}
	}

	logger.Info("Successfully setup connection to database, start the indexing service now")
	return pgxConnPool, nil
}

// Run function runs the polling server to index the data from Tendermint
func (s *IndexServer) Run() error {
	syncManager := NewSyncManager(s.logger, s.TendermintHTTPRPCURL, s.RdbConn)
	if err := syncManager.Run(); err != nil {
		return fmt.Errorf("error running sync manager %v", err)
	}

	return nil
}