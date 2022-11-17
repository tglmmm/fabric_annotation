/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package server

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	_ "net/http/pprof" // This is essentially the main package for the orderer
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-lib-go/healthz"
	cb "github.com/hyperledger/fabric-protos-go/common"
	ab "github.com/hyperledger/fabric-protos-go/orderer"
	"github.com/hyperledger/fabric/bccsp"
	"github.com/hyperledger/fabric/bccsp/factory"
	"github.com/hyperledger/fabric/common/channelconfig"
	"github.com/hyperledger/fabric/common/crypto"
	"github.com/hyperledger/fabric/common/fabhttp"
	"github.com/hyperledger/fabric/common/flogging"
	floggingmetrics "github.com/hyperledger/fabric/common/flogging/metrics"
	"github.com/hyperledger/fabric/common/grpclogging"
	"github.com/hyperledger/fabric/common/grpcmetrics"
	"github.com/hyperledger/fabric/common/ledger/blockledger"
	"github.com/hyperledger/fabric/common/metrics"
	"github.com/hyperledger/fabric/common/metrics/disabled"
	"github.com/hyperledger/fabric/core/operations"
	"github.com/hyperledger/fabric/internal/pkg/comm"
	"github.com/hyperledger/fabric/internal/pkg/identity"
	"github.com/hyperledger/fabric/msp"
	"github.com/hyperledger/fabric/orderer/common/bootstrap/file"
	"github.com/hyperledger/fabric/orderer/common/channelparticipation"
	"github.com/hyperledger/fabric/orderer/common/cluster"
	"github.com/hyperledger/fabric/orderer/common/localconfig"
	"github.com/hyperledger/fabric/orderer/common/metadata"
	"github.com/hyperledger/fabric/orderer/common/multichannel"
	"github.com/hyperledger/fabric/orderer/common/onboarding"
	"github.com/hyperledger/fabric/orderer/consensus"
	"github.com/hyperledger/fabric/orderer/consensus/etcdraft"
	"github.com/hyperledger/fabric/orderer/consensus/kafka"
	"github.com/hyperledger/fabric/orderer/consensus/solo"
	"github.com/hyperledger/fabric/protoutil"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"gopkg.in/alecthomas/kingpin.v2"
)

var logger = flogging.MustGetLogger("orderer.common.server")

// command line flags
var (
	app = kingpin.New("orderer", "Hyperledger Fabric orderer node")
	// 将 start 设置成默认命令
	_ = app.Command("start", "Start the orderer node").Default() // preserved for cli compatibility
	// 版本相关信息
	version = app.Command("version", "Show version information")

	clusterTypes = map[string]struct{}{"etcdraft": {}}
)

