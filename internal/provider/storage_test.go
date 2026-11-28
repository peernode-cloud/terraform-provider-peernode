package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/peernode-cloud/terraform-provider-peernode/internal/client"
)

// fakeStorage imitates the bucket and S3 access-key endpoints (docs/API.md "S3-compatible buckets").
type fakeStorage struct {
	buckets map[string]*client.Bucket // by name
	objects map[string][]string       // bucket -> keys
	keys    map[string]*client.AccessKey
	seq     int
	patches int
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{buckets: map[string]*client.Bucket{}, objects: map[string][]string{}, keys: map[string]*client.AccessKey{}}
}

func (s *fakeStorage) view(b *client.Bucket) *client.Bucket {
	out := *b
	out.PublicURL = ""
	if b.Kind == "s3" {
		out.Endpoint = "https://s3.test"
		if b.Visibility == "public" {
			out.PublicURL = "https://s3.test/" + b.Name + "/"
		}
	}
	out.ObjectCount = int64(len(s.objects[b.Name]))
	return &out
}

func (s *fakeStorage) serve(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/v1/buckets" && r.Method == http.MethodGet:
		out := []*client.Bucket{}
		for _, b := range s.buckets {
			out = append(out, s.view(b))
		}
		writeJSON(w, 200, map[string]any{"buckets": out})
	case p == "/api/v1/buckets" && r.Method == http.MethodPost:
		var in client.CreateBucketInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Kind == "" {
			in.Kind = "vault"
		}
		if s.buckets[in.Name] != nil {
			apiErr(w, 409, "name_taken")
			return
		}
		n := in.NodeCount
		if in.Kind == "s3" {
			if n == 0 {
				n = 2
			}
			if n < 2 {
				apiErr(w, 400, "invalid_node_count")
				return
			}
			if in.Visibility == "" {
				in.Visibility = "private"
			}
		} else if in.Visibility != "" {
			apiErr(w, 400, "invalid_visibility")
			return
		} else if n == 0 {
			n = 3
		}
		s.seq++
		b := &client.Bucket{ID: fmt.Sprintf("b-%d", s.seq), Name: in.Name, Kind: in.Kind, Visibility: in.Visibility, DataShards: max(1, n-1), ParityShards: min(1, n-1),
			NodeCount: n, CreatedAt: "2026-11-28T10:00:00Z"}
		if in.Kind == "vault" {
			b.Visibility = "private" // the API reports it; the provider ignores it for vaults
		}
		s.buckets[in.Name] = b
		writeJSON(w, 201, map[string]any{"bucket": s.view(b)})
	case strings.HasPrefix(p, "/api/v1/buckets/"):
		rest := strings.TrimPrefix(p, "/api/v1/buckets/")
		name, tail, _ := strings.Cut(rest, "/")
		b := s.buckets[name]
		if b == nil {
			apiErr(w, 404, "not_found")
			return
		}
		switch {
		case tail == "" && r.Method == http.MethodPatch:
			var in struct{ Visibility string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			s.patches++
			b.Visibility = in.Visibility
			writeJSON(w, 200, map[string]any{"bucket": s.view(b)})
		case tail == "" && r.Method == http.MethodDelete:
			if len(s.objects[name]) > 0 {
				apiErr(w, 409, "not_empty")
				return
			}
			delete(s.buckets, name)
			w.WriteHeader(204)
		case tail == "objects" && r.Method == http.MethodGet:
			var out []map[string]string
			for _, k := range s.objects[name] {
				out = append(out, map[string]string{"key": k})
			}
			writeJSON(w, 200, map[string]any{"objects": out})
		case strings.HasPrefix(tail, "objects/") && r.Method == http.MethodDelete:
			key := strings.TrimPrefix(tail, "objects/")
			var left []string
			for _, k := range s.objects[name] {
				if k != key {
					left = append(left, k)
				}
			}
			s.objects[name] = left
			w.WriteHeader(204)
		default:
			apiErr(w, 404, "not_found")
		}
	case p == "/api/v1/s3/access-keys" && r.Method == http.MethodPost:
		var in client.CreateAccessKeyInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Bucket != "" && (s.buckets[in.Bucket] == nil || s.buckets[in.Bucket].Kind != "s3") {
			apiErr(w, 404, "not_found")
			return
		}
		s.seq++
		k := &client.AccessKey{ID: fmt.Sprintf("k-%d", s.seq), Name: in.Name, AccessKeyID: fmt.Sprintf("PNKEY%015d", s.seq), Bucket: in.Bucket, ReadOnly: in.ReadOnly}
		s.keys[k.ID] = k
		out := *k
		out.SecretAccessKey = fmt.Sprintf("secret-%d", s.seq)
		writeJSON(w, 201, map[string]any{"access_key": out, "endpoint": "https://s3.test", "region": "us-east-1"})
	case strings.HasPrefix(p, "/api/v1/s3/access-keys/"):
		id := strings.TrimPrefix(p, "/api/v1/s3/access-keys/")
		k := s.keys[id]
		if k == nil {
			apiErr(w, 404, "not_found")
			return
		}
		if r.Method == http.MethodDelete {
			delete(s.keys, id)
			w.WriteHeader(204)
			return
		}
		writeJSON(w, 200, map[string]any{"access_key": k})
	default:
		apiErr(w, 404, "not_found")
	}
}

