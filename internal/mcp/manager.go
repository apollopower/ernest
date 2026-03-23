package mcp

import (
	"context"
	"ernest/internal/provider"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	connectTimeout   = 30 * time.Second
	maxResultBytes   = 100 * 1024 // 100KB
	toolNamePrefix   = "mcp__"
)

// ServerStatus represents the current state of an MCP server connection.
type ServerStatus struct {
	Name      string
	Status    string // "connected", "error", "disconnected"
	ToolCount int
	Error     string
}

// serverConnection holds a single MCP server's session and discovered tools.
type serverConnection struct {
	name    string
	config  MCPServerConfig
	session *mcp.ClientSession
	tools   []*mcp.Tool
	err     error
}

// Manager handles MCP server connections and tool discovery.
type Manager struct {
	mu      sync.RWMutex
	servers map[string]*serverConnection
}

// NewManager creates a new MCP manager.
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*serverConnection),
	}
}

// ConnectAll connects to all configured MCP servers (stdio and HTTP) concurrently.
// Each server gets a 30-second connection timeout. Individual server failures
// are logged but don't prevent other servers from connecting.
func (m *Manager) ConnectAll(ctx context.Context, config *MCPConfig) {
	var wg sync.WaitGroup

	for name, cfg := range config.Servers {
		if cfg.Command == "" && cfg.URL == "" {
			continue
		}

		wg.Add(1)
		go func(name string, cfg MCPServerConfig) {
			defer wg.Done()
			conn := m.connectServer(ctx, name, cfg)
			m.mu.Lock()
			m.servers[name] = conn
			m.mu.Unlock()

			if conn.err != nil {
				log.Printf("[mcp] %s: connection failed: %v", name, conn.err)
			} else {
				log.Printf("[mcp] %s: connected (%d tools)", name, len(conn.tools))
			}
		}(name, cfg)
	}

	wg.Wait()
}

// connectServer establishes a connection to a single MCP server.
// Supports stdio (Command) and HTTP (URL) transports.
func (m *Manager) connectServer(ctx context.Context, name string, cfg MCPServerConfig) *serverConnection {
	conn := &serverConnection{name: name, config: cfg}

	transport, err := buildTransport(cfg)
	if err != nil {
		conn.err = err
		return conn
	}

	// Connect with timeout
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "ernest",
		Version: "0.1.0",
	}, nil)

	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		conn.err = fmt.Errorf("connect: %w", err)
		return conn
	}
	conn.session = session

	// Discover tools
	result, err := session.ListTools(connectCtx, nil)
	if err != nil {
		// Close session to avoid leaking the MCP server process
		_ = session.Close()
		conn.session = nil
		conn.err = fmt.Errorf("list tools: %w", err)
		return conn
	}

	conn.tools = result.Tools
	return conn
}

// buildTransport creates the appropriate MCP transport for the server config.
func buildTransport(cfg MCPServerConfig) (mcp.Transport, error) {
	if cfg.Command != "" {
		// Stdio transport
		cmd := exec.Command(cfg.Command, cfg.Args...)
		if cfg.Env != nil {
			cmd.Env = os.Environ()
			for k, v := range cfg.Env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	}

	if cfg.URL != "" {
		// HTTP transport — build client with custom headers if needed
		httpClient := http.DefaultClient
		if len(cfg.Headers) > 0 {
			httpClient = &http.Client{
				Transport: &headerTransport{
					base:    http.DefaultTransport,
					headers: cfg.Headers,
				},
			}
		}

		switch strings.ToLower(cfg.Type) {
		case "sse":
			return &mcp.SSEClientTransport{
				Endpoint:   cfg.URL,
				HTTPClient: httpClient,
			}, nil
		default:
			// "http", "streamable-http", or empty → StreamableClientTransport
			return &mcp.StreamableClientTransport{
				Endpoint:   cfg.URL,
				HTTPClient: httpClient,
			}, nil
		}
	}

	return nil, fmt.Errorf("server has no command or URL configured")
}

// headerTransport injects custom HTTP headers into every request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	for k, v := range t.headers {
		r.Header.Set(k, v)
	}
	return t.base.RoundTrip(r)
}

// Tools returns all discovered MCP tool definitions, namespaced and sorted.
// Format: mcp__<server>__<tool>
func (m *Manager) Tools() []provider.ToolDef {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var defs []provider.ToolDef
	for serverName, conn := range m.servers {
		if conn.err != nil || conn.tools == nil {
			continue
		}
		for _, tool := range conn.tools {
			namespacedName := toolNamePrefix + serverName + "__" + tool.Name
			// Convert InputSchema (any) to map[string]any
			schema, ok := tool.InputSchema.(map[string]any)
			if !ok {
				schema = map[string]any{"type": "object"}
			}
			defs = append(defs, provider.ToolDef{
				Name:        namespacedName,
				Description: tool.Description,
				InputSchema: schema,
			})
		}
	}

	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	return defs
}

