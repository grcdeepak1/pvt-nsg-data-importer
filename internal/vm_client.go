package internal

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

// VMClient represents a Victoria Metrics client
type VMClient struct {
	serverURL      string
	httpClient     *http.Client
	importPath     string
	metricsCounter int64
	errorCounter   int64
	code204Counter int64
	code200Counter int64
	mu             sync.Mutex // Add mutex for thread safety
}

// VMClientConfig holds configuration for Victoria Metrics client
type VMClientConfig struct {
	ServerURL  string
	Timeout    time.Duration
	ImportPath string
}

// NewVMClient creates a new Victoria Metrics client
func NewVMClient(config VMClientConfig) (*VMClient, error) {
	if config.ServerURL == "" {
		return nil, fmt.Errorf("server URL cannot be empty")
	}

	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second // Default timeout
	}

	if config.ImportPath == "" {
		//config.ImportPath = "/prometheus/api/v1/write"
		config.ImportPath = "/api/v1/import" // Default import path
	}

	return &VMClient{
		serverURL:  config.ServerURL,
		importPath: config.ImportPath,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		metricsCounter: 0,
		errorCounter:   0,
	}, nil
}

// Close performs any necessary cleanup for the VMClient
func (c *VMClient) Close() {
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

// PushMetric sends a single metric to Victoria Metrics
func (c *VMClient) PushMetric(req *prompb.WriteRequest) error {
	if req == nil {
		return fmt.Errorf("req cannot be nil")
	}

	data, err := req.Marshal()
	if err != nil {
		c.mu.Lock()
		c.errorCounter++
		c.mu.Unlock()
		return fmt.Errorf("failed to marshal write request: %w", err)
	}

	// Compress the data
	compressedData := snappy.Encode(nil, data)

	victoriaMetricsURL := fmt.Sprintf("%s%s", c.serverURL, "/insert/0/prometheus/api/v1/write") // Use Prometheus write endpoint
	httpReq, err := http.NewRequest(http.MethodPost, victoriaMetricsURL, bytes.NewReader(compressedData))
	if err != nil {
		c.mu.Lock()
		c.errorCounter++
		c.mu.Unlock()
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")
	httpReq.Header.Set("User-Agent", "go-victoriametrics-pusher/1.0")
	httpReq.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	client := &http.Client{
		Timeout: 10 * time.Second, // Set a timeout for the request
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		c.mu.Lock()
		c.errorCounter++
		c.mu.Unlock()
		return fmt.Errorf("Error sending data to VictoriaMetrics: %v\n", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		c.mu.Lock()
		c.code204Counter++
		c.mu.Unlock()
	}

	if resp.StatusCode == http.StatusOK {
		c.mu.Lock()
		c.code200Counter++
		c.mu.Unlock()
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		c.mu.Lock()
		c.errorCounter++
		c.mu.Unlock()
		return fmt.Errorf("VM client receives unexpected status code: %d", resp.StatusCode)
	} else {
		c.mu.Lock()
		c.metricsCounter++
		c.mu.Unlock()
	}

	return nil
}

func (c *VMClient) GetMetricsCounter() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.metricsCounter
}

func (c *VMClient) GetErrorCounter() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.errorCounter
}

func (c *VMClient) Get204Counter() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.code204Counter
}

func (c *VMClient) Get200Counter() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.code200Counter
}

func (c *VMClient) ResetStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metricsCounter = 0
	c.errorCounter = 0
	c.code204Counter = 0
	c.code200Counter = 0
}
