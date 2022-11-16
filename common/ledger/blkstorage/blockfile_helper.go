/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package blkstorage

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/internal/fileutil"
	"github.com/pkg/errors"
)

// constructBlockfilesInfo scans the last blockfile (if any) and construct the blockfilesInfo
// if the last file contains no block or only a partially written block (potentially because of a crash while writing block to the file),
// this scans the second last file (if any)
// 通过最后一个区块文件构造区块文件信息
// 如果最后一个文件不包含块，或者只包含写入的部分块（有可能在写入时候产生崩溃）
// 这将扫面倒数第二个文件，如果有的话
func constructBlockfilesInfo(rootDir string) (*blockfilesInfo, error) {
	logger.Debugf("constructing BlockfilesInfo")
	var lastFileNum int
	var numBlocksInFile int
	var endOffsetLastBlock int64
	var lastBlockNumber uint64

	var lastBlockBytes []byte
	var lastBlock *common.Block
	var err error

	// 检索到目录下的最后一个区块文件返回文件编号（筛选出最大的文件号）
	if lastFileNum, err = retrieveLastFileSuffix(rootDir); err != nil {
		return nil, err
	}
	logger.Debugf("Last file number found = %d", lastFileNum)
	// 如果文件号-1，没有找到区块文件
	if lastFileNum == -1 {
		blkfilesInfo := &blockfilesInfo{
			latestFileNumber:   0,
			latestFileSize:     0,
			noBlockFiles:       true,
			lastPersistedBlock: 0,
		}
		logger.Debugf("No block file found")
		return blkfilesInfo, nil
	}

	// 找到最后一个区块文件的情况

	// 查询最后一个区块文件描述信息
	fileInfo := getFileInfoOrPanic(rootDir, lastFileNum)
	logger.Debugf("Last Block file info: FileName=[%s], FileSize=[%d]", fileInfo.Name(), fileInfo.Size())
	//扫描区块文件中的区块信息，返回最后一个区块,最后一个区块偏移量， 文件中区块数量
	//此处默认的开始偏移量是0
	if lastBlockBytes, endOffsetLastBlock, numBlocksInFile, err = scanForLastCompleteBlock(rootDir, lastFileNum, 0); err != nil {
		logger.Errorf("Error scanning last file [num=%d]: %s", lastFileNum, err)
		return nil, err
	}

	if numBlocksInFile == 0 && lastFileNum > 0 {
		secondLastFileNum := lastFileNum - 1
		fileInfo := getFileInfoOrPanic(rootDir, secondLastFileNum)
		logger.Debugf("Second last Block file info: FileName=[%s], FileSize=[%d]", fileInfo.Name(), fileInfo.Size())
		// 获取到文件中最后一个已经完成的区块的内容，从文件偏移量为0开始扫扫描整个文件
		if lastBlockBytes, _, _, err = scanForLastCompleteBlock(rootDir, secondLastFileNum, 0); err != nil {
			logger.Errorf("Error scanning second last file [num=%d]: %s", secondLastFileNum, err)
			return nil, err
		}
	}

	if lastBlockBytes != nil {
		// 反序列化区块中的信息
		if lastBlock, err = deserializeBlock(lastBlockBytes); err != nil {
			logger.Errorf("Error deserializing last block: %s. Block bytes length: %d", err, len(lastBlockBytes))
			return nil, err
		}
		lastBlockNumber = lastBlock.Header.Number
	}

	blkfilesInfo := &blockfilesInfo{
		lastPersistedBlock: lastBlockNumber,
		latestFileSize:     int(endOffsetLastBlock),
		latestFileNumber:   lastFileNum,
		noBlockFiles:       lastFileNum == 0 && numBlocksInFile == 0,
	}
	logger.Debugf("blockfilesInfo constructed from file system = %s", spew.Sdump(blkfilesInfo))
	return blkfilesInfo, nil
}

// binarySearchFileNumForBlock locates the file number that contains the given block number.
// This function assumes that the caller invokes this function with a block number that has been committed
// For any uncommitted block, this function returns the last file present
func binarySearchFileNumForBlock(rootDir string, blockNum uint64) (int, error) {
	blkfilesInfo, err := constructBlockfilesInfo(rootDir)
	if err != nil {
		return -1, err
	}

	beginFile := 0
	endFile := blkfilesInfo.latestFileNumber

	for endFile != beginFile {
		searchFile := beginFile + (endFile-beginFile)/2 + 1
		n, err := retrieveFirstBlockNumFromFile(rootDir, searchFile)
		if err != nil {
			return -1, err
		}
		switch {
		case n == blockNum:
			return searchFile, nil
		case n > blockNum:
			endFile = searchFile - 1
		case n < blockNum:
			beginFile = searchFile
		}
	}
	return beginFile, nil
}