// IsReadOnly returns true if the MCP tool has the readOnlyHint annotation.
func (m *Manager) IsReadOnly(serverName, toolName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conn, ok := m.servers[serverName]
	if !ok || conn.err != nil {
		return false
	}
	for _, tool := range conn.tools {
		if tool.Name == toolName && tool.Annotations != nil {
			return tool.Annotations.ReadOnlyHint
		}
	}
	return false
}

// CallTool executes a tool on the named MCP server and returns the text result.
// Non-text content blocks are dropped with a log warning. Results are truncated
// to 100KB.
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	m.mu.RLock()
	conn, ok := m.servers[serverName]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("MCP server %q not connected", serverName)
	}
	if conn.err != nil {
		return "", fmt.Errorf("MCP server %q in error state: %v", serverName, conn.err)
	}
	if conn.session == nil {
		return "", fmt.Errorf("MCP server %q has no active session", serverName)
	}

	result, err := conn.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("MCP tool call failed: %w", err)
	}

	// Extract text content, drop non-text with warning
	var texts []string
	for _, content := range result.Content {
		switch c := content.(type) {
		case *mcp.TextContent:
			texts = append(texts, c.Text)
		default:
			log.Printf("[mcp] dropping non-text content block from %s/%s", serverName, toolName)
		}
	}

	output := strings.Join(texts, "\n")

	// Truncate to 100KB
	if len(output) > maxResultBytes {
		output = output[:maxResultBytes] + "\n... (MCP result truncated at 100KB)"
	}

	if result.IsError {
		return "", fmt.Errorf("MCP tool error: %s", output)
	}

	return output, nil
}

// ParseMCPToolName parses a namespaced tool name into server and tool names.
// Returns ("", "", false) if the name doesn't have the mcp__ prefix.
func ParseMCPToolName(name string) (serverName, toolName string, ok bool) {
	if !strings.HasPrefix(name, toolNamePrefix) {
		return "", "", false
	}
	// Split: "mcp__server__tool" → ["mcp", "server", "tool"]
	parts := strings.SplitN(name[len(toolNamePrefix):], "__", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// Reconnect attempts to reconnect a single MCP server.
func (m *Manager) Reconnect(ctx context.Context, name string) error {
	m.mu.RLock()
	conn, ok := m.servers[name]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("MCP server %q not configured", name)
	}

	// Close existing session if any
	if conn.session != nil {
		conn.session.Close()
	}

	newConn := m.connectServer(ctx, name, conn.config)
	m.mu.Lock()
	m.servers[name] = newConn
	m.mu.Unlock()

	if newConn.err != nil {
		return newConn.err
	}
	return nil
}

// Status returns the current status of all MCP servers.
func (m *Manager) Status() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var statuses []ServerStatus
	for name, conn := range m.servers {
		s := ServerStatus{Name: name}
		if conn.err != nil {
			s.Status = "error"
			s.Error = conn.err.Error()
		} else if conn.session != nil {
			s.Status = "connected"
			s.ToolCount = len(conn.tools)
		} else {
			s.Status = "disconnected"
		}
		statuses = append(statuses, s)
	}

	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})
	return statuses
}

// AddServer connects a new MCP server at runtime.
// Closes any existing server with the same name before connecting.
func (m *Manager) AddServer(ctx context.Context, name string, cfg MCPServerConfig) error {
	// Close existing server outside lock to avoid blocking
	m.mu.Lock()
	existing := m.servers[name]
	m.mu.Unlock()
	if existing != nil && existing.session != nil {
		existing.session.Close()
	}

	conn := m.connectServer(ctx, name, cfg)
	m.mu.Lock()
	m.servers[name] = conn
	m.mu.Unlock()
	if conn.err != nil {
		return conn.err
	}
	return nil
}

// RemoveServer disconnects and removes an MCP server.
func (m *Manager) RemoveServer(name string) error {
	m.mu.Lock()
	conn, ok := m.servers[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("MCP server %q not found", name)
	}
	delete(m.servers, name)
	m.mu.Unlock()

	if conn.session != nil {
		conn.session.Close()
	}
	return nil
}

// RefreshTools re-fetches the tool list for a server (e.g., after tools/list_changed).
func (m *Manager) RefreshTools(ctx context.Context, name string) error {
	m.mu.RLock()
	conn, ok := m.servers[name]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("MCP server %q not found", name)
	}
	if conn.session == nil {
		return fmt.Errorf("MCP server %q not connected", name)
	}

	result, err := conn.session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("refresh tools: %w", err)
	}

	m.mu.Lock()
	conn.tools = result.Tools
	m.mu.Unlock()

	log.Printf("[mcp] %s: refreshed (%d tools)", name, len(conn.tools))
	return nil
}

// Close disconnects all MCP servers. SDK handles subprocess cleanup.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, conn := range m.servers {
		if conn.session != nil {
			conn.session.Close()
			log.Printf("[mcp] %s: disconnected", name)
		}
	}
	m.servers = make(map[string]*serverConnection)
}
