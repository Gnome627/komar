package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Kind describes a resource type komar can list.
type Kind struct {
	Name       string // plural display name, e.g. "Deployments"
	Short      string // kubectl short name, e.g. "deploy"
	Resource   string // API resource, e.g. "deployments"
	Group      string
	Version    string
	KindName   string // e.g. "Deployment"
	Namespaced bool
}

func (k Kind) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: k.Group, Version: k.Version, Resource: k.Resource}
}

// Ref is how kubectl names the type, e.g. "deployments.apps".
func (k Kind) Ref() string {
	if k.Group == "" {
		return k.Resource
	}
	return k.Resource + "." + k.Group
}

func (k Kind) Is(resource string) bool { return k.Resource == resource }

// Scalable kinds have a /scale subresource we drive.
func (k Kind) Scalable() bool {
	switch k.Resource {
	case "deployments", "statefulsets", "replicasets", "replicationcontrollers":
		return true
	}
	return false
}

// Rollable kinds support kubectl rollout.
func (k Kind) Rollable() bool {
	switch k.Resource {
	case "deployments", "statefulsets", "daemonsets":
		return k.Group == "apps"
	}
	return false
}

// HasPods kinds own pods we can show logs for.
func (k Kind) HasPods() bool {
	switch k.Resource {
	case "pods", "deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "services", "replicationcontrollers":
		return true
	}
	return false
}

// Builtin kinds, in the order the tabs cycle through them.
var Builtin = []Kind{
	{"Pods", "po", "pods", "", "v1", "Pod", true},
	{"Deployments", "deploy", "deployments", "apps", "v1", "Deployment", true},
	{"StatefulSets", "sts", "statefulsets", "apps", "v1", "StatefulSet", true},
	{"DaemonSets", "ds", "daemonsets", "apps", "v1", "DaemonSet", true},
	{"Jobs", "job", "jobs", "batch", "v1", "Job", true},
	{"CronJobs", "cj", "cronjobs", "batch", "v1", "CronJob", true},
	{"Services", "svc", "services", "", "v1", "Service", true},
	{"Ingresses", "ing", "ingresses", "networking.k8s.io", "v1", "Ingress", true},
	{"ConfigMaps", "cm", "configmaps", "", "v1", "ConfigMap", true},
	{"Secrets", "secret", "secrets", "", "v1", "Secret", true},
	{"PVCs", "pvc", "persistentvolumeclaims", "", "v1", "PersistentVolumeClaim", true},
	{"Nodes", "no", "nodes", "", "v1", "Node", false},
}

// ReplicaSetKind is used for rollout history and pod owners.
var ReplicaSetKind = Kind{"ReplicaSets", "rs", "replicasets", "apps", "v1", "ReplicaSet", true}

// KindFor finds a kind by resource or kind name among builtins and the
// discovered list.
func (cl *Cluster) KindFor(name string) (Kind, bool) {
	name = strings.ToLower(name)
	match := func(k Kind) bool {
		return k.Resource == name || strings.ToLower(k.KindName) == name || k.Short == name || k.Ref() == name
	}
	for _, k := range Builtin {
		if match(k) {
			return k, true
		}
	}
	if match(ReplicaSetKind) {
		return ReplicaSetKind, true
	}
	for _, k := range cl.cachedResources() {
		if match(k) {
			return k, true
		}
	}
	return Kind{}, false
}

func (cl *Cluster) cachedResources() []Kind {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.resources
}

