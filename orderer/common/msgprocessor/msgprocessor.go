/*
Copyright IBM Corp. 2017 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package msgprocessor provides the implementations for processing of the assorted message
// types which may arrive in the system through Broadcast.
package msgprocessor

import (
	"errors"

	cb "github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/common/flogging"
)

const (
	// These should eventually be derived from the channel support once enabled
	// 一旦启用这些应该最终被channel support 派生出来
	msgVersion = int32(0)
	epoch      = 0
)

var logger = flogging.MustGetLogger("orderer.common.msgprocessor")

// ErrChannelDoesNotExist is returned by the system channel for transactions which
// are not for the system channel ID and are not attempting to create a new channel
var ErrChannelDoesNotExist = errors.New("channel does not exist")

// ErrPermissionDenied is returned by errors which are caused by transactions
// which are not permitted due to an authorization failure.
var ErrPermissionDenied = errors.New("permission denied")

// ErrMaintenanceMode is returned when transactions are rejected because the orderer is in "maintenance mode",
// as defined by ConsensusType.State != NORMAL. This typically happens during consensus-type migration.
var ErrMaintenanceMode = errors.New("maintenance mode")

// Classification represents the possible message types for the system.
// 系统上消息类型分类
type Classification int

const (
	// NormalMsg is the class of standard (endorser or otherwise non-config) messages.
	// Messages of this type should be processed by ProcessNormalMsg.
	// 普通消息（背书或者其他非配置类型）的消息，这种消息类型应该被ProcessNormalMsg处理
	NormalMsg Classification = iota

	// ConfigUpdateMsg indicates messages of type CONFIG_UPDATE.
	// Messages of this type should be processed by ProcessConfigUpdateMsg.
	// 表明消息是一个配置更新消息，应该被 ProcessConfigUpdateMsg
	ConfigUpdateMsg

	// ConfigMsg indicates message of type ORDERER_TRANSACTION or CONFIG.
	// Messages of this type should be processed by ProcessConfigMsg
	// 配置消息
	ConfigMsg
)

// Processor provides the methods necessary to classify and process any message which
// arrives through the Broadcast interface.
// 提供一个必要的方法去分类加工消息，他们通过广播接口到达
// 简而言之就是消息进行加工
type Processor interface {
	// ClassifyMsg inspects the message header to determine which type of processing is necessary
	// 检查消息头确定消息是那种类型
	ClassifyMsg(chdr *cb.ChannelHeader) Classification

	// ProcessNormalMsg will check the validity of a message based on the current configuration.  It returns the current
	// configuration sequence number and nil on success, or an error if the message is not valid
	// 普通消息进行处理
	// 将验证消息有效性基于当前的配置，他将返回当前配置的序列号， 如果成功则再返回Nil,否则返回一个错误如果消息不可用的话
	ProcessNormalMsg(env *cb.Envelope) (configSeq uint64, err error)

	// ProcessConfigUpdateMsg will attempt to apply the config update to the current configuration, and if successful
	// return the resulting config message and the configSeq the config was computed from.  If the config update message
	// is invalid, an error is returned.
	// 是尝试应用配置更新到当前配置，如果成功返回配置的消息结果，配置的序列号， ，如果配置消息更新成功返回nil否则返回错误
	ProcessConfigUpdateMsg(env *cb.Envelope) (config *cb.Envelope, configSeq uint64, err error)

	// ProcessConfigMsg takes message of type `ORDERER_TX` or `CONFIG`, unpack the ConfigUpdate envelope embedded
	// in it, and call `ProcessConfigUpdateMsg` to produce new Config message of the same type as original message.
	// This method is used to re-validate and reproduce config message, if it's deemed not to be valid anymore.
	// 将类型为 ORDERER_TX , CONFIG 解包配置信息从envelop中，调用ProcessConfigUpdateMsg 生成新的与原始消息相同的配置消息
	// 这个方法被用来 再一次验证并且加工配置消息，如果被认为是无效的
	ProcessConfigMsg(env *cb.Envelope) (*cb.Envelope, uint64, error)
}
