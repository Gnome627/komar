package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

func (cl *Cluster) res(k Kind, ns string) interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions, sub ...string) (*unstructured.Unstructured, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions, sub ...string) error
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, sub ...string) (*unstructured.Unstructured, error)
} {
	r := cl.Dynamic.Resource(k.GVR())
	if k.Namespaced {
		return r.Namespace(ns)
	}
	return r
}

// Get fetches one object.
func (cl *Cluster) Get(ctx context.Context, k Kind, ns, name string) (*unstructured.Unstructured, error) {
	return cl.res(k, ns).Get(ctx, name, metav1.GetOptions{})
}

// YAML returns the object as YAML without the managedFields noise.
func (cl *Cluster) YAML(ctx context.Context, k Kind, ns, name string) (string, error) {
	obj, err := cl.Get(ctx, k, ns, name)
	if err != nil {
		return "", err
	}
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
	b, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Delete removes an object. Dependents (a job's pods) go in the background,
// like kubectl. force sets grace period 0.
func (cl *Cluster) Delete(ctx context.Context, k Kind, ns, name string, force bool) error {
	prop := metav1.DeletePropagationBackground
	opts := metav1.DeleteOptions{PropagationPolicy: &prop}
	if force {
		zero := int64(0)
		opts.GracePeriodSeconds = &zero
	}
	return cl.res(k, ns).Delete(ctx, name, opts)
}

// Replicas reads spec.replicas through the scale subresource.
func (cl *Cluster) Replicas(ctx context.Context, k Kind, ns, name string) (desired, ready int, err error) {
	obj, err := cl.Get(ctx, k, ns, name)
	if err != nil {
		return 0, 0, err
	}
	d, _, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	r, _, _ := unstructured.NestedInt64(obj.Object, "status", "readyReplicas")
	return int(d), int(r), nil
}

// Scale sets the replica count through the scale subresource.
func (cl *Cluster) Scale(ctx context.Context, k Kind, ns, name string, replicas int) error {
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := cl.res(k, ns).Patch(ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{}, "scale")
	return err
}

// RolloutRestart does what kubectl rollout restart does: bump an annotation
// on the pod template.
func (cl *Cluster) RolloutRestart(ctx context.Context, k Kind, ns, name string) error {
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		time.Now().Format(time.RFC3339))
	_, err := cl.res(k, ns).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// Revision is one entry of rollout history.
type Revision struct {
	Number   int64
	Images   []string
	Cause    string
	Created  time.Time
	Replicas int64 // pods currently owned (Deployments only)
	Current  bool
}

// RolloutHistory lists revisions, newest first. Deployments read their
// ReplicaSets, StatefulSets and DaemonSets their ControllerRevisions.
func (cl *Cluster) RolloutHistory(ctx context.Context, k Kind, ns, name string) ([]Revision, error) {
	owner, err := cl.Get(ctx, k, ns, name)
	if err != nil {
		return nil, err
	}
	uid := owner.GetUID()
	var revs []Revision
	if k.Resource == "deployments" {
		cur := owner.GetAnnotations()["deployment.kubernetes.io/revision"]
		list, err := cl.Clientset.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for _, rs := range list.Items {
			if !ownedBy(rs.OwnerReferences, uid) {
				continue
			}
			n, _ := strconv.ParseInt(rs.Annotations["deployment.kubernetes.io/revision"], 10, 64)
			var imgs []string
			for _, c := range rs.Spec.Template.Spec.Containers {
				imgs = append(imgs, c.Image)
			}
			revs = append(revs, Revision{
				Number: n, Images: imgs, Cause: rs.Annotations["kubernetes.io/change-cause"],
				Created: rs.CreationTimestamp.Time, Replicas: int64(rs.Status.Replicas),
				Current: strconv.FormatInt(n, 10) == cur,
			})
		}
	} else {
		list, err := cl.Clientset.AppsV1().ControllerRevisions(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		var max int64
		for _, cr := range list.Items {
			if !ownedBy(cr.OwnerReferences, uid) {
				continue
			}
			var data struct {
				Spec struct {
					Template struct {
						Spec corev1.PodSpec `json:"spec"`
					} `json:"template"`
				} `json:"spec"`
			}
			_ = json.Unmarshal(cr.Data.Raw, &data)
			var imgs []string
			for _, c := range data.Spec.Template.Spec.Containers {
				imgs = append(imgs, c.Image)
			}
			if cr.Revision > max {
				max = cr.Revision
			}
			revs = append(revs, Revision{
				Number: cr.Revision, Images: imgs, Cause: cr.Annotations["kubernetes.io/change-cause"],
				Created: cr.CreationTimestamp.Time,
			})
		}
		for i := range revs {
			revs[i].Current = revs[i].Number == max
		}
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].Number > revs[j].Number })
	return revs, nil
}

func ownedBy(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, r := range refs {
		if r.UID == uid {
			return true
		}
	}
	return false
}

// RolloutStatus is a snapshot of rollout progress.
type RolloutStatus struct {
	Desired   int64
	Updated   int64
	Ready     int64
	Available int64
	Old       int64 // pods still on older revisions
	Done      bool
	Message   string
}

// Rollout reads rollout progress from the object's status, the same
// conditions kubectl rollout status waits for.
func (cl *Cluster) Rollout(ctx context.Context, k Kind, ns, name string) (RolloutStatus, error) {
	obj, err := cl.Get(ctx, k, ns, name)
	if err != nil {
		return RolloutStatus{}, err
	}
	n := func(path ...string) int64 { v, _, _ := unstructured.NestedInt64(obj.Object, path...); return v }
	gen := obj.GetGeneration()
	observed := n("status", "observedGeneration")
	var s RolloutStatus
	switch k.Resource {
	case "deployments":
		s.Desired = n("spec", "replicas")
		s.Updated = n("status", "updatedReplicas")
		s.Ready = n("status", "readyReplicas")
		s.Available = n("status", "availableReplicas")
		total := n("status", "replicas")
		s.Old = total - s.Updated
		if s.Old < 0 {
			s.Old = 0
		}
		s.Done = observed >= gen && s.Updated == s.Desired && total == s.Updated && s.Available == s.Desired
		conds, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, c := range conds {
			m, _ := c.(map[string]any)
			if m["type"] == "Progressing" && m["reason"] == "ProgressDeadlineExceeded" {
				s.Message, _ = m["message"].(string)
			}
		}
	case "statefulsets":
		s.Desired = n("spec", "replicas")
		s.Updated = n("status", "updatedReplicas")
		s.Ready = n("status", "readyReplicas")
		s.Available = n("status", "availableReplicas")
		s.Old = n("status", "currentReplicas")
		cur, _, _ := unstructured.NestedString(obj.Object, "status", "currentRevision")
		upd, _, _ := unstructured.NestedString(obj.Object, "status", "updateRevision")
		if cur == upd {
			s.Old = 0
		}
		s.Done = observed >= gen && s.Updated == s.Desired && s.Ready == s.Desired && cur == upd
	case "daemonsets":
		s.Desired = n("status", "desiredNumberScheduled")
		s.Updated = n("status", "updatedNumberScheduled")
		s.Ready = n("status", "numberReady")
		s.Available = n("status", "numberAvailable")
		s.Old = n("status", "currentNumberScheduled") - s.Updated
		if s.Old < 0 {
			s.Old = 0
		}
		s.Done = observed >= gen && s.Updated == s.Desired && s.Available == s.Desired
	}
	return s, nil
}

// CanI runs a SelfSubjectAccessReview. Results are cached for a minute.
func (cl *Cluster) CanI(ctx context.Context, verb, group, resource, subresource, ns string) (bool, string, error) {
	key := cacheKey(verb, group, resource, subresource, ns)
	cl.mu.Lock()
	if r, ok := cl.accessCache[key]; ok && time.Since(r.at) < time.Minute {
		cl.mu.Unlock()
		return r.allowed, r.reason, nil
	}
	cl.mu.Unlock()
	review := &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Namespace: ns, Verb: verb, Group: group, Resource: resource, Subresource: subresource,
			},
		},
	}
	res, err := cl.Clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, "", err
	}
	reason := res.Status.Reason
	if res.Status.EvaluationError != "" && reason == "" {
		reason = res.Status.EvaluationError
	}
	cl.mu.Lock()
	cl.accessCache[key] = accessResult{allowed: res.Status.Allowed, reason: reason, at: time.Now()}
	cl.mu.Unlock()
	return res.Status.Allowed, reason, nil
}

