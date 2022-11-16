/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

/*
创建一个临时的容器， 将链码源码上传到该容器， 根据传递的CMD和ENV完成编译后将结果下载

*/

package util

import (
	"bytes"
	"fmt"
	"io"
	"runtime"
	"strings"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/common/metadata"
	"github.com/spf13/viper"
)

var logger = flogging.MustGetLogger("chaincode.platform.util")

// 镜像构建选项
type DockerBuildOptions struct {
	Image        string
	Cmd          string
	Env          []string
	InputStream  io.Reader
	OutputStream io.Writer
}

func (dbo DockerBuildOptions) String() string {
	return fmt.Sprintf("Image=%s Env=%s Cmd=%s)", dbo.Image, dbo.Env, dbo.Cmd)
}

//-------------------------------------------------------------------------------------------
// DockerBuild
//-------------------------------------------------------------------------------------------
// This function allows a "pass-through" build of chaincode within a docker container as
// an alternative to using standard "docker build" + Dockerfile mechanisms.  The plain docker
// build is somewhat limiting due to the resulting image that is a superset composition of
// the build-time and run-time environments.  This superset can be problematic on several
// fronts, such as a bloated image size, and additional security exposure associated with
// applications that are not needed, etc.
//
// Therefore, this mechanism creates a pipeline consisting of an ephemeral docker
// container that accepts source code as input, runs some function (e.g. "go build"), and
// outputs the result.  The intention is that this output will be consumed as the basis of
// a streamlined container by installing the output into a downstream docker-build based on
// an appropriate minimal image.
//
// The input parameters are fairly simple:
//      - Image:        (optional) The builder image to use or "chaincode.builder"
//      - Cmd:          The command to execute inside the container.
//      - InputStream:  A tarball of files that will be expanded into /chaincode/input.
//      - OutputStream: A tarball of files that will be gathered from /chaincode/output
//                      after successful execution of Cmd.
//-------------------------------------------------------------------------------------------
func DockerBuild(opts DockerBuildOptions, client *docker.Client) error {
	if opts.Image == "" {
		// 从配置文件core.yaml 获取chaincode.builder配置信息
		// 实际上是构建chaincode 基础镜像的名称以及版本信息
		opts.Image = GetDockerImageFromConfig("chaincode.builder")
		if opts.Image == "" {
			return fmt.Errorf("No image provided and \"chaincode.builder\" default does not exist")
		}
	}

	logger.Debugf("Attempting build with options: %s", opts)

	//-----------------------------------------------------------------------------------
	// Ensure the image exists locally, or pull it from a registry if it doesn't
	//-----------------------------------------------------------------------------------
	// 确保镜像在本地已经存在，否则从仓库中拉取镜像
	// 通过镜像名称检查镜像是否存在
	_, err := client.InspectImage(opts.Image)
	if err != nil {
		logger.Debugf("Image %s does not exist locally, attempt pull", opts.Image)
		// 本地主机拉取镜像
		err = client.PullImage(docker.PullImageOptions{Repository: opts.Image}, docker.AuthConfiguration{})
		if err != nil {
			return fmt.Errorf("Failed to pull %s: %s", opts.Image, err)
		}
	}

	//-----------------------------------------------------------------------------------
	// Create an ephemeral container, armed with our Image/Cmd
	//-----------------------------------------------------------------------------------
	// 创建一个临时的容器，构建chaincode
	container, err := client.CreateContainer(docker.CreateContainerOptions{
		Config: &docker.Config{
			Image:        opts.Image,
			Cmd:          []string{"/bin/sh", "-c", opts.Cmd},
			Env:          opts.Env,
			AttachStdout: true,
			AttachStderr: true,
		},
	})
	if err != nil {
		return fmt.Errorf("Error creating container: %s", err)
	}
	// 在构建链码结束后删除临时镜像
	defer client.RemoveContainer(docker.RemoveContainerOptions{ID: container.ID})

	//-----------------------------------------------------------------------------------
	// Upload our input stream
	//-----------------------------------------------------------------------------------
	// 上传链码的归档文件到临时容器中
	err = client.UploadToContainer(container.ID, docker.UploadToContainerOptions{
		Path:        "/chaincode/input", // 容器目录
		InputStream: opts.InputStream,   // 链码包输入流
	})
	if err != nil {
		return fmt.Errorf("Error uploading input to container: %s", err)
	}

	//-----------------------------------------------------------------------------------
	// Attach stdout buffer to capture possible compilation errors
	//-----------------------------------------------------------------------------------
	stdout := bytes.NewBuffer(nil)
	// 使用给定的选项连接到容器，不会被阻塞
	cw, err := client.AttachToContainerNonBlocking(docker.AttachToContainerOptions{
		Container:    container.ID,
		OutputStream: stdout,
		ErrorStream:  stdout,
		Logs:         true,
		Stdout:       true,
		Stderr:       true,
		Stream:       true,
	})
	if err != nil {
		return fmt.Errorf("Error attaching to container: %s", err)
	}

	//-----------------------------------------------------------------------------------
	// Launch the actual build, realizing the Env/Cmd specified at container creation
	//-----------------------------------------------------------------------------------
	err = client.StartContainer(container.ID, nil)
	if err != nil {
		cw.Close()
		return fmt.Errorf("Error executing build: %s \"%s\"", err, stdout.String())
	}

	//-----------------------------------------------------------------------------------
	// Wait for the build to complete and gather the return value
	//-----------------------------------------------------------------------------------
	// 等待容器build完成后结束
	retval, err := client.WaitContainer(container.ID)
	if err != nil {
		cw.Close()
		return fmt.Errorf("Error waiting for container to complete: %s", err)
	}

	// Wait for stream copying to complete before accessing stdout.
	cw.Close()
	if err := cw.Wait(); err != nil {
		logger.Errorf("attach wait failed: %s", err)
	}

	// 如果容器执行完成退出码不为0，则发生了错误
	if retval > 0 {
		logger.Errorf("Docker build failed using options: %s", opts)
		return fmt.Errorf("Error returned from build: %d \"%s\"", retval, stdout.String())
	}

	logger.Debugf("Build output is %s", stdout.String())

	//-----------------------------------------------------------------------------------
	// Finally, download the result
	//-----------------------------------------------------------------------------------
	// 最后从容器中下载 编译好的链码
	err = client.DownloadFromContainer(container.ID, docker.DownloadFromContainerOptions{
		Path:         "/chaincode/output/.",
		OutputStream: opts.OutputStream,
	})
	if err != nil {
		return fmt.Errorf("Error downloading output: %s", err)
	}

	return nil
}

// GetDockerImageFromConfig replaces variables in the config
func GetDockerImageFromConfig(path string) string {
	r := strings.NewReplacer(
		"$(ARCH)", runtime.GOARCH, // AMD64
		"$(PROJECT_VERSION)", metadata.Version, // latest
		"$(TWO_DIGIT_VERSION)", twoDigitVersion(metadata.Version),
		"$(DOCKER_NS)", metadata.DockerNamespace)

	return r.Replace(viper.GetString(path))
}

// twoDigitVersion truncates a 3 digit version (e.g. 2.0.0) to a 2 digit version (e.g. 2.0),
// If version does not include dots (e.g. latest), just return the passed version
func twoDigitVersion(version string) string {
	if strings.LastIndex(version, ".") < 0 {
		return version
	}
	return version[0:strings.LastIndex(version, ".")]
}
