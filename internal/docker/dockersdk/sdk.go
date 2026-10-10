// Package dockersdk adapts the official Docker client to the internal
// docker.Client interface.
package dockersdk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/h0rn3t/inspector-gopher/internal/docker"
)

// SDKClient wraps *client.Client.
type SDKClient struct {
	cli *client.Client
}

// New builds an SDKClient from a socket path (unix:// or tcp://) or
// DOCKER_HOST. An empty socket uses environment defaults.
func New(socket string) (*SDKClient, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if socket != "" {
		opts = append(opts, client.WithHost(socket))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &SDKClient{cli: cli}, nil
}

// ListContainers returns running containers with published ports.
func (s *SDKClient) ListContainers(ctx context.Context) ([]docker.Container, error) {
	list, err := s.cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("status", "running")),
	})
	if err != nil {
		return nil, err
	}
	out := make([]docker.Container, 0, len(list))
	for _, c := range list {
		name := ""
		if len(c.Names) > 0 {
			name = c.Names[0]
			if len(name) > 0 && name[0] == '/' {
				name = name[1:]
			}
		}
		var ports []int
		for _, p := range c.Ports {
			if p.PublicPort != 0 {
				ports = append(ports, int(p.PublicPort))
			}
		}
		out = append(out, docker.Container{
			ID: c.ID[:12], Name: name, Image: c.Image, Ports: ports,
		})
	}
	return out, nil
}

// Exec runs cmd argv in the container and returns stdout.
func (s *SDKClient) Exec(ctx context.Context, containerID string, cmd []string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	execResp, err := s.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, err
	}
	attach, err := s.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, err
	}
	defer attach.Close()
	data, err := io.ReadAll(io.LimitReader(attach.Reader, 128<<20))
	if err != nil {
		return nil, err
	}
	// Strip the Docker multiplexed stream header when present.
	return demux(data), nil
}

// ProbeURL reports whether url answers 200 quickly.
func (s *SDKClient) ProbeURL(ctx context.Context, url string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		var nerr *net.OpError
		_ = nerr
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Close releases client resources.
func (s *SDKClient) Close() error {
	return s.cli.Close()
}

func demux(data []byte) []byte {
	// Docker exec attach multiplexes stdout/stderr in 8-byte frames.
	// If the payload does not look framed, return it as-is.
	if len(data) < 8 {
		return data
	}
	var out bytes.Buffer
	for len(data) >= 8 {
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		if size < 0 || 8+size > len(data) {
			return data
		}
		out.Write(data[8 : 8+size])
		data = data[8+size:]
		if len(data) == 0 {
			break
		}
	}
	if out.Len() == 0 {
		return data
	}
	return out.Bytes()
}
