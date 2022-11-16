/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// The 'viper' package for configuration handling is very flexible, but has
// been found to have extremely poor performance when configuration values are
// accessed repeatedly. The function CacheConfiguration() defined here caches
// all configuration values that are accessed frequently.  These parameters
// are now presented as function calls that access local configuration
// variables.  This seems to be the most robust way to represent these
// parameters in the face of the numerous ways that configuration files are
// loaded and used (e.g, normal usage vs. test cases).

// The CacheConfiguration() function is allowed to be called globally to
// ensure that the correct values are always cached; See for example how
// certain parameters are forced in 'ChaincodeDevMode' in main.go.

package peer

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hyperledger/fabric/common/viperutil"
	"github.com/hyperledger/fabric/core/config"
	"github.com/hyperledger/fabric/internal/pkg/comm"
	gatewayconfig "github.com/hyperledger/fabric/internal/pkg/gateway/config"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// ExternalBuilder represents the configuration structure of
// a chaincode external builder
// core.yaml 配置中对应外部链码构建器
type ExternalBuilder struct {
	// TODO: Remove Environment in 3.0
	// Deprecated: Environment is retained for backwards compatibility.
	// New deployments should use the new PropagateEnvironment field
	Environment          []string `yaml:"environmentWhitelist"` //环境变量白名单
	PropagateEnvironment []string `yaml:"propagateEnvironment"` // 传递环境变量
	Name                 string   `yaml:"name"`
	Path                 string   `yaml:"path"`
}

