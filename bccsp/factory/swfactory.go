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

/*
基于软件的 BCCSP 工厂 ，SW
1. SWFactory 两个方法，获取工厂名称，获取 BCCSP实例

*/

package factory

import (
	"github.com/hyperledger/fabric/bccsp"
	"github.com/hyperledger/fabric/bccsp/sw"
	"github.com/pkg/errors"
)

const (
	// SoftwareBasedFactoryName is the name of the factory of the software-based BCCSP implementation
	// 基于软件 BCCSP 的实现工厂的名称
	SoftwareBasedFactoryName = "SW"
)

// SWFactory is the factory of the software-based BCCSP.
// 基于软件的BCCSP工厂
type SWFactory struct{}

// Name returns the name of this factory
func (f *SWFactory) Name() string {
	return SoftwareBasedFactoryName
}

// Get returns an instance of BCCSP using Opts.
// 返回 BSSCP实例的选项

func (f *SWFactory) Get(config *FactoryOpts) (bccsp.BCCSP, error) {
	// 参数有效性验证
	if config == nil || config.SW == nil {
		return nil, errors.New("Invalid config. It must not be nil.")
	}

	swOpts := config.SW
	// 根据选项创建一个key store，临时或者，长期
	var ks bccsp.KeyStore
	switch {
	// 如果 指向路径不为nil，则根据这个keyStorePath生成
	case swOpts.FileKeystore != nil:
		// 初始化 文件目录，以及key store 打开状态
		// 默认情况下 ， 没有密码，且不是只读
		fks, err := sw.NewFileBasedKeyStore(nil, swOpts.FileKeystore.KeyStorePath, false)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to initialize software key store")
		}
		ks = fks
	default:
		// Default to ephemeral key store
		// 默认创建一个临时的模型的key store，不加载也不存储密钥
		//fmt.Println("创建一个临时的密钥存储")
		ks = sw.NewDummyKeyStore()
	}
	return sw.NewWithParams(swOpts.Security, swOpts.Hash, ks)
}

// SwOpts contains options for the SWFactory

// SwOpts 包含 SWFacroty 的一些选项
type SwOpts struct {
	// Default algorithms when not specified (Deprecated?)
	Security     int               `json:"security" yaml:"Security"`
	Hash         string            `json:"hash" yaml:"Hash"`
	FileKeystore *FileKeystoreOpts `json:"filekeystore,omitempty" yaml:"FileKeyStore,omitempty"`
}

// Pluggable Keystores, could add JKS, P12, etc..
type FileKeystoreOpts struct {
	KeyStorePath string `yaml:"KeyStore"`
}
