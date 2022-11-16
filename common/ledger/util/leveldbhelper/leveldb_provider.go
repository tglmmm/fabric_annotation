/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package leveldbhelper

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/hyperledger/fabric/common/ledger/dataformat"
	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
)

const (
	// internalDBName is used to keep track of data related to internals such as data format
	// _ is used as name because this is not allowed as a channelname
	internalDBName = "_"
	// maxBatchSize limits the memory usage (1MB) for a batch. It is measured by the total number of bytes
	// of all the keys in a batch.
	maxBatchSize = 1000000
)

var (
	dbNameKeySep     = []byte{0x00}
	lastKeyIndicator = byte(0x01)
	formatVersionKey = []byte{'f'} // a single key in db whose value indicates the version of the data format
)

// closeFunc closes the db handle
type closeFunc func()

// Conf configuration for `Provider`
//
// `ExpectedFormat` is the expected value of the format key in the internal database.
// At the time of opening the db, A check is performed that
// either the db is empty (i.e., opening for the first time) or the value
// of the formatVersionKey is equal to `ExpectedFormat`. Otherwise, an error is returned.
// A nil value for ExpectedFormat indicates that the format is never set and hence there is no such record.
type Conf struct {
	DBPath         string
	ExpectedFormat string
}

// Provider enables to use a single leveldb as multiple logical leveldbs
// 允许单个leveldb 作为作为多个逻辑Leveldb使用
type Provider struct {
	db *DB

	mux       sync.Mutex
	dbHandles map[string]*DBHandle
}

// DataFormatInfo contains the information about the version of the data format
type DataFormatInfo struct {
	FormatVerison string // version of the data format
	IsDBEmpty     bool   // set to true if the db does not contain any data
}

// RetrieveDataFormatInfo retrieves the DataFormatInfo for the db at the supplied `dbPath`
func RetrieveDataFormatInfo(dbPath string) (*DataFormatInfo, error) {
	db := CreateDB(&Conf{DBPath: dbPath})
	db.Open()
	defer db.Close()

	dbEmpty, err := db.IsEmpty()
	if err != nil {
		return nil, err
	}

	internalDB := &DBHandle{
		db:     db,
		dbName: internalDBName,
	}

	formatVersion, err := internalDB.Get(formatVersionKey)
	if err != nil {
		return nil, err
	}

	return &DataFormatInfo{
		IsDBEmpty:     dbEmpty,
		FormatVerison: string(formatVersion),
	}, nil
}

// NewProvider constructs a Provider
func NewProvider(conf *Conf) (*Provider, error) {
	//数据库状态标记打开，如果格式版本不存则创建这个键，如果存在则将期望的版本号和数据库中版本对比，不一致就报错
	db, err := openDBAndCheckFormat(conf)
	if err != nil {
		return nil, err
	}
	return &Provider{
		db:        db,
		dbHandles: make(map[string]*DBHandle),
	}, nil
}

func openDBAndCheckFormat(conf *Conf) (d *DB, e error) {
	// 根据配置初始化一个DB实例
	db := CreateDB(conf)
	// 打开底层数据库，修改数据库状态
	db.Open()

	defer func() {
		if e != nil {
			db.Close()
		}
	}()
	// 组织一个内部数据库的handle,用于查询formatVersionKey的值
	internalDB := &DBHandle{
		db:     db,
		dbName: internalDBName,
	}

	dbEmpty, err := db.IsEmpty()
	if err != nil {
		return nil, err
	}

	// 如果数据库是空并且格式变量不是空
	if dbEmpty && conf.ExpectedFormat != "" {
		logger.Infof("DB is empty Setting db format as %s", conf.ExpectedFormat)
		// f => Expectedformat
		if err := internalDB.Put(formatVersionKey, []byte(conf.ExpectedFormat), true); err != nil {
			return nil, err
		}
		return db, nil
	}
	// 如果键 formatVersionKey 已经存在，先获取
	formatVersion, err := internalDB.Get(formatVersionKey)
	if err != nil {
		return nil, err
	}
	logger.Debugf("Checking for db format at path [%s]", conf.DBPath)
	// 当前期望的格式与数据库中已经存在设定的值做对比，如果不一致返回错误
	if !bytes.Equal(formatVersion, []byte(conf.ExpectedFormat)) {
		logger.Errorf("The db at path [%s] contains data in unexpected format. expected data format = [%s] (%#v), data format = [%s] (%#v).",
			conf.DBPath, conf.ExpectedFormat, []byte(conf.ExpectedFormat), formatVersion, formatVersion)
		return nil, &dataformat.ErrFormatMismatch{
			ExpectedFormat: conf.ExpectedFormat,
			Format:         string(formatVersion),
			DBInfo:         fmt.Sprintf("leveldb at [%s]", conf.DBPath),
		}
	}
	// 如果一致则返回DB
	logger.Debug("format is latest, nothing to do")
	return db, nil
}

