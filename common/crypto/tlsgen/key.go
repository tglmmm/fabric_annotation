/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tlsgen

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"time"

	"github.com/pkg/errors"
)

// 新生成ECDSA 私钥
func newPrivKey() (*ecdsa.PrivateKey, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	// 编码程PKCS8
	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, privBytes, nil
}

// 证书模板
func newCertTemplate() (x509.Certificate, error) {
	// Lsh 1 << 128
	sn, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return x509.Certificate{}, err
	}
	return x509.Certificate{
		Subject:      pkix.Name{SerialNumber: sn.String()},
		NotBefore:    time.Now().Add(time.Hour * (-24)),
		NotAfter:     time.Now().Add(time.Hour * 24),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature, // 加密 / 签名
		SerialNumber: sn,
	}, nil
}

// 新生成一个密钥对
func newCertKeyPair(isCA bool, isServer bool, certSigner crypto.Signer, parent *x509.Certificate, hosts ...string) (*CertKeyPair, error) {
	// 新创建一个私钥
	privateKey, privBytes, err := newPrivKey()
	if err != nil {
		return nil, err
	}
	// 新建证书模板
	template, err := newCertTemplate()
	if err != nil {
		return nil, err
	}
	// 十年后
	tenYearsFromNow := time.Now().Add(time.Hour * 24 * 365 * 10)
	if isCA {
		// 如果证书是CA证书
		template.NotAfter = tenYearsFromNow
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign | x509.KeyUsageCRLSign // 证书签名或者CRL签名
		template.ExtKeyUsage = []x509.ExtKeyUsage{                        // 扩展操作集合
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		}
		template.BasicConstraintsValid = true
	} else {
		// 不是根证书
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	// 用于服务端认证
	if isServer {
		template.NotAfter = tenYearsFromNow
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		// 设置证书 IP Address / DNSNames
		for _, host := range hosts {
			if ip := net.ParseIP(host); ip != nil {
				template.IPAddresses = append(template.IPAddresses, ip)
			} else {
				template.DNSNames = append(template.DNSNames, host)
			}
		}
	}
	// 计算 SKI
	template.SubjectKeyId = computeSKI(&privateKey.PublicKey)
	// If no parent cert, it's a self signed cert
	// 如果没有父证书那么他就是一个自签的证书
	if parent == nil || certSigner == nil {
		parent = &template
		certSigner = privateKey
	}
	// 创建证书
	rawBytes, err := x509.CreateCertificate(rand.Reader, &template, parent, &privateKey.PublicKey, certSigner)
	if err != nil {
		return nil, err
	}
	// PEM编码的证书
	pubKey := encodePEM("CERTIFICATE", rawBytes)

	block, _ := pem.Decode(pubKey)
	if block == nil { // Never comes unless x509 or pem has bug
		return nil, errors.Errorf("%s: wrong PEM encoding", pubKey)
	}
	// 解析证书
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	privKey := encodePEM("EC PRIVATE KEY", privBytes)
	return &CertKeyPair{
		Key:     privKey,
		Cert:    pubKey,
		Signer:  privateKey,
		TLSCert: cert,
	}, nil
}

// pem编码
func encodePEM(keyType string, data []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: keyType, Bytes: data})
}

// RFC 7093, Section 2, Method 4
// 根据公钥计算SKI
func computeSKI(key *ecdsa.PublicKey) []byte {
	raw := elliptic.Marshal(key.Curve, key.X, key.Y)
	hash := sha256.Sum256(raw)
	return hash[:]
}
