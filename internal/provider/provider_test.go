package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/peernode-cloud/terraform-provider-peernode/internal/client"
)

// fakeAPI imitates the cluster endpoints of the PeerNode API (docs/API.md "Kubernetes clusters").
type fakeAPI struct {
	mu       sync.Mutex
	clusters map[string]*client.Cluster
	polls    map[string]int // GETs until a provisioning cluster is ready
	issued   map[string]int // kubeconfigs issued per cluster
	puts     int
	seq      int
	storage  *fakeStorage
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{clusters: map[string]*client.Cluster{}, polls: map[string]int{}, issued: map[string]int{}, storage: newFakeStorage()}
}

func apiErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": code}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func sized(cl *client.Cluster) {
	s := cl.Spec
	switch s.Mode {
	case "autopilot":
		cl.Size = client.Size{CPUMilli: int64(s.Autopilot.MaxCPU * 1000), MemoryBytes: int64(s.Autopilot.MaxMemoryGB * (1 << 30)), DiskBytes: s.Autopilot.MaxDiskGB << 30}
		cl.HourlyMicro = 0
	case "auto":
		cl.Size = client.Size{CPUMilli: int64(s.Auto.CPU * 1000), MemoryBytes: int64(s.Auto.MemoryGB * (1 << 30)), DiskBytes: s.Auto.DiskGB << 30}
		cl.HourlyMicro = 1000
	case "pools":
		cl.Size = client.Size{}
		for _, p := range s.Pools {
			cl.Size.CPUMilli += p.Count * int64(p.CPU*1000)
			cl.Size.MemoryBytes += p.Count * int64(p.MemoryGB*(1<<30))
			cl.Size.DiskBytes += p.Count * p.DiskGB << 30
		}
		cl.HourlyMicro = 2000
	}
}

func normalize(s *client.Spec) bool {
	if s.Mode == "" {
		s.Mode = "autopilot"
	}
	switch s.Mode {
	case "autopilot":
		if s.Auto != nil || len(s.Pools) > 0 {
			return false
		}
		if s.Autopilot == nil {
			s.Autopilot = &client.AutopilotSpec{MaxCPU: 4, MaxMemoryGB: 8, MaxDiskGB: 20}
		}
	case "auto":
		return s.Auto != nil && s.Autopilot == nil && len(s.Pools) == 0
	case "pools":
		return len(s.Pools) > 0 && s.Autopilot == nil && s.Auto == nil
	default:
		return false
	}
	return true
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer pnk_test" {
		apiErr(w, 401, "unauthorized")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/api/v1/buckets") || strings.HasPrefix(r.URL.Path, "/api/v1/s3") {
		f.storage.serve(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/k8s/clusters")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		var out []client.Cluster
		for _, c := range f.clusters {
			out = append(out, *c)
		}
		writeJSON(w, 200, map[string]any{"clusters": out})
	case rest == "" && r.Method == http.MethodPost:
		var body struct {
			Name string `json:"name"`
			client.Spec
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, c := range f.clusters {
			if c.Name == body.Name {
				apiErr(w, 409, "name_taken")
				return
			}
		}
		if !normalize(&body.Spec) {
			apiErr(w, 400, "invalid_spec")
			return
		}
		f.seq++
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", f.seq)
		cl := &client.Cluster{ID: id, Name: body.Name, Status: "provisioning", Spec: body.Spec, PublicHosts: []string{},
			CreatedAt: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)}
		sized(cl)
		f.clusters[id] = cl
		f.polls[id] = 2
		writeJSON(w, 201, map[string]any{"cluster": cl})
	default:
		cl := f.clusters[parts[0]]
		if cl == nil {
			apiErr(w, 404, "not_found")
			return
		}
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			if cl.Status == "deleting" {
				delete(f.clusters, cl.ID)
			} else if cl.Status == "provisioning" {
				if f.polls[cl.ID]--; f.polls[cl.ID] <= 0 {
					cl.Status = "ready"
				}
			}
			writeJSON(w, 200, map[string]any{"cluster": cl})
		case len(parts) == 1 && r.Method == http.MethodDelete:
			cl.Status = "deleting"
			w.WriteHeader(204)
		case len(parts) == 2 && parts[1] == "spec" && r.Method == http.MethodPut:
			var s client.Spec
			_ = json.NewDecoder(r.Body).Decode(&s)
			if s.Mode != cl.Spec.Mode {
				apiErr(w, 409, "mode_change")
				return
			}
			if !normalize(&s) {
				apiErr(w, 400, "invalid_spec")
				return
			}
			f.puts++
			cl.Spec = s
			sized(cl)
			writeJSON(w, 200, map[string]any{"cluster": cl})
		case len(parts) == 2 && parts[1] == "kubeconfig" && r.Method == http.MethodPost:
			f.issued[cl.ID]++
			cl.HasKubeconfig = true
			writeJSON(w, 200, map[string]any{"kubeconfig": fmt.Sprintf("apiVersion: v1\n# token %d\n", f.issued[cl.ID]), "filename": cl.Name + ".kubeconfig"})
		default:
			apiErr(w, 404, "not_found")
		}
	}
}

