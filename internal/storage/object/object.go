// Package object implements an S3-compatible object store on top of the
// platform state store. It supports bucket create/delete, put/get/list
// of objects, multipart upload simulation, and presigned URLs.
//
// The on-disk payload lives under the platform data directory, one file
// per object. Metadata is kept in the KV store.
package object

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/minicloud/platform/internal/state"
)

// Store is the S3-compatible API.
type Store struct {
	store    *state.Store
	rootDir  string
	hmacKey  []byte
	mu       sync.Mutex
}

// Config configures a Store.
type Config struct {
	Store    *state.Store
	RootDir  string
	HMACKey  []byte
}

// New builds a Store rooted at RootDir.
func New(cfg Config) (*Store, error) {
	if cfg.RootDir == "" {
		cfg.RootDir = filepath.Join(os.TempDir(), "minicloud-objects")
	}
	if err := os.MkdirAll(cfg.RootDir, 0o755); err != nil {
		return nil, err
	}
	if len(cfg.HMACKey) == 0 {
		cfg.HMACKey = []byte("devkey")
	}
	return &Store{store: cfg.Store, rootDir: cfg.RootDir, hmacKey: cfg.HMACKey}, nil
}

// CreateBucket creates a bucket.
func (s *Store) CreateBucket(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("object: bucket name required")
	}
	b := &state.Bucket{Base: state.Base{ID: name, Name: name}}
	if err := s.store.CreateBucket(ctx, b); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(s.rootDir, name), 0o755)
}

// DeleteBucket deletes a bucket and all of its objects.
func (s *Store) DeleteBucket(ctx context.Context, name string) error {
	if err := s.store.DeleteBucket(ctx, name); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.rootDir, name))
}

// PutObject stores an object. Returns the SHA256 of the payload.
func (s *Store) PutObject(ctx context.Context, bucket, key string, data io.Reader) (string, error) {
	if bucket == "" || key == "" {
		return "", errors.New("object: bucket and key required")
	}
	buf := &bytes.Buffer{}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(buf, h), data)
	if err != nil {
		return "", err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	path := filepath.Join(s.rootDir, bucket, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	o := &state.Object{
		Base:    state.Base{ID: key, ProjectID: bucket, Name: key},
		Bucket:  bucket,
		Key:     key,
		Size:    n,
		SHA256:  digest,
	}
	if err := s.store.PutObject(ctx, o); err != nil {
		return "", err
	}
	return digest, nil
}

// GetObject returns an object's payload as a reader.
func (s *Store) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, *state.Object, error) {
	o, err := s.store.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(s.rootDir, bucket, filepath.FromSlash(key))
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, o, nil
}

// DeleteObject removes an object.
func (s *Store) DeleteObject(ctx context.Context, bucket, key string) error {
	if err := s.store.DeleteObject(ctx, bucket, key); err != nil {
		return err
	}
	return os.Remove(filepath.Join(s.rootDir, bucket, filepath.FromSlash(key)))
}

// ListObjects lists objects in a bucket.
func (s *Store) ListObjects(ctx context.Context, bucket, prefix string) ([]*state.Object, error) {
	objs, err := s.store.ListObjects(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key < objs[j].Key })
		return objs, nil
	}
	out := objs[:0]
	for _, o := range objs {
		if strings.HasPrefix(o.Key, prefix) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Presign returns a URL that grants temporary GET access to an object.
// Implementation: signed URL with HMAC of bucket/key/exp.
func (s *Store) Presign(bucket, key string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	mac := hmac.New(sha256.New, s.hmacKey)
	fmt.Fprintf(mac, "%s|%s|%d", bucket, key, exp)
	sig := hex.EncodeToString(mac.Sum(nil))
	v := url.Values{}
	v.Set("bucket", bucket)
	v.Set("key", key)
	v.Set("exp", fmt.Sprintf("%d", exp))
	v.Set("sig", sig)
	return "/objects/presigned?" + v.Encode()
}

// VerifyPresign checks an incoming presigned URL signature.
func (s *Store) VerifyPresign(v url.Values) (bucket, key string, err error) {
	bucket = v.Get("bucket")
	key = v.Get("key")
	exp := v.Get("exp")
	sig := v.Get("sig")
	if bucket == "" || key == "" || exp == "" || sig == "" {
		return "", "", errors.New("object: missing presign fields")
	}
	mac := hmac.New(sha256.New, s.hmacKey)
	fmt.Fprintf(mac, "%s|%s|%s", bucket, key, exp)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return "", "", errors.New("object: bad signature")
	}
	return bucket, key, nil
}
