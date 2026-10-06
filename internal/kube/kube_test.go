package kube

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func ev(uid, typ, reason, obj, msg string, count int32, first, last time.Time) corev1.Event {
	return corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{UID: types.UID(uid), Namespace: "api"},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: obj},
		Type:           typ, Reason: reason, Message: msg, Count: count,
		FirstTimestamp: metav1.NewTime(first), LastTimestamp: metav1.NewTime(last),
	}
}

func TestEventAggregation(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 30, 0, time.UTC)
	tr := NewEventTracker()
	evs := []corev1.Event{
		ev("1", "Warning", "BackOff", "a", "restarting", 10, now.Add(-9*time.Minute), now),
		ev("2", "Warning", "BackOff", "b", "restarting", 2, now.Add(-time.Minute), now),
		ev("3", "Normal", "Pulled", "a", "pulled", 1, now.Add(-20*time.Minute), now.Add(-20*time.Minute)),
	}
	groups := tr.Aggregate(evs, GroupByObject, SortByCount, false, now)
	if len(groups) != 3 || groups[0].Object != "a" || groups[0].Count != 10 {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	var sum float64
	for _, v := range groups[0].Bins {
		sum += v
	}
	if sum < 9.99 || sum > 10.01 {
		t.Errorf("histogram should hold all 10 occurrences, got %v", sum)
	}
	if r := groups[0].RatePerMin(); r < 0.99 || r > 1.01 {
		t.Errorf("rate = %v, want 1/min", r)
	}

	byReason := tr.Aggregate(evs, GroupByReason, SortByLast, true, now)
	if len(byReason) != 1 || byReason[0].Count != 12 || byReason[0].Objects != 2 {
		t.Fatalf("reason grouping: %+v", byReason[0])
	}

	// A later poll: the count grew by 5, all of it lands in the current minute.
	later := now.Add(time.Minute)
	evs[0].Count = 15
	evs[0].LastTimestamp = metav1.NewTime(later)
	groups = tr.Aggregate(evs, GroupByObject, SortByCount, false, later)
	if got := groups[0].Bins[EventWindow-1]; got < 5 {
		t.Errorf("new occurrences should land in the last bin, got %v", got)
	}
}

func TestSplitTimestamp(t *testing.T) {
	ts, text := splitTimestamp("2026-10-06T21:13:52.123456789Z hello world")
	if ts.IsZero() || text != "hello world" {
		t.Fatalf("got %v %q", ts, text)
	}
	ts, text = splitTimestamp("no timestamp here")
	if !ts.IsZero() || text != "no timestamp here" {
		t.Fatalf("got %v %q", ts, text)
	}
}

func TestKindHelpers(t *testing.T) {
	cl := &Cluster{}
	k, ok := cl.KindFor("deploy")
	if !ok || k.Resource != "deployments" || !k.Scalable() || !k.Rollable() {
		t.Fatalf("deploy: %+v", k)
	}
	if k.Ref() != "deployments.apps" {
		t.Errorf("ref = %s", k.Ref())
	}
	if apiPath(k, "api") != "/apis/apps/v1/namespaces/api/deployments" {
		t.Errorf("path = %s", apiPath(k, "api"))
	}
	n, _ := cl.KindFor("nodes")
	if apiPath(n, "api") != "/api/v1/nodes" {
		t.Errorf("cluster-scoped path = %s", apiPath(n, "api"))
	}
}
