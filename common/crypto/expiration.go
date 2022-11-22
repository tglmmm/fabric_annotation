/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-protos-go/msp"
	"github.com/pkg/errors"
)

// ExpiresAt returns when the given identity expires, or a zero time.Time
// in case we cannot determine that
func ExpiresAt(identityBytes []byte) time.Time {
	sId := &msp.SerializedIdentity{}
	// If protobuf parsing failed, we make no decisions about the expiration time
	if err := proto.Unmarshal(identityBytes, sId); err != nil {
		return time.Time{}
	}
	return certExpirationTime(sId.IdBytes)
}

func certExpirationTime(pemBytes []byte) time.Time {
	bl, _ := pem.Decode(pemBytes)
	if bl == nil {
		// 如果证书不是pem 块，就不做证书过期处理
		// If the identity isn't a PEM block, we make no decisions about the expiration time
		return time.Time{}
	}
	cert, err := x509.ParseCertificate(bl.Bytes)
	if err != nil {
		return time.Time{}
	}
	// 不能在那个时间点之后
	return cert.NotAfter
}

// MessageFunc notifies a message happened with the given format, and can be replaced with Warnf or Infof of a logger.
type MessageFunc func(format string, args ...interface{})

// Scheduler invokes f after d time, and can be replaced with time.AfterFunc.
type Scheduler func(d time.Duration, f func()) *time.Timer

// TrackExpiration warns a week before one of the certificates expires
// 证书过期前一周告警
func TrackExpiration(tls bool, serverCert []byte, clientCertChain [][]byte, sIDBytes []byte, info MessageFunc, warn MessageFunc, now time.Time, s Scheduler) {
	sID := &msp.SerializedIdentity{}
	if err := proto.Unmarshal(sIDBytes, sID); err != nil {
		return
	}

	trackCertExpiration(sID.IdBytes, "enrollment", info, warn, now, s)

	if !tls {
		return
	}
	// 跟踪服务端证书过期
	trackCertExpiration(serverCert, "server TLS", info, warn, now, s)

	if len(clientCertChain) == 0 || len(clientCertChain[0]) == 0 {
		return
	}

	trackCertExpiration(clientCertChain[0], "client TLS", info, warn, now, s)
}

func trackCertExpiration(rawCert []byte, certRole string, info MessageFunc, warn MessageFunc, now time.Time, sched Scheduler) {
	// 读取证书过期时间
	expirationTime := certExpirationTime(rawCert)
	if expirationTime.IsZero() {
		// 如果证书过期时间不能分类，就直接返回
		// If the certificate expiration time cannot be classified, return.
		return
	}
	// 离过期剩余时间
	timeLeftUntilExpiration := expirationTime.Sub(now)
	oneWeek := time.Hour * 24 * 7

	if timeLeftUntilExpiration < 0 {
		warn("The %s certificate has expired", certRole)
		return
	}

	info("The %s certificate will expire on %s", certRole, expirationTime)
	// 如果剩余过期时间小于一周
	if timeLeftUntilExpiration < oneWeek {
		days := timeLeftUntilExpiration / (time.Hour * 24)
		hours := (timeLeftUntilExpiration - (days * time.Hour * 24)) / time.Hour
		warn("The %s certificate expires within %d days and %d hours", certRole, days, hours)
		return
	}
	//
	timeLeftUntilOneWeekBeforeExpiration := timeLeftUntilExpiration - oneWeek
	// 多久以后开始告警
	sched(timeLeftUntilOneWeekBeforeExpiration, func() {
		warn("The %s certificate will expire within one week", certRole)
	})
}

// ErrPubKeyMismatch is used by CertificatesWithSamePublicKey to indicate the two public keys mismatch
var ErrPubKeyMismatch = errors.New("public keys do not match")

// LogNonPubKeyMismatchErr logs an error which is not an ErrPubKeyMismatch error
func LogNonPubKeyMismatchErr(log func(template string, args ...interface{}), err error, cert1DER, cert2DER []byte) {
	cert1PEM := &pem.Block{Type: "CERTIFICATE", Bytes: cert1DER}
	cert2PEM := &pem.Block{Type: "CERTIFICATE", Bytes: cert2DER}
	log("Failed determining if public key of %s matches public key of %s: %s",
		string(pem.EncodeToMemory(cert1PEM)),
		string(pem.EncodeToMemory(cert2PEM)),
		err)
}

// CertificatesWithSamePublicKey returns nil if both byte slices are valid DER encoding of certificates with the same public key.
// 如果两个切片都是具有相同公钥的DER编码的证书则返回nil,否则返回错误
func CertificatesWithSamePublicKey(der1, der2 []byte) error {
	//
	cert1canonized, err := publicKeyFromCertificate(der1)
	if err != nil {
		return err
	}

	cert2canonized, err := publicKeyFromCertificate(der2)
	if err != nil {
		return err
	}

	if bytes.Equal(cert1canonized, cert2canonized) {
		return nil
	}
	return ErrPubKeyMismatch
}

// publicKeyFromCertificate returns the public key of the given ASN1 DER certificate.
func publicKeyFromCertificate(der []byte) ([]byte, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return x509.MarshalPKIXPublicKey(cert.PublicKey)
}
