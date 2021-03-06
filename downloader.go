package cfs

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/natefinch/atomic"
	"golang.org/x/sync/errgroup"
)

type Downloader struct {
	BaseUrl *url.URL
}

func NewDownloader(baseRawurl string) (*Downloader, error) {
	url, err := url.Parse(baseRawurl)
	if err != nil {
		return nil, err
	}

	downloader := &Downloader{
		BaseUrl: url,
	}

	return downloader, nil
}

func (d *Downloader) LoadBucket(location string) (*Bucket, error) {

	b := &Bucket{
		Contents: make(map[string]Content),
		HashType: "md5",
	}

	var body []byte
	if !isHash(location) {
		locationBytes, err := d.FetchTag(location)
		if err != nil {
			return nil, err
		}
		location = string(locationBytes)
		if !isHash(location) {
			return nil, fmt.Errorf("%s is not hash", location)
		}
	}

	body, err := d.Fetch(location, DefaultContentAttribute())
	if err != nil {
		return nil, err
	}

	err = b.Parse(body)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func (d *Downloader) ExistsAll(b *Bucket) (map[string]bool, error) {
	result := map[string]bool{}
	mutex := sync.Mutex{}

	transport := &http.Transport{}
	wg := sync.WaitGroup{}
	ch := make(chan Content, 32)

	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range ch {
				if Verbose {
					fmt.Printf("verifying %s (%s)\n", c.Path, c.Hash)
				}
				url, err := d.dataUrl(c.Hash)
				if err != nil {
					panic(err)
				}
				res, err := headRequest(transport, url)
				if err != nil {
					mutex.Lock()
					result[c.Path] = false
					mutex.Unlock()
					continue
				}
				mutex.Lock()
				result[c.Path] = (res.StatusCode == 200)
				mutex.Unlock()
			}
		}()
	}

	for _, c := range b.Contents {
		ch <- c
	}

	close(ch)
	wg.Wait()

	return result, nil
}

func (d *Downloader) Sync(b *Bucket, dir string) error {
	for _, c := range b.Contents {
		if Verbose {
			fmt.Printf("downloading %s\n", c.Path)
		}

		// TODO: 0 bytesのファイルはアップロードがされていないため、空ファイルを作る
		var err error
		data := []byte{}
		if c.Size > 0 {
			data, err = d.Fetch(c.Hash, c.Attr)
			if err != nil {
				return err
			}
		}

		err = os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(c.Path))), 0777)
		if err != nil {
			return err
		}

		err = atomic.WriteFile(filepath.Join(dir, filepath.FromSlash(c.Path)), bytes.NewBuffer(data))
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *Downloader) FetchAll(b *Bucket) error {
	const RETRY_LIMIT = 3
	limit := make(chan struct{}, 8)
	eg, ctx := errgroup.WithContext(context.Background())
	ctx, cancel := context.WithCancel(ctx)

	for _, c := range b.Contents {
		c := c
		eg.Go(func() error {
			limit <- struct{}{}
			defer func() { <-limit }()

			select {
			case <-ctx.Done():
				return nil
			default:
				if Verbose {
					fmt.Printf("downloading %s\n", c.Path)
				}

				// TODO: 0 bytesのファイルはアップロードがされていないため、空ファイルを作る
				if c.Size == 0 {
					os.Create(filepath.Join(GlobalDataCacheDir(), c.Hash))
					return nil
				}

				retryCount := 0
				for {
					_, err := d.Fetch(c.Hash, c.Attr)
					if err != nil {
						if retryCount < RETRY_LIMIT {
							retryCount++
							fmt.Printf("retry for %v, retry count %d\n", err, retryCount)
							continue
						} else {
							return err
						}
					} else {
						break
					}
				}

			}
			return nil
		})
	}

	err := eg.Wait()
	cancel()
	if err != nil {
		return err
	}

	return nil
}

func (d *Downloader) Fetch(hash string, attr ContentAttribute) ([]byte, error) {
	if !isHash(hash) {
		return nil, fmt.Errorf("cannot fetch data, %s is not a hash", hash)
	}

	var data []byte

	// データをキャッシュしているパス取得
	cache := filepath.Join(GlobalDataCacheDir(), hash)
	_, err := os.Stat(cache)
	if !os.IsNotExist(err) {
		data, err = ioutil.ReadFile(cache)
		if err != nil {
			return nil, err
		}
	} else {

		// ダウンロードURL取得
		fetchUrl, err := d.dataUrl(hash)
		if err != nil {
			return nil, err
		}

		// ファイルダウンロード
		data, err = fetch(fetchUrl)
		if err != nil {
			return nil, err
		}

		// データファイルをキャッシュする
		err = atomic.WriteFile(cache, bytes.NewBuffer(data))
		if err != nil {
			return nil, err
		}
	}

	return decode(data, Option.EncryptKey, Option.EncryptIv, attr)
}

func (d *Downloader) FetchTag(tag string) ([]byte, error) {

	fetchUrl, err := d.BaseUrl.Parse("tag/" + tag)
	if err != nil {
		return nil, err
	}

	data, err := fetch(fetchUrl)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (d *Downloader) dataUrl(hash string) (*url.URL, error) {
	return d.BaseUrl.Parse(fmt.Sprintf("data/%s/%s", hash[0:2], hash[2:]))
}

func getRequest(_url *url.URL) (*http.Response, error) {
	t := &http.Transport{}
	if isWindows() {
		t.RegisterProtocol("file", http.NewFileTransport(http.Dir("")))
	} else {
		t.RegisterProtocol("file", http.NewFileTransport(http.Dir("/")))
	}
	c := &http.Client{Transport: t}
	return c.Get(_url.String())
}

func headRequest(t *http.Transport, _url *url.URL) (*http.Response, error) {
	c := &http.Client{Transport: t}
	return c.Head(_url.String())
}

func fetch(_url *url.URL) ([]byte, error) {
	res, err := getRequest(_url)
	if err != nil {
		return nil, err
	}

	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("bad response status code %d from %v", res.StatusCode, _url)
	}

	defer res.Body.Close()

	contents, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	return contents, nil
}
