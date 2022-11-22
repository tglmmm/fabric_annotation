/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package blockcutter

import (
	"time"

	cb "github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/common/channelconfig"
	"github.com/hyperledger/fabric/common/flogging"
)

var logger = flogging.MustGetLogger("orderer.common.blockcutter")

type OrdererConfigFetcher interface {
	OrdererConfig() (channelconfig.Orderer, bool)
}

// Receiver defines a sink for the ordered broadcast messages
// 给有序的广播消息定义接收器
type Receiver interface {
	// Ordered should be invoked sequentially as messages are ordered
	// Each batch in `messageBatches` will be wrapped into a block.
	// `pending` indicates if there are still messages pending in the receiver.
	// 消息排序时候持续调用，每一批消息都被打包到block
	// pedding 指示是否有消息在接收器中处于pedding
	Ordered(msg *cb.Envelope) (messageBatches [][]*cb.Envelope, pending bool)

	// Cut returns the current batch and starts a new one
	// 返回当前批处理，并且创建一个新的
	Cut() []*cb.Envelope
}

type receiver struct {
	sharedConfigFetcher   OrdererConfigFetcher // 实现返回orderer配置函数，需要知道出块相关的配置
	pendingBatch          []*cb.Envelope       // 等待打包的交易切片
	pendingBatchSizeBytes uint32               // 等待打包的所有交易的大小

	PendingBatchStartTime time.Time // 这次打包开始时间
	ChannelID             string
	Metrics               *Metrics
}

// NewReceiverImpl creates a Receiver implementation based on the given configtxorderer manager
func NewReceiverImpl(channelID string, sharedConfigFetcher OrdererConfigFetcher, metrics *Metrics) Receiver {
	return &receiver{
		sharedConfigFetcher: sharedConfigFetcher,
		Metrics:             metrics,
		ChannelID:           channelID,
	}
}

// ordered 应该按照消息得顺序被调用
// Ordered should be invoked sequentially as messages are ordered

// messageBatches length: 0, pending: false
// 我们刚刚接收到消息
//   - impossible, as we have just received a message

// messageBatches length: 0, pending: true
// 没有需要批处理得消息需要cut ， 当前处于阻塞状态
//   - no batch is cut and there are messages pending

// messageBatches length: 1, pending: false
// 消息得数量达到了 最大消息数量
//   - the message count reaches BatchSize.MaxMessageCount

// messageBatches length: 1, pending: true
// 当前消息将导致阻塞得batch大小超出 PreferredMaxBytes
//   - the current message will cause the pending batch size in bytes to exceed BatchSize.PreferredMaxBytes.

// messageBatches length: 2, pending: false
// 当前消息大小 超出了PreferredMaxBytes ，所以在自己得批次中被隔离
//   - the current message size in bytes exceeds BatchSize.PreferredMaxBytes, therefore isolated in its own batch.

// messageBatches length: 2, pending: true
//   - impossible

// Note that messageBatches can not be greater than 2.
// 排序服务
// 消息批处理不能超过2，这种情况主要原因是缓存中已经有交易等待打包，但是其中一个交易大小超出了PreferredMaxBytes，所以需要将其单独打包到一个块中
// 这里的 messageBatches 相当于块的切片，这个块的切片不能大于2
func (r *receiver) Ordered(msg *cb.Envelope) (messageBatches [][]*cb.Envelope, pending bool) {
	if len(r.pendingBatch) == 0 {
		// We are beginning a new batch, mark the time
		// 说明当前还没有任何交易被缓存
		// 从一个新的batch开始，记录时间
		r.PendingBatchStartTime = time.Now()
	}
	// 获取orderer配置
	ordererConfig, ok := r.sharedConfigFetcher.OrdererConfig()
	if !ok {
		logger.Panicf("Could not retrieve orderer config to query batch parameters, block cutting is not possible")
	}
	// configtx中配置 关于出块相关得配置
	batchSize := ordererConfig.BatchSize()
	// 获取msg 字节数
	messageSizeBytes := messageSizeBytes(msg)
	// 如果当前message> 设置的PreferredMaxBytes
	// 如果一个交易的大小超过了PreferredMaxBytes ， 那么需要单独打包到一个区块中
	if messageSizeBytes > batchSize.PreferredMaxBytes {
		logger.Debugf("The current message, with %v bytes, is larger than the preferred batch size of %v bytes and will be isolated.", messageSizeBytes, batchSize.PreferredMaxBytes)

		// cut pending batch, if it has any messages
		// 如果缓存中已经存在其他交易执行切割
		if len(r.pendingBatch) > 0 {
			messageBatch := r.Cut()
			// 把当前缓存的交易放到队列中
			messageBatches = append(messageBatches, messageBatch)
		}

		// create new batch with single message
		// 如果当前缓存中没有交易，那就把当前的交易打包到一个块中
		messageBatches = append(messageBatches, []*cb.Envelope{msg})

		// Record that this batch took no time to fill
		// 记录这次填充没有花费时间，因为区块中只有一个交易
		r.Metrics.BlockFillDuration.With("channel", r.ChannelID).Observe(0)

		return
	}
	// 判断当前缓存中交易大小 + 当前交易大小 >  PreferredMaxBytes

	messageWillOverflowBatchSizeBytes := r.pendingBatchSizeBytes+messageSizeBytes > batchSize.PreferredMaxBytes

	if messageWillOverflowBatchSizeBytes {
		logger.Debugf("The current message, with %v bytes, will overflow the pending batch of %v bytes.", messageSizeBytes, r.pendingBatchSizeBytes)
		// 如果当前的交易被添加到缓存中会导致Pending大小溢出，所以需要cut
		logger.Debugf("Pending batch would overflow if current message is added, cutting batch now.")
		messageBatch := r.Cut()
		r.PendingBatchStartTime = time.Now()
		messageBatches = append(messageBatches, messageBatch)
	}
	logger.Debugf("Enqueuing message into batch")
	// 将当前的交易添加到缓存中
	r.pendingBatch = append(r.pendingBatch, msg)
	// 缓存随着交易大小累加
	r.pendingBatchSizeBytes += messageSizeBytes
	// 标识缓存中有交易
	pending = true
	// 判断是否超出了一个区块中最大的交易数，如果超出需要执行cut
	if uint32(len(r.pendingBatch)) >= batchSize.MaxMessageCount {
		logger.Debugf("Batch size met, cutting batch")
		messageBatch := r.Cut()
		messageBatches = append(messageBatches, messageBatch)
		pending = false
	}

	return
}

// Cut returns the current batch and starts a new one
// 将当前缓存中交易切片打包
func (r *receiver) Cut() []*cb.Envelope {
	if r.pendingBatch != nil {
		r.Metrics.BlockFillDuration.With("channel", r.ChannelID).Observe(time.Since(r.PendingBatchStartTime).Seconds())
	}
	batch := r.pendingBatch
	// reset batch
	// 一次打包完成时候需要初始化 ： 新的包开始时间，缓存的交易， 缓存交易的纵大小
	r.PendingBatchStartTime = time.Time{}
	r.pendingBatch = nil
	r.pendingBatchSizeBytes = 0
	return batch
}

// 计算交易的总字节数
func messageSizeBytes(message *cb.Envelope) uint32 {
	// len(envelop) = len(payload) + len(signature)
	return uint32(len(message.Payload) + len(message.Signature))
}
