/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

/*
关于 PKCS11标准的工厂选项

*/

package pkcs11

import "time"

const (
	defaultCreateSessionRetries    = 10
	defaultCreateSessionRetryDelay = 100 * time.Millisecond
	defaultSessionCacheSize        = 10
)

//PKCS #11 为加密设备定义了一个技术独立的（technology-independent ）编程接口，称之为 Cryptoki。比如智能卡、PCMCIA卡这种加密设备
// PKCS11Opts contains options for the P11Factory
// 包含 pkcs11 工厂选项
type PKCS11Opts struct {
	// Default algorithms when not specified (Deprecated?)
	Security int    `json:"security"`
	Hash     string `json:"hash"`

	// PKCS11 options ，关于pkcs11 标准的选项
	Library        string         `json:"library"`
	Label          string         `json:"label"`
	Pin            string         `json:"pin"`
	SoftwareVerify bool           `json:"softwareverify,omitempty"`
	Immutable      bool           `json:"immutable,omitempty"`
	AltID          string         `json:"altid,omitempty"`
	KeyIDs         []KeyIDMapping `json:"keyids,omitempty" mapstructure:"keyids"`

	sessionCacheSize        int
	createSessionRetries    int
	createSessionRetryDelay time.Duration
}

// A KeyIDMapping associates the CKA_ID attribute of a cryptoki object with a
// subject key identifer.
type KeyIDMapping struct {
	SKI string `json:"ski,omitempty"`
	ID  string `json:"id,omitempty"`
}