// Main is the entry point of orderer process
func Main() {
	// 子命令 start/version
	fullCmd := kingpin.MustParse(app.Parse(os.Args[1:]))

	// "version" command
	if fullCmd == version.FullCommand() {
		// 打印版本信息
		fmt.Println(metadata.GetVersionInfo())
		return
	}
	// 加载配置，配置进行规范化，默认值填充处理
	// 配置文件orderer.yaml => TopLevel，将配置中与结构体不匹配的值过滤，配置中没有设置的值使用默认值(defaultTopLevel)填充
	conf, err := localconfig.Load()
	if err != nil {
		logger.Error("failed to parse config: ", err)
		os.Exit(1)
	}

	// 日志格式化
	initializeLogging()
	// 格式化输出配置信息
	//prettyPrintStruct(conf)
	// BCCSP 实例，加载一系列默认加密，解密，签名验签函数
	// BCCSP当前并没有指定 keystorePath
	cryptoProvider := factory.GetDefault()
	// 通过配置生成一个MSP实例并且返回默认的一个签名身份
	signer, signErr := loadLocalMSP(conf).GetDefaultSigningIdentity()
	if signErr != nil {
		logger.Panicf("Failed to get local MSP identity: %s", signErr)
	}

	// 运维相关配置设置
	opsSystem := newOperationsSystem(conf.Operations, conf.Metrics)
	// 启动ops
	if err = opsSystem.Start(); err != nil {
		logger.Panicf("failed to start operations subsystem: %s", err)
	}
	// 程序退出时候停止
	defer opsSystem.Stop()
	metricsProvider := opsSystem.Provider
	logObserver := floggingmetrics.NewObserver(metricsProvider)
	flogging.SetObserver(logObserver)

	// 初始化GRPC服务配置
	serverConfig := initializeServerConfig(conf, metricsProvider)

	// 根据配置信息初始化 grpc 服务
	grpcServer := initializeGrpcServer(conf, serverConfig)
	// 证书管理
	caMgr := &caManager{
		appRootCAsByChain:     make(map[string][][]byte),
		ordererRootCAsByChain: make(map[string][][]byte),
		clientRootCAs:         serverConfig.SecOpts.ClientRootCAs,
	}

	// 账本
	lf, err := createLedgerFactory(conf, metricsProvider)
	if err != nil {
		logger.Panicf("Failed to create ledger factory: %v", err)
	}

	var bootstrapBlock *cb.Block
	// 配置中读取引导模式
	switch conf.General.BootstrapMethod {
	// 创世块文件
	case "file":
		if len(lf.ChannelIDs()) > 0 {
			logger.Info("Not bootstrapping the system channel because of existing channels")
			break
		}

		// 解析创世区块到结构体中
		bootstrapBlock = file.New(conf.General.BootstrapFile).GenesisBlock()
		// 验证orderer配置区块的合法性
		if err := onboarding.ValidateBootstrapBlock(bootstrapBlock, cryptoProvider); err != nil {
			logger.Panicf("Failed validating bootstrap block: %v", err)
		}
		// 创世块头部存储块号
		if bootstrapBlock.Header.Number > 0 {
			// 区块号大于0 不能引导系统通道，因为创世块块号必须等于0
			logger.Infof("Not bootstrapping the system channel because the bootstrap block number is %d (>0), replication is needed", bootstrapBlock.Header.Number)
			break
		}

		// bootstrapping with a genesis block (i.e. bootstrap block number = 0)
		// generate the system channel with a genesis block.
		// 通过创世块 ，生成系统通道
		logger.Info("Bootstrapping the system channel")
		initializeBootstrapChannel(bootstrapBlock, lf)
	case "none":
		bootstrapBlock = initSystemChannelWithJoinBlock(conf, cryptoProvider, lf)
	default:
		logger.Panicf("Unknown bootstrap method: %s", conf.General.BootstrapMethod)
	}

	// select the highest numbered block among the bootstrap block and the last config block if the system channel.
	// 选择引导块中编号最高的块 ，如果是系统通道则选择最后一个配置块
	sysChanConfigBlock := extractSystemChannel(lf, cryptoProvider)
	// 选择最后的一个配置区块信息作为引导块
	clusterBootBlock := selectClusterBootBlock(bootstrapBlock, sysChanConfigBlock)

	// determine whether the orderer is of cluster type
	// 决定orderer是否是集群类型
	var isClusterType bool
	if clusterBootBlock == nil {
		logger.Infof("Starting without a system channel")
		isClusterType = true
	} else {
		// 从引导块信息中获取系统通道的ID
		sysChanID, err := protoutil.GetChannelIDFromBlock(clusterBootBlock)
		if err != nil {
			logger.Panicf("Failed getting channel ID from clusterBootBlock: %s", err)
		}
		// 共识类型 , 从区块信息创建bundle ,bundle实际上是 configtx的一个信息的隐射，从中可以获取到共识类型
		consensusTypeName := consensusType(clusterBootBlock, cryptoProvider)
		logger.Infof("Starting with system channel: %s, consensus type: %s", sysChanID, consensusTypeName)
		// etcdraft
		_, isClusterType = clusterTypes[consensusTypeName]
	}

	// configure following artifacts properly if orderer is of cluster type
	// 如果是集群类型，则需要正确的配置以下组件
	var repInitiator *onboarding.ReplicationInitiator
	clusterServerConfig := serverConfig
	// by default, cluster shares the same grpc server
	// 默认情况下集群共享同一个GRPC服务器
	clusterGRPCServer := grpcServer
	var clusterClientConfig comm.ClientConfig
	var clusterDialer *cluster.PredicateDialer

	var reuseGrpcListener bool
	var serversToUpdate []*comm.GRPCServer

	if isClusterType {
		logger.Infof("Setting up cluster")
		// 初始化cluster 客户端配置
		clusterClientConfig, reuseGrpcListener = initializeClusterClientConfig(conf)
		//
		clusterDialer = &cluster.PredicateDialer{
			Config: clusterClientConfig,
		}
		// 如果不是重用rpc监听
		if !reuseGrpcListener {
			clusterServerConfig, clusterGRPCServer = configureClusterListener(conf, serverConfig, ioutil.ReadFile)
		}

		// If we have a separate gRPC server for the cluster,
		// we need to update its TLS CA certificate pool.
		// 如果我们有一个单独的GRPC服务用于集群
		// 我们需要更新TLS证书池
		serversToUpdate = append(serversToUpdate, clusterGRPCServer)

		// If the orderer has a system channel and is of cluster type, it may have to replicate first.
		// 如果orderer节点有系统通道，并且是集群类型，他可能必须先进行复制
		if clusterBootBlock != nil {
			// When we are bootstrapping with a clusterBootBlock with number >0,
			// replication will be performed. Only clusters that are equipped with
			// a recent config block (number i.e. >0) can replicate. This will
			// replicate all channels if the clusterBootBlock number > system-channel
			// height (i.e. there is a gap in the ledger).
			repInitiator = onboarding.NewReplicationInitiator(lf, clusterBootBlock, conf, clusterClientConfig.SecOpts, signer, cryptoProvider)
			repInitiator.ReplicateIfNeeded(clusterBootBlock)
			// With BootstrapMethod == "none", the bootstrapBlock comes from a
			// join-block. If it exists, we need to remove the system channel
			// join-block from the filerepo.
			if conf.General.BootstrapMethod == "none" && bootstrapBlock != nil {
				discardSystemChannelJoinBlock(conf, bootstrapBlock)
			}
		}
	}

	// 序列化签名身份 MSPID+证书内容
	identityBytes, err := signer.Serialize()
	if err != nil {
		logger.Panicf("Failed serializing signing identity: %v", err)
	}

	expirationLogger := flogging.MustGetLogger("certmonitor")
	// 证书过期监控
	crypto.TrackExpiration(
		serverConfig.SecOpts.UseTLS,
		serverConfig.SecOpts.Certificate,
		[][]byte{clusterClientConfig.SecOpts.Certificate},
		identityBytes,
		expirationLogger.Infof,
		expirationLogger.Warnf, // This can be used to piggyback a metric event in the future
		time.Now(),
		time.AfterFunc) // time.AfterFunc 表示多久以后开始执行函数 参数：1.时间， 回调函数

	// if cluster is reusing client-facing server, then it is already  appended to serversToUpdate at this point.
	// 如果集群重用面向客户端的服务，那么此时他已经附加到了serversToUpdate
	if grpcServer.MutualTLSRequired() && !reuseGrpcListener {
		serversToUpdate = append(serversToUpdate, grpcServer)
	}
	// 回调函数用于更新root-CAs
	tlsCallback := func(bundle *channelconfig.Bundle) {
		logger.Debug("Executing callback to update root CAs")
		caMgr.updateTrustedRoots(bundle, serversToUpdate...)
		if isClusterType {
			caMgr.updateClusterDialer(
				clusterDialer,
				clusterClientConfig.SecOpts.ServerRootCAs,
			)
		}
	}
	// 多通道注册管理
	manager := initializeMultichannelRegistrar(
		clusterBootBlock,
		repInitiator,
		clusterDialer,
		clusterServerConfig,
		clusterGRPCServer,
		conf,
		signer,
		metricsProvider,
		opsSystem,
		lf,
		cryptoProvider,
		tlsCallback,
	)
	//
	adminServer := newAdminServer(conf.Admin)
	adminServer.RegisterHandler(
		//
		channelparticipation.URLBaseV1,
		channelparticipation.NewHTTPHandler(conf.ChannelParticipation, manager),
		conf.Admin.TLS.Enabled,
	)
	if err = adminServer.Start(); err != nil {
		logger.Panicf("failed to start admin server: %s", err)
	}
	defer adminServer.Stop()

	// 创建一个原子广播
	mutualTLS := serverConfig.SecOpts.UseTLS && serverConfig.SecOpts.RequireClientCert
	server := NewServer(
		manager,
		metricsProvider,
		&conf.Debug,
		conf.General.Authentication.TimeWindow,
		mutualTLS,
		conf.General.Authentication.NoExpirationChecks,
	)

	logger.Infof("Starting %s", metadata.GetVersionInfo())
	handleSignals(addPlatformSignals(map[os.Signal]func(){
		syscall.SIGTERM: func() {
			grpcServer.Stop()
			if clusterGRPCServer != grpcServer {
				clusterGRPCServer.Stop()
			}
		},
	}))

	if !reuseGrpcListener && isClusterType {
		logger.Info("Starting cluster listener on", clusterGRPCServer.Address())
		// 启动底层的 GRPC服务
		go clusterGRPCServer.Start()
	}
	// go pprof
	if conf.General.Profile.Enabled {
		go initializeProfilingService(conf)
	}
	// 注册广播服务
	ab.RegisterAtomicBroadcastServer(grpcServer.Server(), server)
	logger.Info("Beginning to serve requests")
	if err := grpcServer.Start(); err != nil {
		logger.Fatalf("Atomic Broadcast gRPC server has terminated while serving requests due to: %v", err)
	}
}

