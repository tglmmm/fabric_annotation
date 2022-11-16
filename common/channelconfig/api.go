/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package channelconfig

import (
	"time"

	cb "github.com/hyperledger/fabric-protos-go/common"
	ab "github.com/hyperledger/fabric-protos-go/orderer"
	pb "github.com/hyperledger/fabric-protos-go/peer"
	"github.com/hyperledger/fabric/common/configtx"
	"github.com/hyperledger/fabric/common/policies"
	"github.com/hyperledger/fabric/msp"
)

// Org stores the common organizational config
// 存储组织公共配置
type Org interface {
	// Name returns the name this org is referred to in config
	Name() string

	// MSPID returns the MSP ID associated with this org
	MSPID() string

	// MSP returns the MSP implementation for this org.
	MSP() msp.MSP
}

// ApplicationOrg stores the per org application config

// 存储一个组织的应用的程序配置
type ApplicationOrg interface {
	Org

	// AnchorPeers returns the list of gossip anchor peers
	// 返回锚节点信息
	AnchorPeers() []*pb.AnchorPeer
}

// OrdererOrg stores the per org orderer config.
// 存储一个组织的orderer组织配置
type OrdererOrg interface {
	Org

	// Endpoints returns the endpoints of orderer nodes.
	// 返回端点信息
	Endpoints() []string
}

// Application stores the common shared application config
// 存储应用程序公共共享的配置部分
type Application interface {
	// Organizations returns a map of org ID to ApplicationOrg
	Organizations() map[string]ApplicationOrg

	// APIPolicyMapper returns a PolicyMapper that maps API names to policies
	APIPolicyMapper() PolicyMapper

	// Capabilities defines the capabilities for the application portion of a channel
	// 为通道的应用程序部分定义功能
	Capabilities() ApplicationCapabilities
}

// Channel gives read only access to the channel configuration
// 只读的通道配置
type Channel interface {
	// HashingAlgorithm returns the default algorithm to be used when hashing
	// such as computing block hashes, and CreationPolicy digests
	// 返回默认的被用来做块计算和CreationPolicy摘要的默认hash算法
	HashingAlgorithm() func(input []byte) []byte

	// BlockDataHashingStructureWidth returns the width to use when constructing the  Merkle tree to compute the BlockData hash
	// 返回构建默克尔树计算blockData hash的宽度
	BlockDataHashingStructureWidth() uint32

	// OrdererAddresses returns the list of valid orderer addresses to connect to to invoke Broadcast/Deliver
	// 返回可用的orderer地址 可以连接调用 广播/发布
	OrdererAddresses() []string

	// Capabilities defines the capabilities for a channel
	// 定义通道功能
	Capabilities() ChannelCapabilities
}

// Consortiums represents the set of consortiums serviced by an ordering service
// 代表联盟服务的集合
type Consortiums interface {
	// Consortiums returns the set of consortiums
	Consortiums() map[string]Consortium
}

// Consortium represents a group of orgs which may create channels together
// 代表一组，可以一起创建通道的组织
type Consortium interface {
	// ChannelCreationPolicy returns the policy to check when instantiating a channel for this consortium
	// 返回当为联盟初始化一个通道时候检查的策略
	ChannelCreationPolicy() *cb.Policy

	// Organizations returns the organizations for this consortium
	// 返回联盟中的所有组织
	Organizations() map[string]Org
}

// Orderer stores the common shared orderer config
// 存储orderer公共共享部分配置
type Orderer interface {
	// ConsensusType returns the configured consensus type
	// 共识类型
	ConsensusType() string

	// ConsensusMetadata returns the metadata associated with the consensus type.
	// 共识的元数据
	ConsensusMetadata() []byte

	// ConsensusState returns the consensus-type state.
	// 共识状态 ， 正常状态， 维护状态
	ConsensusState() ab.ConsensusType_State

	// BatchSize returns the maximum number of messages to include in a block
	// 返回包含在块中的最大消息数量
	BatchSize() *ab.BatchSize

	// BatchTimeout returns the amount of time to wait before creating a batch
	// 返回在创建批处理之前等待的时间
	BatchTimeout() time.Duration

	// MaxChannelsCount returns the maximum count of channels to allow for an ordering network
	// 排序网络允许的最大通道数量
	MaxChannelsCount() uint64

	// KafkaBrokers returns the addresses (IP:port notation) of a set of "bootstrap"
	// Kafka brokers, i.e. this is not necessarily the entire set of Kafka brokers
	// used for ordering
	// kafka brokers
	KafkaBrokers() []string

	// Organizations returns the organizations for the ordering service
	// 返回排序服务的组织
	Organizations() map[string]OrdererOrg

	// Capabilities defines the capabilities for the orderer portion of a channel
	// 为通道的orderer部分定义功能
	Capabilities() OrdererCapabilities
}

// ChannelCapabilities defines the capabilities for a channel
type ChannelCapabilities interface {
	// Supported returns an error if there are unknown capabilities in this channel which are required
	// 如果是未知的功能则返回错误
	Supported() error

	// MSPVersion specifies the version of the MSP this channel must understand, including the MSP types
	// and MSP principal types.
	// 指定此通道必须理解的msp版本，和MSP类型
	MSPVersion() msp.MSPVersion

	// ConsensusTypeMigration return true if consensus-type migration is permitted in both orderer and peer.
	// 如果orderer/peer 都允许共识类型的迁移则返回true
	ConsensusTypeMigration() bool

	// OrgSpecificOrdererEndpoints return true if the channel config processing allows orderer orgs to specify their own endpoints
	// 如果通道配置允许Orderer组织指定他们自己的端点则返回true
	OrgSpecificOrdererEndpoints() bool
}

