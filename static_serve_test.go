package staticserve

import (
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
	"github.com/vicanso/cod"
)

const (
	staticPath = "/local"
)

type MockStaticFile struct {
}
type MockFileStat struct{}

func (m *MockStaticFile) Exists(file string) bool {
	if strings.HasSuffix(file, "notfound.html") {
		return false
	}
	return true
}

func (m *MockStaticFile) Get(file string) ([]byte, error) {
	if file == staticPath+"/error" {
		return nil, errors.New("abcd")
	}
	if file == staticPath+"/index.html" {
		return []byte("<html>xxx</html>"), nil
	}
	if file == staticPath+"/banner.jpg" {
		return []byte("image data"), nil
	}
	return []byte("abcd"), nil
}

func (m *MockStaticFile) Stat(file string) os.FileInfo {
	return &MockFileStat{}
}

func (mf *MockFileStat) Name() string {
	return "file"
}

func (mf *MockFileStat) Size() int64 {
	return 1024
}

func (mf *MockFileStat) Mode() os.FileMode {
	return os.ModeAppend
}

func (mf *MockFileStat) ModTime() time.Time {
	return time.Now()
}

func (mf *MockFileStat) IsDir() bool {
	return false
}

func (mf *MockFileStat) Sys() interface{} {
	return nil
}

func TestGenerateETag(t *testing.T) {
	assert := assert.New(t)
	assert.Equal(generateETag([]byte("")), `"0-2jmj7l5rSw0yVb_vlWAYkK_YBwk="`)
	assert.Equal(generateETag([]byte("abc")), `"3-qZk-NkcGgWq6PiVxeFDCbJzQ2J0="`)
}

func TestFS(t *testing.T) {
	file := os.Args[0]
	fs := FS{}
	t.Run("normal", func(t *testing.T) {
		assert := assert.New(t)
		assert.NotNil(NewDefault(Config{}))
		assert.True(fs.Exists(file), "file should be exists")

		fileInfo := fs.Stat(file)
		assert.NotNil(fileInfo, "stat of file shouldn't be nil")

		buf, err := fs.Get(file)
		assert.Nil(err)
		assert.NotEmpty(buf)
	})

	t.Run("out of path", func(t *testing.T) {
		assert := assert.New(t)
		tfs := FS{}

		assert.Nil(tfs.Stat("/b"), "out of path should return nil stat")
		assert.False(tfs.Exists("/b"), "file should be not exists")
	})
}
func TestStaticServe(t *testing.T) {
	staticFile := &MockStaticFile{}
	t.Run("not allow query string", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path:            staticPath,
			DenyQueryString: true,
		})
		req := httptest.NewRequest("GET", "/index.html?a=1", nil)
		c := cod.NewContext(nil, req)
		err := fn(c)
		assert.Equal(err, ErrNotAllowQueryString, "should return not allow query string error")
	})

	t.Run("not allow dot file", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path:    staticPath,
			DenyDot: true,
		})
		req := httptest.NewRequest("GET", "/.index.html", nil)
		c := cod.NewContext(nil, req)
		err := fn(c)
		assert.Equal(err, ErrNotAllowAccessDot, "should return not allow dot error")
	})

	t.Run("not found return error", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path: staticPath,
		})
		req := httptest.NewRequest("GET", "/notfound.html", nil)
		c := cod.NewContext(nil, req)
		c.Next = func() error {
			return nil
		}
		err := fn(c)
		assert.Equal(err, ErrNotFound, "should return not found error")
	})

	t.Run("not found pass to next", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path:         staticPath,
			NotFoundNext: true,
		})
		req := httptest.NewRequest("GET", "/notfound.html", nil)
		c := cod.NewContext(nil, req)
		done := false
		c.Next = func() error {
			done = true
			return nil
		}
		err := fn(c)
		assert.Nil(err)
		assert.True(done)
	})

	t.Run("not compresss", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path: staticPath,
		})
		req := httptest.NewRequest("GET", "/static/banner.jpg", nil)
		res := httptest.NewRecorder()
		c := cod.NewContext(res, req)
		c.RawParams = httprouter.Params{
			httprouter.Param{
				Key:   "file",
				Value: "banner.jpg",
			},
		}
		c.Next = func() error {
			return nil
		}
		err := fn(c)
		assert.Nil(err)
		assert.NotEqual(c.GetHeader(cod.HeaderContentEncoding), "gzip")
		assert.Equal(c.GetHeader(cod.HeaderETag), `"a-1oFGwuX-Q3qfLHqK_7iCcc_0YYI="`)
	})

	t.Run("get index.html", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path: staticPath,
		})
		req := httptest.NewRequest("GET", "/index.html?a=1", nil)
		res := httptest.NewRecorder()
		c := cod.NewContext(res, req)
		c.Next = func() error {
			return nil
		}
		err := fn(c)
		assert.Nil(err, "serve index.html fail")

		assert.Equal(c.GetHeader(cod.HeaderETag), `"10-FKjW3bSjaJvr_QYzQcHNFRn-rxc="`, "generate etag fail")
		assert.NotEmpty(c.GetHeader(cod.HeaderLastModified), "last modified shouldn't be empty")
		assert.Equal(c.GetHeader("Content-Type"), "text/html; charset=utf-8")
		assert.Equal(c.BodyBuffer.Len(), 16, "response compress body fail")
	})

	t.Run("set custom header", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path: staticPath,
			Header: map[string]string{
				"X-IDC": "GZ",
			},
		})
		req := httptest.NewRequest("GET", "/index.html", nil)
		res := httptest.NewRecorder()
		c := cod.NewContext(res, req)
		c.Next = func() error {
			return nil
		}
		err := fn(c)
		assert.Nil(err)
		assert.Equal(c.GetHeader("X-IDC"), "GZ", "set custom header fail")
	})

	t.Run("set (s)max-age", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path:    staticPath,
			MaxAge:  24 * 3600,
			SMaxAge: 300,
		})
		req := httptest.NewRequest("GET", "/index.html", nil)
		res := httptest.NewRecorder()
		c := cod.NewContext(res, req)
		c.Next = func() error {
			return nil
		}
		err := fn(c)
		assert.Nil(err)
		assert.Equal(c.GetHeader(cod.HeaderCacheControl), "public, max-age=86400, s-maxage=300", "set max age header fail")
	})

	t.Run("out of path", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path:    staticPath,
			MaxAge:  24 * 3600,
			SMaxAge: 300,
		})
		req := httptest.NewRequest("GET", "/index.html", nil)
		req.URL.Path = "../../index.html"
		res := httptest.NewRecorder()
		c := cod.NewContext(res, req)
		c.Next = func() error {
			return nil
		}
		err := fn(c)
		assert.Equal(err.Error(), "category=cod-static-serve, message=out of path", "out of path should return error")
	})

	t.Run("get file error", func(t *testing.T) {
		assert := assert.New(t)
		fn := New(staticFile, Config{
			Path:    staticPath,
			MaxAge:  24 * 3600,
			SMaxAge: 300,
		})
		req := httptest.NewRequest("GET", "/error", nil)
		res := httptest.NewRecorder()
		c := cod.NewContext(res, req)
		c.Next = func() error {
			return nil
		}
		err := fn(c)
		assert.Equal(err.Error(), "category=cod-static-serve, message=abcd", "get file fail should return error")
	})
}
