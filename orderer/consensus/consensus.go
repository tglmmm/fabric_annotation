/*
Copyright IBM Corp. 2017 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// 接口的定义
// Consenter
// ClusterConster
// MetadataValidator
// Chain
// ConsenterSupport

package consensus

import (
	cb "github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/common/channelconfig"
	"github.com/hyperledger/fabric/internal/pkg/identity"
	"github.com/hyperledger/fabric/orderer/common/blockcutter"
	"github.com/hyperledger/fabric/orderer/common/msgprocessor"
	"github.com/hyperledger/fabric/protoutil"
)

// Consenter defines the backing ordering mechanism.
// 定义后台的排序机制
type Consenter interface {
	// HandleChain should create and return a reference to a Chain for the given set of resources.
	// It will only be invoked for a given chain once per process.  In general, errors will be treated as irrecoverable and cause system shutdown.
	// See the description of Chain for more details
	// The second argument to HandleChain is a pointer to the metadata stored on the `ORDERER` slot of
	// the last block committed to the ledger of this Chain. For a new chain, or one which is migrated,
	// this metadata will be nil (or contain a zero-length Value), as there is no prior metadata to report.
	// 应该对给定的资源集合创建和返回一个链的引用
	// 对于给定的链每个流程将会被调用一次，通常错误是不可恢复的将会导致系统的关闭,更多细节查看 Chain
	//HandleChain的第二个参数是一个指针存储在 ORDERER槽，在链账本的最后一个被提交的区块
	// 对于一个新的链或者被迁移的链，这个元数据可能是nil,
	HandleChain(support ConsenterSupport, metadata *cb.Metadata) (Chain, error)
}

// ClusterConsenter defines methods implemented by cluster-type consenters.
// 定义的方法被 consenters（cluster-type）实现
type ClusterConsenter interface {
	// IsChannelMember inspects the join block and detects whether it implies that this orderer is a member of the channel
	// It returns true if the orderer is a member of the consenters set, and false if it is not. The method
	// also inspects the consensus type metadata for validity. It returns an error if membership cannot be determined
	// due to errors processing the block.
	// 审查 join block查看他是否指明orderer是通道的成员
	// 当orderer是共识集合的成员时候返回true , 如果不是返回false
	// 这个方法同时检查共识类型元数据有效性，如果成员不能被确定处理块出错返回error
	IsChannelMember(joinBlock *cb.Block) (bool, error)
	// RemoveInactiveChainRegistry stops and removes the inactive chain registry.
	// This is used when removing the system channel.
	// 停止并删除不活跃的链注册器
	// 这是有用的当删除系统通道时候
	RemoveInactiveChainRegistry()
}

// MetadataValidator performs the validation of updates to ConsensusMetadata during config updates to the channel.
// NOTE: We expect the MetadataValidator interface to be optionally implemented by the Consenter implementation.
//       If a Consenter does not implement MetadataValidator, we default to using a no-op MetadataValidator.

// 在通道配置更新期间验证 consensusMetadata的验证
// 我们期望 metadataValidator接口被 Consenter 实现
// 如果一个共识者美哟普实现 MetadataValidator ，默认使用  NoOpMetadataValidator
type MetadataValidator interface {
	// ValidateConsensusMetadata determines the validity of a ConsensusMetadata update during config
	// updates on the channel.
	// Since the ConsensusMetadata is specific to the consensus implementation (independent of the particular
	// chain) this validation also needs to be implemented by the specific consensus implementation.
	ValidateConsensusMetadata(oldOrdererConfig, newOrdererConfig channelconfig.Orderer, newChannel bool) error
}

// Chain defines a way to inject messages for ordering.
// Note, that in order to allow flexibility in the implementation, it is the responsibility of the implementer
// to take the ordered messages, send them through the blockcutter.Receiver supplied via HandleChain to cut blocks,
// and ultimately write the ledger also supplied via HandleChain.  This design allows for two primary flows
//
// 消息被排序进入 stream, stream 被分割成块，块被提交
// 1. Messages are ordered into a stream, the stream is cut into blocks, the blocks are committed (solo, kafka)
// 消息被切割成块，块被排序，区块被提交（简化版的拜占庭容错）
// 2. Messages are cut into blocks, the blocks are ordered, then the blocks are committed (sbft)
type Chain interface {
	// Order accepts a message which has been processed at a given configSeq.
	// If the configSeq advances, it is the responsibility of the consenter
	// to revalidate and potentially discard the message
	// The consenter may return an error, indicating the message was not accepted

	// 接收一个message已经被 给定配置序列号对应配置处理过的消息
	// 如果配置序列号推进，这是共识者的责任重新验证并且可能会丢弃消息
	// 共识者有可能返回error ,表明消息没有被接受
	Order(env *cb.Envelope, configSeq uint64) error

	// Configure accepts a message which reconfigures the channel and will
	// trigger an update to the configSeq if committed.  The configuration must have
	// been triggered by a ConfigUpdate message. If the config sequence advances,
	// it is the responsibility of the consenter to recompute the resulting config,
	// discarding the message if the reconfiguration is no longer valid.
	// The consenter may return an error, indicating the message was not accepted

	// 接收一个消息 重新配置通道，并且将触发一个配置的序列号更新如果提交的话，
	// 如果配置必须被 ConfigUpdate消息触发，如果配置序列累加，共识者有责任重新计算配置结果
	// 如果重新配置不在有效则丢弃消息
	// 共识者可能返回错误，表明消息没有被接收
	Configure(config *cb.Envelope, configSeq uint64) error

	// WaitReady blocks waiting for consenter to be ready for accepting new messages.
	// This is useful when consenter needs to temporarily block ingress messages so
	// that in-flight messages can be consumed. It could return error if consenter is
	// in erroneous states. If this blocking behavior is not desired, consenter could
	// simply return nil.
	// 区块等待被共识者接收，这是有用的当共识者需要临时阻止消息进入时候，他将返回一个错误如果共识者是错误状态时候，
	// 如果不需要这种阻塞行为，共识者返回nil
	WaitReady() error

	// Errored returns a channel which will close when an error has occurred.
	// This is especially useful for the Deliver client, who must terminate waiting clients when the consenter is not up to date.
	// 返回在发生错误时候关闭的通道
	// 这对于Deliver客户机特别有用，因为当同意者没有更新时，它必须终止等待的客户机
	Errored() <-chan struct{}

	// Start should allocate whatever resources are needed for staying up to date with the chain.
	// Typically, this involves creating a thread which reads from the ordering source, passes those
	// messages to a block cutter, and writes the resulting blocks to the ledger.

	// 分配任何链更新所需要的资源
	// 通常这涉及到创建一个从orderer读取的线程，传递消息到切割器，并且写入结果区块到账本
	Start()

	// Halt frees the resources which were allocated for this Chain.
	// 释放分配给该链的资源
	Halt()
}

//go:generate counterfeiter -o mocks/mock_consenter_support.go . ConsenterSupport

// ConsenterSupport provides the resources available to a Consenter implementation.
// 提供资源为Consenter的实现
type ConsenterSupport interface {
	identity.SignerSerializer // 包含身份序列化，消息签名方法
	msgprocessor.Processor    // 消息分类加工

	// VerifyBlockSignature verifies a signature of a block with a given optional
	// configuration (can be nil).
	// 通过给定的参数选项（可以是nil），验证区块签名
	VerifyBlockSignature([]*protoutil.SignedData, *cb.ConfigEnvelope) error

	// BlockCutter returns the block cutting helper for this channel.
	// blockCutter 通道的区块进行分割
	BlockCutter() blockcutter.Receiver

	// SharedConfig provides the shared config from the channel's current config block.
	// 从通道的当前配置区块，提供共有的配置信息
	SharedConfig() channelconfig.Orderer

	// ChannelConfig provides the channel config from the channel's current config block.
	// 从通道当前配置信息提供通道的配置
	ChannelConfig() channelconfig.Channel

	// CreateNextBlock takes a list of messages and creates the next block based on the block with highest block number committed to the ledger
	// Note that either WriteBlock or WriteConfigBlock must be called before invoking this method a second time.
	// 获取消息列表并且通过最高的区块号来创建下一个区块并提交到账本
	// 注意： 第二次调用此方法之前这两个要吗 WriteBlock 要吗 WriteConfigBlock被调用
	CreateNextBlock(messages []*cb.Envelope) *cb.Block

	// Block returns a block with the given number,
	// or nil if such a block doesn't exist.
	// 通过区块号返回对应区块
	Block(number uint64) *cb.Block

	// WriteBlock commits a block to the ledger.
	// 提交区块到账本
	WriteBlock(block *cb.Block, encodedMetadataValue []byte)

	// WriteConfigBlock commits a block to the ledger, and applies the config update inside.
	// 提交区块到账本， 并在内部应用配置更新
	WriteConfigBlock(block *cb.Block, encodedMetadataValue []byte)

	// Sequence returns the current config sequence.
	// 返回当前配置的序列号
	Sequence() uint64

	// ChannelID returns the channel ID this support is associated with.
	// 返回与当前ConsenterSupport关联的ChannelID
	ChannelID() string

	// Height returns the number of blocks in the chain this channel is associated with.
	// 返回与当前通道相关联的区块高度
	Height() uint64

	// Append appends a new block to the ledger in its raw form,
	// unlike WriteBlock that also mutates its metadata.
	// 以原始形式在账本中追加新的块
	// 不像 WriteBlock ，这会改变元数据
	Append(block *cb.Block) error
}

// NoOpMetadataValidator implements a MetadataValidator that always returns nil error irrespecttive of the inputs.
// 实现一个 MetadataValidator 无论输入什么总是返回nil
type NoOpMetadataValidator struct{}

// ValidateConsensusMetadata determines the validity of a ConsensusMetadata update during config updates
// on the channel, and it always returns nil error for the NoOpMetadataValidator implementation.
// 确定在通道配置更新期间 ConsensusMetadata 有效性，他总是返回Nil
func (n NoOpMetadataValidator) ValidateConsensusMetadata(oldChannelConfig, newChannelConfig channelconfig.Orderer, newChannel bool) error {
	return nil
}