// Resources discovers every listable resource type, CRDs included.
func (cl *Cluster) Resources(ctx context.Context) ([]Kind, error) {
	if r := cl.cachedResources(); r != nil {
		return r, nil
	}
	lists, err := cl.Discovery.ServerPreferredResources()
	// Partial discovery failures (a broken aggregated API) still return data.
	if len(lists) == 0 && err != nil {
		return nil, err
	}
	var out []Kind
	seen := map[string]bool{}
	for _, l := range lists {
		gv, perr := schema.ParseGroupVersion(l.GroupVersion)
		if perr != nil {
			continue
		}
		for _, r := range l.APIResources {
			if strings.Contains(r.Name, "/") || !hasVerb(r.Verbs, "list") {
				continue
			}
			k := Kind{
				Name: r.Kind, Resource: r.Name, Group: gv.Group, Version: gv.Version,
				KindName: r.Kind, Namespaced: r.Namespaced,
			}
			if len(r.ShortNames) > 0 {
				k.Short = r.ShortNames[0]
			}
			if seen[k.Ref()] {
				continue
			}
			seen[k.Ref()] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	cl.mu.Lock()
	cl.resources = out
	cl.mu.Unlock()
	return out, nil
}

// HasMetrics reports whether metrics.k8s.io is served (metrics-server).
func (cl *Cluster) HasMetrics() bool {
	cl.mu.Lock()
	if cl.hasMetrics != nil {
		defer cl.mu.Unlock()
		return *cl.hasMetrics
	}
	cl.mu.Unlock()
	has := false
	if groups, err := cl.Discovery.ServerGroups(); err == nil {
		for _, g := range groups.Groups {
			if g.Name == "metrics.k8s.io" {
				has = true
			}
		}
	}
	cl.mu.Lock()
	cl.hasMetrics = &has
	cl.mu.Unlock()
	return has
}

func hasVerb(verbs []string, v string) bool {
	for _, x := range verbs {
		if x == v {
			return true
		}
	}
	return false
}

// Column of a server-side table.
type Column struct {
	Name     string
	Type     string
	Priority int32
}

// Row of a server-side table plus the metadata komar needs.
type Row struct {
	Name      string
	Namespace string
	UID       string
	Cells     []string
	Created   time.Time
	Deleting  bool
	Labels    map[string]string
	Owners    []metav1.OwnerReference
}

// Key identifies a row across refreshes.
func (r Row) Key() string { return r.Namespace + "/" + r.Name }

// Table is what `kubectl get` prints, as data.
type Table struct {
	Kind    Kind
	Columns []Column
	Rows    []Row
}

// Col returns the index of a column by (case-insensitive) name or -1.
func (t *Table) Col(name string) int {
	for i, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}

// ListOptions narrow a table listing.
type ListOptions struct {
	Namespace     string // "" means all namespaces
	LabelSelector string
	FieldSelector string
}

// ListTable asks the API server for its table rendering of a resource, the
// same thing kubectl get prints, so every kind (CRDs too) gets sensible
// columns without per-kind code.
func (cl *Cluster) ListTable(ctx context.Context, k Kind, opts ListOptions) (*Table, error) {
	path := apiPath(k, opts.Namespace)
	req := cl.Clientset.Discovery().RESTClient().Get().AbsPath(path).
		SetHeader("Accept", "application/json;as=Table;v=v1;g=meta.k8s.io,application/json")
	if opts.LabelSelector != "" {
		req = req.Param("labelSelector", opts.LabelSelector)
	}
	if opts.FieldSelector != "" {
		req = req.Param("fieldSelector", opts.FieldSelector)
	}
	raw, err := req.DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var mt metav1.Table
	if err := json.Unmarshal(raw, &mt); err != nil {
		return nil, fmt.Errorf("decode table: %w", err)
	}
	t := &Table{Kind: k}
	for _, c := range mt.ColumnDefinitions {
		t.Columns = append(t.Columns, Column{Name: c.Name, Type: c.Type, Priority: c.Priority})
	}
	for _, r := range mt.Rows {
		row := Row{Cells: make([]string, len(r.Cells))}
		for i, c := range r.Cells {
			row.Cells[i] = cellString(c)
		}
		if len(r.Object.Raw) > 0 {
			var meta metav1.PartialObjectMetadata
			if err := json.Unmarshal(r.Object.Raw, &meta); err == nil {
				row.Name = meta.Name
				row.Namespace = meta.Namespace
				row.UID = string(meta.UID)
				row.Created = meta.CreationTimestamp.Time
				row.Deleting = meta.DeletionTimestamp != nil
				row.Labels = meta.Labels
				row.Owners = meta.OwnerReferences
			}
		}
		if row.Name == "" && len(row.Cells) > 0 {
			row.Name = row.Cells[0]
		}
		t.Rows = append(t.Rows, row)
	}
	return t, nil
}

func apiPath(k Kind, ns string) string {
	base := "/api/" + k.Version
	if k.Group != "" {
		base = "/apis/" + k.Group + "/" + k.Version
	}
	if k.Namespaced && ns != "" {
		return base + "/namespaces/" + ns + "/" + k.Resource
	}
	return base + "/" + k.Resource
}

func cellString(c any) string {
	switch v := c.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = cellString(x)
		}
		return strings.Join(parts, ",")
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// Age formats a duration the way kubectl does (5s, 3m, 2h, 4d).
func Age(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