func setup(t *testing.T) (*fakeAPI, map[string]func() (tfprotov6.ProviderServer, error)) {
	t.Helper()
	PollInterval = 10 * time.Millisecond
	f := newFakeAPI()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("PEERNODE_ENDPOINT", srv.URL)
	t.Setenv("PEERNODE_TOKEN", "pnk_test")
	return f, map[string]func() (tfprotov6.ProviderServer, error){
		"peernode": providerserver.NewProtocol6WithError(New("test")()),
	}
}

func TestAutopilotClusterLifecycle(t *testing.T) {
	f, factories := setup(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				// No cap: the platform default shows up after apply.
				Config: `
resource "peernode_kubernetes_cluster" "a" { name = "web" }
resource "peernode_kubernetes_kubeconfig" "a" { cluster_id = peernode_kubernetes_cluster.a.id }
data "peernode_kubernetes_cluster" "by_name" {
  name       = "web"
  depends_on = [peernode_kubernetes_cluster.a]
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.a", "mode", "autopilot"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.a", "status", "ready"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.a", "autopilot.max_cpu", "4"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.a", "autopilot.max_disk_gb", "20"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.a", "size_cpu_milli", "4000"),
					resource.TestCheckResourceAttr("peernode_kubernetes_kubeconfig.a", "filename", "web.kubeconfig"),
					resource.TestMatchResourceAttr("peernode_kubernetes_kubeconfig.a", "kubeconfig", regexp.MustCompile("token 1")),
					resource.TestCheckResourceAttrPair("data.peernode_kubernetes_cluster.by_name", "id", "peernode_kubernetes_cluster.a", "id"),
				),
			},
			{
				// Setting a cap resizes in place; the kubeconfig is not reissued.
				Config: `
resource "peernode_kubernetes_cluster" "a" {
  name      = "web"
  autopilot = { max_cpu = 2.5, max_memory_gb = 4, max_disk_gb = 10 }
}
resource "peernode_kubernetes_kubeconfig" "a" { cluster_id = peernode_kubernetes_cluster.a.id }`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.a", "autopilot.max_cpu", "2.5"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.a", "size_cpu_milli", "2500"),
					resource.TestMatchResourceAttr("peernode_kubernetes_kubeconfig.a", "kubeconfig", regexp.MustCompile("token 1")),
					func(*terraform.State) error {
						if f.puts != 1 {
							return fmt.Errorf("expected one PUT spec, got %d", f.puts)
						}
						return nil
					},
				),
			},
			{
				// Rotating the token through keepers.
				Config: `
resource "peernode_kubernetes_cluster" "a" {
  name      = "web"
  autopilot = { max_cpu = 2.5, max_memory_gb = 4, max_disk_gb = 10 }
}
resource "peernode_kubernetes_kubeconfig" "a" {
  cluster_id = peernode_kubernetes_cluster.a.id
  keepers    = { rotated = "1" }
}`,
				Check: resource.TestMatchResourceAttr("peernode_kubernetes_kubeconfig.a", "kubeconfig", regexp.MustCompile("token 2")),
			},
			{
				ResourceName:      "peernode_kubernetes_cluster.a",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.clusters) != 0 {
				return fmt.Errorf("%d clusters left", len(f.clusters))
			}
			return nil
		},
	})
}

func TestPoolsAndAutoClusters(t *testing.T) {
	f, factories := setup(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "peernode_kubernetes_cluster" "p" {
  name  = "batch"
  mode  = "pools"
  pools = [
    { name = "big", count = 1, cpu = 1, memory_gb = 1 },
    { name = "small", count = 2, cpu = 0.25, memory_gb = 0.5, disk_gb = 5 },
  ]
}
resource "peernode_kubernetes_cluster" "x" {
  name = "auto"
  mode = "auto"
  auto = { cpu = 1.5, memory_gb = 2 }
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.p", "pools.#", "2"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.p", "pools.0.disk_gb", "0"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.p", "size_cpu_milli", "1500"),
					resource.TestCheckNoResourceAttr("peernode_kubernetes_cluster.p", "autopilot.max_cpu"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.x", "auto.cpu", "1.5"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.x", "auto.disk_gb", "0"),
					resource.TestCheckNoResourceAttr("peernode_kubernetes_cluster.x", "auto.min_pod_cpu"),
				),
			},
			{
				// Count change: in place.
				Config: `
resource "peernode_kubernetes_cluster" "p" {
  name  = "batch"
  mode  = "pools"
  pools = [
    { name = "big", count = 1, cpu = 1, memory_gb = 1 },
    { name = "small", count = 3, cpu = 0.25, memory_gb = 0.5, disk_gb = 5 },
  ]
}
resource "peernode_kubernetes_cluster" "x" {
  name = "auto"
  mode = "auto"
  auto = { cpu = 2, memory_gb = 3, min_pod_cpu = 1 }
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.p", "pools.1.count", "3"),
					resource.TestCheckResourceAttr("peernode_kubernetes_cluster.x", "auto.min_pod_cpu", "1"),
					func(*terraform.State) error {
						if f.puts != 2 {
							return fmt.Errorf("expected two in-place resizes, got %d PUTs", f.puts)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestModeChangeReplaces(t *testing.T) {
	_, factories := setup(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: `resource "peernode_kubernetes_cluster" "a" { name = "m" }`},
			{
				Config: `
resource "peernode_kubernetes_cluster" "a" {
  name = "m"
  mode = "auto"
  auto = { cpu = 1, memory_gb = 1 }
}`,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("peernode_kubernetes_cluster.a", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.TestCheckResourceAttr("peernode_kubernetes_cluster.a", "mode", "auto"),
			},
		},
	})
}

func TestValidation(t *testing.T) {
	_, factories := setup(t)
	for name, cfg := range map[string]string{
		"auto without size":   "resource \"peernode_kubernetes_cluster\" \"a\" {\n name = \"v\"\n mode = \"auto\"\n}",
		"pools without pools": "resource \"peernode_kubernetes_cluster\" \"a\" {\n name = \"v\"\n mode = \"pools\"\n}",
		"pools in autopilot":  "resource \"peernode_kubernetes_cluster\" \"a\" {\n name = \"v\"\n pools = [{ name = \"a\", count = 1, cpu = 1, memory_gb = 1 }]\n}",
		"unknown mode":        "resource \"peernode_kubernetes_cluster\" \"a\" {\n name = \"v\"\n mode = \"spot\"\n}",
	} {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps:                    []resource.TestStep{{Config: cfg, PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)invalid|value must be one of`)}},
			})
		})
	}
}
