// Copyright IBM Corp. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package bootstrap

import (
	ab "github.com/hyperledger/fabric-protos-go/common"
)

// Helper defines the functions a bootstrapping implementation should provide.
// 定义引导提供的函数
type Helper interface {
	// GenesisBlock should return the genesis block required to bootstrap
	// the ledger (be it reading from the filesystem, generating it, etc.)
	// 从文件系统中获取到引导block
	GenesisBlock() *ab.Block
}

// Replacer provides the ability to to replace the current genesis block used for bootstrapping with the supplied block.
//It is used during consensus-type migration in order to replace the original genesis block used for
// bootstrapping with the latest config block of the system channel,

// which contains the new consensus-type. This will ensure the instantiation of the correct consenter type when the server restarts.

// 提供一个能力用于替换当前创世块，使用提供的块进行引导
// 它在 consensus-type期间使用 （共识迁移时候使用），以取代用于系统通道最后的配置区块引导
// 他将包含正确的共识类型，将实例化正确的共识在重启时候
type Replacer interface {
	// ReplaceGenesisBlockFile should first copy the current file to a backup
	// file: <genesis-file-name> => <genesis-file-name>.bak
	// and then overwrite the original file with the content of the given block.
	// If something goes wrong during migration, the original file could be
	// restored from the backup.
	// An error is returned if the operation was not completed successfully.
	// 首先应该拷贝当前的创世文件，重命名为.bak
	// 然后用给定block内容的块覆盖原始文件的内容
	// 如果迁移出现失败，原始的文件能够被重新从备份中恢复
	// 如果操作没有正确的完成，将返回一个错误
	ReplaceGenesisBlockFile(block *ab.Block) error

	// CheckReadWrite checks whether the current file is readable and writable,
	// because if it is not, there is no point in attempting to replace. This
	// check is performed at the beginning of the consensus-type migration
	// process.
	// An error is returned if the file is not readable and writable.
	// 检查当前文件是否可读可写，以为如果不是这样，将没有目标被替换，这个检查在共识迁移开始时候执行
	// 一个错误被返回，如果文件不能读写
	CheckReadWrite() error
}
