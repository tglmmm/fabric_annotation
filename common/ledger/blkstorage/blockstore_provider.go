/*
Copyright IBM Corp. 2017 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package blkstorage

import (
	"os"

	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/common/ledger/dataformat"
	"github.com/hyperledger/fabric/common/ledger/util/leveldbhelper"
	"github.com/hyperledger/fabric/common/metrics"
	"github.com/hyperledger/fabric/internal/fileutil"
	"github.com/pkg/errors"
)

var logger = flogging.MustGetLogger("blkstorage")

// IndexableAttr represents an indexable attribute
// 标识可索引的属性（哪些属性可以作为索引）
type IndexableAttr string

// constants for indexable attributes
const (
	// 区块号
	IndexableAttrBlockNum = IndexableAttr("BlockNum")
	// 区块hash
	IndexableAttrBlockHash = IndexableAttr("BlockHash")
	// 交易ID
	IndexableAttrTxID = IndexableAttr("TxID")
	//
	IndexableAttrBlockNumTranNum = IndexableAttr("BlockNumTranNum")
)

// IndexConfig - a configuration that includes a list of attributes that should be indexed
// 包含属性索引的列表
type IndexConfig struct {
	AttrsToIndex []IndexableAttr
}

// SnapshotInfo captures some of the details about the snapshot
// 捕获快照的详细信息
type SnapshotInfo struct {
	LastBlockNum      uint64
	LastBlockHash     []byte
	PreviousBlockHash []byte
}

// Contains returns true if the supplied parameter is present in the IndexConfig.AttrsToIndex
// 如果当前属性列表包含某个属性返回true
func (c *IndexConfig) Contains(indexableAttr IndexableAttr) bool {
	for _, a := range c.AttrsToIndex {
		if a == indexableAttr {
			return true
		}
	}
	return false
}

// BlockStoreProvider provides handle to block storage - this is not thread-safe
// 提供一个句柄用于块存储，他不是线程安全的
type BlockStoreProvider struct {
	conf            *Conf
	indexConfig     *IndexConfig
	leveldbProvider *leveldbhelper.Provider
	stats           *stats
}

// NewProvider constructs a filesystem based block store provider
// 构建一个基于文件系统的块存储提供程序
// 实质上需要在leveldb上创建一个provider
func NewProvider(conf *Conf, indexConfig *IndexConfig, metricsProvider metrics.Provider) (*BlockStoreProvider, error) {
	dbConf := &leveldbhelper.Conf{
		DBPath:         conf.getIndexDir(),             // 根据配置返回index 目录
		ExpectedFormat: dataFormatVersion(indexConfig), // 数据格式校验
	}

	// 创建一个基于leveldb的provider
	p, err := leveldbhelper.NewProvider(dbConf)
	if err != nil {
		return nil, err
	}

	// 创建目录 root/chains
	dirPath := conf.getChainsDir()
	if _, err := os.Stat(dirPath); err != nil {
		if !os.IsNotExist(err) { // NotExist is the only permitted error type
			return nil, errors.Wrapf(err, "failed to read ledger directory %s", dirPath)
		}

		logger.Info("Creating new file ledger directory at", dirPath)
		if err = os.MkdirAll(dirPath, 0o755); err != nil {
			return nil, errors.Wrapf(err, "failed to create ledger directory: %s", dirPath)
		}
	}

	stats := newStats(metricsProvider)
	return &BlockStoreProvider{conf, indexConfig, p, stats}, nil
}

// Open opens a block store for given ledgerid.
// If a blockstore is not existing, this method creates one
// This method should be invoked only once for a particular ledgerid

// 为指定的账本ID打开一个区块存储
// 如果存储不存在，这个方法将创建它
// 对于指定的账本ID这个方法只会调用一次
func (p *BlockStoreProvider) Open(ledgerid string) (*BlockStore, error) {
	// 账本句柄
	// 从 leveldb 上创建一个DBHandle
	indexStoreHandle := p.leveldbProvider.GetDBHandle(ledgerid)
	return newBlockStore(ledgerid, p.conf, p.indexConfig, indexStoreHandle, p.stats)
}

// ImportFromSnapshot initializes blockstore from a previously generated snapshot
// Any failure during bootstrapping the blockstore may leave the partial loaded data
// on disk. The consumer, such as peer is expected to keep track of failures and cleanup the
// data explicitly.
func (p *BlockStoreProvider) ImportFromSnapshot(
	ledgerID string,
	snapshotDir string,
	snapshotInfo *SnapshotInfo,
) error {
	indexStoreHandle := p.leveldbProvider.GetDBHandle(ledgerID)
	if err := bootstrapFromSnapshottedTxIDs(ledgerID, snapshotDir, snapshotInfo, p.conf, indexStoreHandle); err != nil {
		return err
	}
	return nil
}

// Exists tells whether the BlockStore with given id exists
func (p *BlockStoreProvider) Exists(ledgerid string) (bool, error) {

	exists, err := fileutil.DirExists(p.conf.getLedgerBlockDir(ledgerid))
	return exists, err
}

// Drop drops blockstore data (block index and blocks directory) for the given ledgerid (channelID).
// It is not an error if the channel does not exist.
// This function is not error safe. If this function returns an error or a crash takes place, it is highly likely
// that the data for this ledger is left in an inconsistent state. Opening the ledger again or reusing the previously
// opened ledger can show unknown behavior.
func (p *BlockStoreProvider) Drop(ledgerid string) error {
	// 通过配置对应的账本根目录拼接路径 ： root/chains/channelID 查看是否存在
	exists, err := p.Exists(ledgerid)
	if err != nil {
		return err
	}
	// 如果目录不存在直接返回
	if !exists {
		return nil
	}
	// 删除对应账本数据库的所有键
	if err := p.leveldbProvider.Drop(ledgerid); err != nil {
		return err
	}
	// 删除账本目录
	if err := os.RemoveAll(p.conf.getLedgerBlockDir(ledgerid)); err != nil {
		return err
	}
	return fileutil.SyncDir(p.conf.getChainsDir())
}

// List lists the ids of the existing ledgers
func (p *BlockStoreProvider) List() ([]string, error) {
	// 列出 root/chains 子目录名称并返回
	return fileutil.ListSubdirs(p.conf.getChainsDir())
}

// Close closes the BlockStoreProvider
func (p *BlockStoreProvider) Close() {
	p.leveldbProvider.Close()
}

func dataFormatVersion(indexConfig *IndexConfig) string {
	// in version 2.0 we merged three indexable into one `IndexableAttrTxID`
	// 如果属性包含 交易ID
	if indexConfig.Contains(IndexableAttrTxID) {
		return dataformat.CurrentFormat
	}
	return dataformat.PreviousFormat
}
