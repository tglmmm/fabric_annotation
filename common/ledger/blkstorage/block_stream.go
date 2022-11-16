/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package blkstorage

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/golang/protobuf/proto"
	"github.com/pkg/errors"
)

// ErrUnexpectedEndOfBlockfile error used to indicate an unexpected end of a file segment
// this can happen mainly if a crash occurs during appending a block and partial block contents
// get written towards the end of the file
var ErrUnexpectedEndOfBlockfile = errors.New("unexpected end of blockfile")

// blockfileStream reads blocks sequentially from a single file.
// It starts from the given offset and can traverse till the end of the file
type blockfileStream struct {
	fileNum       int
	file          *os.File
	reader        *bufio.Reader
	currentOffset int64
}

// blockStream reads blocks sequentially from multiple files.
// it starts from a given file offset and continues with the next
// file segment until the end of the last segment (`endFileNum`)
type blockStream struct {
	rootDir           string
	currentFileNum    int
	endFileNum        int
	currentFileStream *blockfileStream
}

// blockPlacementInfo captures the information related
// to block's placement in the file.
type blockPlacementInfo struct {
	fileNum          int
	blockStartOffset int64
	blockBytesOffset int64
}

///////////////////////////////////
// blockfileStream functions
////////////////////////////////////
func newBlockfileStream(rootDir string, fileNum int, startOffset int64) (*blockfileStream, error) {
	// 根据文件编号获取区块文件绝对路径
	filePath := deriveBlockfilePath(rootDir, fileNum)
	logger.Debugf("newBlockfileStream(): filePath=[%s], startOffset=[%d]", filePath, startOffset)
	var file *os.File
	var err error
	// 只读形式打开文件
	if file, err = os.OpenFile(filePath, os.O_RDONLY, 0o600); err != nil {
		return nil, errors.Wrapf(err, "error opening block file %s", filePath)
	}
	// 定义一个位置变量
	var newPosition int64
	// whence： 0标识从文件开头， 1标识从当前位置 ，2标识从文件末尾
	if newPosition, err = file.Seek(startOffset, 0); err != nil {
		return nil, errors.Wrapf(err, "error seeking block file [%s] to startOffset [%d]", filePath, startOffset)
	}
	//
	if newPosition != startOffset {
		panic(fmt.Sprintf("Could not seek block file [%s] to startOffset [%d]. New position = [%d]",
			filePath, startOffset, newPosition))
	}
	// 区块文件编号 ， 文件对象， 文件reader对象 ，文件读取偏移量
	s := &blockfileStream{fileNum, file, bufio.NewReader(file), startOffset}
	return s, nil
}

//
func (s *blockfileStream) nextBlockBytes() ([]byte, error) {
	// 从文件中根据偏移量读取下一个区块的内容
	blockBytes, _, err := s.nextBlockBytesAndPlacementInfo()
	return blockBytes, err
}

// nextBlockBytesAndPlacementInfo returns bytes for the next block
// along with the offset information in the block file.
// An error `ErrUnexpectedEndOfBlockfile` is returned if a partial written data is detected
// which is possible towards the tail of the file if a crash had taken place during appending of a block