// Searches whether there is a join block for a system channel, and if there is, and it is a genesis block,
// initializes the ledger with it. Returns the join-block if it finds one.
func initSystemChannelWithJoinBlock(
	config *localconfig.TopLevel,
	cryptoProvider bccsp.BCCSP,
	lf blockledger.Factory,
) (bootstrapBlock *cb.Block) {
	if !config.ChannelParticipation.Enabled {
		return nil
	}

	joinBlockFileRepo, err := multichannel.InitJoinBlockFileRepo(config)
	if err != nil {
		logger.Panicf("Failed initializing join-block file repo: %v", err)
	}

	joinBlockFiles, err := joinBlockFileRepo.List()
	if err != nil {
		logger.Panicf("Failed listing join-block file repo: %v", err)
	}

	var systemChannelID string
	for _, fileName := range joinBlockFiles {
		channelName := joinBlockFileRepo.FileToBaseName(fileName)
		blockBytes, err := joinBlockFileRepo.Read(channelName)
		if err != nil {
			logger.Panicf("Failed reading join-block for channel '%s', error: %v", channelName, err)
		}
		block, err := protoutil.UnmarshalBlock(blockBytes)
		if err != nil {
			logger.Panicf("Failed unmarshalling join-block for channel '%s', error: %v", channelName, err)
		}
		if err = onboarding.ValidateBootstrapBlock(block, cryptoProvider); err == nil {
			bootstrapBlock = block
			systemChannelID = channelName
			break
		}
	}

	if bootstrapBlock == nil {
		logger.Debug("No join-block was found for the system channel")
		return nil
	}

	if bootstrapBlock.Header.Number == 0 {
		initializeBootstrapChannel(bootstrapBlock, lf)
	}

	logger.Infof("Join-block was found for the system channel: %s, number: %d", systemChannelID, bootstrapBlock.Header.Number)
	return bootstrapBlock
}

func discardSystemChannelJoinBlock(config *localconfig.TopLevel, bootstrapBlock *cb.Block) {
	if !config.ChannelParticipation.Enabled {
		return
	}

	systemChannelName, err := protoutil.GetChannelIDFromBlock(bootstrapBlock)
	if err != nil {
		logger.Panicf("Failed to extract system channel name from join-block: %s", err)
	}
	joinBlockFileRepo, err := multichannel.InitJoinBlockFileRepo(config)
	if err != nil {
		logger.Panicf("Failed initializing join-block file repo: %v", err)
	}
	err = joinBlockFileRepo.Remove(systemChannelName)
	if err != nil {
		logger.Panicf("Failed to remove join-block for system channel: %s", err)
	}
}

func reuseListener(conf *localconfig.TopLevel) bool {
	clusterConf := conf.General.Cluster
	// If listen address is not configured, and the TLS certificate isn't configured,
	// it means we use the general listener of the node.
	if clusterConf.ListenPort == 0 && clusterConf.ServerCertificate == "" && clusterConf.ListenAddress == "" && clusterConf.ServerPrivateKey == "" {
		logger.Info("Cluster listener is not configured, defaulting to use the general listener on port", conf.General.ListenPort)

		if !conf.General.TLS.Enabled {
			logger.Panicf("TLS is required for running ordering nodes of cluster type.")
		}
		return true
	}

	// Else, one of the above is defined, so all 4 properties should be defined.
	if clusterConf.ListenPort == 0 || clusterConf.ServerCertificate == "" || clusterConf.ListenAddress == "" || clusterConf.ServerPrivateKey == "" {
		logger.Panic("Options: General.Cluster.ListenPort, General.Cluster.ListenAddress, General.Cluster.ServerCertificate, General.Cluster.ServerPrivateKey, should be defined altogether.")
	}

	return false
}

