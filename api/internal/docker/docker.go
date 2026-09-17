// Package docker is the handful of Docker Engine API calls Bob needs, over the host socket.
// Deliberately not the Docker SDK: four endpoints do not justify its dependency tree.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
)

const apiVersion = "v1.43"

var ErrNotFound = errors.New("docker: no such container")

type Client struct{ http *http.Client }

func New(socket string) *Client {
	return &Client{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}}
}

type ContainerSpec struct {
	Name    string
	Image   string
	Env     []string
	Labels  map[string]string
	Binds   []string // "volume-or-host-path:/container/path[:ro]"
	Network string   // join this network; empty = default bridge
	// PublishLoopback publishes container port Port on 127.0.0.1 at a port Docker picks.
	PublishLoopback bool
	Port            int
}

type ContainerState struct {
	ID      string
	Running bool
	// LoopbackPort is the host port published for ContainerSpec.Port on 127.0.0.1, or 0.
	LoopbackPort string
}

func (c *Client) Inspect(ctx context.Context, name string) (ContainerState, error) {
	var out struct {
		ID              string
		State           struct{ Running bool }
		NetworkSettings struct {
			Ports map[string][]struct{ HostIP, HostPort string }
		}
	}
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil, &out); err != nil {
		return ContainerState{}, err
	}
	st := ContainerState{ID: out.ID, Running: out.State.Running}
	for _, bindings := range out.NetworkSettings.Ports {
		for _, b := range bindings {
			if b.HostIP == "127.0.0.1" {
				st.LoopbackPort = b.HostPort
			}
		}
	}
	return st, nil
}

func (c *Client) Create(ctx context.Context, s ContainerSpec) (string, error) {
	port := fmt.Sprintf("%d/tcp", s.Port)
	body := map[string]any{
		"Image":        s.Image,
		"Env":          s.Env,
		"Labels":       s.Labels,
		"ExposedPorts": map[string]any{port: struct{}{}},
		"HostConfig": map[string]any{
			"Binds":         s.Binds,
			"RestartPolicy": map[string]string{"Name": "unless-stopped"},
			"NetworkMode":   s.Network,
		},
	}
	if s.PublishLoopback {
		body["HostConfig"].(map[string]any)["PortBindings"] = map[string]any{
			port: []map[string]string{{"HostIp": "127.0.0.1", "HostPort": ""}},
		}
	}
	var out struct{ ID string }
	err := c.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(s.Name), body, &out)
	return out.ID, err
}

func (c *Client) Start(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil)
}

// Remove force-removes a container (running or not). Its named volumes are kept.
func (c *Client) Remove(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name)+"?force=true", nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// RemoveVolume deletes a named volume. A volume that does not exist is not an error.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name)+"?force=true", nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/"+apiVersion+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("docker %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("docker %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(msg))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
