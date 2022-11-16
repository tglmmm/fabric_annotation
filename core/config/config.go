/*
Copyright Greg Haskins <gregory.haskins@gmail.com> 2017, All Rights Reserved.
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.IsDir()
}

func AddConfigPath(v *viper.Viper, p string) {
	if v != nil {
		// viper添加一个查找配置文件的路径
		v.AddConfigPath(p)
	} else {
		viper.AddConfigPath(p)
	}
}

//----------------------------------------------------------------------------------
// TranslatePath()
//----------------------------------------------------------------------------------
// Translates a relative path into a fully qualified path relative to the config
// file that specified it.  Absolute paths are passed unscathed.
//----------------------------------------------------------------------------------
func TranslatePath(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}

	return filepath.Join(base, p)
}

//----------------------------------------------------------------------------------
// TranslatePathInPlace()
//----------------------------------------------------------------------------------
// Translates a relative path into a fully qualified path in-place (updating the
// pointer) relative to the config file that specified it.  Absolute paths are
// passed unscathed.
//----------------------------------------------------------------------------------
func TranslatePathInPlace(base string, p *string) {
	*p = TranslatePath(base, *p)
}

//----------------------------------------------------------------------------------
// GetPath()
//----------------------------------------------------------------------------------
// GetPath allows configuration strings that specify a (config-file) relative path
//
// For example: Assume our config is located in /etc/hyperledger/fabric/core.yaml with
// a key "msp.configPath" = "msp/config.yaml".
//
// This function will return:
//      GetPath("msp.configPath") -> /etc/hyperledger/fabric/msp/config.yaml
//
//----------------------------------------------------------------------------------
func GetPath(key string) string {
	p := viper.GetString(key)
	if p == "" {
		return ""
	}
	// 配置文件core 所在目录下
	return TranslatePath(filepath.Dir(viper.ConfigFileUsed()), p)
}

const OfficialPath = "/etc/hyperledger/fabric"

//----------------------------------------------------------------------------------
// InitViper()
//----------------------------------------------------------------------------------
// Performs basic initialization of our viper-based configuration layer.
// Primary thrust is to establish the paths that should be consulted to find
// the configuration we need.  If v == nil, we will initialize the global
// Viper instance
//----------------------------------------------------------------------------------

// 执行基于viper基本配置层的初始化
func InitViper(v *viper.Viper, configName string) error {
	// 从环境变量中获取 FABRIC_CFG_PATH 信息
	altPath := os.Getenv("FABRIC_CFG_PATH")
	// 如果配置信息在环境变量中存在
	if altPath != "" {
		// If the user has overridden the path with an envvar, its the only path
		// we will consider

		if !dirExists(altPath) {
			return fmt.Errorf("FABRIC_CFG_PATH %s does not exist", altPath)
		}

		AddConfigPath(v, altPath)
	} else {
		// If we get here, we should use the default paths in priority order:
		//
		// *) CWD
		// *) /etc/hyperledger/fabric

		// CWD
		AddConfigPath(v, "./")

		// And finally, the official path
		// OfficialPath 是默认的路径
		if dirExists(OfficialPath) {
			AddConfigPath(v, OfficialPath)
		}
	}

	// Now set the configuration file.
	// 设置配置文件名称core
	if v != nil {
		v.SetConfigName(configName)
	} else {
		viper.SetConfigName(configName)
	}

	return nil
}