// 返回下一个区块数据，以及区块文件中偏移量信息，如果检测到部分写入的数据就返回错误(ErrUnexpectedEndOfBlockfile)
// 这个函数本质上就是读取区块文件，然后遍历文件中的每一个块
func (s *blockfileStream) nextBlockBytesAndPlacementInfo() ([]byte, *blockPlacementInfo, error) {
	var lenBytes []byte
	var err error
	var fileInfo os.FileInfo
	moreContentAvailable := true

	// 检查文件状态
	if fileInfo, err = s.file.Stat(); err != nil {
		return nil, nil, errors.Wrapf(err, "error getting block file stat")
	}
	//  如果当前偏移量== 文件大小，说明已经读取完毕
	if s.currentOffset == fileInfo.Size() {
		logger.Debugf("Finished reading file number [%d]", s.fileNum)
		return nil, nil, nil
	}
	// 文件总的大小减去当前偏移量，剩余部分的大小
	remainingBytes := fileInfo.Size() - s.currentOffset
	// 如果剩余字节数小于8
	// Peek 8 or smaller number of bytes (if remaining bytes are less than 8)
	// 假设块大小足够小可以用八字节的 varint表述
	// Assumption is that a block size would be small enough to be represented in 8 bytes varint
	peekBytes := 8
	// 如果剩余字节小于8bytes
	if remainingBytes < int64(peekBytes) {
		peekBytes = int(remainingBytes)
		moreContentAvailable = false // 标记没有更多可用的内容了
	}
	logger.Debugf("Remaining bytes=[%d], Going to peek [%d] bytes", remainingBytes, peekBytes)
	// lenByte is verint
	if lenBytes, err = s.reader.Peek(peekBytes); err != nil {
		return nil, nil, errors.Wrapf(err, "error peeking [%d] bytes from block file", peekBytes)
	}
	// 从lenBytes中解析一个经过编码的整数，返回整数值和变量的长度。如果存在解析错误，则返回(0,0)。
	// length 编码后的整数
	length, n := proto.DecodeVarint(lenBytes)
	if n == 0 {
		// 没有消耗任何字节这表示代表块大小的字节是部分字节
		// proto.DecodeVarint did not consume any byte at all which means that the bytes representing the size of the block are partial bytes
		if !moreContentAvailable {
			return nil, nil, ErrUnexpectedEndOfBlockfile
		}
		panic(errors.Errorf("Error in decoding varint bytes [%#v]", lenBytes))
	}
	// n 代表标识区块内容长度的字节数（例如长度94，则长度表示94对应字varint节数数）,  Length字节数量，也就是94
	bytesExpected := int64(n) + int64(length)

	// 此处说明区块文件在写入过程中可能发生过崩溃，导致期望大小和实际大小并不一致，所以返回错误
	if bytesExpected > remainingBytes {
		logger.Debugf("At least [%d] bytes expected. Remaining bytes = [%d]. Returning with error [%s]",
			bytesExpected, remainingBytes, ErrUnexpectedEndOfBlockfile)
		return nil, nil, ErrUnexpectedEndOfBlockfile
	}
	// skip the bytes representing the block size
	// 跳过(推进)n字节
	if _, err = s.reader.Discard(n); err != nil {
		return nil, nil, errors.Wrapf(err, "error discarding [%d] bytes", n)
	}
	blockBytes := make([]byte, length)
	// 读取区块信息
	if _, err = io.ReadAtLeast(s.reader, blockBytes, int(length)); err != nil {
		logger.Errorf("Error reading [%d] bytes from file number [%d], error: %s", length, s.fileNum, err)
		return nil, nil, errors.Wrapf(err, "error reading [%d] bytes from file number [%d]", length, s.fileNum)
	}
	blockPlacementInfo := &blockPlacementInfo{
		fileNum:          s.fileNum,
		blockStartOffset: s.currentOffset,
		blockBytesOffset: s.currentOffset + int64(n),
	}
	s.currentOffset += int64(n) + int64(length)
	logger.Debugf("Returning blockbytes - length=[%d], placementInfo={%s}", len(blockBytes), blockPlacementInfo)
	return blockBytes, blockPlacementInfo, nil
}

func (s *blockfileStream) close() error {
	return errors.WithStack(s.file.Close())
}

///////////////////////////////////
// blockStream functions
////////////////////////////////////
func newBlockStream(rootDir string, startFileNum int, startOffset int64, endFileNum int) (*blockStream, error) {
	startFileStream, err := newBlockfileStream(rootDir, startFileNum, startOffset)
	if err != nil {
		return nil, err
	}
	return &blockStream{rootDir, startFileNum, endFileNum, startFileStream}, nil
}

func (s *blockStream) moveToNextBlockfileStream() error {
	var err error
	if err = s.currentFileStream.close(); err != nil {
		return err
	}
	s.currentFileNum++
	if s.currentFileStream, err = newBlockfileStream(s.rootDir, s.currentFileNum, 0); err != nil {
		return err
	}
	return nil
}

func (s *blockStream) nextBlockBytes() ([]byte, error) {
	blockBytes, _, err := s.nextBlockBytesAndPlacementInfo()
	return blockBytes, err
}

func (s *blockStream) nextBlockBytesAndPlacementInfo() ([]byte, *blockPlacementInfo, error) {
	var blockBytes []byte
	var blockPlacementInfo *blockPlacementInfo
	var err error
	if blockBytes, blockPlacementInfo, err = s.currentFileStream.nextBlockBytesAndPlacementInfo(); err != nil {
		logger.Errorf("Error reading next block bytes from file number [%d]: %s", s.currentFileNum, err)
		return nil, nil, err
	}
	logger.Debugf("blockbytes [%d] read from file [%d]", len(blockBytes), s.currentFileNum)
	if blockBytes == nil && (s.currentFileNum < s.endFileNum || s.endFileNum < 0) {
		logger.Debugf("current file [%d] exhausted. Moving to next file", s.currentFileNum)
		if err = s.moveToNextBlockfileStream(); err != nil {
			return nil, nil, err
		}
		return s.nextBlockBytesAndPlacementInfo()
	}
	return blockBytes, blockPlacementInfo, nil
}

func (s *blockStream) close() error {
	return s.currentFileStream.close()
}

func (i *blockPlacementInfo) String() string {
	return fmt.Sprintf("fileNum=[%d], startOffset=[%d], bytesOffset=[%d]",
		i.fileNum, i.blockStartOffset, i.blockBytesOffset)
}