// Config is the struct that defines the Peer configurations.
type Config struct {
	// LocalMSPID is the identifier of the local MSP.
	LocalMSPID string
	// ListenAddress is the local address the peer will listen on. It must be
	// formatted as [host | ipaddr]:port.
	ListenAddress string
	// PeerID provides a name for this peer instance. It is used when naming
	// docker resources to segregate fabric networks and peers.
	PeerID string // 对等体实例的名称
	// PeerAddress is the address other peers and clients should use to
	// communicate with the peer. It must be formatted as [host | ipaddr]:port.
	// When used by the CLI, it represents the target peer endpoint.
	PeerAddress string
	// NetworkID specifies a name to use for logical separation of networks. It
	// is used when naming docker resources to segregate fabric networks and
	// peers.
	NetworkID string // 用于实现将网络在逻辑上的分离
	// ChaincodeListenAddress is the endpoint on which this peer will listen for
	// chaincode connections. If omitted, it defaults to the host portion of
	// PeerAddress and port 7052.
	ChaincodeListenAddress string
	// ChaincodeAddress specifies the endpoint chaincode launched by the peer
	// should use to connect to the peer. If omitted, it defaults to
	// ChaincodeListenAddress and falls back to ListenAddress.
	ChaincodeAddress string
	// ValidatorPoolSize indicates the number of goroutines that will execute
	// transaction validation in parallel. If omitted, it defaults to number of
	// hardware threads on the machine.
	// 并行验证交易的goroutines数量，如果不指定默认数量是服务器硬件上核心数
	ValidatorPoolSize int

	// ----- Profile -----
	// TODO: create separate sub-struct for Profile config.

	// ProfileEnabled determines if the go pprof endpoint is enabled in the peer.
	// 是否启用pprof
	ProfileEnabled bool
	// ProfileListenAddress is the address the pprof server should accept
	// connections on.
	// pprof 监听的地址端口设置
	ProfileListenAddress string

	// ----- Discovery -----

	// The discovery service is used by clients to query information about peers,
	// such as - which peers have joined a certain channel, what is the latest
	// channel config, and most importantly - given a chaincode and a channel, what
	// possible sets of peers satisfy the endorsement policy.
	// TODO: create separate sub-struct for Discovery config.

	// DiscoveryEnabled is used to enable the discovery service.
	// 启用服务发现
	DiscoveryEnabled bool
	// DiscoveryOrgMembersAllowed allows non-admins to perform non channel-scoped queries.
	// 允许非管理员执行非通道范围的查询
	DiscoveryOrgMembersAllowed bool
	// DiscoveryAuthCacheEnabled is used to enable the authentication cache.
	// 用于启用验证缓存
	DiscoveryAuthCacheEnabled bool
	// DiscoveryAuthCacheMaxSize sets the maximum size of authentication cache.
	// 验证缓存的最大值
	DiscoveryAuthCacheMaxSize int
	// DiscoveryAuthCachePurgeRetentionRatio set the proportion of entries remains in cache
	// after overpopulation purge.
	// 设置剩余条目的比例，用于过剩时候的清理
	DiscoveryAuthCachePurgeRetentionRatio float64

	// ----- Limits -----
	// Limits is used to configure some internal resource limits.
	// TODO: create separate sub-struct for Limits config.

	// LimitsConcurrencyEndorserService sets the limits for concurrent requests sent to
	// endorser service that handles chaincode deployment, query and invocation,
	// including both user chaincodes and system chaincodes.
	// 设置限制并发发送到背书服务的请求,包括链码部署，查询，执行，包含应用链码和系统链码两种类型
	LimitsConcurrencyEndorserService int

	// LimitsConcurrencyDeliverService sets the limits for concurrent event listeners
	// registered to deliver service for blocks and transaction events.
	// 设置事件监听注册到deliver服务的区块和交易时间请求的并发数
	LimitsConcurrencyDeliverService int

	// LimitsConcurrencyGatewayService sets the limits for concurrent requests to
	// gateway service that handles the submission and evaluation of transactions.
	// 设置并发请求到gateway服务，它处理交易的提交和评估
	LimitsConcurrencyGatewayService int

	// ----- TLS -----
	// Require server-side TLS.
	// TODO: create separate sub-struct for PeerTLS config.

	// PeerTLSEnabled enables/disables Peer TLS.
	// 是否启用peer TLS
	PeerTLSEnabled bool

	// ----- Authentication -----
	// Authentication contains configuration parameters related to authenticating
	// client messages.
	// TODO: create separate sub-struct for Authentication config.

	// AuthenticationTimeWindow sets the acceptable time duration for current
	// server time and client's time as specified in a client request message.
	// 设置可接受的时间间隔，对于当前服务器时间和客户端时间，在客户端请求中指定这个信息
	// 认证时间窗口
	AuthenticationTimeWindow time.Duration

	// Endpoint of the vm management system. For docker can be one of the following in general
	// unix:///var/run/docker.sock
	// http://localhost:2375
	// https://localhost:2376

	// 虚拟机管理系统 docker一般指向他的套接字文件
	VMEndpoint string

	// ----- vm.docker.tls -----
	// TODO: create separate sub-struct for VM.Docker.TLS config.

	// VMDockerTLSEnabled enables/disables TLS for dockers.
	// 虚拟机是否开启TLS
	VMDockerTLSEnabled   bool
	VMDockerAttachStdout bool
	// VMNetworkMode sets the networking mode for the container.
	// docker 网络模式 net/host
	VMNetworkMode string

	// ChaincodePull enables/disables force pulling of the base docker image.
	// 强制拉取链码镜像
	ChaincodePull bool
	// ExternalBuilders represents the builders and launchers for
	// chaincode. The external builder detection processing will iterate over the
	// builders in the order specified below.
	// 构建并启动链码，外部构建器将按照这个列表的顺序遍历处理
	ExternalBuilders []ExternalBuilder

	// ----- Operations config -----
	// TODO: create separate sub-struct for Operations config.

	// OperationsListenAddress provides the host and port for the operations server
	// 为操作者提供地址端口
	OperationsListenAddress string
	// OperationsTLSEnabled enables/disables TLS for operations.
	// 是否开启TLS
	OperationsTLSEnabled bool
	// OperationsTLSCertFile provides the path to PEM encoded server certificate for
	// the operations server.
	// TLS证书路径
	OperationsTLSCertFile string
	// OperationsTLSKeyFile provides the path to PEM encoded server key for the
	// operations server.
	// TLS key路径
	OperationsTLSKeyFile string
	// OperationsTLSClientAuthRequired enables/disables the requirements for client
	// certificate authentication at the TLS layer to access all resource.
	// TLS 客户端是否需要证书认证访问所有资源
	OperationsTLSClientAuthRequired bool
	// OperationsTLSClientRootCAs provides the path to PEM encoded ca certiricates to
	// trust for client authentication.
	// 提供PEM编码的ca 证书，用于信任客户端认证
	OperationsTLSClientRootCAs []string

	// ----- Metrics config -----
	// TODO: create separate sub-struct for Metrics config.

	// MetricsProvider provides the categories of metrics providers, which is one of
	// statsd, prometheus, or disabled.
	// 指标提供程序，包括 statsd , prometheus ,或者禁用
	MetricsProvider string
	// StatsdNetwork indicate the network type used by statsd metrics. (tcp or udp).
	// statsd 网络类型，tcp/udp
	StatsdNetwork string
	// StatsdAaddress provides the address for statsd server.
	// Statsd地址
	StatsdAaddress string
	// StatsdWriteInterval set the time interval at which locally cached counters and
	// gauges are pushed.
	// 为本地缓存监控数据的上报的时间间隔
	StatsdWriteInterval time.Duration
	// StatsdPrefix provides the prefix that prepended to all emitted statsd metrics.
	// stats前缀
	StatsdPrefix string

	// ----- Docker config ------

	// DockerCert is the path to the PEM encoded TLS client certificate required to access
	// the docker daemon.
	// pem编码的客户端TLS证书，用于访问docker
	DockerCert string
	// DockerKey is the path to the PEM encoded key required to access the docker daemon.
	// 用于访问docker 的PEM编码的证书私钥
	DockerKey string
	// DockerCA is the path to the PEM encoded CA certificate for the docker daemon.
	// docker CA 证书
	DockerCA string

	// ----- Gateway config -----

	// The gateway service is used by client SDKs to
	// interact with fabric networks
	// 网关服务用于服务端SDK与fabric网络进行交互
	GatewayOptions gatewayconfig.Options
}

