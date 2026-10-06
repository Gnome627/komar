package kube

import (
	"context"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EventWindow is how far back the frequency histogram goes, in minutes.
const EventWindow = 30

// EventGroup is a set of events folded together, with a per-minute
// histogram of how often they happened.
type EventGroup struct {
	Type      string
	Reason    string
	Namespace string
	Kind      string
	Object    string
	Objects   int // distinct objects folded in (reason grouping)
	Message   string
	Count     int64
	First     time.Time
	Last      time.Time
	Bins      [EventWindow]float64 // oldest first; last bin is the current minute
}

// RatePerMin is the average rate over the last 10 minutes.
func (g *EventGroup) RatePerMin() float64 {
	var sum float64
	for _, v := range g.Bins[EventWindow-10:] {
		sum += v
	}
	return sum / 10
}

type GroupBy int

const (
	GroupByObject GroupBy = iota
	GroupByReason
)

type SortBy int

const (
	SortByLast SortBy = iota
	SortByCount
	SortByRate
)

type evRecord struct {
	count int64
	bins  map[int64]float64 // unix minute → occurrences
	seen  bool
}

// EventTracker keeps per-event histograms across polls. Events only carry a
// count and first/last timestamps, so on first sight occurrences are spread
// evenly between those; after that every count increase lands in the
// minute it was observed, which makes the histogram sharpen over time.
type EventTracker struct {
	recs map[string]*evRecord
}

func NewEventTracker() *EventTracker { return &EventTracker{recs: map[string]*evRecord{}} }

// Reset forgets history (on context switch).
func (t *EventTracker) Reset() { t.recs = map[string]*evRecord{} }

// Events lists core/v1 events in ns ("" = all namespaces).
func (cl *Cluster) Events(ctx context.Context, ns string) ([]corev1.Event, error) {
	list, err := cl.Clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{Limit: 5000})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// Events for one object, newest last.
func (cl *Cluster) EventsFor(ctx context.Context, ns, kind, name string) ([]corev1.Event, error) {
	list, err := cl.Clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name + ",involvedObject.kind=" + kind,
	})
	if err != nil {
		return nil, err
	}
	items := list.Items
	sort.Slice(items, func(i, j int) bool { return eventTimes(&items[i]).last.Before(eventTimes(&items[j]).last) })
	return items, nil
}

type evTimes struct {
	count       int64
	first, last time.Time
}

func eventTimes(e *corev1.Event) evTimes {
	t := evTimes{count: int64(e.Count), first: e.FirstTimestamp.Time, last: e.LastTimestamp.Time}
	if e.Series != nil {
		if int64(e.Series.Count) > t.count {
			t.count = int64(e.Series.Count)
		}
		if e.Series.LastObservedTime.After(t.last) {
			t.last = e.Series.LastObservedTime.Time
		}
	}
	if !e.EventTime.IsZero() {
		if t.first.IsZero() {
			t.first = e.EventTime.Time
		}
		if t.last.IsZero() || e.EventTime.After(t.last) {
			t.last = e.EventTime.Time
		}
	}
	if t.last.IsZero() {
		t.last = e.CreationTimestamp.Time
	}
	if t.first.IsZero() {
		t.first = t.last
	}
	if t.count < 1 {
		t.count = 1
	}
	return t
}

// Aggregate updates histograms from a fresh event list and returns groups.
func (t *EventTracker) Aggregate(events []corev1.Event, by GroupBy, sortBy SortBy, warningsOnly bool, now time.Time) []*EventGroup {
	nowMin := now.Unix() / 60
	for _, r := range t.recs {
		r.seen = false
	}
	for i := range events {
		e := &events[i]
		tm := eventTimes(e)
		rec, ok := t.recs[string(e.UID)]
		if !ok {
			rec = &evRecord{count: tm.count, bins: map[int64]float64{}}
			spread(rec.bins, tm, nowMin)
			t.recs[string(e.UID)] = rec
		} else if tm.count > rec.count {
			rec.bins[min64(tm.last.Unix()/60, nowMin)] += float64(tm.count - rec.count)
			rec.count = tm.count
		}
		rec.seen = true
	}
	for uid, r := range t.recs {
		if !r.seen {
			delete(t.recs, uid)
		}
	}

	groups := map[string]*EventGroup{}
	objects := map[string]map[string]bool{}
	for i := range events {
		e := &events[i]
		if warningsOnly && e.Type != corev1.EventTypeWarning {
			continue
		}
		tm := eventTimes(e)
		obj := e.InvolvedObject
		var key string
		if by == GroupByReason {
			key = e.Type + "|" + e.Reason + "|" + obj.Kind + "|" + e.Namespace
		} else {
			key = e.Type + "|" + e.Reason + "|" + e.Namespace + "|" + obj.Kind + "|" + obj.Name + "|" + e.Message
		}
		g, ok := groups[key]
		if !ok {
			g = &EventGroup{Type: e.Type, Reason: e.Reason, Namespace: e.Namespace, Kind: obj.Kind,
				Object: obj.Name, Message: e.Message, First: tm.first, Last: tm.last}
			groups[key] = g
			objects[key] = map[string]bool{}
		}
		objects[key][obj.Name] = true
		g.Count += tm.count
		if tm.first.Before(g.First) {
			g.First = tm.first
		}
		if tm.last.After(g.Last) {
			g.Last = tm.last
			g.Message = e.Message
			g.Object = obj.Name
		}
		if rec := t.recs[string(e.UID)]; rec != nil {
			for m, v := range rec.bins {
				idx := EventWindow - 1 - int(nowMin-m)
				if idx >= 0 && idx < EventWindow {
					g.Bins[idx] += v
				}
			}
		}
	}
	out := make([]*EventGroup, 0, len(groups))
	for k, g := range groups {
		g.Objects = len(objects[k])
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch sortBy {
		case SortByCount:
			if a.Count != b.Count {
				return a.Count > b.Count
			}
		case SortByRate:
			ra, rb := a.RatePerMin(), b.RatePerMin()
			if ra != rb {
				return ra > rb
			}
		}
		return a.Last.After(b.Last)
	})
	return out
}

// spread distributes an event's count evenly over its lifetime minutes.
func spread(bins map[int64]float64, tm evTimes, nowMin int64) {
	first := tm.first.Unix() / 60
	last := min64(tm.last.Unix()/60, nowMin)
	if first > last {
		first = last
	}
	span := last - first + 1
	per := float64(tm.count) / float64(span)
	start := first
	if lo := nowMin - EventWindow; start < lo {
		start = lo
	}
	for m := start; m <= last; m++ {
		bins[m] += per
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