func retrieveFirstBlockNumFromFile(rootDir string, fileNum int) (uint64, error) {
	s, err := newBlockfileStream(rootDir, fileNum, 0)
	if err != nil {
		return 0, err
	}
	defer s.close()
	bb, err := s.nextBlockBytes()
	if err != nil {
		return 0, err
	}
	blockInfo, err := extractSerializedBlockInfo(bb)
	if err != nil {
		return 0, err
	}
	return blockInfo.blockHeader.Number, nil
}

func retrieveLastFileSuffix(rootDir string) (int, error) {
	logger.Debugf("retrieveLastFileSuffix()")
	// 最大文件号
	biggestFileNum := -1
	// 从对应链ID目录下读取文件
	filesInfo, err := ioutil.ReadDir(rootDir)
	if err != nil {
		return -1, errors.Wrapf(err, "error reading dir %s", rootDir)
	}
	for _, fileInfo := range filesInfo {
		name := fileInfo.Name()
		// 如果文件是目录或者不是一个区块文件则进行下一次的扫描
		if fileInfo.IsDir() || !isBlockFileName(name) {
			logger.Debugf("Skipping File name = %s", name)
			continue
		}
		// 获取文件后缀，剩余部分也就是区块文件号
		fileSuffix := strings.TrimPrefix(name, blockfilePrefix)
		fileNum, err := strconv.Atoi(fileSuffix)
		if err != nil {
			return -1, err
		}
		// 筛选出最大的文件号
		if fileNum > biggestFileNum {
			biggestFileNum = fileNum
		}
	}
	logger.Debugf("retrieveLastFileSuffix() - biggestFileNum = %d", biggestFileNum)
	return biggestFileNum, err
}

// 如何判断一个文件是否是一个区块文件，文件名是否包含:blockfile_
func isBlockFileName(name string) bool {
	return strings.HasPrefix(name, blockfilePrefix)
}

func getFileInfoOrPanic(rootDir string, fileNum int) os.FileInfo {
	filePath := deriveBlockfilePath(rootDir, fileNum)
	// 返回文件描述信息
	fileInfo, err := os.Lstat(filePath)
	if err != nil {
		panic(errors.Wrapf(err, "error retrieving file info for file number %d", fileNum))
	}
	return fileInfo
}

// 加载快照信息
func loadBootstrappingSnapshotInfo(rootDir string) (*BootstrappingSnapshotInfo, error) {
	//
	bsiBytes, err := ioutil.ReadFile(filepath.Join(rootDir, bootstrappingSnapshotInfoFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrapf(err, "error while reading bootstrappingSnapshotInfo file")
	}
	bsi := &BootstrappingSnapshotInfo{}
	if err := proto.Unmarshal(bsiBytes, bsi); err != nil {
		return nil, errors.Wrapf(err, "error while unmarshalling bootstrappingSnapshotInfo")
	}
	return bsi, nil
}

func IsBootstrappedFromSnapshot(blockStorageDir, ledgerID string) (bool, error) {
	ledgerDir := filepath.Join(blockStorageDir, ChainsDir, ledgerID)
	_, err := os.Stat(filepath.Join(ledgerDir, bootstrappingSnapshotInfoFile))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, errors.Wrapf(err, "failed to read bootstrappingSnapshotInfo file under blockstore directory %s", ledgerDir)
	}
	return true, nil
}

func GetLedgersBootstrappedFromSnapshot(blockStorageDir string) ([]string, error) {
	chainsDir := filepath.Join(blockStorageDir, ChainsDir)
	ledgerIDs, err := fileutil.ListSubdirs(chainsDir)
	if err != nil {
		return nil, err
	}

	isFromSnapshot := false
	ledgersFromSnapshot := []string{}
	for _, ledgerID := range ledgerIDs {
		if isFromSnapshot, err = IsBootstrappedFromSnapshot(blockStorageDir, ledgerID); err != nil {
			return nil, err
		}
		if isFromSnapshot {
			ledgersFromSnapshot = append(ledgersFromSnapshot, ledgerID)
		}
	}

	return ledgersFromSnapshot, nil
}