func TestStorageBucketsAndKeys(t *testing.T) {
	f, factories := setup(t)
	st := f.storage
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "peernode_storage_bucket" "site" { name = "my-site" }
resource "peernode_storage_bucket" "vault" {
  name = "secrets"
  kind = "vault"
}
resource "peernode_storage_access_key" "ci" {
  name      = "ci"
  bucket    = peernode_storage_bucket.site.name
  read_only = true
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("peernode_storage_bucket.site", "kind", "s3"),
					resource.TestCheckResourceAttr("peernode_storage_bucket.site", "visibility", "private"),
					resource.TestCheckResourceAttr("peernode_storage_bucket.site", "node_count", "2"),
					resource.TestCheckResourceAttr("peernode_storage_bucket.site", "endpoint", "https://s3.test"),
					resource.TestCheckResourceAttr("peernode_storage_bucket.site", "public_url", ""),
					resource.TestCheckResourceAttr("peernode_storage_bucket.vault", "kind", "vault"),
					resource.TestCheckNoResourceAttr("peernode_storage_bucket.vault", "visibility"),
					resource.TestCheckResourceAttr("peernode_storage_bucket.vault", "node_count", "3"),
					resource.TestCheckResourceAttr("peernode_storage_access_key.ci", "secret_access_key", "secret-3"),
					resource.TestCheckResourceAttr("peernode_storage_access_key.ci", "endpoint", "https://s3.test"),
					resource.TestCheckResourceAttr("peernode_storage_access_key.ci", "region", "us-east-1"),
					resource.TestMatchResourceAttr("peernode_storage_access_key.ci", "access_key_id", regexp.MustCompile(`^PNKEY`)),
				),
			},
			{
				// Visibility changes in place; the key stays.
				Config: `
resource "peernode_storage_bucket" "site" {
  name       = "my-site"
  visibility = "public"
}
resource "peernode_storage_bucket" "vault" {
  name = "secrets"
  kind = "vault"
}
resource "peernode_storage_access_key" "ci" {
  name      = "ci"
  bucket    = peernode_storage_bucket.site.name
  read_only = true
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("peernode_storage_bucket.site", "visibility", "public"),
					resource.TestCheckResourceAttr("peernode_storage_bucket.site", "public_url", "https://s3.test/my-site/"),
					resource.TestCheckResourceAttr("peernode_storage_access_key.ci", "secret_access_key", "secret-3"),
					func(*terraform.State) error {
						if st.patches != 1 {
							return fmt.Errorf("expected one visibility change, got %d", st.patches)
						}
						return nil
					},
				),
			},
			{
				// Rotation: a new key replaces the old one, which is revoked.
				Config: `
resource "peernode_storage_bucket" "site" {
  name          = "my-site"
  visibility    = "public"
  force_destroy = true
}
resource "peernode_storage_access_key" "ci" {
  name      = "ci"
  bucket    = peernode_storage_bucket.site.name
  read_only = true
  keepers   = { rotated = "2026-12" }
}`,
				PreConfig: func() {
					f.mu.Lock()
					st.objects["my-site"] = []string{"index.html", "img/logo.png"}
					f.mu.Unlock()
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("peernode_storage_access_key.ci", "secret_access_key", "secret-4"),
					func(*terraform.State) error {
						if len(st.keys) != 1 || st.buckets["secrets"] != nil {
							return fmt.Errorf("keys %d, vault bucket left: %v", len(st.keys), st.buckets["secrets"] != nil)
						}
						return nil
					},
				),
			},
			{
				ResourceName:                         "peernode_storage_bucket.site",
				ImportState:                          true,
				ImportStateId:                        "my-site",
				ImportStateVerify:                    true,
				ImportStateVerifyIgnore:              []string{"force_destroy"},
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(st.buckets) != 0 || len(st.keys) != 0 {
				return fmt.Errorf("left: %d buckets, %d keys", len(st.buckets), len(st.keys))
			}
			return nil
		},
	})
}

func TestStorageBucketValidation(t *testing.T) {
	_, factories := setup(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config:      `resource "peernode_storage_bucket" "x" { name = "Bad_Name" }`,
				ExpectError: regexp.MustCompile(`lowercase`),
			},
			{
				Config: `
resource "peernode_storage_bucket" "x" {
  name       = "vault-one"
  kind       = "vault"
  visibility = "public"
}`,
				ExpectError: regexp.MustCompile(`Only s3 buckets`),
			},
			{
				Config: `
resource "peernode_storage_bucket" "x" {
  name       = "one-node"
  node_count = 1
}`,
				ExpectError: regexp.MustCompile(`at least 2`),
			},
		},
	})
}