// Pod fetches a typed pod.
func (cl *Cluster) Pod(ctx context.Context, ns, name string) (*corev1.Pod, error) {
	return cl.Clientset.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
}

// OwnerReplicas returns how many replicas the pod's controller wants. Pods
// whose controller keeps more than one replica can be deleted without
// confirmation: the controller replaces them and the service stays up.
func (cl *Cluster) OwnerReplicas(ctx context.Context, ns string, owners []metav1.OwnerReference) int64 {
	for _, o := range owners {
		if o.Controller == nil || !*o.Controller {
			continue
		}
		switch o.Kind {
		case "ReplicaSet":
			rs, err := cl.Clientset.AppsV1().ReplicaSets(ns).Get(ctx, o.Name, metav1.GetOptions{})
			if err == nil && rs.Spec.Replicas != nil {
				return int64(*rs.Spec.Replicas)
			}
		case "StatefulSet":
			s, err := cl.Clientset.AppsV1().StatefulSets(ns).Get(ctx, o.Name, metav1.GetOptions{})
			if err == nil && s.Spec.Replicas != nil {
				return int64(*s.Spec.Replicas)
			}
		case "DaemonSet":
			d, err := cl.Clientset.AppsV1().DaemonSets(ns).Get(ctx, o.Name, metav1.GetOptions{})
			if err == nil {
				return int64(d.Status.DesiredNumberScheduled)
			}
		case "ReplicationController":
			rc, err := cl.Clientset.CoreV1().ReplicationControllers(ns).Get(ctx, o.Name, metav1.GetOptions{})
			if err == nil && rc.Spec.Replicas != nil {
				return int64(*rc.Spec.Replicas)
			}
		}
	}
	return 0
}