// extractSystemChannel loops through all channels, and return the last
// config block for the system channel. Returns nil if no system channel
// was found.
// 遍历所有通道，返回系统通道的最后一个配置块的，如果没有找到系统通道返回nil
func extractSystemChannel(lf blockledger.Factory, bccsp bccsp.BCCSP) *cb.Block {
	for _, cID := range lf.ChannelIDs() {
		// 通过账本id获取账本， 如果不存在则创建
		channelLedger, err := lf.GetOrCreate(cID)
		if err != nil {
			logger.Panicf("Failed getting channel %v's ledger: %v", cID, err)
		}
		// 如果账本高度0
		if channelLedger.Height() == 0 {
			continue // Some channels may have an empty ledger and (possibly) a join-block, skip those
		}
		// 给定的账本中查找到最新的配置区块
		channelConfigBlock := multichannel.ConfigBlockOrPanic(channelLedger)
		//
		err = onboarding.ValidateBootstrapBlock(channelConfigBlock, bccsp)
		if err == nil {
			logger.Infof("Found system channel config block, number: %d", channelConfigBlock.Header.Number)
			return channelConfigBlock
		}
	}
	return nil
}

// Select cluster boot block
// 选择 引导区块
func selectClusterBootBlock(bootstrapBlock, sysChanLastConfig *cb.Block) *cb.Block {
	// 如果查询到的配置区块是Nil则返回 引导文件导出的区块
	if sysChanLastConfig == nil {
		logger.Debug("Selected bootstrap block, because system channel last config block is nil")
		return bootstrapBlock
	}

	if bootstrapBlock == nil {
		logger.Debug("Selected system channel last config block, because bootstrap block is nil")
		return sysChanLastConfig
	}
	// 如果读取到的配置区块的区块号大于 文件导出的区块的区块号，说明发生过区块配置的更新，则使用最后一次更新的配置区块作为引导
	// 不适用文件导出的区块作为引导
	if sysChanLastConfig.Header.Number > bootstrapBlock.Header.Number {
		logger.Infof("Cluster boot block is system channel last config block; Blocks Header.Number system-channel=%d, bootstrap=%d",
			sysChanLastConfig.Header.Number, bootstrapBlock.Header.Number)
		return sysChanLastConfig
	}

	logger.Infof("Cluster boot block is bootstrap (genesis) block; Blocks Header.Number system-channel=%d, bootstrap=%d",
		sysChanLastConfig.Header.Number, bootstrapBlock.Header.Number)
	return bootstrapBlock
}

func initializeLogging() {
	loggingSpec := os.Getenv("FABRIC_LOGGING_SPEC")
	loggingFormat := os.Getenv("FABRIC_LOGGING_FORMAT")
	// 日志初始化
	flogging.Init(flogging.Config{
		Format:  loggingFormat,
		Writer:  os.Stderr,
		LogSpec: loggingSpec,
	})
}

// Start the profiling service if enabled.
// 开启 go pprof服务，用于信息收集
func initializeProfilingService(conf *localconfig.TopLevel) {
	logger.Info("Starting Go pprof profiling service on:", conf.General.Profile.Address)
	// The ListenAndServe() call does not return unless an error occurs.
	logger.Panic("Go pprof service failed:", http.ListenAndServe(conf.General.Profile.Address, nil))
}

func handleSignals(handlers map[os.Signal]func()) {
	var signals []os.Signal
	for sig := range handlers {
		signals = append(signals, sig)
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, signals...)

	go func() {
		for sig := range signalChan {
			logger.Infof("Received signal: %d (%s)", sig, sig)
			handlers[sig]()
		}
	}()
}

type loadPEMFunc func(string) ([]byte, error)

// configureClusterListener returns a new ServerConfig and a new gRPC server (with its own TLS listener).
func configureClusterListener(conf *localconfig.TopLevel, generalConf comm.ServerConfig, loadPEM loadPEMFunc) (comm.ServerConfig, *comm.GRPCServer) {
	clusterConf := conf.General.Cluster

	cert, err := loadPEM(clusterConf.ServerCertificate)
	if err != nil {
		logger.Panicf("Failed to load cluster server certificate from '%s' (%s)", clusterConf.ServerCertificate, err)
	}

	key, err := loadPEM(clusterConf.ServerPrivateKey)
	if err != nil {
		logger.Panicf("Failed to load cluster server key from '%s' (%s)", clusterConf.ServerPrivateKey, err)
	}

	port := fmt.Sprintf("%d", clusterConf.ListenPort)
	bindAddr := net.JoinHostPort(clusterConf.ListenAddress, port)

	var clientRootCAs [][]byte
	for _, serverRoot := range conf.General.Cluster.RootCAs {
		rootCACert, err := loadPEM(serverRoot)
		if err != nil {
			logger.Panicf("Failed to load CA cert file '%s' (%s)", serverRoot, err)
		}
		clientRootCAs = append(clientRootCAs, rootCACert)
	}

	serverConf := comm.ServerConfig{
		StreamInterceptors: generalConf.StreamInterceptors,
		UnaryInterceptors:  generalConf.UnaryInterceptors,
		ConnectionTimeout:  generalConf.ConnectionTimeout,
		ServerStatsHandler: generalConf.ServerStatsHandler,
		Logger:             generalConf.Logger,
		KaOpts:             generalConf.KaOpts,
		SecOpts: comm.SecureOptions{
			TimeShift:         conf.General.Cluster.TLSHandshakeTimeShift,
			CipherSuites:      comm.DefaultTLSCipherSuites,
			ClientRootCAs:     clientRootCAs,
			RequireClientCert: true,
			Certificate:       cert,
			UseTLS:            true,
			Key:               key,
		},
	}

	srv, err := comm.NewGRPCServer(bindAddr, serverConf)
	if err != nil {
		logger.Panicf("Failed creating gRPC server on %s:%d due to %v", clusterConf.ListenAddress, clusterConf.ListenPort, err)
	}

	return serverConf, srv
}

