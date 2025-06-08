package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitlab.com/crusoeenergy/neteng/nsg-data-importer/internal"
)

type Config struct {
	KafkaConnect  string
	CertFile      string
	AccessKeyFile string
	CaPem         string
	Topic         string
	GroupID       string
	Parallelism   int
	VMUrl         string
	VMPath        string
	VMTimeout     time.Duration
	Drain         bool
	InitialOffset string
}

func main() {
	config := Config{}

	// Define command line flags
	flag.StringVar(&config.KafkaConnect, "kafka_connect", "crusoe-kafka-nsg-cloud-1.a.aivencloud.com:23580", "Kafka connection string (required)")
	flag.StringVar(&config.CertFile, "cert", "./secrets/service.cert",
		"Path to certificate file, typically 'service.cert' (required)")
	flag.StringVar(&config.AccessKeyFile, "access-key", "./secrets/service.key",
		"Path to access key file, typically 'service.key' (required)")
	flag.StringVar(&config.CaPem, "ca_pem", "./secrets/ca.pem",
		"Path to ca.pem, (required)")
	flag.StringVar(&config.Topic, "topic", "nsg.crusoe.metrics-export.v1.pb", "Kafka topic name (required)")
	flag.IntVar(&config.Parallelism, "parallelism", 5, "Consumer parallelism (default 1)")

	flag.StringVar(&config.VMUrl, "vm_url", "http://localhost:8480",
		//flag.StringVar(&config.VMUrl, "vm_url", "http://localhost:8428", or "http://localhost:8480"
		"Victoria Metrics connection url (required). Example: http://localhost:8428/api/v1/write")
	flag.StringVar(&config.VMPath, "vm_path", "/api/v1/import", "Path for writing metrics")
	flag.DurationVar(&config.VMTimeout, "vm_timeout", 5*time.Second,
		"Timeout for Victoria Metrics requests (default 5s)")
	flag.BoolVar(&config.Drain, "drain", false,
		"Read messages from Kafka, decode, but do not push metrics to Victoria Metrics. Useful for testing purposes and to drain the topic manually (default false)")

	flag.StringVar(
		&config.InitialOffset,
		"initial_offset",
		"oldest",
		"Initial offset to start reading from. Valid values are 'oldest' (default) or 'newest'. If you use 'newest', all messages sitting in the topic while this program was not running will be lost",
	)

	// Parse command line flags
	flag.Parse()

	// group ID must be unique so as not to be mixed up with other consumer groups connected to same
	// Kafka brokers, otherwise it can be anything. Somtimes UUID is used, but that creates its own problems
	// because it changes after program restart and if you want to run several instances of this program,
	// they must always agree on the group ID. Also, it helps troubleshoot things if you can inspect
	// state of the consumer group on the server side using Kafka command line tools (such as kafkactl).
	// This seems to be pretty explicit as to what it means and also unique enough.
	config.GroupID = "nsg-otel-collector"

	// Validate all required flags
	var missingFlags []string

	if config.KafkaConnect == "" {
		missingFlags = append(missingFlags, "kafka_connect")
	}
	if config.Topic == "" {
		missingFlags = append(missingFlags, "topic")
	}

	if config.VMUrl == "" {
		missingFlags = append(missingFlags, "vm_url")
	}
	if config.CertFile == "" {
		missingFlags = append(missingFlags, "cert")
	}

	if config.AccessKeyFile == "" {
		missingFlags = append(missingFlags, "access-key")
	}
	if len(missingFlags) > 0 {
		fmt.Printf("Error: required flags not provided: %v\n", missingFlags)
		flag.Usage()
		os.Exit(1)
	}

	if config.InitialOffset != "oldest" && config.InitialOffset != "newest" {
		fmt.Println("Error: invalid value for 'initial_offset'. Valid values are 'oldest' or 'newest'.")
		flag.Usage()
		os.Exit(1)
	}

	// Validate file existence
	if config.CertFile != "" {
		if _, err := os.Stat(config.CertFile); os.IsNotExist(err) {
			fmt.Printf("Error: certificate file not found: %s\n", config.CertFile)
			os.Exit(1)
		}
	}

	if config.AccessKeyFile != "" {
		if _, err := os.Stat(config.AccessKeyFile); os.IsNotExist(err) {
			fmt.Printf("Error: access key file not found: %s\n", config.AccessKeyFile)
			os.Exit(1)
		}
	}

	caCert, err := os.ReadFile(config.CaPem)
	if err != nil {
		fmt.Printf("Error reading ca.pem: %v\n", err)
		return
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	// Your application logic here
	fmt.Printf("Configuration:\n")
	fmt.Printf("Kafka Connect: %s\n", config.KafkaConnect)
	fmt.Printf("Certificate File: %s\n", config.CertFile)
	fmt.Printf("Access Key File: %s\n", config.AccessKeyFile)
	fmt.Printf("Topic: %s\n", config.Topic)
	fmt.Printf("Consumer group: %s\n", config.GroupID)
	fmt.Printf("Parallelism: %d\n", config.Parallelism)
	fmt.Printf("Victoria Metrics URL: %s\n", config.VMUrl)
	fmt.Printf("Victoria Metrics Timeout: %s\n", config.VMTimeout)
	fmt.Printf("Drain: %t\n", config.Drain)
	fmt.Printf("Initial Offset: %s\n", config.InitialOffset)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("Received signal: %v, initiating shutdown...", sig)
		cancel()
	}()

	vmConfig := internal.VMClientConfig{
		ServerURL:  config.VMUrl,
		Timeout:    config.VMTimeout,
		ImportPath: config.VMPath,
	}

	cert, err := tls.LoadX509KeyPair(config.CertFile, config.AccessKeyFile)
	if err != nil {
		log.Fatalf("Failed to load certificate: %v", err)
	}

	// Get instance identifier (could be from environment variable, command line flag, etc.)
	// You can pass instance name via environment variable when this runs on Kubernetes. Example:
	//
	//    # Example Kubernetes deployment
	//   apiVersion: apps/v1
	//   kind: Deployment
	//   metadata:
	//     name: metrics-consumer
	//   spec:
	//     replicas: 3  # Number of instances you want to run
	//     template:
	//       spec:
	//         containers:
	//         - name: metrics-consumer
	//           env:
	//           - name: INSTANCE_ID
	//             valueFrom:
	//               fieldRef:
	//                 fieldPath: metadata.name
	//
	instanceID := os.Getenv("INSTANCE_ID")
	if instanceID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		instanceID = hostname
	}

	cc := internal.ConsumerConfig{
		Brokers:       []string{config.KafkaConnect},
		Topic:         config.Topic,
		GroupID:       config.GroupID,
		Certificate:   cert,
		CaCertPool:    caCertPool,
		InitialOffset: config.InitialOffset,
	}

	processors := make([]*internal.MetricsProcessor, 0, config.Parallelism)

	for identity := range config.Parallelism {
		cc.ClientID = fmt.Sprintf("metrics-consumer-%s-%d", instanceID, identity)
		mp := internal.NewMetricsProcessor(ctx, cc, vmConfig, identity, config.Drain)
		processors = append(processors, mp)
		mp.Start()
	}

	internal.NewMonitor(ctx, processors).Start()

	<-ctx.Done()

	for _, mp := range processors {
		mp.Close()
	}

	log.Printf("All done")
}
