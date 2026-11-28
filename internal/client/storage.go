package client

import (
	"context"
	"net/http"
	"net/url"
)

// Storage buckets and S3 access keys (docs/API.md "Storage" and "S3-compatible buckets").

// Bucket is a storage bucket. Kind is "s3" (S3-compatible) or "vault" (end-to-end encrypted by the client).
type Bucket struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Visibility   string `json:"visibility,omitempty"`
	DataShards   int64  `json:"data_shards"`
	ParityShards int64  `json:"parity_shards"`
	NodeCount    int64  `json:"node_count"`
	Endpoint     string `json:"endpoint,omitempty"`
	PublicURL    string `json:"public_url,omitempty"`
	ObjectCount  int64  `json:"object_count"`
	TotalBytes   int64  `json:"total_bytes"`
	CreatedAt    string `json:"created_at"`
}

// Nodes returns how many storage nodes hold each file.
func (b *Bucket) Nodes() int64 {
	if b.NodeCount > 0 {
		return b.NodeCount
	}
	return b.DataShards + b.ParityShards
}

// CreateBucketInput creates a bucket.
type CreateBucketInput struct {
	Name       string `json:"name"`
	Kind       string `json:"kind,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	NodeCount  int64  `json:"node_count,omitempty"`
}

// CreateBucket creates a bucket.
func (c *Client) CreateBucket(ctx context.Context, in CreateBucketInput) (*Bucket, error) {
	var out struct {
		Bucket *Bucket `json:"bucket"`
	}
	if err := c.Do(ctx, http.MethodPost, "/api/v1/buckets", in, &out); err != nil {
		return nil, err
	}
	return out.Bucket, nil
}

// ListBuckets lists the account's buckets.
func (c *Client) ListBuckets(ctx context.Context) ([]Bucket, error) {
	var out struct {
		Buckets []Bucket `json:"buckets"`
	}
	return out.Buckets, c.Do(ctx, http.MethodGet, "/api/v1/buckets", nil, &out)
}

// GetBucket finds a bucket by name; a missing bucket is a not-found *Error.
func (c *Client) GetBucket(ctx context.Context, name string) (*Bucket, error) {
	bs, err := c.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}
	for i := range bs {
		if bs[i].Name == name {
			return &bs[i], nil
		}
	}
	return nil, &Error{Status: http.StatusNotFound, Code: "not_found", Message: "bucket not found"}
}

// SetBucketVisibility makes an s3 bucket private or public.
func (c *Client) SetBucketVisibility(ctx context.Context, name, visibility string) (*Bucket, error) {
	var out struct {
		Bucket *Bucket `json:"bucket"`
	}
	if err := c.Do(ctx, http.MethodPatch, "/api/v1/buckets/"+url.PathEscape(name), map[string]string{"visibility": visibility}, &out); err != nil {
		return nil, err
	}
	return out.Bucket, nil
}

// DeleteBucket deletes an empty bucket.
func (c *Client) DeleteBucket(ctx context.Context, name string) error {
	err := c.Do(ctx, http.MethodDelete, "/api/v1/buckets/"+url.PathEscape(name), nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ObjectKeys lists the keys of a bucket's objects.
func (c *Client) ObjectKeys(ctx context.Context, bucket string) ([]string, error) {
	var out struct {
		Objects []struct {
			Key string `json:"key"`
		} `json:"objects"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/v1/buckets/"+url.PathEscape(bucket)+"/objects", nil, &out); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(out.Objects))
	for _, o := range out.Objects {
		keys = append(keys, o.Key)
	}
	return keys, nil
}

// DeleteObject deletes one object.
func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	err := c.Do(ctx, http.MethodDelete, "/api/v1/buckets/"+url.PathEscape(bucket)+"/objects/"+escapeKey(key), nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func escapeKey(key string) string {
	u := url.URL{Path: key}
	return u.EscapedPath()
}

// AccessKey is an S3 access key. Secret is returned only when the key is created.
type AccessKey struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	AccessKeyID     string  `json:"access_key_id"`
	SecretAccessKey string  `json:"secret_access_key,omitempty"`
	BucketID        *string `json:"bucket_id"`
	Bucket          string  `json:"bucket,omitempty"`
	ReadOnly        bool    `json:"read_only"`
	CreatedAt       string  `json:"created_at"`
}

// CreateAccessKeyInput creates an S3 access key; Bucket limits it to one bucket.
type CreateAccessKeyInput struct {
	Name     string `json:"name,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// AccessKeyCreated is a new key with the S3 endpoint to use it with.
type AccessKeyCreated struct {
	AccessKey AccessKey `json:"access_key"`
	Endpoint  string    `json:"endpoint"`
	Region    string    `json:"region"`
}

// CreateAccessKey creates an S3 access key.
func (c *Client) CreateAccessKey(ctx context.Context, in CreateAccessKeyInput) (*AccessKeyCreated, error) {
	var out AccessKeyCreated
	if err := c.Do(ctx, http.MethodPost, "/api/v1/s3/access-keys", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAccessKey reads an access key (without its secret).
func (c *Client) GetAccessKey(ctx context.Context, id string) (*AccessKey, error) {
	var out struct {
		AccessKey *AccessKey `json:"access_key"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/v1/s3/access-keys/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return out.AccessKey, nil
}

// DeleteAccessKey revokes an access key.
func (c *Client) DeleteAccessKey(ctx context.Context, id string) error {
	err := c.Do(ctx, http.MethodDelete, "/api/v1/s3/access-keys/"+url.PathEscape(id), nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// S3Info describes the S3 endpoint.
type S3Info struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
}

// GetS3Info reads the S3 endpoint settings.
func (c *Client) GetS3Info(ctx context.Context) (*S3Info, error) {
	var out S3Info
	return &out, c.Do(ctx, http.MethodGet, "/api/v1/s3", nil, &out)
}
