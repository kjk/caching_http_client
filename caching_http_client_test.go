package caching_http_client

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"
	"time"

	"github.com/alecthomas/assert"
)

const (
	// there's a very small chance of port conflict
	// running explicitly on 127.0.0.1 to not trigger windows firewall
	httpAddr = "127.0.0.1:5892"
	httpRoot = "http://" + httpAddr
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func startServer() func() {
	mux := &http.ServeMux{}

	handler := func(w http.ResponseWriter, r *http.Request) {
		rsp := fmt.Sprintf("URL: %s\n", r.URL.String())
		w.Write([]byte(rsp))
	}

	mux.HandleFunc("/", handler)
	httpSrv := &http.Server{
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  120 * time.Second, // introduced in Go 1.8
		Handler:      mux,
		Addr:         httpAddr,
	}

	go func() {
		err := httpSrv.ListenAndServe()
		// mute error caused by Shutdown()
		if err == http.ErrServerClosed {
			err = nil
		}
		must(err)
	}()

	closeServer := func() {

	}
	return closeServer
}

func TestDidCache(t *testing.T) {
	cancel := startServer()
	defer cancel()
	cache := NewCache()
	assert.Equal(t, 0, len(cache.CachedRequests))

	client := New(cache)
	cache2 := GetCache(client)
	assert.Equal(t, cache, cache2)

	uri := httpRoot + "/test"

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	assert.NoError(t, err)
	rsp, err := client.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(cache.CachedRequests))
	rspBody, err := ioutil.ReadAll(rsp.Body)
	assert.NoError(t, err)
	rsp.Body.Close()

	var cachedBody []byte
	rr, err := cache.findCachedResponse(req, &cachedBody)
	assert.NoError(t, err)
	assert.NotNil(t, rr)
	assert.Equal(t, rspBody, cachedBody)

}
