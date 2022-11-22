/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package channelparticipation

import (
	"errors"

	cb "github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/bccsp/factory"
	"github.com/hyperledger/fabric/common/channelconfig"
	"github.com/hyperledger/fabric/protoutil"
)

// ValidateJoinBlock checks whether this block can be used as a join block for the channel participation API.
// It returns the channel ID, and whether it is an system channel if it contains consortiums, or otherwise
// an application channel if an application group exists. It returns an error when it cannot be used as a join-block.

// 验证 加入某个配置区块
// 检查是否这个区块可以被用来作为一个加入区块
// 他返回channelID ， 是否是一个系统通道如果包含consortiums(联盟) , 否则如果application group存在就是一个应用通道，
// 如果返回错误不能被用来作为加入通道的block
func ValidateJoinBlock(configBlock *cb.Block) (channelID string, isAppChannel bool, err error) {
	// 检查是否是配置交易区块或者 orderer_tx
	if !protoutil.IsConfigBlock(configBlock) {
		return "", false, errors.New("block is not a config block")
	}
	// 获取envelope
	envelope, err := protoutil.ExtractEnvelope(configBlock, 0)
	if err != nil {
		return "", false, err
	}
	// BCCSP
	cryptoProvider := factory.GetDefault()
	bundle, err := channelconfig.NewBundleFromEnvelope(envelope, cryptoProvider)
	if err != nil {
		return "", false, err
	}

	channelID = bundle.ConfigtxValidator().ChannelID()

	// Check channel type
	_, isSystemChannel := bundle.ConsortiumsConfig()
	if !isSystemChannel {
		_, isAppChannel = bundle.ApplicationConfig()
		if !isAppChannel {
			return "", false, errors.New("invalid config: must have at least one of application or consortiums")
		}
	}

	return channelID, isAppChannel, err
}
