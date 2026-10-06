package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LogLine is one line from one container.
type LogLine struct {
	Pod       string
	Container string
	Text      string
	Time      time.Time
	// Err marks a status line about a stream (failed to start, ended).
	Err bool
}

// LogOptions control what part of the log is fetched.
type LogOptions struct {
	Previous  bool
	Since     time.Duration // 0 = from the tail
	Tail      int64         // lines per container when Since is 0
	Follow    bool
	Container string // "" = all containers
}

// MaxStreams caps concurrent container streams in one session (a node can
// run hundreds of pods).
const MaxStreams = 64

// LogSession streams logs of every pod matched by a selector, like stern:
// new pods that match later (after a rollout) are picked up too.
type LogSession struct {
	Lines  chan LogLine
	cancel context.CancelFunc
	once   sync.Once
}

// Stop ends all streams.
func (s *LogSession) Stop() {
	s.once.Do(func() { s.cancel() })
}

type streamState int

const (
	streamActive streamState = iota
	streamDone
)

// StreamLogs starts a log session for pods matching sel.
func (cl *Cluster) StreamLogs(parent context.Context, sel ListOptions, opts LogOptions) *LogSession {
	ctx, cancel := context.WithCancel(parent)
	s := &LogSession{Lines: make(chan LogLine, 4096), cancel: cancel}
	go cl.runLogSession(ctx, s, sel, opts)
	return s
}

func (cl *Cluster) runLogSession(ctx context.Context, s *LogSession, sel ListOptions, opts LogOptions) {
	var mu sync.Mutex
	streams := map[string]streamState{}
	capped := false
	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		close(s.Lines)
	}()

	send := func(l LogLine) bool {
		select {
		case s.Lines <- l:
			return true
		case <-ctx.Done():
			return false
		}
	}

	resolve := func() {
		pods, err := cl.Clientset.CoreV1().Pods(sel.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: sel.LabelSelector, FieldSelector: sel.FieldSelector,
		})
		if err != nil {
			if ctx.Err() == nil {
				send(LogLine{Text: ErrString(err), Err: true})
			}
			return
		}
		for i := range pods.Items {
			p := &pods.Items[i]
			for _, c := range logContainers(p, opts) {
				key := string(p.UID) + "/" + c
				mu.Lock()
				_, seen := streams[key]
				if seen || !containerStarted(p, c, opts.Previous) {
					mu.Unlock()
					continue
				}
				if active(streams) >= MaxStreams {
					mu.Unlock()
					if !capped {
						capped = true
						send(LogLine{Text: fmt.Sprintf("only the first %d containers are streamed", MaxStreams), Err: true})
					}
					continue
				}
				streams[key] = streamActive
				mu.Unlock()
				wg.Add(1)
				go func(pod, ns, container string) {
					defer wg.Done()
					cl.streamOne(ctx, ns, pod, container, opts, send)
					mu.Lock()
					streams[key] = streamDone
					mu.Unlock()
				}(p.Name, p.Namespace, c)
			}
		}
	}

	resolve()
	if !opts.Follow {
		return
	}
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			resolve()
		}
	}
}

func active(streams map[string]streamState) int {
	n := 0
	for _, s := range streams {
		if s == streamActive {
			n++
		}
	}
	return n
}

func logContainers(p *corev1.Pod, opts LogOptions) []string {
	if opts.Container != "" {
		for _, c := range Containers(p) {
			if c == opts.Container {
				return []string{c}
			}
		}
		return nil
	}
	var out []string
	for _, c := range p.Spec.Containers {
		out = append(out, c.Name)
	}
	return out
}

// containerStarted avoids asking for logs of containers that never ran;
// the API answers those with an error.
func containerStarted(p *corev1.Pod, name string, previous bool) bool {
	all := append(append([]corev1.ContainerStatus{}, p.Status.ContainerStatuses...), p.Status.InitContainerStatuses...)
	all = append(all, p.Status.EphemeralContainerStatuses...)
	for _, s := range all {
		if s.Name != name {
			continue
		}
		if previous {
			return s.RestartCount > 0 || s.LastTerminationState.Terminated != nil
		}
		return s.State.Running != nil || s.State.Terminated != nil || s.RestartCount > 0
	}
	return false
}

func (cl *Cluster) streamOne(ctx context.Context, ns, pod, container string, opts LogOptions, send func(LogLine) bool) {
	po := &corev1.PodLogOptions{Container: container, Follow: opts.Follow, Previous: opts.Previous, Timestamps: true}
	if opts.Since > 0 {
		secs := int64(opts.Since.Seconds())
		po.SinceSeconds = &secs
	} else if opts.Tail > 0 {
		tail := opts.Tail
		po.TailLines = &tail
	}
	rc, err := cl.Streamer.CoreV1().Pods(ns).GetLogs(pod, po).Stream(ctx)
	if err != nil {
		if ctx.Err() == nil {
			send(LogLine{Pod: pod, Container: container, Text: ErrString(err), Err: true})
		}
		return
	}
	defer rc.Close()
	r := bufio.NewReaderSize(rc, 64*1024)
	for {
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			ts, text := splitTimestamp(strings.TrimRight(line, "\r\n"))
			if !send(LogLine{Pod: pod, Container: container, Text: text, Time: ts}) {
				return
			}
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				send(LogLine{Pod: pod, Container: container, Text: ErrString(err), Err: true})
			}
			return
		}
	}
}

// splitTimestamp separates the RFC3339 prefix the API adds with
// timestamps=true.
func splitTimestamp(line string) (time.Time, string) {
	i := strings.IndexByte(line, ' ')
	if i <= 0 {
		return time.Time{}, line
	}
	t, err := time.Parse(time.RFC3339Nano, line[:i])
	if err != nil {
		return time.Time{}, line
	}
	return t, line[i+1:]
}
