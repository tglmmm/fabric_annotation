/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package chaincode

import (
	"sync"

	"github.com/hyperledger/fabric-protos-go/gossip"
	"github.com/hyperledger/fabric-protos-go/peer"
)

// InstalledChaincode defines metadata about an installed chaincode
// 定义链码安装 元数据
type InstalledChaincode struct {
	PackageID string // 包ID
	Hash      []byte // 包 hash
	Label     string // LABEL 一般是 namespace_version
	// References is a map of channel name to chaincode metadata.
	//This represents the channels and chaincode definitions that use this installed chaincode package.
	// 通道名称到链码元数据的映射，它代表通道和链码和链码定义使用了已安装的链码包
	References map[string][]*Metadata

	// FIXME: we should remove these two
	// fields since they are not properties
	// of the chaincode (FAB-14561)
	Name    string
	Version string
}

// Metadata defines channel-scoped metadata of a chaincode
// 为链码定义通道域元数据
type Metadata struct {
	Name    string
	Version string
	Policy  []byte
	// CollectionPolicies will only be set for _lifecycle
	// chaincodes and stores a map from collection name to
	// that collection's endorsement policy if one exists.
	CollectionPolicies map[string][]byte
	Id                 []byte
	CollectionsConfig  *peer.CollectionConfigPackage
	// These two fields (Approved, Installed) are only set for
	// _lifecycle chaincodes. They are used to ensure service
	// discovery doesn't publish a stale chaincode definition
	// when the _lifecycle definition exists but has not yet
	// been installed or approved by the peer's org.
	// 被批准
	Approved bool
	// 被安装
	Installed bool
}

// MetadataSet defines an aggregation of Metadata
// 元数据聚合
type MetadataSet []Metadata

// AsChaincodes converts this MetadataSet to a slice of gossip.Chaincodes
// 将聚合元数据转换成 切片类型的 gossip.Chaincode
func (ccs MetadataSet) AsChaincodes() []*gossip.Chaincode {
	var res []*gossip.Chaincode
	for _, cc := range ccs {
		res = append(res, &gossip.Chaincode{
			Name:    cc.Name,
			Version: cc.Version,
		})
	}
	return res
}

// MetadataMapping defines a mapping from chaincode name to Metadata
// 定义一个 chaincodeName => metdata的映射
type MetadataMapping struct {
	sync.RWMutex                     // 读写锁
	mdByName     map[string]Metadata //chaincodeName => metdata的映射
}

// NewMetadataMapping creates a new metadata mapping
func NewMetadataMapping() *MetadataMapping {
	return &MetadataMapping{
		mdByName: make(map[string]Metadata),
	}
}

// Lookup returns the Metadata that is associated with the given chaincode
func (m *MetadataMapping) Lookup(cc string) (Metadata, bool) {
	m.RLock()
	defer m.RUnlock()
	md, exists := m.mdByName[cc]
	return md, exists
}

// Update updates the chaincode metadata in the mapping
// 更新元数据
func (m *MetadataMapping) Update(ccMd Metadata) {
	m.Lock()
	defer m.Unlock()
	m.mdByName[ccMd.Name] = ccMd
}

// Aggregate aggregates all Metadata to a MetadataSet
// 聚合所有的元数据到集合中
func (m *MetadataMapping) Aggregate() MetadataSet {
	m.RLock()
	defer m.RUnlock()
	var set MetadataSet
	for _, md := range m.mdByName {
		set = append(set, md)
	}
	return set
}