// GetDataFormat returns the format of the data
func (p *Provider) GetDataFormat() (string, error) {
	f, err := p.GetDBHandle(internalDBName).Get(formatVersionKey)
	return string(f), err
}

func (p *Provider) SetDataFormat(format string) error {
	db := p.GetDBHandle(internalDBName)
	return db.Put(formatVersionKey, []byte(format), true)
}

// GetDBHandle returns a handle to a named db
// 返回一个命名数据库句柄
func (p *Provider) GetDBHandle(dbName string) *DBHandle {
	p.mux.Lock()
	defer p.mux.Unlock()
	dbHandle := p.dbHandles[dbName]
	// 如果句柄是nil
	if dbHandle == nil {
		// 关闭函数
		closeFunc := func() {
			p.mux.Lock()
			defer p.mux.Unlock()
			// 字典中删除对应名称的句柄
			delete(p.dbHandles, dbName)
		}
		// 定义当前账本的句柄
		dbHandle = &DBHandle{dbName, p.db, closeFunc}
		// 存储到句柄字典中
		p.dbHandles[dbName] = dbHandle
	}
	return dbHandle
}

// Close closes the underlying leveldb
func (p *Provider) Close() {
	p.db.Close()
}

// Drop drops all the data for the given dbName
func (p *Provider) Drop(dbName string) error {
	// 通过数据库名返回一个数据库句柄
	dbHandle := p.GetDBHandle(dbName)
	// 函数推出时候将其关闭
	defer dbHandle.Close()
	return dbHandle.deleteAll()
}

// DBHandle is an handle to a named db
type DBHandle struct {
	dbName    string
	db        *DB
	closeFunc closeFunc
}

// Get returns the value for the given key
// 获取指定key对应value
func (h *DBHandle) Get(key []byte) ([]byte, error) {
	return h.db.Get(constructLevelKey(h.dbName, key))
}

// Put saves the key/value
func (h *DBHandle) Put(key []byte, value []byte, sync bool) error {
	return h.db.Put(constructLevelKey(h.dbName, key), value, sync)
}

// Delete deletes the given key
func (h *DBHandle) Delete(key []byte, sync bool) error {
	return h.db.Delete(constructLevelKey(h.dbName, key), sync)
}

// DeleteAll deletes all the keys that belong to the channel (dbName).
// 删除所有属于这个数据库的键
func (h *DBHandle) deleteAll() error {
	iter, err := h.GetIterator(nil, nil)
	if err != nil {
		return err
	}
	defer iter.Release()

	// use leveldb iterator directly to be more efficient
	dbIter := iter.Iterator

	// This is common code shared by all the leveldb instances. Because each leveldb has its own key size pattern,
	// each batch is limited by memory usage instead of number of keys. Once the batch memory usage reaches maxBatchSize,
	// the batch will be committed.
	// 这是所有Leveldb实例共享的通用代码， 因为每个leveldb都有自己的键大小模式
	// 每个批处理都会受到内存使用量的限制，而不是键的数量，如果一次内存使用量达到最大批处理大小，这个批处理将被提交
	numKeys := 0
	batchSize := 0
	// 创建一个批处理
	batch := &leveldb.Batch{}
	for dbIter.Next() {
		if err := dbIter.Error(); err != nil {
			return errors.Wrap(err, "internal leveldb error while retrieving data from db iterator")
		}
		key := dbIter.Key()
		numKeys++
		batchSize = batchSize + len(key)
		batch.Delete(key)
		// 数量大于等于
		if batchSize >= maxBatchSize {
			// 提交这个批处理
			if err := h.db.WriteBatch(batch, true); err != nil {
				return err
			}
			logger.Infof("Have removed %d entries for channel %s in leveldb %s", numKeys, h.dbName, h.db.conf.DBPath)
			// 下一次批处理之前，充值batchSize大小
			batchSize = 0
			batch.Reset()
		}
	}
	// 最后一次批处理的大小没有达到最大批处理大小，则在这里进行一次提交
	if batch.Len() > 0 {
		return h.db.WriteBatch(batch, true)
	}
	return nil
}

