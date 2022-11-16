/*
Copyright IBM Corp. 2016 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fileledger

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/hyperledger/fabric/common/ledger/blkstorage"
	"github.com/hyperledger/fabric/common/ledger/blockledger"
	"github.com/hyperledger/fabric/common/metrics"
	"github.com/hyperledger/fabric/orderer/common/filerepo"
)

//go:generate counterfeiter -o mock/block_store_provider.go --fake-name BlockStoreProvider . blockStoreProvider
type blockStoreProvider interface {
	Open(ledgerid string) (*blkstorage.BlockStore, error)
	Drop(ledgerid string) error
	List() ([]string, error)
	Close()
}

type fileLedgerFactory struct {
	blkstorageProvider blockStoreProvider
	ledgers            map[string]*FileLedger
	mutex              sync.Mutex
	removeFileRepo     *filerepo.Repo
}

// GetOrCreate gets an existing ledger (if it exists) or creates it
// if it does not.
// 如果账本存在获取存在的账本 ， 如果不存在则创建
func (f *fileLedgerFactory) GetOrCreate(channelID string) (blockledger.ReadWriter, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	// check cache
	// 在缓存中查看，存在直接返回
	ledger, ok := f.ledgers[channelID]
	if ok {
		return ledger, nil
	}
	// open fresh
	// 通过账本提供者打开一个新的账本
	blockStore, err := f.blkstorageProvider.Open(channelID)
	if err != nil {
		return nil, err
	}
	// 根据blockStore  创建 fileLedger
	ledger = NewFileLedger(blockStore)
	f.ledgers[channelID] = ledger
	return ledger, nil
}

// Remove removes an existing ledger and its indexes. This operation
// is blocking.
// 删除存在的账本和他的索引，这个操作是阻塞的
func (f *fileLedgerFactory) Remove(channelID string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	// 清空 channelID.remove 文件对应内容
	if err := f.removeFileRepo.Save(channelID, []byte{}); err != nil && err != os.ErrExist {
		return err
	}

	// check cache for open blockstore and, if one exists,
	// shut it down in order to avoid resource contention
	//查看缓存中是否存在blockstore 存在就关闭
	ledger, ok := f.ledgers[channelID]
	if ok {
		// 本质上就是关闭文件句柄
		ledger.blockStore.Shutdown()
	}
	// 删除账本对应的所有键
	err := f.blkstorageProvider.Drop(channelID)
	if err != nil {
		return err
	}
	// 删除 map中对应账本对象指针
	delete(f.ledgers, channelID)

	if err := f.removeFileRepo.Remove(channelID); err != nil {
		return err
	}

	return nil
}

// ChannelIDs returns the channel IDs the factory is aware of.
func (f *fileLedgerFactory) ChannelIDs() []string {
	channelIDs, err := f.blkstorageProvider.List()
	if err != nil {
		logger.Panic(err)
	}
	return channelIDs
}

// Close releases all resources acquired by the factory.
func (f *fileLedgerFactory) Close() {
	f.blkstorageProvider.Close()
}

// New creates a new ledger factory
// 创建一个信息的账本工厂
func New(directory string, metricsProvider metrics.Provider) (blockledger.Factory, error) {
	// 根据目录， 索引配置，监视器创建一个区块存储提供者
	p, err := blkstorage.NewProvider(
		blkstorage.NewConf(directory, -1),
		&blkstorage.IndexConfig{
			// 索引配置，通过区块号索引
			AttrsToIndex: []blkstorage.IndexableAttr{blkstorage.IndexableAttrBlockNum},
		},
		metricsProvider,
	)
	if err != nil {
		return nil, err
	}
	// 新建一个文件仓库
	// 创建目录，删除临时文件
	fileRepo, err := filerepo.New(filepath.Join(directory, "pendingops"), "remove")
	if err != nil {
		return nil, err
	}

	factory := &fileLedgerFactory{
		blkstorageProvider: p,
		ledgers:            map[string]*FileLedger{},
		removeFileRepo:     fileRepo,
	}
	// 匹配目录下满足规则：*remove 的文件
	files, err := factory.removeFileRepo.List()
	if err != nil {
		return nil, err
	}
	for _, fileName := range files {
		// channelID.suffix
		channelID := factory.removeFileRepo.FileToBaseName(fileName)
		err = factory.Remove(channelID)
		if err != nil {
			logger.Errorf("Failed to remove channel %s: %s", channelID, err.Error())
			return nil, err
		}
		logger.Infof("Removed channel: %s", channelID)
	}

	return factory, nil
}