// GlobalConfig obtains a set of configuration from viper, build and returns
// the config struct.

// 从viper 获取一个配置结果返回
func GlobalConfig() (*Config, error) {
	c := &Config{}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

// 加载所有peer相关的配置信息
func (c *Config) load() error {

	peerAddress, err := getLocalAddress()
	if err != nil {
		return err
	}

	// 获取配置文件目录
	configDir := filepath.Dir(viper.ConfigFileUsed())

	c.PeerAddress = peerAddress
	//
	c.PeerID = viper.GetString("peer.id")
	c.LocalMSPID = viper.GetString("peer.localMspId")
	c.ListenAddress = viper.GetString("peer.listenAddress")

	c.AuthenticationTimeWindow = viper.GetDuration("peer.authentication.timewindow")
	// 如果认证时间窗口未设置则默认15min
	if c.AuthenticationTimeWindow == 0 {
		defaultTimeWindow := 15 * time.Minute
		logger.Warningf("`peer.authentication.timewindow` not set; defaulting to %s", defaultTimeWindow)
		c.AuthenticationTimeWindow = defaultTimeWindow
	}
	//
	c.PeerTLSEnabled = viper.GetBool("peer.tls.enabled")
	c.NetworkID = viper.GetString("peer.networkId")
	c.LimitsConcurrencyEndorserService = viper.GetInt("peer.limits.concurrency.endorserService")
	c.LimitsConcurrencyDeliverService = viper.GetInt("peer.limits.concurrency.deliverService")
	c.LimitsConcurrencyGatewayService = viper.GetInt("peer.limits.concurrency.gatewayService")
	c.DiscoveryEnabled = viper.GetBool("peer.discovery.enabled")
	c.ProfileEnabled = viper.GetBool("peer.profile.enabled")
	c.ProfileListenAddress = viper.GetString("peer.profile.listenAddress")
	c.DiscoveryOrgMembersAllowed = viper.GetBool("peer.discovery.orgMembersAllowedAccess")
	c.DiscoveryAuthCacheEnabled = viper.GetBool("peer.discovery.authCacheEnabled")
	c.DiscoveryAuthCacheMaxSize = viper.GetInt("peer.discovery.authCacheMaxSize")
	c.DiscoveryAuthCachePurgeRetentionRatio = viper.GetFloat64("peer.discovery.authCachePurgeRetentionRatio")
	c.ChaincodeListenAddress = viper.GetString("peer.chaincodeListenAddress")
	c.ChaincodeAddress = viper.GetString("peer.chaincodeAddress")

	c.ValidatorPoolSize = viper.GetInt("peer.validatorPoolSize")
	if c.ValidatorPoolSize <= 0 {
		// 如果未设置则取当前CPU核心数
		c.ValidatorPoolSize = runtime.NumCPU()
	}

	c.GatewayOptions = gatewayconfig.GetOptions(viper.GetViper())

	c.VMEndpoint = viper.GetString("vm.endpoint")
	c.VMDockerTLSEnabled = viper.GetBool("vm.docker.tls.enabled")
	c.VMDockerAttachStdout = viper.GetBool("vm.docker.attachStdout")

	c.VMNetworkMode = viper.GetString("vm.docker.hostConfig.NetworkMode")
	if c.VMNetworkMode == "" {
		// 不设置默认为host模式
		c.VMNetworkMode = "host"
	}

	c.ChaincodePull = viper.GetBool("chaincode.pull")
	var externalBuilders []ExternalBuilder

	err = viper.UnmarshalKey("chaincode.externalBuilders", &externalBuilders, viper.DecodeHook(viperutil.YamlStringToStructHook(externalBuilders)))
	if err != nil {
		return err
	}

	c.ExternalBuilders = externalBuilders
	for builderIndex, builder := range c.ExternalBuilders {
		if builder.Path == "" {
			return fmt.Errorf("invalid external builder configuration, path attribute missing in one or more builders")
		}
		if builder.Name == "" {
			return fmt.Errorf("external builder at path %s has no name attribute", builder.Path)
		}
		if builder.Environment != nil && builder.PropagateEnvironment == nil {
			c.ExternalBuilders[builderIndex].PropagateEnvironment = builder.Environment
		}
	}

	c.OperationsListenAddress = viper.GetString("operations.listenAddress")
	c.OperationsTLSEnabled = viper.GetBool("operations.tls.enabled")
	c.OperationsTLSCertFile = config.GetPath("operations.tls.cert.file")
	c.OperationsTLSKeyFile = config.GetPath("operations.tls.key.file")
	c.OperationsTLSClientAuthRequired = viper.GetBool("operations.tls.clientAuthRequired")

	for _, rca := range viper.GetStringSlice("operations.tls.clientRootCAs.files") {
		c.OperationsTLSClientRootCAs = append(c.OperationsTLSClientRootCAs, config.TranslatePath(configDir, rca))
	}

	c.MetricsProvider = viper.GetString("metrics.provider")
	c.StatsdNetwork = viper.GetString("metrics.statsd.network")
	c.StatsdAaddress = viper.GetString("metrics.statsd.address")
	c.StatsdWriteInterval = viper.GetDuration("metrics.statsd.writeInterval")
	c.StatsdPrefix = viper.GetString("metrics.statsd.prefix")

	c.DockerCert = config.GetPath("vm.docker.tls.cert.file")
	c.DockerKey = config.GetPath("vm.docker.tls.key.file")
	c.DockerCA = config.GetPath("vm.docker.tls.ca.file")

	return nil
}

// getLocalAddress returns the address:port the local peer is operating on.  Affected by env:peer.addressAutoDetect
// 返回 peer的地址和端口，这个值受环境变量：peer.addressAutoDetect影响
func getLocalAddress() (string, error) {
	// 从配置字符串中获取值
	peerAddress := viper.GetString("peer.address")
	if peerAddress == "" {
		return "", fmt.Errorf("peer.address isn't set")
	}
	host, port, err := net.SplitHostPort(peerAddress)
	if err != nil {
		return "", errors.Errorf("peer.address isn't in host:port format: %s", peerAddress)
	}

	localIP, err := getLocalIP()
	if err != nil {
		peerLogger.Errorf("local IP address not auto-detectable: %s", err)
		return "", err
	}
	// 通过自动发现获取到的地址
	autoDetectedIPAndPort := net.JoinHostPort(localIP, port)
	peerLogger.Info("Auto-detected peer address:", autoDetectedIPAndPort)
	// If host is the IPv4 address "0.0.0.0" or the IPv6 address "::",
	// then fallback to auto-detected address
	// Unspecified is 0.0.0.0
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		peerLogger.Info("Host is", host, ", falling back to auto-detected address:", autoDetectedIPAndPort)
		return autoDetectedIPAndPort, nil
	}

	// 查看配置是否开启地址自动发现
	if viper.GetBool("peer.addressAutoDetect") {
		peerLogger.Info("Auto-detect flag is set, returning", autoDetectedIPAndPort)
		return autoDetectedIPAndPort, nil
	}
	peerLogger.Info("Returning", peerAddress)
	return peerAddress, nil
}

