/*
Copyright IBM Corp. 2016 All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

		 http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package pkcs11

import (
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/hyperledger/fabric/bccsp"
)

/*
pkcs11 椭圆曲线算法

*/

type ecdsaPrivateKey struct {
	ski []byte
	pub ecdsaPublicKey
}

// Bytes converts this key to its byte representation,
// if this operation is allowed.
// 将 密钥转换成字节形式，如果操作被允许
func (k *ecdsaPrivateKey) Bytes() ([]byte, error) {
	return nil, errors.New("Not supported.")
}

// SKI returns the subject key identifier of this key.
// 返回 密钥标识
func (k *ecdsaPrivateKey) SKI() []byte {
	return k.ski
}

// Symmetric returns true if this key is a symmetric key,
// false if this key is asymmetric
// 是否对称加密
func (k *ecdsaPrivateKey) Symmetric() bool {
	return false
}

// Private returns true if this key is a private key,
// false otherwise.
// 是否是私钥
func (k *ecdsaPrivateKey) Private() bool {
	return true
}

// PublicKey returns the corresponding public key part of an asymmetric public/private key pair.
// This method returns an error in symmetric key schemes.
// 返回非对称公钥的对的公钥部分
// 方法在对称密钥中返回错误
func (k *ecdsaPrivateKey) PublicKey() (bccsp.Key, error) {
	return &k.pub, nil
}

type ecdsaPublicKey struct {
	ski []byte
	pub *ecdsa.PublicKey
}

// Bytes converts this key to its byte representation,
// if this operation is allowed.
func (k *ecdsaPublicKey) Bytes() (raw []byte, err error) {
	raw, err = x509.MarshalPKIXPublicKey(k.pub)
	if err != nil {
		return nil, fmt.Errorf("Failed marshalling key [%s]", err)
	}
	return
}

// SKI returns the subject key identifier of this key.
func (k *ecdsaPublicKey) SKI() []byte {
	return k.ski
}

// Symmetric returns true if this key is a symmetric key,
// false if this key is asymmetric
func (k *ecdsaPublicKey) Symmetric() bool {
	return false
}

// Private returns true if this key is a private key,
// false otherwise.
func (k *ecdsaPublicKey) Private() bool {
	return false
}

// PublicKey returns the corresponding public key part of an asymmetric public/private key pair.
// This method returns an error in symmetric key schemes.
func (k *ecdsaPublicKey) PublicKey() (bccsp.Key, error) {
	return k, nil
}