// 初始化集群客户端配置
func initializeClusterClientConfig(conf *localconfig.TopLevel) (comm.ClientConfig, bool) {
	cc := comm.ClientConfig{
		AsyncConnect:   true,
		KaOpts:         comm.DefaultKeepaliveOptions,
		DialTimeout:    conf.General.Cluster.DialTimeout,
		SecOpts:        comm.SecureOptions{},
		MaxRecvMsgSize: int(conf.General.MaxRecvMsgSize),
		MaxSendMsgSize: int(conf.General.MaxSendMsgSize),
	}

	reuseGrpcListener := reuseListener(conf)
	certFile := conf.General.Cluster.ClientCertificate
	keyFile := conf.General.Cluster.ClientPrivateKey
	if certFile == "" && keyFile == "" {
		if !reuseGrpcListener {
			return cc, reuseGrpcListener
		}
		certFile = conf.General.TLS.Certificate
		keyFile = conf.General.TLS.PrivateKey
	}

	certBytes, err := ioutil.ReadFile(certFile)
	if err != nil {
		logger.Fatalf("Failed to load client TLS certificate file '%s' (%s)", certFile, err)
	}

	keyBytes, err := ioutil.ReadFile(keyFile)
	if err != nil {
		logger.Fatalf("Failed to load client TLS key file '%s' (%s)", keyFile, err)
	}

	var serverRootCAs [][]byte
	for _, serverRoot := range conf.General.Cluster.RootCAs {
		rootCACert, err := ioutil.ReadFile(serverRoot)
		if err != nil {
			logger.Fatalf("Failed to load ServerRootCAs file '%s' (%s)", serverRoot, err)
		}
		serverRootCAs = append(serverRootCAs, rootCACert)
	}

	timeShift := conf.General.TLS.TLSHandshakeTimeShift
	if !reuseGrpcListener {
		timeShift = conf.General.Cluster.TLSHandshakeTimeShift
	}

	cc.SecOpts = comm.SecureOptions{
		TimeShift:         timeShift,
		RequireClientCert: true,
		CipherSuites:      comm.DefaultTLSCipherSuites,
		ServerRootCAs:     serverRootCAs,
		Certificate:       certBytes,
		Key:               keyBytes,
		UseTLS:            true,
	}

	return cc, reuseGrpcListener
}

// 初始化服务配置
func initializeServerConfig(conf *localconfig.TopLevel, metricsProvider metrics.Provider) comm.ServerConfig {
	// secure server config
	secureOpts := comm.SecureOptions{
		UseTLS:            conf.General.TLS.Enabled,
		RequireClientCert: conf.General.TLS.ClientAuthRequired,
		TimeShift:         conf.General.TLS.TLSHandshakeTimeShift,
	}
	// check to see if TLS is enabled
	if secureOpts.UseTLS {
		msg := "TLS"
		// load crypto material from files
		// 从文件中获取加密证书内容

		// tls/server.crt
		serverCertificate, err := ioutil.ReadFile(conf.General.TLS.Certificate)
		if err != nil {
			logger.Fatalf("Failed to load server Certificate file '%s' (%s)",
				conf.General.TLS.Certificate, err)
		}

		// tls/server.key
		serverKey, err := ioutil.ReadFile(conf.General.TLS.PrivateKey)
		if err != nil {
			logger.Fatalf("Failed to load PrivateKey file '%s' (%s)",
				conf.General.TLS.PrivateKey, err)
		}
		// tls/ca.crt
		var serverRootCAs, clientRootCAs [][]byte
		for _, serverRoot := range conf.General.TLS.RootCAs {
			root, err := ioutil.ReadFile(serverRoot)
			if err != nil {
				logger.Fatalf("Failed to load ServerRootCAs file '%s' (%s)",
					err, serverRoot)
			}
			serverRootCAs = append(serverRootCAs, root)
		}
		// 判断是否启用客户端身份验证
		// client cert tls/server.crt
		// client key tls/server.key
		if secureOpts.RequireClientCert {
			for _, clientRoot := range conf.General.TLS.ClientRootCAs {
				root, err := ioutil.ReadFile(clientRoot)
				if err != nil {
					logger.Fatalf("Failed to load ClientRootCAs file '%s' (%s)",
						err, clientRoot)
				}
				clientRootCAs = append(clientRootCAs, root)
			}
			msg = "mutual TLS"
		}
		secureOpts.Key = serverKey
		secureOpts.Certificate = serverCertificate
		secureOpts.ServerRootCAs = serverRootCAs
		secureOpts.ClientRootCAs = clientRootCAs //tls/ca.crt
		logger.Infof("Starting orderer with %s enabled", msg)
	}
	kaOpts := comm.DefaultKeepaliveOptions // 默认长连接配置
	// keepalive settings
	// ServerMinInterval must be greater than 0
	// 服务端最小间隔必须大于0

	//Keepalive:
	//	# ServerMinInterval is the minimum permitted time between client pings.
	// 客户端允许的最小的时间间隔
	//	# If clients send pings more frequently, the server will
	// 如果客户端ping很频繁，服务端将断开连接
	//	# disconnect them.
	//		ServerMinInterval: 60s
	//	# ServerInterval is the time between pings to clients.
	// 两个客户端之间的ping间隔 2h
	//		ServerInterval: 7200s
	//	# ServerTimeout is the duration the server waits for a response from
	//	# a client before closing the connection.
	// 服务端等待客户端响应的超时时间
	//		ServerTimeout: 20s
	if conf.General.Keepalive.ServerMinInterval > time.Duration(0) {
		kaOpts.ServerMinInterval = conf.General.Keepalive.ServerMinInterval
	}
	kaOpts.ServerInterval = conf.General.Keepalive.ServerInterval
	kaOpts.ServerTimeout = conf.General.Keepalive.ServerTimeout

	commLogger := flogging.MustGetLogger("core.comm").With("server", "Orderer")

	// 如果是Nil则禁用
	if metricsProvider == nil {
		metricsProvider = &disabled.Provider{}
	}

	// grpc server 相关配置
	return comm.ServerConfig{
		SecOpts:            secureOpts,
		KaOpts:             kaOpts,
		Logger:             commLogger,
		ServerStatsHandler: comm.NewServerStatsHandler(metricsProvider),
		ConnectionTimeout:  conf.General.ConnectionTimeout,
		StreamInterceptors: []grpc.StreamServerInterceptor{
			grpcmetrics.StreamServerInterceptor(grpcmetrics.NewStreamMetrics(metricsProvider)),
			grpclogging.StreamServerInterceptor(flogging.MustGetLogger("comm.grpc.server").Zap()),
		},
		UnaryInterceptors: []grpc.UnaryServerInterceptor{
			grpcmetrics.UnaryServerInterceptor(grpcmetrics.NewUnaryMetrics(metricsProvider)),
			grpclogging.UnaryServerInterceptor(
				flogging.MustGetLogger("comm.grpc.server").Zap(),
				grpclogging.WithLeveler(grpclogging.LevelerFunc(grpcLeveler)),
			),
		},
		MaxRecvMsgSize: int(conf.General.MaxRecvMsgSize),
		MaxSendMsgSize: int(conf.General.MaxSendMsgSize),
	}
}

