/*
Copyright IBM Corp. 2017 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// 负责 验证 创建 保存  关闭 连接
// 这些连接是结群内的成员之间通讯使用
package cluster

import (
	"crypto/x509"
	"sync"

	"github.com/hyperledger/fabric/common/crypto"
	"github.com/hyperledger/fabric/common/metrics"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

// RemoteVerifier verifies the connection to the remote host
// 验证远程主机连接
type RemoteVerifier func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error

//go:generate mockery -dir . -name SecureDialer -case underscore -output ./mocks/

// SecureDialer connects to a remote address
// 安全的连接
type SecureDialer interface {
	Dial(address string, verifyFunc RemoteVerifier) (*grpc.ClientConn, error)
}

// ConnectionMapper maps certificates to connections
// 证书与连接的映射
type ConnectionMapper interface {
	Lookup(cert []byte) (*grpc.ClientConn, bool)
	Put(cert []byte, conn *grpc.ClientConn)
	Remove(cert []byte)
	Size() int
}

// ConnectionStore stores connections to remote nodes
// 存储与远程节点的连接
type ConnectionStore struct {
	lock        sync.RWMutex
	Connections ConnectionMapper
	dialer      SecureDialer
}

// NewConnectionStore creates a new ConnectionStore with the given SecureDialer
// 创建一个新的 连接存储结构
func NewConnectionStore(dialer SecureDialer, tlsConnectionCount metrics.Gauge) *ConnectionStore {
	connMapping := &ConnectionStore{
		Connections: &connMapperReporter{
			ConnectionMapper:          make(ConnByCertMap),
			tlsConnectionCountMetrics: tlsConnectionCount,
		},
		dialer: dialer,
	}
	return connMapping
}

// verifyHandshake returns a predicate that verifies that the remote node authenticates
// itself with the given TLS certificate
// 返回一个用于验证远程节点的方法
func (c *ConnectionStore) verifyHandshake(endpoint string, certificate []byte) RemoteVerifier {
	//
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		// 两个证书的公钥相同即可，理解是：私钥和公钥是对应的，证书是在公钥的基础上加了CA签名的摘要
		err := crypto.CertificatesWithSamePublicKey(certificate, rawCerts[0])
		if err == nil {
			return nil
		}
		return errors.Errorf("public key of server certificate presented by %s doesn't match the expected public key",
			endpoint)
	}
}

// Disconnect closes the gRPC connection that is mapped to the given certificate
// 关闭给定证书映射的GRPC连接
func (c *ConnectionStore) Disconnect(expectedServerCert []byte) {
	c.lock.Lock()
	defer c.lock.Unlock()
	// 从map读取
	conn, connected := c.Connections.Lookup(expectedServerCert)
	// 已经关闭则返回
	if !connected {
		return
	}
	conn.Close()
	// 从map出删除
	c.Connections.Remove(expectedServerCert)
}

// Connection obtains a connection to the given endpoint and expects the given server certificate to be presented by the remote node
// 从给定的端点获得一个连接， 并且期望给定的服务证书被远程节点提供
func (c *ConnectionStore) Connection(endpoint string, expectedServerCert []byte) (*grpc.ClientConn, error) {
	c.lock.RLock()
	//
	conn, alreadyConnected := c.Connections.Lookup(expectedServerCert)
	c.lock.RUnlock()
	// 查看是否连接，如果已经存在就直接返回连接，
	if alreadyConnected {
		return conn, nil
	}

	// Else, we need to connect to the remote endpoint
	// 如果连接不存在我们需要建立远程连接
	return c.connect(endpoint, expectedServerCert)
}

// connect connects to the given endpoint and expects the given TLS server certificate
// to be presented at the time of authentication
// 通过给定的节点和期望的TLS服务证书 连接节点
func (c *ConnectionStore) connect(endpoint string, expectedServerCert []byte) (*grpc.ClientConn, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	// Check again to see if some other goroutine has already connected while
	// we were waiting on the lock
	// 再一次检查，如果一些线程已经连接，我们需要等待锁的释放
	conn, alreadyConnected := c.Connections.Lookup(expectedServerCert)
	if alreadyConnected {
		return conn, nil
	}
	// 验证握手（证书校验）
	v := c.verifyHandshake(endpoint, expectedServerCert)
	// 拨号
	conn, err := c.dialer.Dial(endpoint, v)
	if err != nil {
		return nil, err
	}
	// 将证书和连接加入到对应的map
	c.Connections.Put(expectedServerCert, conn)
	return conn, nil
}

type connMapperReporter struct {
	tlsConnectionCountMetrics metrics.Gauge
	ConnectionMapper
}

func (cmg *connMapperReporter) Put(cert []byte, conn *grpc.ClientConn) {
	cmg.ConnectionMapper.Put(cert, conn)
	cmg.reportSize()
}

func (cmg *connMapperReporter) Remove(cert []byte) {
	cmg.ConnectionMapper.Remove(cert)
	cmg.reportSize()
}

func (cmg *connMapperReporter) reportSize() {
	cmg.tlsConnectionCountMetrics.Set(float64(cmg.ConnectionMapper.Size()))
}
