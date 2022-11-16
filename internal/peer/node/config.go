/*
Copyright IBM Corp. 2016 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package node

import (
	"path/filepath"
	"time"

	coreconfig "github.com/hyperledger/fabric/core/config"
	"github.com/hyperledger/fabric/core/ledger"
	"github.com/spf13/viper"
)

// 配置信息也core.yml文件相对应
func ledgerConfig() *ledger.Config {
	// set defaults
	// 账本查询频率限制，如果不设置默认1k
	internalQueryLimit := 1000
	if viper.IsSet("ledger.state.couchDBConfig.internalQueryLimit") {
		internalQueryLimit = viper.GetInt("ledger.state.couchDBConfig.internalQueryLimit")
	}
	// 最大批量更新大小
	maxBatchUpdateSize := 500
	if viper.IsSet("ledger.state.couchDBConfig.maxBatchUpdateSize") {
		maxBatchUpdateSize = viper.GetInt("ledger.state.couchDBConfig.maxBatchUpdateSize")
	}
	collElgProcMaxDbBatchSize := 5000
	if viper.IsSet("ledger.pvtdataStore.collElgProcMaxDbBatchSize") {
		collElgProcMaxDbBatchSize = viper.GetInt("ledger.pvtdataStore.collElgProcMaxDbBatchSize")
	}
	collElgProcDbBatchesInterval := 1000
	if viper.IsSet("ledger.pvtdataStore.collElgProcDbBatchesInterval") {
		collElgProcDbBatchesInterval = viper.GetInt("ledger.pvtdataStore.collElgProcDbBatchesInterval")
	}
	purgeInterval := 100
	if viper.IsSet("ledger.pvtdataStore.purgeInterval") {
		purgeInterval = viper.GetInt("ledger.pvtdataStore.purgeInterval")
	}
	deprioritizedDataReconcilerInterval := 60 * time.Minute
	if viper.IsSet("ledger.pvtdataStore.deprioritizedDataReconcilerInterval") {
		deprioritizedDataReconcilerInterval = viper.GetDuration("ledger.pvtdataStore.deprioritizedDataReconcilerInterval")
	}

	// 获取文件系统路径
	fsPath := coreconfig.GetPath("peer.fileSystemPath")
	// 账本数据根目录 /var/hyperledger/production/ledgersData
	ledgersDataRootDir := filepath.Join(fsPath, "ledgersData")
	snapshotsRootDir := viper.GetString("ledger.snapshots.rootDir")
	if snapshotsRootDir == "" {
		snapshotsRootDir = filepath.Join(fsPath, "snapshots")
	}
	// 账本相关配置
	conf := &ledger.Config{
		RootFSPath: ledgersDataRootDir,
		StateDBConfig: &ledger.StateDBConfig{
			StateDatabase: viper.GetString("ledger.state.stateDatabase"),
			CouchDB:       &ledger.CouchDBConfig{},
		},
		PrivateDataConfig: &ledger.PrivateDataConfig{
			MaxBatchSize:                        collElgProcMaxDbBatchSize,
			BatchesInterval:                     collElgProcDbBatchesInterval,
			PurgeInterval:                       purgeInterval,
			DeprioritizedDataReconcilerInterval: deprioritizedDataReconcilerInterval,
		},
		HistoryDBConfig: &ledger.HistoryDBConfig{
			Enabled: viper.GetBool("ledger.history.enableHistoryDatabase"),
		},
		SnapshotsConfig: &ledger.SnapshotsConfig{
			RootDir: snapshotsRootDir,
		},
	}

	// 关于状态数据库的配置
	if conf.StateDBConfig.StateDatabase == ledger.CouchDB {
		conf.StateDBConfig.CouchDB = &ledger.CouchDBConfig{
			Address:               viper.GetString("ledger.state.couchDBConfig.couchDBAddress"),
			Username:              viper.GetString("ledger.state.couchDBConfig.username"),
			Password:              viper.GetString("ledger.state.couchDBConfig.password"),
			MaxRetries:            viper.GetInt("ledger.state.couchDBConfig.maxRetries"),
			MaxRetriesOnStartup:   viper.GetInt("ledger.state.couchDBConfig.maxRetriesOnStartup"),
			RequestTimeout:        viper.GetDuration("ledger.state.couchDBConfig.requestTimeout"),
			InternalQueryLimit:    internalQueryLimit,
			MaxBatchUpdateSize:    maxBatchUpdateSize,
			CreateGlobalChangesDB: viper.GetBool("ledger.state.couchDBConfig.createGlobalChangesDB"),
			RedoLogPath:           filepath.Join(ledgersDataRootDir, "couchdbRedoLogs"),
			UserCacheSizeMBs:      viper.GetInt("ledger.state.couchDBConfig.cacheSize"),
		}
	}
	return conf
}
