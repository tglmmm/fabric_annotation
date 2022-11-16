/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

/*
1.包含一个默默人的BCCSP 变量，还有一个临时使用的bootBCCSP，只能被初始化同步一次
2.BCCSP工厂接口，工厂名称方法，工厂对应BCCSP实例
3.
*/

package factory

import (
	"sync"

	"github.com/hyperledger/fabric/bccsp"
	"github.com/hyperledger/fabric/common/flogging"
	"github.com/pkg/errors"
)

var (
	defaultBCCSP       bccsp.BCCSP // default BCCSP
	factoriesInitOnce  sync.Once   // factories' Sync on Initialization
	factoriesInitError error       // Factories' Initialization Error

	// when InitFactories has not been called yet (should only happen
	// in test cases), use this BCCSP temporarily
	// 如果 InitFactories 没有被调用，bootBCCSP 临时使用，一般发生在测试案例中
	bootBCCSP         bccsp.BCCSP
	bootBCCSPInitOnce sync.Once

	logger = flogging.MustGetLogger("bccsp")
)

// BCCSPFactory is used to get instances of the BCCSP interface.
// A Factory has name used to address it.
// 被用来获取 BCCSP 接口的一个 工厂接口，
type BCCSPFactory interface {

	// Name returns the name of this factory
	Name() string

	// Get returns an instance of BCCSP using opts.
	Get(opts *FactoryOpts) (bccsp.BCCSP, error)
}

// GetDefault returns a non-ephemeral (long-term) BCCSP
// 返回一个长期有效的BCCSP实例
func GetDefault() bccsp.BCCSP {

	// InitFactories 没有被调用
	if defaultBCCSP == nil {
		logger.Debug("Before using BCCSP, please call InitFactories(). Falling back to bootBCCSP.")
		// 如果defaultBCCSP 是Nil则初始化一次
		bootBCCSPInitOnce.Do(func() {
			var err error
			// 根据默认的选项，获取一个bccsp
			bootBCCSP, err = (&SWFactory{}).Get(GetDefaultOpts())
			if err != nil {
				panic("BCCSP Internal error, failed initialization with GetDefaultOpts!")
			}
		})
		return bootBCCSP
	}
	return defaultBCCSP
}

// 初始化 BCCSP
func initBCCSP(f BCCSPFactory, config *FactoryOpts) (bccsp.BCCSP, error) {

	csp, err := f.Get(config)
	if err != nil {
		return nil, errors.Errorf("Could not initialize BCCSP %s [%s]", f.Name(), err)
	}
	return csp, nil
}