// getLocalIP returns the a loopback local IP of the host.
// 如果不是回环地址就返回
func getLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, address := range addrs {
		// check the address type and if it is not a loopback then display it
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String(), nil
			}
		}
	}
	return "", errors.Errorf("no non-loopback, IPv4 interface detected")
}

// GetServerConfig returns the gRPC server configuration for the peer

// 返回 peer grpc服务的配置信息
func GetServerConfig() (comm.ServerConfig, error) {
	serverConfig := comm.ServerConfig{
		ConnectionTimeout: viper.GetDuration("peer.connectiontimeout"),
		SecOpts: comm.SecureOptions{
			UseTLS: viper.GetBool("peer.tls.enabled"),
		},
	}
	if serverConfig.SecOpts.UseTLS {
		// get the certs from the file system
		serverKey, err := ioutil.ReadFile(config.GetPath("peer.tls.key.file"))
		if err != nil {
			return serverConfig, fmt.Errorf("error loading TLS key (%s)", err)
		}
		serverCert, err := ioutil.ReadFile(config.GetPath("peer.tls.cert.file"))
		if err != nil {
			return serverConfig, fmt.Errorf("error loading TLS certificate (%s)", err)
		}
		serverConfig.SecOpts.Certificate = serverCert
		serverConfig.SecOpts.Key = serverKey
		serverConfig.SecOpts.RequireClientCert = viper.GetBool("peer.tls.clientAuthRequired")
		if serverConfig.SecOpts.RequireClientCert {
			var clientRoots [][]byte
			for _, file := range viper.GetStringSlice("peer.tls.clientRootCAs.files") {
				clientRoot, err := ioutil.ReadFile(
					config.TranslatePath(filepath.Dir(viper.ConfigFileUsed()), file))
				if err != nil {
					return serverConfig,
						fmt.Errorf("error loading client root CAs (%s)", err)
				}
				clientRoots = append(clientRoots, clientRoot)
			}
			serverConfig.SecOpts.ClientRootCAs = clientRoots
		}
		// check for root cert
		if config.GetPath("peer.tls.rootcert.file") != "" {
			rootCert, err := ioutil.ReadFile(config.GetPath("peer.tls.rootcert.file"))
			if err != nil {
				return serverConfig, fmt.Errorf("error loading TLS root certificate (%s)", err)
			}
			serverConfig.SecOpts.ServerRootCAs = [][]byte{rootCert}
		}
	}
	// get the default keepalive options
	serverConfig.KaOpts = comm.DefaultKeepaliveOptions
	// check to see if interval is set for the env
	if viper.IsSet("peer.keepalive.interval") {
		serverConfig.KaOpts.ServerInterval = viper.GetDuration("peer.keepalive.interval")
	}
	// check to see if timeout is set for the env
	if viper.IsSet("peer.keepalive.timeout") {
		serverConfig.KaOpts.ServerTimeout = viper.GetDuration("peer.keepalive.timeout")
	}
	// check to see if minInterval is set for the env
	if viper.IsSet("peer.keepalive.minInterval") {
		serverConfig.KaOpts.ServerMinInterval = viper.GetDuration("peer.keepalive.minInterval")
	}

	serverConfig.MaxRecvMsgSize = comm.DefaultMaxRecvMsgSize
	serverConfig.MaxSendMsgSize = comm.DefaultMaxSendMsgSize

	if viper.IsSet("peer.maxRecvMsgSize") {
		serverConfig.MaxRecvMsgSize = int(viper.GetInt32("peer.maxRecvMsgSize"))
	}
	if viper.IsSet("peer.maxSendMsgSize") {
		serverConfig.MaxSendMsgSize = int(viper.GetInt32("peer.maxSendMsgSize"))
	}
	return serverConfig, nil
}

