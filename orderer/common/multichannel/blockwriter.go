/*
Copyright IBM Corp. 2017 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package multichannel

import (
	"sync"

	"github.com/golang/protobuf/proto"
	cb "github.com/hyperledger/fabric-protos-go/common"
	newchannelconfig "github.com/hyperledger/fabric/common/channelconfig"
	"github.com/hyperledger/fabric/common/configtx"
	"github.com/hyperledger/fabric/common/ledger/blockledger"
	"github.com/hyperledger/fabric/common/util"
	"github.com/hyperledger/fabric/internal/pkg/identity"
	"github.com/hyperledger/fabric/protoutil"
)

type blockWriterSupport interface {
	identity.SignerSerializer
	blockledger.ReadWriter
	configtx.Validator
	Update(*newchannelconfig.Bundle)
	CreateBundle(channelID string, config *cb.Config) (*newchannelconfig.Bundle, error)
	SharedConfig() newchannelconfig.Orderer
}

// BlockWriter efficiently writes the blockchain to disk.
// To safely use BlockWriter, only one thread should interact with it.
// BlockWriter will spawn additional committing go routines and handle locking
// so that these other go routines safely interact with the calling one.
type BlockWriter struct {
	support            blockWriterSupport
	registrar          *Registrar
	lastConfigBlockNum uint64
	lastConfigSeq      uint64
	lastBlock          *cb.Block
	committingBlock    sync.Mutex
}

func newBlockWriter(lastBlock *cb.Block, r *Registrar, support blockWriterSupport) *BlockWriter {
	bw := &BlockWriter{
		support:       support,
		lastConfigSeq: support.Sequence(),
		lastBlock:     lastBlock,
		registrar:     r,
	}

	// If this is the genesis block, the lastconfig field may be empty, and, the last config block is necessarily block 0
	// so no need to initialize lastConfig
	if lastBlock.Header.Number != 0 {
		var err error
		bw.lastConfigBlockNum, err = protoutil.GetLastConfigIndexFromBlock(lastBlock)
		if err != nil {
			logger.Panicf("[channel: %s] Error extracting last config block from block metadata: %s", support.ChannelID(), err)
		}
	}

	logger.Debugf("[channel: %s] Creating block writer for tip of chain (blockNumber=%d, lastConfigBlockNum=%d, lastConfigSeq=%d)", support.ChannelID(), lastBlock.Header.Number, bw.lastConfigBlockNum, bw.lastConfigSeq)
	return bw
}

// CreateNextBlock creates a new block with the next block number, and the given contents.
// 创建一个新的区块通过洗一个区块号和给定的消息切片
func (bw *BlockWriter) CreateNextBlock(messages []*cb.Envelope) *cb.Block {
	// 从上一个区块信息中获取到区块hash
	previousBlockHash := protoutil.BlockHeaderHash(bw.lastBlock.Header)
	// 交易数据
	data := &cb.BlockData{
		Data: make([][]byte, len(messages)),
	}

	var err error
	for i, msg := range messages {
		// 将切片中的每个交易编码
		data.Data[i], err = proto.Marshal(msg)
		if err != nil {
			logger.Panicf("Could not marshal envelope: %s", err)
		}
	}
	// 根据区块号和 前一个区块hash创建一个新的区块
	block := protoutil.NewBlock(bw.lastBlock.Header.Number+1, previousBlockHash)
	// 当前区块的hash计算
	block.Header.DataHash = protoutil.BlockDataHash(data)
	block.Data = data

	return block
}

// WriteConfigBlock should be invoked for blocks which contain a config transaction.
// This call will block until the new config has taken effect, then will return
// while the block is written asynchronously to disk.
func (bw *BlockWriter) WriteConfigBlock(block *cb.Block, encodedMetadataValue []byte) {
	ctx, err := protoutil.ExtractEnvelope(block, 0)
	if err != nil {
		logger.Panicf("Told to write a config block, but could not get configtx: %s", err)
	}

	payload, err := protoutil.UnmarshalPayload(ctx.Payload)
	if err != nil {
		logger.Panicf("Told to write a config block, but configtx payload is invalid: %s", err)
	}

	if payload.Header == nil {
		logger.Panicf("Told to write a config block, but configtx payload header is missing")
	}

	chdr, err := protoutil.UnmarshalChannelHeader(payload.Header.ChannelHeader)
	if err != nil {
		logger.Panicf("Told to write a config block with an invalid channel header: %s", err)
	}

	switch chdr.Type {
	case int32(cb.HeaderType_ORDERER_TRANSACTION):
		newChannelConfig, err := protoutil.UnmarshalEnvelope(payload.Data)
		if err != nil {
			logger.Panicf("Told to write a config block with new channel, but did not have config update embedded: %s", err)
		}
		bw.registrar.newChain(newChannelConfig)

	case int32(cb.HeaderType_CONFIG):
		configEnvelope, err := configtx.UnmarshalConfigEnvelope(payload.Data)
		if err != nil {
			logger.Panicf("Told to write a config block with new channel, but did not have config envelope encoded: %s", err)
		}

		err = bw.support.Validate(configEnvelope)
		if err != nil {
			logger.Panicf("Told to write a config block with new config, but could not apply it: %s", err)
		}

		bundle, err := bw.support.CreateBundle(chdr.ChannelId, configEnvelope.Config)
		if err != nil {
			logger.Panicf("Told to write a config block with a new config, but could not convert it to a bundle: %s", err)
		}

		oc, ok := bundle.OrdererConfig()
		if !ok {
			logger.Panicf("[channel: %s] OrdererConfig missing from bundle", bw.support.ChannelID())
		}

		currentType := bw.support.SharedConfig().ConsensusType()
		nextType := oc.ConsensusType()
		if currentType != nextType {
			encodedMetadataValue = nil
			logger.Debugf("[channel: %s] Consensus-type migration: maintenance mode, change from %s to %s, setting metadata to nil",
				bw.support.ChannelID(), currentType, nextType)
		}

		// Avoid Bundle update before the go-routine in WriteBlock() finished writing the previous block.
		// We do this (in particular) to prevent bw.support.Sequence() from advancing before the go-routine reads it.
		// In general, this prevents the StableBundle from changing before the go-routine in WriteBlock() finishes.
		bw.committingBlock.Lock()
		bw.committingBlock.Unlock()
		bw.support.Update(bundle)
	default:
		logger.Panicf("Told to write a config block with unknown header type: %v", chdr.Type)
	}

	bw.WriteBlockSync(block, encodedMetadataValue)
}

// WriteBlock should be invoked for blocks which contain normal transactions.
// It sets the target block as the pending next block, and returns before it is committed.
// Before returning, it acquires the committing lock, and spawns a go routine which will
// annotate the block with metadata and signatures, and write the block to the ledger
// then release the lock.  This allows the calling thread to begin assembling the next block
// before the commit phase is complete.

// 应该被包含普通交易的的区块调用
// 他将目标块设置为阻塞的下一个区块，并且在提交之前返回
// 在返回之前，他获得提交锁，生成以个goroutine使用元数据和签名来标注区块，并且将区块写入到账本，释放锁
// 这允许线程开始组装下一个块在提交完成之前
func (bw *BlockWriter) WriteBlock(block *cb.Block, encodedMetadataValue []byte) {
	bw.committingBlock.Lock() // 获得提交锁（互斥锁）
	bw.lastBlock = block

	go func() {
		defer bw.committingBlock.Unlock()
		// 提交区块
		bw.commitBlock(encodedMetadataValue)
	}()
}

// WriteBlockSync is same as WriteBlock, but commits block synchronously.
// Note: WriteConfigBlock should use WriteBlockSync instead of WriteBlock.
//       If the block contains a transaction that remove the node from consenters,
//       the node will switch to follower and pull blocks from other nodes.
//       Suppose writing block asynchronously, the block maybe not persist to disk
//       when the follower chain starts working. The follower chain will read a block
//       before the config block, in which the node is still a consenter, so the follower
//       chain will switch to the consensus chain. That's a dead loop!
//       So WriteConfigBlock should use WriteBlockSync instead of WriteBlock.
func (bw *BlockWriter) WriteBlockSync(block *cb.Block, encodedMetadataValue []byte) {
	bw.committingBlock.Lock()
	bw.lastBlock = block

	defer bw.committingBlock.Unlock()
	bw.commitBlock(encodedMetadataValue)
}

// commitBlock should only ever be invoked with the bw.committingBlock held
// this ensures that the encoded config sequence numbers stay in sync
// 应该只在持有 bw.committingBlock（提交锁）时候被调用，
// 这确保了编码的配置序列号保持同步
func (bw *BlockWriter) commitBlock(encodedMetadataValue []byte) {
	// 元数据添加 LAST_CONFIG
	bw.addLastConfig(bw.lastBlock)
	// 元数据添加签名
	bw.addBlockSignature(bw.lastBlock, encodedMetadataValue)
	//
	err := bw.support.Append(bw.lastBlock)
	if err != nil {
		logger.Panicf("[channel: %s] Could not append block: %s", bw.support.ChannelID(), err)
	}
	logger.Debugf("[channel: %s] Wrote block [%d]", bw.support.ChannelID(), bw.lastBlock.GetHeader().Number)
}

// metadata solt 0 添加区块签名
func (bw *BlockWriter) addBlockSignature(block *cb.Block, consenterMetadata []byte) {
	// 先设置区块头
	blockSignature := &cb.MetadataSignature{
		// 参数是接口类型 identity.Serializer ， 这里是由support接口实例实现 Serializer
		SignatureHeader: protoutil.MarshalOrPanic(protoutil.NewSignatureHeaderOrPanic(bw.support)),
	}
	//
	blockSignatureValue := protoutil.MarshalOrPanic(&cb.OrdererBlockMetadata{
		// 这里以 在GRPC protobuf中定义，需要编码的字段需要编码
		LastConfig:        &cb.LastConfig{Index: bw.lastConfigBlockNum},
		ConsenterMetadata: protoutil.MarshalOrPanic(&cb.Metadata{Value: consenterMetadata}),
	})

	blockSignature.Signature = protoutil.SignOrPanic(
		bw.support,
		// 连接规则：&cb.OrdererBlockMetadata + SignatureHeader + 当前区块头
		util.ConcatenateBytes(blockSignatureValue, blockSignature.SignatureHeader, protoutil.BlockHeaderBytes(block.Header)),
	)

	block.Metadata.Metadata[cb.BlockMetadataIndex_SIGNATURES] = protoutil.MarshalOrPanic(&cb.Metadata{
		Value: blockSignatureValue,
		Signatures: []*cb.MetadataSignature{
			blockSignature,
		},
	})
}

func (bw *BlockWriter) addLastConfig(block *cb.Block) {
	// 获取通道的配置序列号
	configSeq := bw.support.Sequence()
	// 如果获取到的配置序列号最新
	// TODO: 没搞明白
	if configSeq > bw.lastConfigSeq {
		// 获取到
		logger.Debugf("[channel: %s] Detected lastConfigSeq transitioning from %d to %d, setting lastConfigBlockNum from %d to %d", bw.support.ChannelID(), bw.lastConfigSeq, configSeq, bw.lastConfigBlockNum, block.Header.Number)
		// 当前区块号作为最新的配置区块
		bw.lastConfigBlockNum = block.Header.Number
		// 更新配置序列号
		bw.lastConfigSeq = configSeq
	}
	// 编码最后一个配置区块的索引号（也就是区块号）
	lastConfigValue := protoutil.MarshalOrPanic(&cb.LastConfig{Index: bw.lastConfigBlockNum})
	logger.Debugf("[channel: %s] About to write block, setting its LAST_CONFIG to %d", bw.support.ChannelID(), bw.lastConfigBlockNum)
	// 区块元数据元数据添加 LAST_CONFIG它对应的插槽是 1
	block.Metadata.Metadata[cb.BlockMetadataIndex_LAST_CONFIG] = protoutil.MarshalOrPanic(&cb.Metadata{
		Value: lastConfigValue,
	})
}
