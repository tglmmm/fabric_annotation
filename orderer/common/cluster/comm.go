/*
Copyright IBM Corp. 2017 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package cluster

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-protos-go/orderer"
	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/common/util"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

const (
	// MinimumExpirationWarningInterval is the default minimum time interval
	// between consecutive warnings about certificate expiration.
	// 最小的证书过期预警间隔 5min
	MinimumExpirationWarningInterval = time.Minute * 5
)

var (
	errOverflow = errors.New("send queue overflown") // 发送的队列满了
	errAborted  = errors.New("aborted")              // 终止
	errTimeout  = errors.New("rpc timeout expired")  // RPC超时过期
)

// ChannelExtractor extracts the channel of a given message,
// or returns an empty string if that's not possible
// 通过给定的信息提取通道
type ChannelExtractor interface {
	TargetChannel(message proto.Message) string
}

//go:generate mockery -dir . -name Handler -case underscore -output ./mocks/

// Handler handles Step() and Submit() requests and returns a corresponding response
//
type Handler interface {
	// 共识相关的请求
	OnConsensus(channel string, sender uint64, req *orderer.ConsensusRequest) error
	// 普通交易相关的请求
	OnSubmit(channel string, sender uint64, req *orderer.SubmitRequest) error
}

// RemoteNode represents a cluster member
// 代表集群成员
type RemoteNode struct {
	// ID is unique among all members, and cannot be 0.
	// 成员的唯一序号但不是能0
	ID uint64
	// Endpoint is the endpoint of the node, denoted in %s:%d format
	// address:port
	Endpoint string
	// ServerTLSCert is the DER encoded TLS server certificate of the node
	// DER编码的服务器证书
	ServerTLSCert []byte
	// ClientTLSCert is the DER encoded TLS client certificate of the node
	// DER编码的客户端证书
	ClientTLSCert []byte
}

// String returns a string representation of this RemoteNode
func (rm RemoteNode) String() string {
	return fmt.Sprintf("ID: %d,\nEndpoint: %s,\nServerTLSCert:%s, ClientTLSCert:%s",
		rm.ID, rm.Endpoint, DERtoPEM(rm.ServerTLSCert), DERtoPEM(rm.ClientTLSCert))
}

//go:generate mockery -dir . -name Communicator -case underscore -output ./mocks/

// Communicator defines communication for a consenter
// 定义共识者之间的通讯
type Communicator interface {
	// Remote returns a RemoteContext for the given RemoteNode ID in the context
	// of the given channel, or error if connection cannot be established, or
	// the channel wasn't configured
	// 从给定的通道和远程节点ID返回一个remoteContext
	Remote(channel string, id uint64) (*RemoteContext, error)
	// Configure configures the communication to connect to all
	// given members, and disconnect from any members not among the given
	// members.
	// 配置通讯为了连接所有给定的节点，并且断开与其他不是给定的成员
	Configure(channel string, members []RemoteNode)
	// Shutdown shuts down the communicator
	// 关闭通讯器
	Shutdown()
}

// MembersByChannel is a mapping from channel name
// to MemberMapping
type MembersByChannel map[string]MemberMapping

// Comm implements Communicator

// 实现通讯器
type Comm struct {
	MinimumExpirationWarningInterval time.Duration
	CertExpWarningThreshold          time.Duration
	// 关闭信号
	shutdownSignal chan struct{}
	// 关闭状态标识
	shutdown bool
	// 发送缓冲区大小
	SendBufferSize int
	Lock           sync.RWMutex
	Logger         *flogging.FabricLogger
	// channel 提取
	ChanExt ChannelExtractor
	H       Handler
	// 连接的验证 ，创建，存储， 断开，
	Connections *ConnectionStore
	// 通过channel获取成员
	Chan2Members MembersByChannel
	Metrics      *Metrics
	//
	CompareCertificate CertificateComparator
}

type requestContext struct {
	channel string
	sender  uint64
}

// DispatchSubmit identifies the channel and sender of the submit request and passes it
// to the underlying Handler
// 标识提交请求的通道和发送方并且传递他到底层处理器
func (c *Comm) DispatchSubmit(ctx context.Context, request *orderer.SubmitRequest) error {
	reqCtx, err := c.requestContext(ctx, request)
	if err != nil {
		return err
	}
	return c.H.OnSubmit(reqCtx.channel, reqCtx.sender, request)
}

// DispatchConsensus identifies the channel and sender of the step request and passes it
// to the underlying Handler
func (c *Comm) DispatchConsensus(ctx context.Context, request *orderer.ConsensusRequest) error {
	reqCtx, err := c.requestContext(ctx, request)
	if err != nil {
		return err
	}
	return c.H.OnConsensus(reqCtx.channel, reqCtx.sender, request)
}

// requestContext identifies the sender and channel of the request and returns
// it wrapped in a requestContext
// 标识请求的从通道和发送者并且返回 将其包装的 requestContext
func (c *Comm) requestContext(ctx context.Context, msg proto.Message) (*requestContext, error) {
	// 从请求中获取 channelID
	channel := c.ChanExt.TargetChannel(msg)
	if channel == "" {
		return nil, errors.Errorf("badly formatted message, cannot extract channel")
	}

	c.Lock.RLock()
	// 通过chanenl从映射中获取道成员
	mapping, exists := c.Chan2Members[channel]
	c.Lock.RUnlock()
	// 如果Channel 对应的map不存在则直接返回错误
	if !exists {
		return nil, errors.Errorf("channel %s doesn't exist", channel)
	}
	// 从上下文中提取证书
	cert := util.ExtractRawCertificateFromContext(ctx)
	if len(cert) == 0 {
		return nil, errors.Errorf("no TLS certificate sent")
	}
	// stub客户证书与ctx客户端证书对比
	stub := mapping.LookupByClientCert(cert)
	if stub == nil {
		return nil, errors.Errorf("certificate extracted from TLS connection isn't authorized")
	}
	return &requestContext{
		channel: channel,
		sender:  stub.ID,
	}, nil
}

// Remote obtains a RemoteContext linked to the destination node on the context
// of a given channel
// 获得一个remoteContext
func (c *Comm) Remote(channel string, id uint64) (*RemoteContext, error) {
	c.Lock.RLock()
	defer c.Lock.RUnlock()
	// 建立的连接已经被关闭
	if c.shutdown {
		return nil, errors.New("communication has been shut down")
	}
	// 通道下的成员是否存在
	mapping, exists := c.Chan2Members[channel]
	// 如果不存在直接返回通道不存在
	if !exists {
		return nil, errors.Errorf("channel %s doesn't exist", channel)
	}
	// 通过通道remoteID 找到对应的RemoteContext
	stub := mapping.ByID(id)
	if stub == nil {
		return nil, errors.Errorf("node %d doesn't exist in channel %s's membership", id, channel)
	}
	// stub.RemoteContext != nil
	// Remmote.Context 是否为空
	if stub.Active() {
		return stub.RemoteContext, nil
	}
	// 如果是RemoteContext是nil
	//
	err := stub.Activate(c.createRemoteContext(stub, channel))
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return stub.RemoteContext, nil
}

// Configure configures the channel with the given RemoteNodes
func (c *Comm) Configure(channel string, newNodes []RemoteNode) {
	c.Logger.Infof("Entering, channel: %s, nodes: %v", channel, newNodes)
	defer c.Logger.Infof("Exiting")

	c.Lock.Lock()
	defer c.Lock.Unlock()

	c.createShutdownSignalIfNeeded()

	if c.shutdown {
		return
	}

	beforeConfigChange := c.serverCertsInUse()
	// Update the channel-scoped mapping with the new nodes
	c.applyMembershipConfig(channel, newNodes)
	// Close connections to nodes that are not present in the new membership
	c.cleanUnusedConnections(beforeConfigChange)
}

func (c *Comm) createShutdownSignalIfNeeded() {
	if c.shutdownSignal == nil {
		c.shutdownSignal = make(chan struct{})
	}
}

// Shutdown shuts down the instance
func (c *Comm) Shutdown() {
	c.Lock.Lock()
	defer c.Lock.Unlock()

	c.createShutdownSignalIfNeeded()
	if !c.shutdown {
		close(c.shutdownSignal)
	}

	c.shutdown = true
	for _, members := range c.Chan2Members {
		members.Foreach(func(id uint64, stub *Stub) {
			c.Connections.Disconnect(stub.ServerTLSCert)
		})
	}
}

// cleanUnusedConnections disconnects all connections that are un-used
// at the moment of the invocation
func (c *Comm) cleanUnusedConnections(serverCertsBeforeConfig StringSet) {
	// Scan all nodes after the reconfiguration
	serverCertsAfterConfig := c.serverCertsInUse()
	// Filter out the certificates that remained after the reconfiguration
	serverCertsBeforeConfig.subtract(serverCertsAfterConfig)
	// Close the connections to all these nodes as they shouldn't be in use now
	for serverCertificate := range serverCertsBeforeConfig {
		c.Connections.Disconnect([]byte(serverCertificate))
	}
}

// serverCertsInUse returns the server certificates that are in use
// represented as strings.
func (c *Comm) serverCertsInUse() StringSet {
	endpointsInUse := make(StringSet)
	for _, mapping := range c.Chan2Members {
		endpointsInUse.union(mapping.ServerCertificates())
	}
	return endpointsInUse
}

// applyMembershipConfig sets the given RemoteNodes for the given channel
func (c *Comm) applyMembershipConfig(channel string, newNodes []RemoteNode) {
	mapping := c.getOrCreateMapping(channel)
	newNodeIDs := make(map[uint64]struct{})

	for _, node := range newNodes {
		newNodeIDs[node.ID] = struct{}{}
		c.updateStubInMapping(channel, mapping, node)
	}

	// Remove all stubs without a corresponding node
	// in the new nodes
	mapping.Foreach(func(id uint64, stub *Stub) {
		if _, exists := newNodeIDs[id]; exists {
			c.Logger.Info(id, "exists in both old and new membership for channel", channel, ", skipping its deactivation")
			return
		}
		c.Logger.Info("Deactivated node", id, "who's endpoint is", stub.Endpoint, "as it's removed from membership")
		mapping.Remove(id)
		stub.Deactivate()
	})
}

// updateStubInMapping updates the given RemoteNode and adds it to the MemberMapping
func (c *Comm) updateStubInMapping(channel string, mapping MemberMapping, node RemoteNode) {
	stub := mapping.ByID(node.ID)
	if stub == nil {
		c.Logger.Info("Allocating a new stub for node", node.ID, "with endpoint of", node.Endpoint, "for channel", channel)
		stub = &Stub{}
	}

	// Check if the TLS server certificate of the node is replaced
	// and if so - then deactivate the stub, to trigger
	// a re-creation of its gRPC connection
	if !bytes.Equal(stub.ServerTLSCert, node.ServerTLSCert) {
		c.Logger.Info("Deactivating node", node.ID, "in channel", channel,
			"with endpoint of", node.Endpoint, "due to TLS certificate change")
		stub.Deactivate()
	}

	// Overwrite the stub Node data with the new data
	stub.RemoteNode = node

	// Put the stub into the mapping
	mapping.Put(stub)

	// Check if the stub needs activation.
	if stub.Active() {
		return
	}

	// Activate the stub
	stub.Activate(c.createRemoteContext(stub, channel))
}

// createRemoteStub returns a function that creates a RemoteContext.
// It is used as a parameter to Stub.Activate() in order to activate
// a stub atomically.
// 创建一个RemoteStub ，返回一个函数 用于创建 RemoteContext
func (c *Comm) createRemoteContext(stub *Stub, channel string) func() (*RemoteContext, error) {
	return func() (*RemoteContext, error) {
		// 从PEM中解析一个证书
		cert, err := x509.ParseCertificate(stub.ServerTLSCert)
		if err != nil {
			pemString := string(pem.EncodeToMemory(&pem.Block{Bytes: stub.ServerTLSCert}))
			c.Logger.Errorf("Invalid DER for channel %s, endpoint %s, ID %d: %v", channel, stub.Endpoint, stub.ID, pemString)
			return nil, errors.Wrap(err, "invalid certificate DER")
		}
		// 连接指定的节点
		c.Logger.Debug("Connecting to", stub.RemoteNode, "for channel", channel)
		// 获取连接或者重新创建连接
		conn, err := c.Connections.Connection(stub.Endpoint, stub.ServerTLSCert)
		if err != nil {
			c.Logger.Warningf("Unable to obtain connection to %d(%s) (channel %s): %v", stub.ID, stub.Endpoint, channel, err)
			return nil, err
		}
		// 连接的探针
		probeConnection := func(conn *grpc.ClientConn) error {
			// GRPC实现的方法
			connState := conn.GetState()
			// 正在连接
			if connState == connectivity.Connecting {
				return errors.Errorf("connection to %d(%s) is in state %s", stub.ID, stub.Endpoint, connState)
			}
			return nil
		}
		// 在GRPC connection 上包了一层
		clusterClient := orderer.NewClusterClient(conn)
		//
		workerCountReporter := workerCountReporter{
			channel: channel,
		}
		//
		rc := &RemoteContext{
			// 证书过期时间
			expiresAt: cert.NotAfter,
			// 证书过期预警间隔
			minimumExpirationWarningInterval: c.MinimumExpirationWarningInterval,
			// 证书过期预警阈值
			certExpWarningThreshold: c.CertExpWarningThreshold,
			// worker计数器
			workerCountReporter: workerCountReporter,
			Channel:             channel,
			Metrics:             c.Metrics,
			SendBuffSize:        c.SendBufferSize,
			shutdownSignal:      c.shutdownSignal,
			endpoint:            stub.Endpoint,
			Logger:              c.Logger,
			ProbeConn:           probeConnection,
			conn:                conn,
			Client:              clusterClient,
		}
		return rc, nil
	}
}

// getOrCreateMapping creates a MemberMapping for the given channel
// or returns the existing one.
func (c *Comm) getOrCreateMapping(channel string) MemberMapping {
	// Lazily create a mapping if it doesn't already exist
	mapping, exists := c.Chan2Members[channel]
	if !exists {
		mapping = MemberMapping{
			id2stub:       make(map[uint64]*Stub),
			SamePublicKey: c.CompareCertificate,
		}
		c.Chan2Members[channel] = mapping
	}
	return mapping
}

// Stub holds all information about the remote node,
// including the RemoteContext for it, and serializes
// some operations on it.
type Stub struct {
	lock sync.RWMutex
	RemoteNode
	*RemoteContext
}

// Active returns whether the Stub
// is active or not
func (stub *Stub) Active() bool {
	stub.lock.RLock()
	defer stub.lock.RUnlock()
	return stub.isActive()
}

// Active returns whether the Stub
// is active or not.
func (stub *Stub) isActive() bool {
	return stub.RemoteContext != nil
}

// Deactivate deactivates the Stub and
// ceases all communication operations
// invoked on it.
func (stub *Stub) Deactivate() {
	stub.lock.Lock()
	defer stub.lock.Unlock()
	if !stub.isActive() {
		return
	}
	stub.RemoteContext.Abort()
	stub.RemoteContext = nil
}

// Activate creates a remote context with the given function callback
// in an atomic manner - if two parallel invocations are invoked on this Stub,
// only a single invocation of createRemoteStub takes place.
// 与集群内的节点通过给定的回调函数创建一个远程上下文
func (stub *Stub) Activate(createRemoteContext func() (*RemoteContext, error)) error {
	stub.lock.Lock()
	defer stub.lock.Unlock()
	// Check if the stub has already been activated while we were waiting for the lock
	if stub.isActive() {
		return nil
	}
	remoteStub, err := createRemoteContext()
	if err != nil {
		return errors.WithStack(err)
	}

	stub.RemoteContext = remoteStub
	return nil
}

// RemoteContext interacts with remote cluster
// nodes. Every call can be aborted via call to Abort()
type RemoteContext struct {
	expiresAt                        time.Time
	minimumExpirationWarningInterval time.Duration
	certExpWarningThreshold          time.Duration
	Metrics                          *Metrics
	Channel                          string
	SendBuffSize                     int
	shutdownSignal                   chan struct{}
	Logger                           *flogging.FabricLogger
	endpoint                         string
	Client                           orderer.ClusterClient
	ProbeConn                        func(conn *grpc.ClientConn) error
	conn                             *grpc.ClientConn
	nextStreamID                     uint64
	streamsByID                      streamsMapperReporter
	workerCountReporter              workerCountReporter
}

// Stream is used to send/receive messages to/from the remote cluster member.
// 用于发送和接收 msg 从远程集群成员那里，注意是成员节点
type Stream struct {
	abortChan <-chan struct{}
	sendBuff  chan struct {
		request *orderer.StepRequest
		report  func(error)
	}
	commShutdown chan struct{}
	abortReason  *atomic.Value
	metrics      *Metrics
	ID           uint64
	Channel      string
	NodeName     string
	Endpoint     string
	Logger       *flogging.FabricLogger
	Timeout      time.Duration
	orderer.Cluster_StepClient
	Cancel   func(error)
	canceled *uint32
	expCheck *certificateExpirationCheck
}

// StreamOperation denotes an operation done by a stream, such a Send or Receive.
type StreamOperation func() (*orderer.StepResponse, error)

// Canceled returns whether the stream was canceled.
func (stream *Stream) Canceled() bool {
	return atomic.LoadUint32(stream.canceled) == uint32(1)
}

// Send sends the given request to the remote cluster member.
func (stream *Stream) Send(request *orderer.StepRequest) error {
	return stream.SendWithReport(request, func(_ error) {})
}

// SendWithReport sends the given request to the remote cluster member and invokes report on the send result.
// 发送给定的请求到远程的集群几点，并且调用 report
func (stream *Stream) SendWithReport(request *orderer.StepRequest, report func(error)) error {
	if stream.Canceled() {
		return errors.New(stream.abortReason.Load().(string))
	}
	var allowDrop bool
	// We want to drop consensus transactions if the remote node cannot keep up with us,
	// otherwise we'll slow down the entire FSM.
	// 我们希望丢弃共识交易，如果远程节点无法与我们保持
	// 否则会拖垮系统
	if request.GetConsensusRequest() != nil {
		allowDrop = true
	}

	return stream.sendOrDrop(request, allowDrop, report)
}

// sendOrDrop sends the given request to the remote cluster member, or drops it
// if it is a consensus request and the queue is full.
// 如果是一个共识请求并且队列已经满了，就会被丢弃
func (stream *Stream) sendOrDrop(request *orderer.StepRequest, allowDrop bool, report func(error)) error {
	// 判断交易类型
	msgType := "transaction"
	if allowDrop {
		msgType = "consensus"
	}
	// 报告 出口队列相关的 len ,cap指标
	stream.metrics.reportQueueOccupancy(stream.Endpoint, msgType, stream.Channel, len(stream.sendBuff), cap(stream.sendBuff))
	// 允许删除并且 队列满了
	if allowDrop && len(stream.sendBuff) == cap(stream.sendBuff) {
		stream.Cancel(errOverflow)
		// 被丢弃
		stream.metrics.reportMessagesDropped(stream.Endpoint, stream.Channel)
		return errOverflow
	}

	select {
	case <-stream.abortChan: // 由于某个原因出错了，这个通道会返回错误
		return errors.Errorf("stream %d aborted", stream.ID)
		// 请求加入队列
	case stream.sendBuff <- struct {
		request *orderer.StepRequest
		report  func(error)
	}{request: request, report: report}:
		return nil
	case <-stream.commShutdown:
		return nil
	}
}

// sendMessage sends the request down the stream
// 请求发送到stream
func (stream *Stream) sendMessage(request *orderer.StepRequest, report func(error)) {
	start := time.Now()
	var err error
	defer func() {
		message := fmt.Sprintf("Send of %s to %s(%s) took %v",
			requestAsString(request), stream.NodeName, stream.Endpoint, time.Since(start))
		if err != nil {
			stream.Logger.Warnf("%s but failed due to %s", message, err.Error())
		} else {
			stream.Logger.Debug(message)
		}
	}()

	f := func() (*orderer.StepResponse, error) {
		// 开始发送时间
		startSend := time.Now()
		// 检查证书过期
		stream.expCheck.checkExpiration(startSend, stream.Channel)
		// 发送求情
		err := stream.Cluster_StepClient.Send(request)
		// 更新指标
		stream.metrics.reportMsgSendTime(stream.Endpoint, stream.Channel, time.Since(startSend))
		return nil, err
	}
	//
	_, err = stream.operateWithTimeout(f, report)
}

func (stream *Stream) serviceStream() {
	// 记录开始时间
	streamStartTime := time.Now()
	defer func() {
		// 函数退出时候
		stream.Cancel(errAborted)
		stream.Logger.Debugf("Stream %d to (%s) terminated with total lifetime of %s",
			stream.ID, stream.Endpoint, time.Since(streamStartTime))
	}()

	for {
		select {
		// 队列中有数据就会被执行
		case reqReport := <-stream.sendBuff:
			// 发送请求数据
			stream.sendMessage(reqReport.request, reqReport.report)
		case <-stream.abortChan:
			return
		case <-stream.commShutdown:
			return
		}
	}
}

// Recv receives a message from a remote cluster member.
func (stream *Stream) Recv() (*orderer.StepResponse, error) {
	start := time.Now()
	defer func() {
		if !stream.Logger.IsEnabledFor(zap.DebugLevel) {
			return
		}
		stream.Logger.Debugf("Receive from %s(%s) took %v", stream.NodeName, stream.Endpoint, time.Since(start))
	}()

	f := func() (*orderer.StepResponse, error) {
		return stream.Cluster_StepClient.Recv()
	}

	return stream.operateWithTimeout(f, func(_ error) {})
}

// operateWithTimeout performs the given operation on the stream, and blocks until the timeout expires.
// 在流上执行给定的操作，阻塞直到过期
func (stream *Stream) operateWithTimeout(invoke StreamOperation, report func(error)) (*orderer.StepResponse, error) {
	// 开启定时器
	timer := time.NewTimer(stream.Timeout)
	defer timer.Stop()

	var operationEnded sync.WaitGroup
	operationEnded.Add(1)

	responseChan := make(chan struct {
		res *orderer.StepResponse
		err error
	}, 1)

	go func() {
		// 释放wg
		defer operationEnded.Done()
		res, err := invoke() //执行发送
		responseChan <- struct {
			res *orderer.StepResponse
			err error
		}{res: res, err: err}
	}()

	select {
	case r := <-responseChan:
		report(r.err)
		if r.err != nil {
			stream.Cancel(r.err)
		}
		return r.res, r.err
	case <-timer.C: // 超时
		report(errTimeout)
		stream.Logger.Warningf("Stream %d to %s(%s) was forcibly terminated because timeout (%v) expired",
			stream.ID, stream.NodeName, stream.Endpoint, stream.Timeout)
		stream.Cancel(errTimeout)
		// Wait for the operation goroutine to end
		operationEnded.Wait()
		return nil, errTimeout
	}
}

func requestAsString(request *orderer.StepRequest) string {
	switch t := request.GetPayload().(type) {
	case *orderer.StepRequest_SubmitRequest:
		if t.SubmitRequest == nil || t.SubmitRequest.Payload == nil {
			return fmt.Sprintf("Empty SubmitRequest: %v", t.SubmitRequest)
		}
		return fmt.Sprintf("SubmitRequest for channel %s with payload of size %d",
			t.SubmitRequest.Channel, len(t.SubmitRequest.Payload.Payload))
	case *orderer.StepRequest_ConsensusRequest:
		return fmt.Sprintf("ConsensusRequest for channel %s with payload of size %d",
			t.ConsensusRequest.Channel, len(t.ConsensusRequest.Payload))
	default:
		return fmt.Sprintf("unknown type: %v", request)
	}
}

// NewStream creates a new stream.
// It is not thread safe, and Send() or Recv() block only until the timeout expires.
// 他不是线程安全的， 并且 Send() , Recv() 是阻塞的直到超时过期
func (rc *RemoteContext) NewStream(timeout time.Duration) (*Stream, error) {
	// 检查连接状态
	if err := rc.ProbeConn(rc.conn); err != nil {
		return nil, err
	}
	//
	ctx, cancel := context.WithCancel(context.TODO())
	stream, err := rc.Client.Step(ctx)
	if err != nil {
		cancel()
		return nil, errors.WithStack(err)
	}
	// nextStream + 1
	streamID := atomic.AddUint64(&rc.nextStreamID, 1)
	// 从证书中获取CommonName
	nodeName := commonNameFromContext(stream.Context())

	var canceled uint32
	// 终止信号的接收的通道
	abortChan := make(chan struct{})
	// 终止原因
	abortReason := &atomic.Value{}
	// 执行一次
	once := &sync.Once{}

	cancelWithReason := func(err error) {
		once.Do(func() {
			abortReason.Store(err.Error())  // 存储错误
			cancel()                        // 执行上下文取消函数
			rc.streamsByID.Delete(streamID) // 字典中将StreamID删除
			// 记录指标数据
			rc.Metrics.reportEgressStreamCount(rc.Channel, atomic.LoadUint32(&rc.streamsByID.size))
			//
			rc.Logger.Debugf("Stream %d to %s(%s) is aborted", streamID, nodeName, rc.endpoint)
			// 记录 canceled
			atomic.StoreUint32(&canceled, 1)
			// 通道发送 abortChan 关闭的信号
			close(abortChan)
		})
	}

	logger := flogging.MustGetLogger("orderer.common.cluster.step")
	stepLogger := logger.WithOptions(zap.AddCallerSkip(1))

	s := &Stream{
		Channel:     rc.Channel,
		metrics:     rc.Metrics,
		abortReason: abortReason,
		abortChan:   abortChan,
		sendBuff: make(chan struct {
			request *orderer.StepRequest
			report  func(error)
		}, rc.SendBuffSize),
		commShutdown:       rc.shutdownSignal,
		NodeName:           nodeName,
		Logger:             stepLogger,
		ID:                 streamID,
		Endpoint:           rc.endpoint,
		Timeout:            timeout,
		Cluster_StepClient: stream,
		Cancel:             cancelWithReason,
		canceled:           &canceled,
	}
	// 证书过期检查
	s.expCheck = &certificateExpirationCheck{
		minimumExpirationWarningInterval: rc.minimumExpirationWarningInterval,
		expirationWarningThreshold:       rc.certExpWarningThreshold,
		expiresAt:                        rc.expiresAt,
		endpoint:                         s.Endpoint,
		nodeName:                         s.NodeName,
		alert: func(template string, args ...interface{}) {
			s.Logger.Warningf(template, args...)
		},
	}

	rc.Logger.Debugf("Created new stream to %s with ID of %d and buffer size of %d",
		rc.endpoint, streamID, cap(s.sendBuff))
	// sync.Map中存储StreamID: stream
	rc.streamsByID.Store(streamID, s)
	// 出口的流的数量
	rc.Metrics.reportEgressStreamCount(rc.Channel, atomic.LoadUint32(&rc.streamsByID.size))

	go func() {
		// 工作线程数指标记录
		rc.workerCountReporter.increment(s.metrics)
		s.serviceStream()
		rc.workerCountReporter.decrement(s.metrics)
	}()

	return s, nil
}

// Abort aborts the contexts the RemoteContext uses, thus effectively
// causes all operations that use this RemoteContext to terminate.
func (rc *RemoteContext) Abort() {
	rc.streamsByID.Range(func(_, value interface{}) bool {
		value.(*Stream).Cancel(errAborted)
		return false
	})
}

func commonNameFromContext(ctx context.Context) string {
	// 从上下文中获取证书
	cert := util.ExtractCertificateFromContext(ctx)
	if cert == nil {
		return "unidentified node"
	}
	// 从证书中获取commonName
	return cert.Subject.CommonName
}

type streamsMapperReporter struct {
	size uint32
	sync.Map
}

func (smr *streamsMapperReporter) Delete(key interface{}) {
	smr.Map.Delete(key)
	atomic.AddUint32(&smr.size, ^uint32(0))
}

func (smr *streamsMapperReporter) Store(key, value interface{}) {
	smr.Map.Store(key, value)
	atomic.AddUint32(&smr.size, 1)
}

type workerCountReporter struct {
	channel     string
	workerCount uint32
}

func (wcr *workerCountReporter) increment(m *Metrics) {
	count := atomic.AddUint32(&wcr.workerCount, 1)
	m.reportWorkerCount(wcr.channel, count)
}

func (wcr *workerCountReporter) decrement(m *Metrics) {
	// ^0 flips all zeros to ones, which means
	// 2^32 - 1, and then we add this number wcr.workerCount.
	// It follows from commutativity of the unsigned integers group
	// that wcr.workerCount + 2^32 - 1 = wcr.workerCount - 1 + 2^32
	// which is just wcr.workerCount - 1.
	count := atomic.AddUint32(&wcr.workerCount, ^uint32(0))
	m.reportWorkerCount(wcr.channel, count)
}
