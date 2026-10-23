package discovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	URL, Node, Selector, TokenFile string
	SelfUID                        string
	Excluded                       map[string]bool
	HTTP                           *http.Client
}

func InCluster(node, selector, excluded string) (*Client, error) {
	if node == "" {
		return nil, fmt.Errorf("discovery requires NODE_NAME or -node-name")
	}
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")
	if port == "" {
		port = "443"
	}
	if host == "" {
		return nil, fmt.Errorf("Kubernetes service environment is missing")
	}
	const sa = "/var/run/secrets/kubernetes.io/serviceaccount/"
	raw, err := os.ReadFile(sa + "ca.crt")
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("invalid Kubernetes CA")
	}
	blocked := make(map[string]bool)
	for _, v := range strings.Split(excluded, ",") {
		if v = strings.TrimSpace(v); v != "" {
			blocked[v] = true
		}
	}
	return &Client{URL: "https://" + net.JoinHostPort(host, port), SelfUID: os.Getenv("AXON_POD_UID"), Node: node, Selector: selector, TokenFile: sa + "token", Excluded: blocked, HTTP: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, MaxIdleConnsPerHost: 2}}}, nil
}

type podList struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []struct {
		Metadata struct {
			Name, Namespace, UID string
			Labels               map[string]string
		} `json:"metadata"`
		Spec struct {
			NodeName string `json:"nodeName"`
		} `json:"spec"`
		Status struct {
			Containers []containerStatus `json:"containerStatuses"`
			Init       []containerStatus `json:"initContainerStatuses"`
			Ephemeral  []containerStatus `json:"ephemeralContainerStatuses"`
		} `json:"status"`
	} `json:"items"`
}
type containerStatus struct {
	Name        string `json:"name"`
	ContainerID string `json:"containerID"`
	State       struct {
		Running *json.RawMessage `json:"running"`
	} `json:"state"`
}

func (c *Client) Containers(ctx context.Context) (map[string]Workload, error) {
	// Re-read projected tokens on each refresh, allowing Kubernetes rotation.
	raw, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return nil, fmt.Errorf("empty Kubernetes token")
	}
	result := make(map[string]Workload)
	cursor := ""
	seen := make(map[string]bool)
	for page := 0; page < 20; page++ {
		params := url.Values{"fieldSelector": {"spec.nodeName=" + c.Node}, "limit": {"500"}}
		if c.Selector != "" {
			params.Set("labelSelector", c.Selector)
		}
		if cursor != "" {
			params.Set("continue", cursor)
		}
		req, err := http.NewRequestWithContext(ctx, "GET", c.URL+"/api/v1/pods?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		response, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(body) > 16<<20 {
			return nil, fmt.Errorf("Kubernetes pod page exceeds 16MiB")
		}
		if response.StatusCode != 200 {
			return nil, fmt.Errorf("listing node pods: HTTP %d", response.StatusCode)
		}
		var pods podList
		if err := json.Unmarshal(body, &pods); err != nil {
			return nil, err
		}
		for _, p := range pods.Items {
			if p.Spec.NodeName != c.Node || c.Excluded[p.Metadata.Namespace] || c.SelfUID != "" && p.Metadata.UID == c.SelfUID {
				continue
			}
			statuses := append(append(p.Status.Containers, p.Status.Init...), p.Status.Ephemeral...)
			for _, s := range statuses {
				if s.State.Running == nil {
					continue
				}
				parts := strings.SplitN(s.ContainerID, "://", 2)
				if len(parts) != 2 {
					continue
				}
				ids := ContainerIDs(parts[1])
				if len(ids) != 1 || ids[0] != parts[1] {
					continue
				}
				service := p.Metadata.Labels["app.kubernetes.io/name"]
				if service == "" {
					service = p.Metadata.Labels["app"]
				}
				if service == "" {
					service = s.Name
				}
				w := Workload{Service: service, Namespace: p.Metadata.Namespace, Pod: p.Metadata.Name, PodUID: p.Metadata.UID, Container: s.Name, ContainerID: ids[0], Node: c.Node}
				if prior, ok := result[ids[0]]; ok && prior != w {
					return nil, fmt.Errorf("ambiguous container identity")
				}
				result[ids[0]] = w
			}
		}
		cursor = pods.Metadata.Continue
		if cursor == "" {
			return result, nil
		}
		if seen[cursor] {
			return nil, fmt.Errorf("repeated Kubernetes pagination token")
		}
		seen[cursor] = true
	}
	return nil, fmt.Errorf("node pod list exceeds pagination bound")
}
