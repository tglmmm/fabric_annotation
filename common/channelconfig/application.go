/*
Copyright IBM Corp. 2017 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package channelconfig

import (
	cb "github.com/hyperledger/fabric-protos-go/common"
	pb "github.com/hyperledger/fabric-protos-go/peer"
	"github.com/hyperledger/fabric/common/capabilities"
	"github.com/pkg/errors"
)

const (
	// ApplicationGroupKey is the group name for the Application config
	// 通道配置中应用程序部分组的名称
	ApplicationGroupKey = "Application"

	// ACLsKey is the name of the ACLs config
	ACLsKey = "ACLs"
)

// ApplicationProtos is used as the source of the ApplicationConfig
// 被作为应用程序配置的一部分
type ApplicationProtos struct {
	ACLs         *pb.ACLs
	Capabilities *cb.Capabilities
}

// ApplicationConfig implements the Application interface
// 实现一个应用程序接口
type ApplicationConfig struct {
	applicationOrgs map[string]ApplicationOrg
	protos          *ApplicationProtos
}

// NewApplicationConfig creates config from an Application config group
//
func NewApplicationConfig(appGroup *cb.ConfigGroup, mspConfig *MSPConfigHandler) (*ApplicationConfig, error) {
	ac := &ApplicationConfig{
		applicationOrgs: make(map[string]ApplicationOrg),
		protos:          &ApplicationProtos{},
	}
	// 反序列化组中的原型值
	if err := DeserializeProtoValuesFromGroup(appGroup, ac.protos); err != nil {
		return nil, errors.Wrap(err, "failed to deserialize values")
	}

	// 应用通道中ACL功能没有被指定
	if !ac.Capabilities().ACLs() {
		// 配置中如果存在ACL的部分是没有必要的，因为通道没有启用ACL的功能，就不需要ACL值存在
		if _, ok := appGroup.Values[ACLsKey]; ok {
			// 没有所需的功能不需要指定ACLs
			return nil, errors.New("ACLs may not be specified without the required capability")
		}
	}

	var err error
	for orgName, orgGroup := range appGroup.Groups {
		ac.applicationOrgs[orgName], err = NewApplicationOrgConfig(orgName, orgGroup, mspConfig)
		if err != nil {
			return nil, err
		}
	}

	return ac, nil
}

// Organizations returns a map of org ID to ApplicationOrg
func (ac *ApplicationConfig) Organizations() map[string]ApplicationOrg {
	return ac.applicationOrgs
}

// Capabilities returns a map of capability name to Capability
func (ac *ApplicationConfig) Capabilities() ApplicationCapabilities {
	return capabilities.NewApplicationProvider(ac.protos.Capabilities.Capabilities)
}

// APIPolicyMapper returns a PolicyMapper that maps API names to policies
func (ac *ApplicationConfig) APIPolicyMapper() PolicyMapper {
	pm := newAPIsProvider(ac.protos.ACLs.Acls)

	return pm
}