// GetClientCertificate returns the TLS certificate to use for gRPC client
// connections
// 返回grpc 客户端的Tls证书
func GetClientCertificate() (tls.Certificate, error) {
	cert := tls.Certificate{}
	// 配置中获取
	keyPath := viper.GetString("peer.tls.clientKey.file")
	certPath := viper.GetString("peer.tls.clientCert.file")

	if keyPath != "" || certPath != "" {
		// need both keyPath and certPath to be set
		if keyPath == "" || certPath == "" {
			return cert, errors.New("peer.tls.clientKey.file and " +
				"peer.tls.clientCert.file must both be set or must both be empty")
		}
		keyPath = config.GetPath("peer.tls.clientKey.file")
		certPath = config.GetPath("peer.tls.clientCert.file")

	} else {
		// use the TLS server keypair
		keyPath = viper.GetString("peer.tls.key.file")
		certPath = viper.GetString("peer.tls.cert.file")

		if keyPath != "" || certPath != "" {
			// need both keyPath and certPath to be set
			if keyPath == "" || certPath == "" {
				return cert, errors.New("peer.tls.key.file and " +
					"peer.tls.cert.file must both be set or must both be empty")
			}
			keyPath = config.GetPath("peer.tls.key.file")
			certPath = config.GetPath("peer.tls.cert.file")
		} else {
			return cert, errors.New("must set either " +
				"[peer.tls.key.file and peer.tls.cert.file] or " +
				"[peer.tls.clientKey.file and peer.tls.clientCert.file]" +
				"when peer.tls.clientAuthEnabled is set to true")
		}
	}
	// get the keypair from the file system
	clientKey, err := ioutil.ReadFile(keyPath)
	if err != nil {
		return cert, errors.WithMessage(err,
			"error loading client TLS key")
	}
	clientCert, err := ioutil.ReadFile(certPath)
	if err != nil {
		return cert, errors.WithMessage(err,
			"error loading client TLS certificate")
	}
	cert, err = tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		return cert, errors.WithMessage(err,
			"error parsing client TLS key pair")
	}
	return cert, nil
}
