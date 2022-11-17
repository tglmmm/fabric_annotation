/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package deliver

import (
	"bytes"
	"context"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric/common/util"
	"github.com/pkg/errors"
)

// BindingInspector receives as parameters a gRPC context and an Envelope,
// and verifies whether the message contains an appropriate binding to the context
// 绑定检查
type BindingInspector func(context.Context, proto.Message) error

// CertHashExtractor extracts a certificate from a proto.Message message
// 从grpc消息中提取证书hash
type CertHashExtractor func(proto.Message) []byte

// NewBindingInspector returns a BindingInspector according to whether
// mutualTLS is configured or not, and according to a function that extracts
// TLS certificate hashes from proto messages

// TLS绑定检查
func NewBindingInspector(mutualTLS bool, extractTLSCertHash CertHashExtractor) BindingInspector {
	if extractTLSCertHash == nil {
		panic(errors.New("extractTLSCertHash parameter is nil"))
	}
	// 验证消息中获取的TLS证书的 hash与 GRPC 上下文中解析出TLS证书计算出的hash是否一致，不一致返回错误
	inspectMessage := mutualTLSBinding
	// 不绑定
	if !mutualTLS {
		inspectMessage = noopBinding
	}
	return func(ctx context.Context, msg proto.Message) error {
		if msg == nil {
			return errors.New("message is nil")
		}
		return inspectMessage(ctx, extractTLSCertHash(msg))
	}
}

// mutualTLSBinding enforces the client to send its TLS cert hash in the message,
// and then compares it to the computed hash that is derived
// from the gRPC context.
// In case they don't match, or the cert hash is missing from the request or
// there is no TLS certificate to be excavated from the gRPC context,
// an error is returned.
// TLS 双向绑定
// 强制客户端发送他证书的hash再消息中
// 然后将其与计算出来的hash进行比较 （这些信息都在GRPC上下文中获取）
// 如果hash不匹配,或者请求中证书hash丢失，或者从 GRPC上下文中并没有找到TLS证书都会返回错误
func mutualTLSBinding(ctx context.Context, claimedTLScertHash []byte) error {
	// 消息中提供的证书hash
	if len(claimedTLScertHash) == 0 {
		return errors.Errorf("client didn't include its TLS cert hash")
	}
	// 从grpc 上下文中提取TLS证书,在计算出证书hash
	actualTLScertHash := util.ExtractCertificateHashFromContext(ctx)
	if len(actualTLScertHash) == 0 {
		return errors.Errorf("client didn't send a TLS certificate")
	}
	// 如果证书hash不相等标识验证失败
	if !bytes.Equal(actualTLScertHash, claimedTLScertHash) {
		return errors.Errorf("claimed TLS cert hash is %v but actual TLS cert hash is %v", claimedTLScertHash, actualTLScertHash)
	}
	return nil
}

// noopBinding is a BindingInspector that always returns nil
func noopBinding(_ context.Context, _ []byte) error {
	return nil
}
