package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultClientName    = "test_skill_agent"
	defaultClientVersion = "dev"
)

type Server struct {
	Config  ServerConfig
	Session *mcp.ClientSession
	Tools   []*mcp.Tool
}

func (s *Server) Close() error {
	if s == nil || s.Session == nil {
		return nil
	}
	return s.Session.Close()
}

func ConnectServers(ctx context.Context, configs []ServerConfig) ([]*Server, error) {
	if len(configs) == 0 {
		return nil, nil
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    defaultClientName,
		Version: defaultClientVersion,
	}, nil)

	servers := make([]*Server, 0, len(configs))
	errs := make([]string, 0)
	seen := make(map[string]bool)

	for _, cfg := range configs {
		if cfg.Disabled {
			continue
		}
		name := strings.TrimSpace(cfg.Name)
		if name == "" {
			errs = append(errs, "server name is required")
			continue
		}
		if seen[name] {
			errs = append(errs, fmt.Sprintf("duplicate server name: %s", name))
			continue
		}
		seen[name] = true

		transport, err := transportFromConfig(cfg)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}

		session, err := client.Connect(ctx, transport, nil)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s connect: %v", name, err))
			continue
		}

		tools, err := listAllTools(ctx, session)
		if err != nil {
			_ = session.Close()
			errs = append(errs, fmt.Sprintf("%s list tools: %v", name, err))
			continue
		}

		servers = append(servers, &Server{Config: cfg, Session: session, Tools: tools})
	}

	if len(errs) > 0 {
		return servers, fmt.Errorf("mcp: %s", strings.Join(errs, "; "))
	}
	return servers, nil
}

func CloseServers(servers []*Server) error {
	if len(servers) == 0 {
		return nil
	}
	errs := make([]string, 0)
	for _, server := range servers {
		if server == nil {
			continue
		}
		if err := server.Close(); err != nil {
			name := strings.TrimSpace(server.Config.Name)
			if name == "" {
				name = "(unknown)"
			}
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func listAllTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	tools := make([]*mcp.Tool, 0)
	cursor := ""
	for {
		params := &mcp.ListToolsParams{}
		if cursor != "" {
			params.Cursor = cursor
		}
		res, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, err
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return tools, nil
}

func transportFromConfig(cfg ServerConfig) (mcp.Transport, error) {
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	switch transport {
	case "", "command", "stdio":
		if strings.TrimSpace(cfg.Command) == "" {
			return nil, errors.New("command is required for command transport")
		}
		cmd := exec.Command(cfg.Command, cfg.Args...)
		if strings.TrimSpace(cfg.Dir) != "" {
			cmd.Dir = cfg.Dir
		}

		inheritEnv := true
		if cfg.InheritEnv != nil {
			inheritEnv = *cfg.InheritEnv
		}
		if inheritEnv {
			cmd.Env = os.Environ()
		}
		if len(cfg.Env) > 0 {
			if cmd.Env == nil {
				cmd.Env = os.Environ()
			}
			for k, v := range cfg.Env {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			}
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	case "sse":
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, errors.New("url is required for sse transport")
		}
		return &mcp.SSEClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClientWithHeaders(cfg.Headers),
		}, nil
	case "streamable_http", "streamable", "http":
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, errors.New("url is required for streamable_http transport")
		}
		return &mcp.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClientWithHeaders(cfg.Headers),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported transport: %s", cfg.Transport)
	}
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if h.base == nil {
		h.base = http.DefaultTransport
	}
	for k, v := range h.headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	return h.base.RoundTrip(req)
}

func httpClientWithHeaders(headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return nil
	}
	return &http.Client{
		Transport: &headerRoundTripper{
			base:    http.DefaultTransport,
			headers: headers,
		},
	}
}
