package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Usage is cluster-wide resource usage from metrics-server.
type Usage struct {
	CPUPercent float64
	MemPercent float64
	CPUMilli   int64
	MemBytes   int64
	Nodes      int
}

// ClusterUsage sums node metrics against allocatable capacity.
func (cl *Cluster) ClusterUsage(ctx context.Context) (Usage, error) {
	nm, err := cl.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Usage{}, err
	}
	nodes, err := cl.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Usage{}, err
	}
	var u Usage
	var cpuCap, memCap int64
	for _, n := range nodes.Items {
		cpuCap += n.Status.Allocatable.Cpu().MilliValue()
		memCap += n.Status.Allocatable.Memory().Value()
	}
	for _, m := range nm.Items {
		u.CPUMilli += m.Usage.Cpu().MilliValue()
		u.MemBytes += m.Usage.Memory().Value()
	}
	u.Nodes = len(nodes.Items)
	if cpuCap > 0 {
		u.CPUPercent = 100 * float64(u.CPUMilli) / float64(cpuCap)
	}
	if memCap > 0 {
		u.MemPercent = 100 * float64(u.MemBytes) / float64(memCap)
	}
	return u, nil
}

// PodUsage returns total CPU (millicores) and memory (bytes) of a pod.
func (cl *Cluster) PodUsage(ctx context.Context, ns, name string) (int64, int64, error) {
	pm, err := cl.Metrics.MetricsV1beta1().PodMetricses(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, 0, err
	}
	var cpu, mem int64
	for _, c := range pm.Containers {
		cpu += c.Usage.Cpu().MilliValue()
		mem += c.Usage.Memory().Value()
	}
	return cpu, mem, nil
}

// HumanBytes formats bytes as Ki/Mi/Gi.
func HumanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f%ci", float64(b)/float64(div), "KMGTPE"[exp])
}
