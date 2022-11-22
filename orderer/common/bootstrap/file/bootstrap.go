// Copyright IBM Corp. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package file

import (
	"io"
	"io/ioutil"
	"os"

	"github.com/golang/protobuf/proto"
	cb "github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/orderer/common/bootstrap"
	"github.com/pkg/errors"
)

//type fileBootstrapper struct {
//	GenesisBlockFile string
//}

// 文件引导
type fileBootstrapper struct {
	GenesisBlockFile string
}

// New returns a new static bootstrap helper.
// fileBootstrapper 实现了Helper接口的方法GenesisBlock
func New(fileName string) bootstrap.Helper {
	return &fileBootstrapper{
		GenesisBlockFile: fileName,
	}
}

// NewReplacer returns a new bootstrap replacer.
func NewReplacer(fileName string) bootstrap.Replacer {
	return &fileBootstrapper{
		GenesisBlockFile: fileName,
	}
}

// GenesisBlock returns the genesis block to be used for bootstrapping.
// 返回用于引导的创世块文件
func (b *fileBootstrapper) GenesisBlock() *cb.Block {
	// 读取创世块文件
	bootstrapFile, fileErr := ioutil.ReadFile(b.GenesisBlockFile)
	if fileErr != nil {
		panic(errors.Errorf("unable to bootstrap orderer. Error reading genesis block file: %v", fileErr))
	}
	// 创建一个区块结构
	genesisBlock := &cb.Block{}
	// 解析创世块内容到区块结构体
	unmarshallErr := proto.Unmarshal(bootstrapFile, genesisBlock)
	if unmarshallErr != nil {
		panic(errors.Errorf("unable to bootstrap orderer. Error unmarshalling genesis block: %v", unmarshallErr))
	}
	return genesisBlock
} // GenesisBlock

// ReplaceGenesisBlockFile creates a backup of the genesis block file, and then replaces
// it with the content of the given block.
// This is used during consensus-type migration in order to generate a bootstrap file that
// specifies the new consensus-type.

// 先备份创世块文件，然后用给定的区块内容替换之
// 这被用于在共识迁移期间为了生成一个引导文件，它被指定了新的共识类型
func (b *fileBootstrapper) ReplaceGenesisBlockFile(block *cb.Block) error {
	// 给定的区块进行编码
	buff, marshalErr := proto.Marshal(block)
	if marshalErr != nil {
		return errors.Wrap(marshalErr, "could not marshal block into a []byte")
	}
	// 读取创世块文件
	genFileStat, statErr := os.Stat(b.GenesisBlockFile)
	if statErr != nil {
		return errors.Wrapf(statErr, "could not get the os.Stat of the genesis block file: %s", b.GenesisBlockFile)
	}
	// 校验文件是否是一个普通文件
	if !genFileStat.Mode().IsRegular() {
		return errors.Errorf("genesis block file: %s, is not a regular file", b.GenesisBlockFile)
	}
	// 将原创世块文件备份
	backupFile := b.GenesisBlockFile + ".bak"
	if err := backupGenesisFile(b.GenesisBlockFile, backupFile); err != nil {
		return errors.Wrapf(err, "could not copy genesis block file (%s) into backup file: %s",
			b.GenesisBlockFile, backupFile)
	}
	// 将新区块写入原来的创世文件中，保持文件mode不变
	if err := ioutil.WriteFile(b.GenesisBlockFile, buff, genFileStat.Mode()); err != nil {
		return errors.Wrapf(err, "could not write new genesis block into file: %s; use backup if necessary: %s",
			b.GenesisBlockFile, backupFile)
	}

	return nil
}

func backupGenesisFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

// 在进行共识迁移之前对创世块文件进行检查
func (b *fileBootstrapper) CheckReadWrite() error {
	//文件状态获取
	genFileStat, statErr := os.Stat(b.GenesisBlockFile)
	if statErr != nil {
		return errors.Wrapf(statErr, "could not get the os.Stat of the genesis block file: %s", b.GenesisBlockFile)
	}
	// 判断 是否是一个普通文件
	if !genFileStat.Mode().IsRegular() {
		return errors.Errorf("genesis block file: %s, is not a regular file", b.GenesisBlockFile)
	}
	// 判断是否对创世块文件有读写权限
	genFile, openErr := os.OpenFile(b.GenesisBlockFile, os.O_RDWR, genFileStat.Mode().Perm())
	if openErr != nil {
		// 没有权限所致
		if os.IsPermission(openErr) {
			return errors.Wrapf(openErr, "genesis block file: %s, cannot be opened for read-write, check permissions", b.GenesisBlockFile)
		} else {
			return errors.Wrapf(openErr, "genesis block file: %s, cannot be opened for read-write", b.GenesisBlockFile)
		}
	}
	genFile.Close()

	return nil
}