func grpcLeveler(ctx context.Context, fullMethod string) zapcore.Level {
	switch fullMethod {
	case "/orderer.Cluster/Step":
		return flogging.DisabledLevel
	default:
		return zapcore.InfoLevel
	}
}

func extractBootstrapBlock(conf *localconfig.TopLevel) *cb.Block {
	var bootstrapBlock *cb.Block

	// Select the bootstrapping mechanism
	switch conf.General.BootstrapMethod {
	case "file": // For now, "file" is the only supported genesis method
		bootstrapBlock = file.New(conf.General.BootstrapFile).GenesisBlock()
	case "none": // simply honor the configuration value
		return nil
	default:
		logger.Panic("Unknown genesis method:", conf.General.BootstrapMethod)
	}

	return bootstrapBlock
}

// 初始化系统通道
func initializeBootstrapChannel(genesisBlock *cb.Block, lf blockledger.Factory) {
	// 从区块中获取到通道ID
	channelID, err := protoutil.GetChannelIDFromBlock(genesisBlock)
	if err != nil {
		logger.Fatal("Failed to parse channel ID from genesis block:", err)
	}
	// fileledger
	gl, err := lf.GetOrCreate(channelID)
	if err != nil {
		logger.Fatal("Failed to create the system channel:", err)
	}
	// 获取当前链的信息，且高度是0的话，添加创世块到
	if gl.Height() == 0 {
		if err := gl.Append(genesisBlock); err != nil {
			logger.Fatal("Could not write genesis block to ledger:", err)
		}
	}
	logger.Infof("Initialized the system channel '%s' from bootstrap block", channelID)
}

func isClusterType(genesisBlock *cb.Block, bccsp bccsp.BCCSP) bool {
	_, exists := clusterTypes[consensusType(genesisBlock, bccsp)]
	return exists
}

// 创世块中获取共识算法类型
func consensusType(genesisBlock *cb.Block, bccsp bccsp.BCCSP) string {
	// 校验区块信息
	if genesisBlock == nil || genesisBlock.Data == nil || len(genesisBlock.Data.Data) == 0 {
		logger.Fatalf("Empty genesis block")
	}
	env := &cb.Envelope{}
	if err := proto.Unmarshal(genesisBlock.Data.Data[0], env); err != nil {
		logger.Fatalf("Failed to unmarshal the genesis block's envelope: %v", err)
	}
	//
	bundle, err := channelconfig.NewBundleFromEnvelope(env, bccsp)
	if err != nil {
		logger.Fatalf("Failed creating bundle from the genesis block: %v", err)
	}
	ordConf, exists := bundle.OrdererConfig()
	if !exists {
		logger.Fatalf("Orderer config doesn't exist in bundle derived from genesis block")
	}
	return ordConf.ConsensusType()
}

// 初始化 GRPC 服务
func initializeGrpcServer(conf *localconfig.TopLevel, serverConfig comm.ServerConfig) *comm.GRPCServer {
	// 启用socket监听
	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", conf.General.ListenAddress, conf.General.ListenPort))
	if err != nil {
		logger.Fatal("Failed to listen:", err)
	}

	// Create GRPC server - return if an error occurs
	grpcServer, err := comm.NewGRPCServerFromListener(lis, serverConfig)
	if err != nil {
		logger.Fatal("Failed to return new GRPC server:", err)
	}

	return grpcServer
}

// 加载本地MSP
func loadLocalMSP(conf *localconfig.TopLevel) msp.MSP {
	// MUST call GetLocalMspConfig first, so that default BCCSP is properly
	// initialized prior to LoadByType.
	// 获取 FabricMSPConfig
	// 生成FabricMSPConfig 初始化 DefaultBCCSP
	mspConfig, err := msp.GetLocalMspConfig(conf.General.LocalMSPDir, conf.General.BCCSP, conf.General.LocalMSPID)
	if err != nil {
		logger.Panicf("Failed to get local msp config: %v", err)
	}

	// 将fabric类型转换成字符串形式
	typ := msp.ProviderTypeToString(msp.FABRIC)
	opts, found := msp.Options[typ]
	// 如果没有查找到，说明源代码不完整，关于msp.Options 的部分是硬编码到msp包中
	if !found {
		logger.Panicf("MSP option for type %s is not found", typ)
	}
	// 基于msp选项参数和bccsp，创建一个新的msp实例
	localmsp, err := msp.New(opts, factory.GetDefault())
	if err != nil {
		logger.Panicf("Failed to load local MSP: %v", err)
	}
	// 根据mspConfig设置bccsmsp
	if err = localmsp.Setup(mspConfig); err != nil {
		logger.Panicf("Failed to setup local msp with config: %v", err)
	}

	return localmsp
}

