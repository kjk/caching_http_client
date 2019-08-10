package caching_http_client

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"time"
)

// RequestsResponse is a cache entry. It remembers important details
// of the request and response
type RequestResponse struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   []byte `json:"body"`

	Response []byte      `json:"response"`
	Header   http.Header `json:"header"`
}

// Cache remembers past requests and responses
type Cache struct {
	// CachedRequests remembers past requests and their responses
	CachedRequests []*RequestResponse `json:"cached_requests"`

	// if true, will not return cached responses (but will still
	// record requests / responses)
	// Useful for tracing requests (but only those that return 200)
	DisableRespondingFromCache bool

	// if true, when comparing body of the request, and the body
	// is json, we'll normalize JSON
	CompareNormalizedJSONBody bool

	// for diagnostics, you can check how many http requests
	// were served from a cache and how many from network requests
	RequestsFromCache    int `json:"-"`
	RequestsNotFromCache int `json:"-"`
}

// NewCache returns a cache for http requests
func NewCache() *Cache {
	return &Cache{}
}

// Add remembers a given RequestResponse
func (c *Cache) Add(rr *RequestResponse) {
	c.CachedRequests = append(c.CachedRequests, rr)
}

// closeableBuffer adds Close() error method to bytes.Buffer
// to satisfy io.ReadCloser interface
type closeableBuffer struct {
	*bytes.Buffer
}

// Close is to satisfy io.Closer interface
func (b *closeableBuffer) Close() error {
	// nothing to do
	return nil
}

func readAndReplaceReadCloser(pBody *io.ReadCloser) ([]byte, error) {
	// have to read the body from r and put it back
	var err error
	body := *pBody
	// not all requests have body
	if body == nil {
		return nil, nil
	}
	d, err := ioutil.ReadAll(body)
	if err != nil {
		return nil, err
	}
	buf := &closeableBuffer{bytes.NewBuffer(d)}
	*pBody = buf
	return d, nil
}

// pretty-print if valid JSON. If not, return unchanged
func ppJSON(js []byte) []byte {
	var m map[string]interface{}
	err := json.Unmarshal(js, &m)
	if err != nil {
		return js
	}
	d, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return js
	}
	return d
}

func (c *Cache) isBodySame(r *http.Request, rr *RequestResponse, cachedBody *[]byte) (bool, error) {
	// only POST request takes body
	if r.Method != http.MethodPost {
		return true, nil
	}
	if r.Body == nil && len(rr.Body) == 0 {
		return true, nil
	}

	d := *cachedBody
	if d == nil {
		var err error
		d, err = readAndReplaceReadCloser(&r.Body)
		if err != nil {
			return false, err
		}
		if d == nil {
			*cachedBody = []byte{}
		} else {
			if c.CompareNormalizedJSONBody {
				d = ppJSON(d)
			}
			*cachedBody = d
		}
	}
	rrBody := rr.Body
	if c.CompareNormalizedJSONBody {
		rrBody = ppJSON(rr.Body)
	}
	return bytes.Equal(d, rrBody), nil
}

func (c *Cache) isCachedRequest(r *http.Request, rr *RequestResponse, cachedBody *[]byte) (bool, error) {
	if rr.Method != r.Method {
		return false, nil
	}
	uri1 := rr.URL
	uri2 := r.URL.String()
	if uri1 != uri2 {
		return false, nil
	}
	return c.isBodySame(r, rr, cachedBody)
}

func (c *Cache) findCachedResponse(r *http.Request, cachedBody *[]byte) (*RequestResponse, error) {
	if c.DisableRespondingFromCache {
		return nil, nil
	}

	for _, rr := range c.CachedRequests {
		same, err := c.isCachedRequest(r, rr, cachedBody)
		if err != nil {
			return nil, err
		}
		if same {
			return rr, nil
		}
	}
	return nil, nil
}

// CachingTransport is a http round-tripper that implements caching
// of past requests
type CachingTransport struct {
	Cache     *Cache
	transport http.RoundTripper
}

func (t *CachingTransport) cachedRoundTrip(r *http.Request, cachedRequestBody []byte) (*http.Response, error) {
	transport := t.transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if cachedRequestBody == nil {
		var err error
		cachedRequestBody, err = readAndReplaceReadCloser(&r.Body)
		if err != nil {
			return nil, err
		}
	}
	rsp, err := transport.RoundTrip(r)
	if err != nil {
		return rsp, err
	}

	// only cache 200 responses
	if rsp.StatusCode != 200 {
		return rsp, nil
	}

	d, err := readAndReplaceReadCloser(&rsp.Body)
	if err != nil {
		return nil, err
	}

	rr := &RequestResponse{
		Method: r.Method,
		URL:    r.URL.String(),
		Body:   cachedRequestBody,

		Response: d,
		Header:   rsp.Header,
	}
	t.Cache.Add(rr)
	t.Cache.RequestsNotFromCache++
	return rsp, nil
}

// RoundTrip is to satisfy http.RoundTripper interface
func (t *CachingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var cachedRequestBody []byte
	rr, err := t.Cache.findCachedResponse(r, &cachedRequestBody)
	if err != nil {
		return nil, err
	}

	if rr == nil {
		return t.cachedRoundTrip(r, cachedRequestBody)
	}

	t.Cache.RequestsFromCache++
	d := rr.Response
	rsp := &http.Response{
		Status:        "200",
		StatusCode:    200,
		Header:        rr.Header,
		Body:          &closeableBuffer{bytes.NewBuffer(d)},
		ContentLength: int64(len(d)),
	}
	return rsp, nil
}

// New creates http.Client
func New(cache *Cache) *http.Client {
	if cache == nil {
		cache = NewCache()
	}
	c := *http.DefaultClient
	c.Timeout = time.Second * 30
	origTransport := c.Transport
	c.Transport = &CachingTransport{
		Cache:     cache,
		transport: origTransport,
	}
	return &c
}

// GetCache gets from the client if it's a client created by us
func GetCache(client *http.Client) *Cache {
	if client == nil {
		return nil
	}
	t := client.Transport
	if t == nil {
		return nil
	}
	if ct, ok := t.(*CachingTransport); ok {
		return ct.Cache
	}
	return nil
}
