/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package broadcast

import (
	"io"
	"time"

	cb "github.com/hyperledger/fabric-protos-go/common"
	ab "github.com/hyperledger/fabric-protos-go/orderer"
	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/common/util"
	"github.com/hyperledger/fabric/orderer/common/msgprocessor"
	"github.com/pkg/errors"
)

var logger = flogging.MustGetLogger("orderer.common.broadcast")

//go:generate counterfeiter -o mock/channel_support_registrar.go --fake-name ChannelSupportRegistrar . ChannelSupportRegistrar

// ChannelSupportRegistrar provides a way for the Handler to look up the Support for a channel
// 为处理程序提供一个方法用于支持查找通道查找
type ChannelSupportRegistrar interface {
	// BroadcastChannelSupport returns the message channel header, whether the message is a config update
	// and the channel resources for a message or an error if the message is not a message which can
	// be processed directly (like CONFIG and ORDERER_TRANSACTION messages)
	// 返回一个消息的头，消息是否是配置更新消息，和通道资源消息 或者是一个错误，如果msg不是一个消息，他们将被直接处理就像配置消息或者交易消息那样
	BroadcastChannelSupport(msg *cb.Envelope) (*cb.ChannelHeader, bool, ChannelSupport, error)
}

//go:generate counterfeiter -o mock/channel_support.go --fake-name ChannelSupport . ChannelSupport

// ChannelSupport provides the backing resources needed to support broadcast on a channel
// 提供在通道上广播需要的后备资源
type ChannelSupport interface {
	msgprocessor.Processor
	Consenter
}

// Consenter provides methods to send messages through consensus
// 提供通过共识发送消息的方法
type Consenter interface {
	// Order accepts a message or returns an error indicating the cause of failure
	// It ultimately passes through to the consensus.Chain interface
	// 排序 接收的一个消息，返回错误，解释失败的原因
	// 他最终会被传递到共识链接口
	Order(env *cb.Envelope, configSeq uint64) error

	// Configure accepts a reconfiguration or returns an error indicating the cause of failure
	// It ultimately passes through to the consensus.Chain interface
	Configure(config *cb.Envelope, configSeq uint64) error

	// WaitReady blocks waiting for consenter to be ready for accepting new messages.
	// This is useful when consenter needs to temporarily block ingress messages so
	// that in-flight messages can be consumed. It could return error if consenter is
	// in erroneous states. If this blocking behavior is not desired, consenter could
	// simply return nil.
	// 区块等待共识者准备好接收新的消息
	// 这是有用的当共识者需要暂时阻止消息进入，那些在in-flight消息能够被消费，
	// 如果共识者处于错误状态就返回错误，如果这个阻塞行为不是被期望的，共识者返回nil
	WaitReady() error
}

// Handler is designed to handle connections from Broadcast AB gRPC service
// 被设计用来处理 与 broadcast grpc 服务的连接
type Handler struct {
	SupportRegistrar ChannelSupportRegistrar
	Metrics          *Metrics
}

// Handle reads requests from a Broadcast stream, processes them, and returns the responses to the stream
// 从广播流中读取请求， 处理器消息，并且返回请求到流中
func (bh *Handler) Handle(srv ab.AtomicBroadcast_BroadcastServer) error {
	// 从ctx 获取远程peer节点的地址
	addr := util.ExtractRemoteAddress(srv.Context())
	logger.Debugf("Starting new broadcast loop for %s", addr)
	// 开始一个新的广播循环
	for {
		// 接收消息
		msg, err := srv.Recv()
		// 结束标识符
		if err == io.EOF {
			logger.Debugf("Received EOF from %s, hangup", addr)
			return nil
		}
		if err != nil {
			logger.Warningf("Error reading from %s: %s", addr, err)
			return err
		}

		resp := bh.ProcessMessage(msg, addr)
		err = srv.Send(resp)
		if resp.Status != cb.Status_SUCCESS {
			return err
		}

		if err != nil {
			logger.Warningf("Error sending to %s: %s", addr, err)
			return err
		}
	}
}