// ApplicationCapabilities defines the capabilities for the application portion of a channel
// 定义应用程序通道功能的的部分
type ApplicationCapabilities interface {
	// Supported returns an error if there are unknown capabilities in this channel which are required
	// 如果不支持的功能就会返回错误
	Supported() error

	// ForbidDuplicateTXIdInBlock specifies whether two transactions with the same TXId are permitted
	// in the same block or whether we mark the second one as TxValidationCode_DUPLICATE_TXID
	// 指定是否允许具有相同txId的两个事物在同一个区块中
	// 或者是否我们将其中第二个交易标记为 TxValidationCode_DUPLICATE_TXID
	ForbidDuplicateTXIdInBlock() bool

	// ACLs returns true is ACLs may be specified in the Application portion of the config tree
	// ACL在通道配置应用程序部分被指定
	ACLs() bool

	// PrivateChannelData returns true if support for private channel data (a.k.a. collections) is enabled.
	// In v1.1, the private channel data is experimental and has to be enabled explicitly.
	// In v1.2, the private channel data is enabled by default.
	// 如果启用了通道私有数据的支持
	// v1.1中私有通道数据是实验性的必须显示的启用
	// v1.2中默认启用
	PrivateChannelData() bool

	// CollectionUpgrade returns true if this channel is configured to allow updates to
	// existing collection or add new collections through chaincode upgrade (as introduced in v1.2)
	// 如果通道被配置为允许更新已经存在的集合，或者通过chaincode添加新的集合返回true
	CollectionUpgrade() bool

	// V1_1Validation returns true is this channel is configured to perform stricter validation
	// of transactions (as introduced in v1.1).
	// 通道被配置为执行更严格的交易验证
	V1_1Validation() bool

	// V1_2Validation returns true is this channel is configured to perform stricter validation
	// of transactions (as introduced in v1.2).
	//
	V1_2Validation() bool

	// V1_3Validation returns true if this channel supports transaction validation
	// as introduced in v1.3. This includes:
	//  - policies expressible at a ledger key granularity, as described in FAB-8812
	//  - new chaincode lifecycle, as described in FAB-11237
	V1_3Validation() bool

	// StorePvtDataOfInvalidTx returns true if the peer needs to store the pvtData of invalid transactions (as introduced in v142).
	// 如果peer节点需要存储无效交易的privateData则返回true
	StorePvtDataOfInvalidTx() bool

	// V2_0Validation returns true if this channel supports transaction validation
	// as introduced in v2.0. This includes:
	//  - new chaincode lifecycle
	//  - implicit per-org collections
	V2_0Validation() bool

	// LifecycleV20 indicates whether the peer should use the deprecated and problematic
	// v1.x lifecycle, or whether it should use the newer per channel approve/commit definitions
	// process introduced in v2.0.  Note, this should only be used on the endorsing side
	// of peer processing, so that we may safely remove all checks against it in v2.1.
	LifecycleV20() bool

	// MetadataLifecycle always returns false
	MetadataLifecycle() bool

	// KeyLevelEndorsement returns true if this channel supports endorsement
	// policies expressible at a ledger key granularity, as described in FAB-8812
	// 如果支持背书则返回true,支持可在账本键粒度
	KeyLevelEndorsement() bool
}

// OrdererCapabilities defines the capabilities for the orderer portion of a channel
type OrdererCapabilities interface {
	// PredictableChannelTemplate specifies whether the v1.0 undesirable behavior of setting the /Channel
	// group's mod_policy to "" and copy versions from the orderer system channel config should be fixed or not.
	PredictableChannelTemplate() bool

	// Resubmission specifies whether the v1.0 non-deterministic commitment of tx should be fixed by re-submitting
	// the re-validated tx.
	Resubmission() bool

	// Supported returns an error if there are unknown capabilities in this channel which are required
	Supported() error

	// ExpirationCheck specifies whether the orderer checks for identity expiration checks
	// when validating messages
	ExpirationCheck() bool

	// ConsensusTypeMigration checks whether the orderer permits a consensus-type migration.
	ConsensusTypeMigration() bool

	// UseChannelCreationPolicyAsAdmins checks whether the orderer should use more sophisticated
	// channel creation logic using channel creation policy as the Admins policy if
	// the creation transaction appears to support it.
	UseChannelCreationPolicyAsAdmins() bool
}

// PolicyMapper is an interface for
type PolicyMapper interface {
	// PolicyRefForAPI takes the name of an API, and returns the policy name
	// or the empty string if the API is not found
	PolicyRefForAPI(apiName string) string
}

// Resources is the common set of config resources for all channels
// Depending on whether chain is used at the orderer or at the peer, other
// config resources may be available
type Resources interface {
	// ConfigtxValidator returns the configtx.Validator for the channel
	// 返回通道的 configtx验证器
	ConfigtxValidator() configtx.Validator

	// PolicyManager returns the policies.Manager for the channel
	// 返回通道的策略管理
	PolicyManager() policies.Manager

	// ChannelConfig returns the config.Channel for the chain
	ChannelConfig() Channel

	// OrdererConfig returns the config.Orderer for the channel
	// and whether the Orderer config exists
	OrdererConfig() (Orderer, bool)

	// ConsortiumsConfig returns the config.Consortiums for the channel
	// and whether the consortiums' config exists
	ConsortiumsConfig() (Consortiums, bool)

	// ApplicationConfig returns the configtxapplication.SharedConfig for the channel
	// and whether the Application config exists
	ApplicationConfig() (Application, bool)

	// MSPManager returns the msp.MSPManager for the chain
	MSPManager() msp.MSPManager

	// ValidateNew should return an error if a new set of configuration resources is incompatible with the current one
	ValidateNew(resources Resources) error
}
