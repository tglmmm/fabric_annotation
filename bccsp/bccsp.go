/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

/*
定义了 Key 的结构信息，包含密钥所有信息和属性，密钥的字节形式，密钥的标识，是否对称加密，是否私钥是私钥，如果是非对称加密返回公钥密钥的结构信息
bsscp 兼容了密钥相关的所有选项，方法的接口{
	密钥生成 // 通过密钥生成选项生生密钥
	密钥派生 // 通过密钥派生选项，派生密钥
	密钥导入 // 通过密钥导入选项导入密钥
	通过SKI获取密钥结构信息 // 获取 Key
	计算消息hash // 通过hash选项 计算消息摘要
	获取hash函数 //通过hash选项 获取对应hash函数
	签名验签 // 对于非对称加密而言
	加密解密 // 对对称加密而言
}
*/

package bccsp

import (
	"crypto"
	"hash"
)

// Key represents a cryptographic key
type Key interface {

	// Bytes converts this key to its byte representation,
	// if this operation is allowed.
	Bytes() ([]byte, error) // 密钥转化为字节类型

	// SKI returns the subject key identifier of this key.
	SKI() []byte // 返回密钥的标识符 ECDSA...

	// Symmetric returns true if this key is a symmetric key,
	// false is this key is asymmetric
	Symmetric() bool // 判断是对称加密或者非对称加密

	// Private returns true if this key is a private key,
	// false otherwise.
	Private() bool // 如果是私钥则返回true

	// PublicKey returns the corresponding public key part of an asymmetric public/private key pair.
	// This method returns an error in symmetric key schemes.
	PublicKey() (Key, error) // 返回非对称加密密钥对的公钥，这个方法在对称加密中返回错误
}

// KeyGenOpts contains options for key-generation with a CSP.
// 密钥生成选项
// CSP 加密服务提供程序
type KeyGenOpts interface {

	// Algorithm returns the key generation algorithm identifier (to be used).
	Algorithm() string // 返回密钥的标识

	// Ephemeral returns true if the key to generate has to be ephemeral,
	// false otherwise.
	Ephemeral() bool // 是否为临时密钥
}

// KeyDerivOpts contains options for key-derivation with a CSP.
// 派生密钥选项
type KeyDerivOpts interface {

	// Algorithm returns the key derivation algorithm identifier (to be used).
	Algorithm() string

	// Ephemeral returns true if the key to derived has to be ephemeral,
	// false otherwise.
	Ephemeral() bool
}

// KeyImportOpts contains options for importing the raw material of a key with a CSP.
type KeyImportOpts interface {

	// Algorithm returns the key importation algorithm identifier (to be used).
	Algorithm() string

	// Ephemeral returns true if the key generated has to be ephemeral,
	// false otherwise.
	Ephemeral() bool
}

// HashOpts contains options for hashing with a CSP.

// hash算法
type HashOpts interface {

	// Algorithm returns the hash algorithm identifier (to be used).
	Algorithm() string // 返回算法标识
}

// SignerOpts contains options for signing with a CSP.
// 签名选项
type SignerOpts interface {
	crypto.SignerOpts
}

// EncrypterOpts contains options for encrypting with a CSP.
// 加密
type EncrypterOpts interface{}

// DecrypterOpts contains options for decrypting with a CSP.
// 解密
type DecrypterOpts interface{}

// BCCSP is the blockchain cryptographic service provider that offers
// the implementation of cryptographic standards and algorithms.
// BSSCP (blockchain cryptographic service provider)加密标准和算法实现
type BCCSP interface {

	// KeyGen generates a key using opts.
	KeyGen(opts KeyGenOpts) (k Key, err error)

	// KeyDeriv derives a key from k using opts.
	// The opts argument should be appropriate for the primitive used.
	// 通过一个密钥导出另外一个密钥
	KeyDeriv(k Key, opts KeyDerivOpts) (dk Key, err error)

	// KeyImport imports a key from its raw representation using opts.
	// The opts argument should be appropriate for the primitive used.
	// 导入密钥
	KeyImport(raw interface{}, opts KeyImportOpts) (k Key, err error)

	// GetKey returns the key this CSP associates to
	// the Subject Key Identifier ski.
	// 通过SKI标识获取对应的密钥
	GetKey(ski []byte) (k Key, err error)

	// Hash hashes messages msg using options opts.
	// If opts is nil, the default hash function will be used.
	// 使用选项对消息进行hash计算，如果hash选项为空则使用默认的hash函数计算
	Hash(msg []byte, opts HashOpts) (hash []byte, err error)

	// GetHash returns and instance of hash.Hash using options opts.
	// If opts is nil, the default hash function will be returned.
	// 通过hash选项返回hash函数实例，如果选项为空默认的hash函数返回
	GetHash(opts HashOpts) (h hash.Hash, err error)

	// Sign signs digest using key k.
	// The opts argument should be appropriate for the algorithm used.
	//
	// Note that when a signature of a hash of a larger message is needed,
	// the caller is responsible for hashing the larger message and passing
	// the hash (as digest).

	// 通过密钥进行签名，选项应该是一个合适算法被使用
	Sign(k Key, digest []byte, opts SignerOpts) (signature []byte, err error)

	// Verify verifies signature against key k and digest
	// The opts argument should be appropriate for the algorithm used.
	// 验证签名
	Verify(k Key, signature, digest []byte, opts SignerOpts) (valid bool, err error)

	// Encrypt encrypts plaintext using key k.
	// The opts argument should be appropriate for the algorithm used.
	// 加密明文
	Encrypt(k Key, plaintext []byte, opts EncrypterOpts) (ciphertext []byte, err error)

	// Decrypt decrypts ciphertext using key k.
	// The opts argument should be appropriate for the algorithm used.
	// 解密
	Decrypt(k Key, ciphertext []byte, opts DecrypterOpts) (plaintext []byte, err error)
}