// ProcessMessage validates and enqueues a single message
// 验证一个交易并且将其加入到排序队列
func (bh *Handler) ProcessMessage(msg *cb.Envelope, addr string) (resp *ab.BroadcastResponse) {
	tracker := &MetricsTracker{
		ChannelID: "unknown",
		TxType:    "unknown",
		Metrics:   bh.Metrics,
	}
	defer func() {
		// This looks a little unnecessary, but if done directly as
		// a defer, resp gets the (always nil) current state of resp
		// and not the return value
		// 这看起来有点没必要， 但是如果直接做一个延迟，响应将总是nil,resp当前状态而不是返回值
		// 记录当前处理交易的指标信息，验证花费的时间，入队后换费时间，处理交易数量+1
		tracker.Record(resp)
	}()
	// 记录Metrics 验证开始的时间
	tracker.BeginValidate()
	//
	chdr, isConfig, processor, err := bh.SupportRegistrar.BroadcastChannelSupport(msg)
	if chdr != nil {
		// 通道中获取channelID
		tracker.ChannelID = chdr.ChannelId
		// 通道头部中获取 消息类型
		tracker.TxType = cb.HeaderType(chdr.Type).String()
	}
	if err != nil {
		logger.Warningf("[channel: %s] Could not get message processor for serving %s: %s", tracker.ChannelID, addr, err)
		return &ab.BroadcastResponse{Status: cb.Status_BAD_REQUEST, Info: err.Error()}
	}
	// 如果不是一个配置消息
	if !isConfig {
		logger.Debugf("[channel: %s] Broadcast is processing normal message from %s with txid '%s' of type %s", chdr.ChannelId, addr, chdr.TxId, cb.HeaderType_name[chdr.Type])
		// 验证msg 并且fetch 通道的配置，查询当前配置块的序列号
		configSeq, err := processor.ProcessNormalMsg(msg)
		if err != nil {
			logger.Warningf("[channel: %s] Rejecting broadcast of normal message from %s because of error: %s", chdr.ChannelId, addr, err)
			return &ab.BroadcastResponse{Status: ClassifyError(err), Info: err.Error()}
		}
		// 验证交易结束，更新关于验证指标的数据
		tracker.EndValidate()
		// 交易加入到队列中的开始时间更新
		tracker.BeginEnqueue()
		// 检查当前是否处于WaitReady
		if err = processor.WaitReady(); err != nil {
			logger.Warningf("[channel: %s] Rejecting broadcast of message from %s with SERVICE_UNAVAILABLE: rejected by Consenter: %s", chdr.ChannelId, addr, err)
			return &ab.BroadcastResponse{Status: cb.Status_SERVICE_UNAVAILABLE, Info: err.Error()}
		}
		// 将消息发送到lead
		err = processor.Order(msg, configSeq)
		if err != nil {
			logger.Warningf("[channel: %s] Rejecting broadcast of normal message from %s with SERVICE_UNAVAILABLE: rejected by Order: %s", chdr.ChannelId, addr, err)
			return &ab.BroadcastResponse{Status: cb.Status_SERVICE_UNAVAILABLE, Info: err.Error()}
		}
	} else { // isConfig
		// 来自配置更新的消息
		logger.Debugf("[channel: %s] Broadcast is processing config update message from %s", chdr.ChannelId, addr)
		// 校验合法性
		config, configSeq, err := processor.ProcessConfigUpdateMsg(msg)
		if err != nil {
			logger.Warningf("[channel: %s] Rejecting broadcast of config message from %s because of error: %s", chdr.ChannelId, addr, err)
			return &ab.BroadcastResponse{Status: ClassifyError(err), Info: err.Error()}
		}
		tracker.EndValidate()

		tracker.BeginEnqueue()
		if err = processor.WaitReady(); err != nil {
			logger.Warningf("[channel: %s] Rejecting broadcast of message from %s with SERVICE_UNAVAILABLE: rejected by Consenter: %s", chdr.ChannelId, addr, err)
			return &ab.BroadcastResponse{Status: cb.Status_SERVICE_UNAVAILABLE, Info: err.Error()}
		}
		// 更新配置交易
		err = processor.Configure(config, configSeq)
		if err != nil {
			logger.Warningf("[channel: %s] Rejecting broadcast of config message from %s with SERVICE_UNAVAILABLE: rejected by Configure: %s", chdr.ChannelId, addr, err)
			return &ab.BroadcastResponse{Status: cb.Status_SERVICE_UNAVAILABLE, Info: err.Error()}
		}
	}

	logger.Debugf("[channel: %s] Broadcast has successfully enqueued message of type %s from %s", chdr.ChannelId, cb.HeaderType_name[chdr.Type], addr)

	return &ab.BroadcastResponse{Status: cb.Status_SUCCESS}
}

type MetricsTracker struct {
	ValidateStartTime time.Time     // 记录验证开始时间
	EnqueueStartTime  time.Time     // 入队的开始时间
	ValidateDuration  time.Duration // 验证经过的时间
	ChannelID         string
	TxType            string
	Metrics           *Metrics
}

func (mt *MetricsTracker) Record(resp *ab.BroadcastResponse) {
	labels := []string{
		"status", resp.Status.String(),
		"channel", mt.ChannelID,
		"type", mt.TxType,
	}
	// 如果验证交易时间是 0s
	if mt.ValidateDuration == 0 {
		mt.EndValidate()
	}
	mt.Metrics.ValidateDuration.With(labels...).Observe(mt.ValidateDuration.Seconds())
	// 计算入队到交易被处理完成花费的时间
	if mt.EnqueueStartTime != (time.Time{}) {
		enqueueDuration := time.Since(mt.EnqueueStartTime)
		mt.Metrics.EnqueueDuration.With(labels...).Observe(enqueueDuration.Seconds())
	}
	// 处理的交易计数器 +1
	mt.Metrics.ProcessedCount.With(labels...).Add(1)
}

func (mt *MetricsTracker) BeginValidate() {
	mt.ValidateStartTime = time.Now()
}

func (mt *MetricsTracker) EndValidate() {
	// 计算当前到mt.ValidateStartTime
	mt.ValidateDuration = time.Since(mt.ValidateStartTime)
}

func (mt *MetricsTracker) BeginEnqueue() {
	mt.EnqueueStartTime = time.Now()
}

// ClassifyError converts an error type into a status code.
func ClassifyError(err error) cb.Status {
	switch errors.Cause(err) {
	case msgprocessor.ErrChannelDoesNotExist:
		return cb.Status_NOT_FOUND
	case msgprocessor.ErrPermissionDenied:
		return cb.Status_FORBIDDEN
	case msgprocessor.ErrMaintenanceMode:
		return cb.Status_SERVICE_UNAVAILABLE
	default:
		return cb.Status_BAD_REQUEST
	}
}
