// Package podrunner runs each agent session in its own Kubernetes pod.
//
// The single-process design gives every session the same uid and the same
// filesystem, so isolation rests on the tool layer plus Landlock. That is enough
// for users who trust each other. A pod per session is the boundary that holds
// when they do not: separate filesystem, process tree, network identity and
// resource limits, enforced by the kubelet rather than by this process.
//
// The pod does not expose a port. It runs `dross rc` and dials back over the
// existing device relay, so prompts and events flow through machinery that is
// already in production — and the server needs no exec rights on the pod, only
// permission to create and delete one.
//
// This is a deliberately small API client rather than client-go: the entire
// need is create/delete/get on two resource kinds, and client-go would add tens
// of megabytes of dependency for that.
package podrunner

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	tokenFile  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caFile     = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	nsFile     = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	apiHostEnv = "KUBERNETES_SERVICE_HOST"
	apiPortEnv = "KUBERNETES_SERVICE_PORT"
)

// Client talks to the Kubernetes API using the pod's own service account.
type Client struct {
	base      string
	namespace string
	token     string
	hc        *http.Client
}

// InCluster builds a client from the mounted service-account credentials.
// Returns (nil, nil) when not running in a cluster, so callers can degrade to
// in-process sessions rather than failing to start.
func InCluster() (*Client, error) {
	host, port := os.Getenv(apiHostEnv), os.Getenv(apiPortEnv)
	if host == "" || port == "" {
		return nil, nil
	}
	tok, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("service account token: %w", err)
	}
	ns, err := os.ReadFile(nsFile)
	if err != nil {
		return nil, fmt.Errorf("namespace: %w", err)
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("cluster CA is not valid PEM")
	}
	return &Client{
		base:      fmt.Sprintf("https://%s:%s", host, port),
		namespace: strings.TrimSpace(string(ns)),
		token:     strings.TrimSpace(string(tok)),
		hc: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig(pool)},
		},
	}, nil
}

// Namespace is where session pods are created.
func (c *Client) Namespace() string { return c.namespace }

// do performs an API call and decodes the response into out (may be nil).
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return &apiError{Status: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// apiError carries the API server's status so callers can treat "already
// exists" and "not found" as success where that is the intent.
type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("kubernetes api %d: %s", e.Status, e.Body) }

// IsNotFound reports whether err is a 404 from the API server.
func IsNotFound(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsAlreadyExists reports whether err is a 409 from the API server.
func IsAlreadyExists(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == http.StatusConflict
}

// tlsConfig pins the cluster CA; the API server is never dialled without it.
func tlsConfig(pool *x509.CertPool) *tls.Config {
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}
