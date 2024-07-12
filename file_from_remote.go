package gguf_parser

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/thxcode/gguf-parser-go/util/httpx"
	"github.com/thxcode/gguf-parser-go/util/osx"
)

// ParseGGUFFileFromHuggingFace parses a GGUF file from Hugging Face(https://huggingface.co/),
// and returns a GGUFFile, or an error if any.
func ParseGGUFFileFromHuggingFace(ctx context.Context, repo, file string, opts ...GGUFReadOption) (*GGUFFile, error) {
	return ParseGGUFFileRemote(ctx, fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repo, file), opts...)
}

// ParseGGUFFileFromModelScope parses a GGUF file from Model Scope(https://modelscope.cn/),
// and returns a GGUFFile, or an error if any.
func ParseGGUFFileFromModelScope(ctx context.Context, repo, file string, opts ...GGUFReadOption) (*GGUFFile, error) {
	opts = append(opts[:len(opts):len(opts)], SkipRangeDownloadDetection())
	return ParseGGUFFileRemote(ctx, fmt.Sprintf("https://modelscope.cn/models/%s/resolve/master/%s", repo, file), opts...)
}

// ParseGGUFFileRemote parses a GGUF file from a remote BlobURL,
// and returns a GGUFFile, or an error if any.
func ParseGGUFFileRemote(ctx context.Context, url string, opts ...GGUFReadOption) (gf *GGUFFile, err error) {
	var o _GGUFReadOptions
	for _, opt := range opts {
		opt(&o)
	}

	// Cache.
	{
		if o.CachePath != "" {
			o.CachePath = filepath.Join(o.CachePath, "remote")
			if o.SkipLargeMetadata {
				o.CachePath = filepath.Join(o.CachePath, "brief")
			}
		}
		c := GGUFFileCache(o.CachePath)

		// Get from cache.
		if gf, err = c.Get(url, o.CacheExpiration); err == nil {
			return gf, nil
		}

		// Put to cache.
		defer func() {
			if err == nil {
				_ = c.Put(url, gf)
			}
		}()
	}

	cli := httpx.Client(
		httpx.ClientOptions().
			WithUserAgent("gguf-parser-go").
			If(o.Debug, func(x *httpx.ClientOption) *httpx.ClientOption {
				return x.WithDebug()
			}).
			WithTimeout(0).
			WithTransport(
				httpx.TransportOptions().
					WithoutKeepalive().
					TimeoutForDial(5*time.Second).
					TimeoutForTLSHandshake(5*time.Second).
					TimeoutForResponseHeader(5*time.Second).
					If(o.SkipProxy, func(x *httpx.TransportOption) *httpx.TransportOption {
						return x.WithoutProxy()
					}).
					If(o.ProxyURL != nil, func(x *httpx.TransportOption) *httpx.TransportOption {
						return x.WithProxy(http.ProxyURL(o.ProxyURL))
					}).
					If(o.SkipTLSVerification, func(x *httpx.TransportOption) *httpx.TransportOption {
						return x.WithoutInsecureVerify()
					}).
					If(o.SkipDNSCache, func(x *httpx.TransportOption) *httpx.TransportOption {
						return x.WithoutDNSCache()
					})))

	return parseGGUFFileFromRemote(ctx, cli, url, o)
}

func parseGGUFFileFromRemote(ctx context.Context, cli *http.Client, url string, o _GGUFReadOptions) (*GGUFFile, error) {
	var (
		f io.ReadSeeker
		s int64
	)
	{
		req, err := httpx.NewGetRequestWithContext(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}

		sf, err := httpx.OpenSeekerFile(cli, req,
			httpx.SeekerFileOptions().
				WithBufferSize(o.BufferSize).
				If(o.SkipRangeDownloadDetection, func(x *httpx.SeekerFileOption) *httpx.SeekerFileOption {
					return x.WithoutRangeDownloadDetect()
				}))
		if err != nil {
			return nil, fmt.Errorf("open http file: %w", err)
		}
		defer osx.Close(sf)

		f = io.NewSectionReader(sf, 0, sf.Len())
		s = sf.Len()
	}

	return parseGGUFFile(s, f, o)
}
