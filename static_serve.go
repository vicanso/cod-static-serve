// Copyright 2018 tree xie
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package staticserve

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vicanso/cod"
	"github.com/vicanso/hes"
)

type (
	// StaticFile static file
	StaticFile interface {
		Exists(string) bool
		Get(string) ([]byte, error)
		Stat(string) os.FileInfo
	}
	// Config static serve config
	Config struct {
		// 静态文件目录
		Path string
		// http cache control max age
		MaxAge int
		// http cache control s-maxage
		SMaxAge int
		// http response header
		Header map[string]string
		// 禁止query string（因为有时静态文件为CDN回源，避免生成各种重复的缓存）
		DenyQueryString bool
		// 是否禁止文件路径以.开头（因为这些文件有可能包括重要信息）
		DenyDot bool
		// 禁止生成ETag
		DisableETag bool
		// 禁止生成 last-modifed
		DisableLastModified bool
		// 如果404，是否调用next执行后续的中间件（默认为不执行，返回404错误）
		NotFoundNext bool
		Skipper      cod.Skipper
	}
	// FS file system
	FS struct {
	}
)

const (
	// ErrCategory static serve error category
	ErrCategory = "cod-static-serve"
)

var (
	// ErrNotAllowQueryString not all query string
	ErrNotAllowQueryString = getStaticServeError("static serve not allow query string", http.StatusBadRequest)
	// ErrNotFound static file not found
	ErrNotFound = getStaticServeError("static file not found", http.StatusNotFound)
	// ErrOutOfPath file out of path
	ErrOutOfPath = getStaticServeError("out of path", http.StatusBadRequest)
	// ErrNotAllowAccessDot file include dot
	ErrNotAllowAccessDot = getStaticServeError("static server not allow with dot", http.StatusBadRequest)
)

// Exists check the file exists
func (fs *FS) Exists(file string) bool {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return false
	}
	return true
}

// Stat get stat of file
func (fs *FS) Stat(file string) os.FileInfo {
	info, _ := os.Stat(file)
	return info
}

// Get get the file's content
func (fs *FS) Get(file string) (buf []byte, err error) {
	buf, err = ioutil.ReadFile(file)
	return
}

// getStaticServeError 获取static serve的出错
func getStaticServeError(message string, statusCode int) *hes.Error {
	return &hes.Error{
		StatusCode: statusCode,
		Message:    message,
		Category:   ErrCategory,
	}
}

// generateETag generate eTag
func generateETag(buf []byte) string {
	size := len(buf)
	if size == 0 {
		return "\"0-2jmj7l5rSw0yVb_vlWAYkK_YBwk=\""
	}
	h := sha1.New()
	h.Write(buf)
	hash := base64.URLEncoding.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("\"%x-%s\"", size, hash)
}

// NewDefault create a static server milldeware use FS
func NewDefault(config Config) cod.Handler {
	return New(&FS{}, config)
}

// New create a static serve middleware
func New(staticFile StaticFile, config Config) cod.Handler {
	cacheArr := []string{
		"public",
	}
	if config.MaxAge > 0 {
		cacheArr = append(cacheArr, "max-age="+strconv.Itoa(config.MaxAge))
	}
	if config.SMaxAge > 0 {
		cacheArr = append(cacheArr, "s-maxage="+strconv.Itoa(config.SMaxAge))
	}
	cacheControl := ""
	if len(cacheArr) > 1 {
		cacheControl = strings.Join(cacheArr, ", ")
	}
	skipper := config.Skipper
	if skipper == nil {
		skipper = cod.DefaultSkipper
	}
	return func(c *cod.Context) (err error) {
		if skipper(c) {
			return c.Next()
		}
		file := ""
		// 从第一个参数获取文件名
		if c.Params != nil {
			for _, value := range c.Params {
				if value != "" {
					file = value
				}
			}
		}

		url := c.Request.URL

		if file == "" {
			file = url.Path
		}

		// 检查文件（路径）是否包括.
		if config.DenyDot {
			arr := strings.SplitN(file, "/", -1)
			for _, item := range arr {
				if item != "" && item[0] == '.' {
					err = ErrNotAllowAccessDot
					return
				}
			}
		}

		file = filepath.Join(config.Path, file)
		// 避免文件名是有 .. 等导致最终文件路径越过配置的路径
		if !strings.HasPrefix(file, config.Path) {
			err = ErrOutOfPath
			return
		}

		// 禁止 querystring
		if config.DenyQueryString && url.RawQuery != "" {
			err = ErrNotAllowQueryString
			return
		}
		exists := staticFile.Exists(file)
		if !exists {
			if config.NotFoundNext {
				return c.Next()
			}
			err = ErrNotFound
			return
		}

		c.SetContentTypeByExt(file)
		buf, e := staticFile.Get(file)
		if e != nil {
			he, ok := e.(*hes.Error)
			if !ok {
				he = hes.NewWithErrorStatusCode(e, http.StatusInternalServerError)
				he.Category = ErrCategory
			}
			err = he
			return
		}
		if !config.DisableETag {
			eTag := generateETag(buf)
			c.SetHeader(cod.HeaderETag, eTag)
		}
		if !config.DisableLastModified {
			fileInfo := staticFile.Stat(file)
			if fileInfo != nil {
				lmd := fileInfo.ModTime().UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
				c.SetHeader(cod.HeaderLastModified, lmd)
			}
		}

		for k, v := range config.Header {
			c.SetHeader(k, v)
		}
		if cacheControl != "" {
			c.SetHeader(cod.HeaderCacheControl, cacheControl)
		}
		c.BodyBuffer = bytes.NewBuffer(buf)
		return c.Next()
	}
}
