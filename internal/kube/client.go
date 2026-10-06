// Package kube wraps client-go for the things komar needs: switching
// contexts, listing any resource as a server-side table, and the actions
// (delete, scale, rollout, logs, events, metrics, access checks).
package kube

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Config is the parsed kubeconfig (all files from KUBECONFIG merged).
type Config struct {
	rules   *clientcmd.ClientConfigLoadingRules
	raw     clientcmdapi.Config
	Current string
}

// LoadConfig reads kubeconfig the same way kubectl does.
func LoadConfig(explicitPath string) (*Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if explicitPath != "" {
		rules.ExplicitPath = explicitPath
	}
	raw, err := rules.Load()
	if err != nil {
		return nil, err
	}
	return &Config{rules: rules, raw: *raw, Current: raw.CurrentContext}, nil
}

// Reload re-reads kubeconfig from disk, so new contexts show up without a
// restart.
func (c *Config) Reload() error {
	raw, err := c.rules.Load()
	if err != nil {
		return err
	}
	c.raw = *raw
	return nil
}

// Contexts returns context names sorted alphabetically.
func (c *Config) Contexts() []string {
	names := make([]string, 0, len(c.raw.Contexts))
	for n := range c.raw.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ContextInfo returns cluster and user names for display.
func (c *Config) ContextInfo(name string) (cluster, user, namespace string) {
	if ctx, ok := c.raw.Contexts[name]; ok {
		return ctx.Cluster, ctx.AuthInfo, ctx.Namespace
	}
	return "", "", ""
}

// Paths returns the kubeconfig files in play, for passing to kubectl.
func (c *Config) Paths() []string {
	if c.rules.ExplicitPath != "" {
		return []string{c.rules.ExplicitPath}
	}
	return c.rules.GetLoadingPrecedence()
}

// Cluster is a live connection to one context.
type Cluster struct {
	Context    string
	DefaultNS  string
	KubeUser   string
	Server     string
	REST       *rest.Config
	stream     *rest.Config
	Clientset  kubernetes.Interface
	Streamer   kubernetes.Interface
	Dynamic    dynamic.Interface
	Discovery  discovery.DiscoveryInterface
	Metrics    metricsv.Interface
	kubeconfig *Config

	mu          sync.Mutex
	resources   []Kind
	hasMetrics  *bool
	whoami      string
	accessCache map[string]accessResult
}

type accessResult struct {
	allowed bool
	reason  string
	at      time.Time
}

// Connect builds clients for a context without touching the kubeconfig
// file: switching contexts in komar does not change kubectl's
// current-context.
func (c *Config) Connect(name string) (*Cluster, error) {
	overrides := &clientcmd.ConfigOverrides{CurrentContext: name}
	cc := clientcmd.NewNonInteractiveClientConfig(c.raw, name, overrides, c.rules)
	restCfg, err := cc.ClientConfig()
	if err != nil {
		return nil, err
	}
	ns, _, _ := cc.Namespace()
	if ns == "" {
		ns = "default"
	}
	restCfg.QPS = 50
	restCfg.Burst = 100
	restCfg.UserAgent = "komar"

	// Log streams and long requests use a config without a timeout.
	stream := rest.CopyConfig(restCfg)
	restCfg.Timeout = 20 * time.Second

	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	streamer, err := kubernetes.NewForConfig(stream)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	mc, err := metricsv.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	_, user, _ := c.ContextInfo(name)
	return &Cluster{
		Context: name, DefaultNS: ns, KubeUser: user, Server: restCfg.Host,
		REST: restCfg, stream: stream,
		Clientset: cs, Streamer: streamer, Dynamic: dyn, Discovery: cs.Discovery(), Metrics: mc,
		kubeconfig: c, accessCache: map[string]accessResult{},
	}, nil
}

// KubectlArgs are the flags that point kubectl at this cluster.
func (cl *Cluster) KubectlArgs() []string {
	return []string{"--context", cl.Context}
}

// KubectlEnv makes sure kubectl reads the same kubeconfig files we did.
func (cl *Cluster) KubectlEnv() []string {
	env := os.Environ()
	if paths := cl.kubeconfig.Paths(); len(paths) > 0 && cl.kubeconfig.rules.ExplicitPath != "" {
		env = append(env, "KUBECONFIG="+strings.Join(paths, string(os.PathListSeparator)))
	}
	return env
}

// Ping checks the API server is reachable and returns its version.
func (cl *Cluster) Ping(ctx context.Context) (string, error) {
	type res struct {
		v   string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		v, err := cl.Discovery.ServerVersion()
		if err != nil {
			ch <- res{"", err}
			return
		}
		ch <- res{v.GitVersion, nil}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// WhoAmI asks the server who we are (SelfSubjectReview), falling back to the
// kubeconfig user name on older clusters.
func (cl *Cluster) WhoAmI(ctx context.Context) string {
	cl.mu.Lock()
	if cl.whoami != "" {
		defer cl.mu.Unlock()
		return cl.whoami
	}
	cl.mu.Unlock()
	name := cl.KubeUser
	r, err := cl.Clientset.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err == nil && r.Status.UserInfo.Username != "" {
		name = r.Status.UserInfo.Username
	}
	cl.mu.Lock()
	cl.whoami = name
	cl.mu.Unlock()
	return name
}

// Namespaces lists namespace names. When listing is forbidden it returns
// just the default namespace so the UI still works with narrow RBAC.
func (cl *Cluster) Namespaces(ctx context.Context) ([]string, error) {
	list, err := cl.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return []string{cl.DefaultNS}, err
	}
	out := make([]string, 0, len(list.Items))
	for _, n := range list.Items {
		out = append(out, n.Name)
	}
	sort.Strings(out)
	return out, nil
}

// ErrString shortens API errors for the status line.
func ErrString(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	return s
}

func cacheKey(parts ...string) string { return fmt.Sprint(strings.Join(parts, "|")) }