//go:generate counterfeiter -o mocks/health_checker.go -fake-name HealthChecker . healthChecker

// HealthChecker defines the contract for health checker
type healthChecker interface {
	RegisterChecker(component string, checker healthz.HealthChecker) error
}

func initializeMultichannelRegistrar(
	bootstrapBlock *cb.Block,
	repInitiator *onboarding.ReplicationInitiator,
	clusterDialer *cluster.PredicateDialer,
	srvConf comm.ServerConfig,
	srv *comm.GRPCServer,
	conf *localconfig.TopLevel,
	signer identity.SignerSerializer,
	metricsProvider metrics.Provider,
	healthChecker healthChecker,
	lf blockledger.Factory,
	bccsp bccsp.BCCSP,
	callbacks ...channelconfig.BundleActor,
) *multichannel.Registrar {
	registrar := multichannel.NewRegistrar(*conf, lf, signer, metricsProvider, bccsp, clusterDialer, callbacks...)

	consenters := map[string]consensus.Consenter{}

	var icr etcdraft.InactiveChainRegistry
	if conf.General.BootstrapMethod == "file" || conf.General.BootstrapMethod == "none" {
		if bootstrapBlock != nil && isClusterType(bootstrapBlock, bccsp) {
			// with a system channel
			etcdConsenter := initializeEtcdraftConsenter(consenters, conf, lf, clusterDialer, bootstrapBlock, repInitiator, srvConf, srv, registrar, metricsProvider, bccsp)
			icr = etcdConsenter.InactiveChainRegistry
		} else if bootstrapBlock == nil {
			// without a system channel: assume cluster type, InactiveChainRegistry == nil, no go-routine.
			consenters["etcdraft"] = etcdraft.New(clusterDialer, conf, srvConf, srv, registrar, nil, metricsProvider, bccsp)
		}
	}

	consenters["solo"] = solo.New()
	var kafkaMetrics *kafka.Metrics
	consenters["kafka"], kafkaMetrics = kafka.New(conf.Kafka, metricsProvider, healthChecker, icr, registrar.CreateChain)

	// Note, we pass a 'nil' channel here, we could pass a channel that
	// closes if we wished to cleanup this routine on exit.
	go kafkaMetrics.PollGoMetricsUntilStop(time.Minute, nil)
	registrar.Initialize(consenters)
	return registrar
}

// 初始化 ETCDRAFT 共识
func initializeEtcdraftConsenter(
	consenters map[string]consensus.Consenter,
	conf *localconfig.TopLevel,
	lf blockledger.Factory,
	clusterDialer *cluster.PredicateDialer,
	bootstrapBlock *cb.Block,
	ri *onboarding.ReplicationInitiator,
	srvConf comm.ServerConfig,
	srv *comm.GRPCServer,
	registrar *multichannel.Registrar,
	metricsProvider metrics.Provider,
	bccsp bccsp.BCCSP,
) *etcdraft.Consenter {
	systemChannelName, err := protoutil.GetChannelIDFromBlock(bootstrapBlock)
	if err != nil {
		logger.Panicf("Failed extracting system channel name from bootstrap block: %v", err)
	}
	systemLedger, err := lf.GetOrCreate(systemChannelName)
	if err != nil {
		logger.Panicf("Failed obtaining system channel (%s) ledger: %v", systemChannelName, err)
	}
	getConfigBlock := func() *cb.Block {
		return multichannel.ConfigBlockOrPanic(systemLedger)
	}

	icr := onboarding.NewInactiveChainReplicator(ri, getConfigBlock, ri.RegisterChain, conf.General.Cluster.ReplicationBackgroundRefreshInterval)

	// Use the inactiveChainReplicator as a channel lister, since it has knowledge of all inactive chains.
	// This is to prevent us pulling the entire system chain when attempting to enumerate the channels in the system.
	// 使用 inactiveChainReplicator 作为通道的一个监听者，因为他知道所有的未活跃的链
	// 这是为了防止我们在试图枚举系统中的通道时拉出整个系统链
	ri.ChannelLister = icr

	go icr.Run()
	raftConsenter := etcdraft.New(clusterDialer, conf, srvConf, srv, registrar, icr, metricsProvider, bccsp)
	consenters["etcdraft"] = raftConsenter
	return raftConsenter
}

// 载入运维系统相关配置
func newOperationsSystem(ops localconfig.Operations, metrics localconfig.Metrics) *operations.System {
	return operations.NewSystem(operations.Options{
		Options: fabhttp.Options{
			// fabric http
			Logger:        flogging.MustGetLogger("orderer.operations"),
			ListenAddress: ops.ListenAddress,
			TLS: fabhttp.TLS{
				Enabled:            ops.TLS.Enabled,
				CertFile:           ops.TLS.Certificate,
				KeyFile:            ops.TLS.PrivateKey,
				ClientCertRequired: ops.TLS.ClientAuthRequired,
				ClientCACertFiles:  ops.TLS.ClientRootCAs,
			},
		},
		// 监控指标配置项
		Metrics: operations.MetricsOptions{
			Provider: metrics.Provider,
			Statsd: &operations.Statsd{
				Network:       metrics.Statsd.Network,
				Address:       metrics.Statsd.Address,
				WriteInterval: metrics.Statsd.WriteInterval,
				Prefix:        metrics.Statsd.Prefix,
			},
		},
		Version: metadata.Version,
	})
}

