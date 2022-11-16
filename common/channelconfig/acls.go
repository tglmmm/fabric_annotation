/*
Copyright State Street Corp. 2018 All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package channelconfig

import (
	pb "github.com/hyperledger/fabric-protos-go/peer"
)

// aclsProvider provides mappings for resource to policy names
// 提供资源到策略名称的映射
type aclsProvider struct {
	aclPolicyRefs map[string]string
}

// 根据名称返回相应的策略
func (ag *aclsProvider) PolicyRefForAPI(aclName string) string {
	return ag.aclPolicyRefs[aclName]
}

// this translates policies to absolute paths if needed
// 将策略转换为绝对路径
func newAPIsProvider(acls map[string]*pb.APIResource) *aclsProvider {
	aclPolicyRefs := make(map[string]string)

	for key, acl := range acls {
		if len(acl.PolicyRef) == 0 {
			logger.Warningf("Policy reference for resource '%s' is specified, but empty, falling back to default", key)
			continue
		}
		// If the policy is fully qualified, ie to /Channel/Application/Readers leave it alone
		// otherwise, make it fully qualified referring to /Channel/Application/policyName
		// 如果是绝对路径就不进行任何操作， 如果不是则需要转换
		if acl.PolicyRef[0] != '/' {
			aclPolicyRefs[key] = "/" + ChannelGroupKey + "/" + ApplicationGroupKey + "/" + acl.PolicyRef
		} else {
			aclPolicyRefs[key] = acl.PolicyRef
		}
	}

	return &aclsProvider{
		aclPolicyRefs: aclPolicyRefs,
	}
}
