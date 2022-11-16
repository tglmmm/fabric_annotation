/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package filerepo

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hyperledger/fabric/internal/fileutil"
	"github.com/pkg/errors"
)

const (
	repoFilePermPrivateRW      os.FileMode = 0o600
	defaultTransientFileMarker             = "~"
)

// Repo manages filesystem operations for saving files marked by the fileSuffix
// in order to support crash fault tolerance for components that need it by maintaining
// a file repo structure storing intermediate state.
type Repo struct {
	mu                  sync.Mutex
	fileRepoDir         string
	fileSuffix          string
	transientFileMarker string
}

// New initializes a new file repo at repoParentDir/fileSuffix.
// All file system operations on the returned file repo are thread safe.
// 在repoParentDir/fileSuffix.初始化一个文件仓库，
// 返回的所有文件仓库都市线程安全的
func New(repoParentDir, fileSuffix string) (*Repo, error) {
	// 文件后缀校验,不能是空也不能包含"//"
	if err := validateFileSuffix(fileSuffix); err != nil {
		return nil, err
	}
	// pendingops/remove
	fileRepoDir := filepath.Join(repoParentDir, fileSuffix)
	// 创建目录， 如果目录是空的则返回true
	if _, err := fileutil.CreateDirIfMissing(fileRepoDir); err != nil {
		return nil, err
	}
	// 这是一个空操作
	if err := fileutil.SyncDir(repoParentDir); err != nil {
		return nil, err
	}
	// 读取目录下所有文件 pendingops/remove/*
	files, err := ioutil.ReadDir(fileRepoDir)
	if err != nil {
		return nil, err
	}

	// Remove existing transient files in the repo
	// 删除目录中所有的临时文件 ，临时文件规则是 *remove~
	transientFilePattern := "*" + fileSuffix + defaultTransientFileMarker
	for _, f := range files {
		isTransientFile, err := filepath.Match(transientFilePattern, f.Name())
		if err != nil {
			return nil, err
		}
		// 如果文件名称匹配临时文件名称的规则就将其删除
		if isTransientFile {
			if err := os.Remove(filepath.Join(fileRepoDir, f.Name())); err != nil {
				return nil, errors.Wrapf(err, "error cleaning up transient files")
			}
		}
	}

	if err := fileutil.SyncDir(fileRepoDir); err != nil {
		return nil, err
	}

	return &Repo{
		transientFileMarker: defaultTransientFileMarker,
		fileSuffix:          fileSuffix,
		fileRepoDir:         fileRepoDir,
	}, nil
}

// Save atomically persists the content to suffix/baseName+suffix file by first writing it
// to a tmp file marked by the transientFileMarker and then moves the file to the final
// destination indicated by the FileSuffix.

// 第一次写入时，原子的持久的将内容写入文件，并且被transientFileMarker标记，然后把文件移动到后缀指定的位置
func (r *Repo) Save(baseName string, content []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 通过basename拼接 得到文件名
	// basename.suffix
	fileName := r.baseToFileName(baseName)
	//  文件绝对路径
	dest := r.baseToFilePath(baseName)
	// 文件是否存在，存在则返回错误
	if _, err := os.Stat(dest); err == nil {
		return os.ErrExist
	}
	// 临时文件名的规则： 文件名+临时文件标记(~)
	tmpFileName := fileName + r.transientFileMarker
	// 文件写入后同步并且重命名
	if err := fileutil.CreateAndSyncFileAtomically(r.fileRepoDir, tmpFileName, fileName, content, repoFilePermPrivateRW); err != nil {
		return err
	}
	return fileutil.SyncDir(r.fileRepoDir)
}

// Remove removes the file associated with baseName from the file system.
func (r *Repo) Remove(baseName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// basename.remove
	filePath := r.baseToFilePath(baseName)

	if err := os.RemoveAll(filePath); err != nil {
		return err
	}

	return fileutil.SyncDir(r.fileRepoDir)
}

// Read reads the file in the fileRepo associated with baseName's contents.
func (r *Repo) Read(baseName string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	filePath := r.baseToFilePath(baseName)
	return ioutil.ReadFile(filePath)
}

// List parses the directory and produce a list of file names, filtered by suffix.
// 解析目录并且生成文件名称列表（通过后缀过滤的）
func (r *Repo) List() ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var repoFiles []string

	files, err := ioutil.ReadDir(r.fileRepoDir)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		// 匹配规则：*remove
		isFileSuffix, err := filepath.Match("*"+r.fileSuffix, f.Name())
		if err != nil {
			return nil, err
		}
		// 将文件放到列表中
		if isFileSuffix {
			repoFiles = append(repoFiles, f.Name())
		}
	}

	return repoFiles, nil
}

// FileToBaseName strips the suffix from the file name to get the associated channel name.
func (r *Repo) FileToBaseName(fileName string) string {
	baseFile := filepath.Base(fileName)

	return strings.TrimSuffix(baseFile, "."+r.fileSuffix)
}

func (r *Repo) baseToFilePath(baseName string) string {
	return filepath.Join(r.fileRepoDir, r.baseToFileName(baseName))
}

func (r *Repo) baseToFileName(baseName string) string {
	// basename.filesuffix
	return baseName + "." + r.fileSuffix
}

func validateFileSuffix(fileSuffix string) error {
	if len(fileSuffix) == 0 {
		return errors.New("fileSuffix illegal, cannot be empty")
	}

	// 字符类型转换成字符串类型 // ; 不允许包含
	if strings.Contains(fileSuffix, string(os.PathSeparator)) {
		return errors.Errorf("fileSuffix [%s] illegal, cannot contain os path separator", fileSuffix)
	}

	return nil
}