func newAdminServer(admin localconfig.Admin) *fabhttp.Server {
	return fabhttp.NewServer(fabhttp.Options{
		Logger:        flogging.MustGetLogger("orderer.admin"),
		ListenAddress: admin.ListenAddress,
		TLS: fabhttp.TLS{
			Enabled:            admin.TLS.Enabled,
			CertFile:           admin.TLS.Certificate,
			KeyFile:            admin.TLS.PrivateKey,
			ClientCertRequired: admin.TLS.ClientAuthRequired,
			ClientCACertFiles:  admin.TLS.ClientRootCAs,
		},
	})
}

// caMgr manages certificate authorities scoped by channel
// 管理通道作用域的证书
type caManager struct {
	sync.Mutex
	appRootCAsByChain     map[string][][]byte
	ordererRootCAsByChain map[string][][]byte
	clientRootCAs         [][]byte
}

func (mgr *caManager) updateTrustedRoots(
	cm channelconfig.Resources,
	servers ...*comm.GRPCServer,
) {
	mgr.Lock()
	defer mgr.Unlock()

	appRootCAs := [][]byte{}
	ordererRootCAs := [][]byte{}
	appOrgMSPs := make(map[string]struct{})
	ordOrgMSPs := make(map[string]struct{})

	if ac, ok := cm.ApplicationConfig(); ok {
		// loop through app orgs and build map of MSPIDs
		for _, appOrg := range ac.Organizations() {
			appOrgMSPs[appOrg.MSPID()] = struct{}{}
		}
	}

	if ac, ok := cm.OrdererConfig(); ok {
		// loop through orderer orgs and build map of MSPIDs
		for _, ordOrg := range ac.Organizations() {
			ordOrgMSPs[ordOrg.MSPID()] = struct{}{}
		}
	}

	if cc, ok := cm.ConsortiumsConfig(); ok {
		for _, consortium := range cc.Consortiums() {
			// loop through consortium orgs and build map of MSPIDs
			for _, consortiumOrg := range consortium.Organizations() {
				appOrgMSPs[consortiumOrg.MSPID()] = struct{}{}
			}
		}
	}

	cid := cm.ConfigtxValidator().ChannelID()
	logger.Debugf("updating root CAs for channel [%s]", cid)
	msps, err := cm.MSPManager().GetMSPs()
	if err != nil {
		logger.Errorf("Error getting root CAs for channel %s (%s)", cid, err)
		return
	}
	for k, v := range msps {
		// check to see if this is a FABRIC MSP
		if v.GetType() == msp.FABRIC {
			for _, root := range v.GetTLSRootCerts() {
				// check to see of this is an app org MSP
				if _, ok := appOrgMSPs[k]; ok {
					logger.Debugf("adding app root CAs for MSP [%s]", k)
					appRootCAs = append(appRootCAs, root)
				}
				// check to see of this is an orderer org MSP
				if _, ok := ordOrgMSPs[k]; ok {
					logger.Debugf("adding orderer root CAs for MSP [%s]", k)
					ordererRootCAs = append(ordererRootCAs, root)
				}
			}
			for _, intermediate := range v.GetTLSIntermediateCerts() {
				// check to see of this is an app org MSP
				if _, ok := appOrgMSPs[k]; ok {
					logger.Debugf("adding app root CAs for MSP [%s]", k)
					appRootCAs = append(appRootCAs, intermediate)
				}
				// check to see of this is an orderer org MSP
				if _, ok := ordOrgMSPs[k]; ok {
					logger.Debugf("adding orderer root CAs for MSP [%s]", k)
					ordererRootCAs = append(ordererRootCAs, intermediate)
				}
			}
		}
	}
	mgr.appRootCAsByChain[cid] = appRootCAs
	mgr.ordererRootCAsByChain[cid] = ordererRootCAs

	// now iterate over all roots for all app and orderer chains
	trustedRoots := [][]byte{}
	for _, roots := range mgr.appRootCAsByChain {
		trustedRoots = append(trustedRoots, roots...)
	}
	for _, roots := range mgr.ordererRootCAsByChain {
		trustedRoots = append(trustedRoots, roots...)
	}
	// also need to append statically configured root certs
	if len(mgr.clientRootCAs) > 0 {
		trustedRoots = append(trustedRoots, mgr.clientRootCAs...)
	}

	// now update the client roots for the gRPC server
	for _, srv := range servers {
		err = srv.SetClientRootCAs(trustedRoots)
		if err != nil {
			msg := "Failed to update trusted roots for orderer from latest config " +
				"block.  This orderer may not be able to communicate " +
				"with members of channel %s (%s)"
			logger.Warningf(msg, cm.ConfigtxValidator().ChannelID(), err)
		}
	}
}

func (mgr *caManager) updateClusterDialer(
	clusterDialer *cluster.PredicateDialer,
	localClusterRootCAs [][]byte,
) {
	mgr.Lock()
	defer mgr.Unlock()

	// Iterate over all orderer root CAs for all chains and add them
	// to the root CAs
	clusterRootCAs := make(cluster.StringSet)
	for _, orgRootCAs := range mgr.ordererRootCAsByChain {
		for _, rootCA := range orgRootCAs {
			clusterRootCAs[string(rootCA)] = struct{}{}
		}
	}

	// Add the local root CAs too
	for _, localRootCA := range localClusterRootCAs {
		clusterRootCAs[string(localRootCA)] = struct{}{}
	}

	// Convert StringSet to byte slice
	var clusterRootCAsBytes [][]byte
	for root := range clusterRootCAs {
		clusterRootCAsBytes = append(clusterRootCAsBytes, []byte(root))
	}

	// Update the cluster config with the new root CAs
	clusterDialer.UpdateRootCAs(clusterRootCAsBytes)
}

// 美化配置信息的打印
func prettyPrintStruct(i interface{}) {
	// 将结构体中配置信息以 k=v形式格式化
	params := localconfig.Flatten(i)
	var buffer bytes.Buffer
	for i := range params {
		buffer.WriteString("\n\t")
		buffer.WriteString(params[i])
	}
	logger.Infof("Orderer config values:%s\n", buffer.String())
}
