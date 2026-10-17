package main

// The news service's internal API, as this service uses it. News keeps authoring, storage and
// the datasets; everything console-facing is pulled from it:
//
//	GET /admin/v1/bcat/current                {build, revision}           (News channels)
//	GET /admin/v1/bcat/builds/{n}/seed.tar    the unsigned seed of build n
//	GET /internal/datasets/{title}            nativebcat.Dataset, Last-Modified; 404 = none
//	GET /internal/objects/{key}               the object's bytes
//
// All carry X-Internal-Key. The first two are on newscdn's admin listener (NEWS_ADMIN_URL,
// NEWS_ADMIN_KEY); the dataset pair is on bcatserve (NEWS_DATASET_URL, NX_INTERNAL_KEY).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"nx-bcat/bcatcontainer"
	"nx-bcat/nativebcat"
)

type upstream struct {
	base   string
	key    string
	client *http.Client
}

func newUpstream(base, key string) *upstream {
	return &upstream{base: base, key: key, client: &http.Client{Timeout: 30 * time.Second}}
}

func (u *upstream) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Key", u.key)
	return u.client.Do(req)
}

// datasetSource implements nativebcat.Source over the news service's dataset API.
type datasetSource struct{ *upstream }

func (s datasetSource) CurrentDataset(ctx context.Context, title string) (*nativebcat.Dataset, time.Time, error) {
	resp, err := s.get(ctx, "/internal/datasets/"+url.PathEscape(title))
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, time.Time{}, nativebcat.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("news datasets: %s", resp.Status)
	}
	var d nativebcat.Dataset
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return nil, time.Time{}, err
	}
	mod, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	return &d, mod, nil
}

type object struct{ *bytes.Reader }

func (object) Close() error { return nil }

func (s datasetSource) OpenObject(ctx context.Context, key string) (io.ReadSeekCloser, int64, time.Time, error) {
	resp, err := s.get(ctx, "/internal/objects/"+url.PathEscape(key))
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, time.Time{}, fmt.Errorf("news objects: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, bcatcontainer.MaxPayload+1))
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	return object{bytes.NewReader(b)}, int64(len(b)), time.Time{}, nil
}
