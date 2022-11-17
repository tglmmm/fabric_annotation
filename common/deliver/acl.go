/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package deliver

import (
	"time"

	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/protoutil"
	"github.com/pkg/errors"
)

// ExpiresAtFunc is used to extract the time at which an identity expires.
// 用于提取身份过期的时间
type ExpiresAtFunc func(identityBytes []byte) time.Time

// ConfigSequencer provides the sequence number of the current config block.
// 提供当前配置块的序列（一般被保存在metadata中）
type ConfigSequencer interface {
	Sequence() uint64
}

// NewSessionAC creates an instance of SessionAccessControl. This constructor will
// return an error if a signature header cannot be extracted from the envelope.
// 创建一个SessionAccessControl 实例，这个构造函数将返回错误（如果签名的头部不能从envelope被提取）
func NewSessionAC(chain ConfigSequencer, env *common.Envelope, policyChecker PolicyChecker, channelID string, expiresAt ExpiresAtFunc) (*SessionAccessControl, error) {
	// 从envelope中解析出SignedData
	signedData, err := protoutil.EnvelopeAsSignedData(env)
	if err != nil {
		return nil, err
	}

	return &SessionAccessControl{
		envelope:      env,
		channelID:     channelID,
		sequencer:     chain,
		policyChecker: policyChecker,
		// 这里的Identity 是Envelope头部的证书（待验证）
		sessionEndTime: expiresAt(signedData[0].Identity),
	}, nil
}

// SessionAccessControl holds access control related data for a common Envelope
// that is used to determine if a request is allowed for the identity associated with the request envelope.

// 保存公共的Envelope的访问控制数据
// 被用于决定 是否允许请求与请求envelope相关联的标识。
type SessionAccessControl struct {
	sequencer          ConfigSequencer // 提供当前配置块的序列号
	policyChecker      PolicyChecker   //
	channelID          string
	envelope           *common.Envelope
	lastConfigSequence uint64
	sessionEndTime     time.Time
	usedAtLeastOnce    bool
}

// Evaluate uses the PolicyChecker to determine if a request should be allowed.
// The decision is cached until the identity expires or the chain configuration changes.
// 使用 PolicyChecker 确定请求是否被允许， 决策被缓存直到身份过期或者配置被更改
func (ac *SessionAccessControl) Evaluate() error {
	// sessionEndTime 不是0且 当前时间大于sessionEndTime，发送请求的客户端身份过期
	if !ac.sessionEndTime.IsZero() && time.Now().After(ac.sessionEndTime) {
		return errors.Errorf("deliver client identity expired %v before", time.Since(ac.sessionEndTime))
	}

	policyCheckNeeded := !ac.usedAtLeastOnce
	// 如果当前配置区块的序列号大于最后一个配置区块序列号
	if currentConfigSequence := ac.sequencer.Sequence(); currentConfigSequence > ac.lastConfigSequence {
		// 更新
		ac.lastConfigSequence = currentConfigSequence
		// 需要策略检查
		policyCheckNeeded = true
	}

	if !policyCheckNeeded {
		return nil
	}

	ac.usedAtLeastOnce = true
	return ac.policyChecker.CheckPolicy(ac.envelope, ac.channelID)
}
