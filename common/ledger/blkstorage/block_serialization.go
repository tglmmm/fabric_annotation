/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package blkstorage

import (
	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/protoutil"
	"github.com/pkg/errors"
)

type serializedBlockInfo struct {
	blockHeader *common.BlockHeader
	txOffsets   []*txindexInfo
	metadata    *common.BlockMetadata
}

// The order of the transactions must be maintained for history
type txindexInfo struct {
	txID string
	loc  *locPointer
}

func serializeBlock(block *common.Block) ([]byte, *serializedBlockInfo, error) {
	buf := proto.NewBuffer(nil)
	var err error
	info := &serializedBlockInfo{}
	info.blockHeader = block.Header
	info.metadata = block.Metadata
	if err = addHeaderBytes(block.Header, buf); err != nil {
		return nil, nil, err
	}
	if info.txOffsets, err = addDataBytesAndConstructTxIndexInfo(block.Data, buf); err != nil {
		return nil, nil, err
	}
	if err = addMetadataBytes(block.Metadata, buf); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), info, nil
}

// 区块信息反序列化
func deserializeBlock(serializedBlockBytes []byte) (*common.Block, error) {
	block := &common.Block{}
	var err error
	// proto.NewBuffer
	b := newBuffer(serializedBlockBytes)
	// 分别提取

	// 提取块头部信息
	if block.Header, err = extractHeader(b); err != nil {
		return nil, err
	}
	// 提取块内容
	if block.Data, _, err = extractData(b); err != nil {
		return nil, err
	}
	// 提取块元数据
	if block.Metadata, err = extractMetadata(b); err != nil {
		return nil, err
	}
	return block, nil
}

func extractSerializedBlockInfo(serializedBlockBytes []byte) (*serializedBlockInfo, error) {
	info := &serializedBlockInfo{}
	var err error
	b := newBuffer(serializedBlockBytes)
	info.blockHeader, err = extractHeader(b)
	if err != nil {
		return nil, err
	}
	_, info.txOffsets, err = extractData(b)
	if err != nil {
		return nil, err
	}

	info.metadata, err = extractMetadata(b)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func addHeaderBytes(blockHeader *common.BlockHeader, buf *proto.Buffer) error {
	if err := buf.EncodeVarint(blockHeader.Number); err != nil {
		return errors.Wrapf(err, "error encoding the block number [%d]", blockHeader.Number)
	}
	if err := buf.EncodeRawBytes(blockHeader.DataHash); err != nil {
		return errors.Wrapf(err, "error encoding the data hash [%v]", blockHeader.DataHash)
	}
	if err := buf.EncodeRawBytes(blockHeader.PreviousHash); err != nil {
		return errors.Wrapf(err, "error encoding the previous hash [%v]", blockHeader.PreviousHash)
	}
	return nil
}

func addDataBytesAndConstructTxIndexInfo(blockData *common.BlockData, buf *proto.Buffer) ([]*txindexInfo, error) {
	var txOffsets []*txindexInfo

	if err := buf.EncodeVarint(uint64(len(blockData.Data))); err != nil {
		return nil, errors.Wrap(err, "error encoding the length of block data")
	}
	for _, txEnvelopeBytes := range blockData.Data {
		offset := len(buf.Bytes())
		txid, err := protoutil.GetOrComputeTxIDFromEnvelope(txEnvelopeBytes)
		if err != nil {
			logger.Warningf("error while extracting txid from tx envelope bytes during serialization of block. Ignoring this error as this is caused by a malformed transaction. Error:%s",
				err)
		}
		if err := buf.EncodeRawBytes(txEnvelopeBytes); err != nil {
			return nil, errors.Wrap(err, "error encoding the transaction envelope")
		}
		idxInfo := &txindexInfo{txID: txid, loc: &locPointer{offset, len(buf.Bytes()) - offset}}
		txOffsets = append(txOffsets, idxInfo)
	}
	return txOffsets, nil
}

func addMetadataBytes(blockMetadata *common.BlockMetadata, buf *proto.Buffer) error {
	numItems := uint64(0)
	if blockMetadata != nil {
		numItems = uint64(len(blockMetadata.Metadata))
	}
	if err := buf.EncodeVarint(numItems); err != nil {
		return errors.Wrap(err, "error encoding the length of metadata")
	}
	for _, b := range blockMetadata.Metadata {
		if err := buf.EncodeRawBytes(b); err != nil {
			return errors.Wrap(err, "error encoding the block metadata")
		}
	}
	return nil
}

// 提取块的头部信息
func extractHeader(buf *buffer) (*common.BlockHeader, error) {
	header := &common.BlockHeader{}
	var err error
	// 解析用 varint编码的区块号

	// buf.Decodexx 方法每次调用都会更新位置，也就是header.Number , header.DataHash，header.PrevioudHash的偏移量
	if header.Number, err = buf.DecodeVarint(); err != nil {
		return nil, errors.Wrap(err, "error decoding the block number")
	}
	if header.DataHash, err = buf.DecodeRawBytes(false); err != nil {
		return nil, errors.Wrap(err, "error decoding the data hash")
	}
	if header.PreviousHash, err = buf.DecodeRawBytes(false); err != nil {
		return nil, errors.Wrap(err, "error decoding the previous hash")
	}
	// 如果迁移个区块hash长度是0，那么他的hash值为nil
	if len(header.PreviousHash) == 0 {
		header.PreviousHash = nil
	}
	return header, nil
}

// 提取区块的数据
func extractData(buf *buffer) (*common.BlockData, []*txindexInfo, error) {
	data := &common.BlockData{}
	var txOffsets []*txindexInfo
	var numItems uint64
	var err error

	// 将 varint 解码到 变量 numItems表示包含多少的交易条目
	if numItems, err = buf.DecodeVarint(); err != nil {
		return nil, nil, errors.Wrap(err, "error decoding the length of block data")
	}
	for i := uint64(0); i < numItems; i++ {
		var txEnvBytes []byte
		var txid string
		// 返回当前 buf 偏移量，方便记录这一次读取的偏移量
		txOffset := buf.GetBytesConsumed()
		if txEnvBytes, err = buf.DecodeRawBytes(false); err != nil {
			return nil, nil, errors.Wrap(err, "error decoding the transaction envelope")
		}
		// 从envelop中获取到交易ID
		if txid, err = protoutil.GetOrComputeTxIDFromEnvelope(txEnvBytes); err != nil {
			logger.Warningf("error while extracting txid from tx envelope bytes during deserialization of block. Ignoring this error as this is caused by a malformed transaction. Error:%s",
				err)
		}
		data.Data = append(data.Data, txEnvBytes)
		idxInfo := &txindexInfo{txID: txid, loc: &locPointer{txOffset, buf.GetBytesConsumed() - txOffset}}
		// 每个交易的偏移量信息
		txOffsets = append(txOffsets, idxInfo)
	}
	return data, txOffsets, nil
}

func extractMetadata(buf *buffer) (*common.BlockMetadata, error) {
	metadata := &common.BlockMetadata{}
	var numItems uint64
	var metadataEntry []byte
	var err error
	// 条目数量
	if numItems, err = buf.DecodeVarint(); err != nil {
		return nil, errors.Wrap(err, "error decoding the length of block metadata")
	}
	//
	for i := uint64(0); i < numItems; i++ {
		if metadataEntry, err = buf.DecodeRawBytes(false); err != nil {
			return nil, errors.Wrap(err, "error decoding the block metadata")
		}
		metadata.Metadata = append(metadata.Metadata, metadataEntry)
	}
	return metadata, nil
}