// IsEmpty returns true if no data exists for the DBHandle
func (h *DBHandle) IsEmpty() (bool, error) {
	itr, err := h.GetIterator(nil, nil)
	if err != nil {
		return false, err
	}
	defer itr.Release()

	if err := itr.Error(); err != nil {
		return false, errors.WithMessagef(itr.Error(), "internal leveldb error while obtaining next entry from iterator")
	}

	return !itr.Next(), nil
}

// NewUpdateBatch returns a new UpdateBatch that can be used to update the db
func (h *DBHandle) NewUpdateBatch() *UpdateBatch {
	return &UpdateBatch{
		dbName:       h.dbName,
		leveldbBatch: &leveldb.Batch{},
	}
}

// WriteBatch writes a batch in an atomic way
func (h *DBHandle) WriteBatch(batch *UpdateBatch, sync bool) error {
	if batch == nil || batch.leveldbBatch.Len() == 0 {
		return nil
	}
	if err := h.db.WriteBatch(batch.leveldbBatch, sync); err != nil {
		return err
	}
	return nil
}

// GetIterator gets an handle to iterator. The iterator should be released after the use.
// The resultset contains all the keys that are present in the db between the startKey (inclusive) and the endKey (exclusive).
// A nil startKey represents the first available key and a nil endKey represent a logical key after the last available key
func (h *DBHandle) GetIterator(startKey []byte, endKey []byte) (*Iterator, error) {
	sKey := constructLevelKey(h.dbName, startKey)
	eKey := constructLevelKey(h.dbName, endKey)
	if endKey == nil {
		// replace the last byte 'dbNameKeySep' by 'lastKeyIndicator'
		eKey[len(eKey)-1] = lastKeyIndicator
	}
	logger.Debugf("Getting iterator for range [%#v] - [%#v]", sKey, eKey)
	itr := h.db.GetIterator(sKey, eKey)
	if err := itr.Error(); err != nil {
		itr.Release()
		return nil, errors.Wrapf(err, "internal leveldb error while obtaining db iterator")
	}
	return &Iterator{h.dbName, itr}, nil
}

// Close closes the DBHandle after its db data have been deleted
func (h *DBHandle) Close() {
	if h.closeFunc != nil {
		h.closeFunc()
	}
}

// UpdateBatch encloses the details of multiple `updates`
type UpdateBatch struct {
	leveldbBatch *leveldb.Batch
	dbName       string
	size         int
}

// Put adds a KV
func (b *UpdateBatch) Put(key []byte, value []byte) {
	if value == nil {
		panic("Nil value not allowed")
	}
	k := constructLevelKey(b.dbName, key)
	b.leveldbBatch.Put(k, value)
	b.size += len(k) + len(value)
}

// Delete deletes a Key and associated value
func (b *UpdateBatch) Delete(key []byte) {
	k := constructLevelKey(b.dbName, key)
	b.size += len(k)
	b.leveldbBatch.Delete(k)
}

// Size returns the current size of the batch
func (b *UpdateBatch) Size() int {
	return b.size
}

// Len returns number of records in the batch
func (b *UpdateBatch) Len() int {
	return b.leveldbBatch.Len()
}

// Reset resets the batch
func (b *UpdateBatch) Reset() {
	b.leveldbBatch.Reset()
	b.size = 0
}

// Iterator extends actual leveldb iterator
type Iterator struct {
	dbName string
	iterator.Iterator
}

// Key wraps actual leveldb iterator method
func (itr *Iterator) Key() []byte {
	return retrieveAppKey(itr.Iterator.Key())
}

// Seek moves the iterator to the first key/value pair
// whose key is greater than or equal to the given key.
// It returns whether such pair exist.
func (itr *Iterator) Seek(key []byte) bool {
	levelKey := constructLevelKey(itr.dbName, key)
	return itr.Iterator.Seek(levelKey)
}

func constructLevelKey(dbName string, key []byte) []byte {
	return append(append([]byte(dbName), dbNameKeySep...), key...)
}

func retrieveAppKey(levelKey []byte) []byte {
	return bytes.SplitN(levelKey, dbNameKeySep, 2)[1]
}