// PodSelector works out which pods belong to an object: a label selector,
// or a field selector for nodes. ok is false when the kind has no pods.
func (cl *Cluster) PodSelector(ctx context.Context, k Kind, ns, name string) (sel ListOptions, ok bool, err error) {
	if k.Resource == "nodes" {
		return ListOptions{FieldSelector: "spec.nodeName=" + name}, true, nil
	}
	if k.Resource == "pods" {
		return ListOptions{Namespace: ns, FieldSelector: "metadata.name=" + name}, true, nil
	}
	if !k.HasPods() {
		return ListOptions{}, false, nil
	}
	obj, err := cl.Get(ctx, k, ns, name)
	if err != nil {
		return ListOptions{}, false, err
	}
	if k.Resource == "services" || k.Resource == "replicationcontrollers" {
		m, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "selector")
		if len(m) == 0 {
			return ListOptions{}, false, nil
		}
		return ListOptions{Namespace: ns, LabelSelector: labels.SelectorFromSet(m).String()}, true, nil
	}
	raw, found, _ := unstructured.NestedMap(obj.Object, "spec", "selector")
	if !found {
		return ListOptions{}, false, nil
	}
	var ls metav1.LabelSelector
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &ls); err != nil {
		return ListOptions{}, false, err
	}
	s, err := metav1.LabelSelectorAsSelector(&ls)
	if err != nil {
		return ListOptions{}, false, err
	}
	return ListOptions{Namespace: ns, LabelSelector: s.String()}, true, nil
}

// JobsOf lists the jobs a CronJob owns, as a table.
func (cl *Cluster) JobsOf(ctx context.Context, ns, cronjob string) (*Table, error) {
	jobKind, _ := cl.KindFor("jobs")
	t, err := cl.ListTable(ctx, jobKind, ListOptions{Namespace: ns})
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, r := range t.Rows {
		for _, o := range r.Owners {
			if o.Kind == "CronJob" && o.Name == cronjob {
				rows = append(rows, r)
			}
		}
	}
	t.Rows = rows
	return t, nil
}

// Containers lists container names of a pod (regular first, then init and
// ephemeral ones).
func Containers(p *corev1.Pod) []string {
	var out []string
	for _, c := range p.Spec.Containers {
		out = append(out, c.Name)
	}
	for _, c := range p.Spec.InitContainers {
		out = append(out, c.Name)
	}
	for _, c := range p.Spec.EphemeralContainers {
		out = append(out, c.Name)
	}
	return out
}

// ContainerState is a short human status for a container.
func ContainerState(p *corev1.Pod, name string) (state string, ready bool, restarts int32) {
	all := append(append([]corev1.ContainerStatus{}, p.Status.ContainerStatuses...), p.Status.InitContainerStatuses...)
	all = append(all, p.Status.EphemeralContainerStatuses...)
	for _, s := range all {
		if s.Name != name {
			continue
		}
		switch {
		case s.State.Running != nil:
			state = "Running"
		case s.State.Waiting != nil:
			state = s.State.Waiting.Reason
		case s.State.Terminated != nil:
			state = s.State.Terminated.Reason
		}
		return state, s.Ready, s.RestartCount
	}
	return "Pending", false, 0
}

// IsForbidden reports whether an API error is an RBAC denial.
func IsForbidden(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "Forbidden"))
}
