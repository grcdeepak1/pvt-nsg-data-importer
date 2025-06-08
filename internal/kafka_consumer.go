package internal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/IBM/sarama"
)

// KafkaConsumer wraps Kafka client and takes care of initial connection setup, including user auth
// and TLS set up, reconnects on errors, Kafka consumer loop and simple self monitoring where it records
// and logs consumer lag.
//
// Consumer loop starts when you call Start(). To stop it and clear resources, call Close().
//
// Call GetMessage() to get next available message. This class makes no assumption about the structure
// of keys and values in messages so it just returns `[]byte`.
type KafkaConsumer struct {
	client   sarama.Client
	consumer sarama.ConsumerGroup
	topic    string
	groupID  string
	brokers  []string
	handler  *consumerGroupHandler
	ctx      context.Context
	cancel   context.CancelFunc
}

type ConsumerConfig struct {
	Brokers       []string
	Topic         string
	GroupID       string
	ClientID      string // Added ClientID field
	Certificate   tls.Certificate
	CaCertPool    *x509.CertPool
	InitialOffset string
}

type consumerGroupHandler struct {
	ready    chan bool
	messages chan []byte
}

func NewKafkaConsumer(parentCtx context.Context, config ConsumerConfig) (*KafkaConsumer, error) {

	ctx, cancel := context.WithCancel(parentCtx)

	if config.ClientID == "" {
		// Generate a unique client ID if none provided
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		config.ClientID = fmt.Sprintf("%s-%s-%d", config.GroupID, hostname, time.Now().UnixNano())
	}

	// Create Sarama config
	cfg := sarama.NewConfig()
	cfg.ClientID = config.ClientID
	cfg.Net.DialTimeout = 30 * time.Second // this is equal to the default

	// Configure TLS
	// todo: Uncomment and configure TLS if needed
	cfg.Net.TLS.Enable = true
	cfg.Net.TLS.Config = &tls.Config{
		RootCAs:      config.CaCertPool,
		Certificates: []tls.Certificate{config.Certificate},
	}

	// Consumer group settings
	cfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	if config.InitialOffset == "newest" {
		cfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	} else {
		cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	}

	// Create client
	client, err := sarama.NewClient(config.Brokers, cfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Create consumer group
	consumer, err := sarama.NewConsumerGroupFromClient(config.GroupID, client)
	if err != nil {
		cancel()
		client.Close()
		return nil, fmt.Errorf("failed to create consumer group: %w", err)
	}

	handler := &consumerGroupHandler{
		ready:    make(chan bool),
		messages: make(chan []byte, 1000),
	}

	mc := &KafkaConsumer{
		ctx:      ctx,
		client:   client,
		consumer: consumer,
		topic:    config.Topic,
		groupID:  config.GroupID,
		brokers:  config.Brokers,
		handler:  handler,
		cancel:   cancel,
	}

	return mc, nil
}

func (c *KafkaConsumer) Start() {
	// Start consuming
	c.consume()
}

func (c *KafkaConsumer) Close() error {
	log.Printf("closing consumer")
	c.cancel()
	if err := c.consumer.Close(); err != nil {
		return fmt.Errorf("failed to close consumer: %w", err)
	}
	if err := c.client.Close(); err != nil {
		return fmt.Errorf("failed to close client: %w", err)
	}
	log.Printf("consumer closed")
	return nil
}

func (c *KafkaConsumer) consume() {

	for {
		// Check if context was cancelled, signaling shutdown
		if c.ctx.Err() != nil {
			return
		}

		log.Printf("    Starting consumer for topic %s", c.topic)
		err := c.consumer.Consume(c.ctx, []string{c.topic}, c.handler)
		log.Printf("    Stopped consumer for topic %s error=%v", c.topic, err)

		// Check if context was cancelled, signaling shutdown
		if c.ctx.Err() != nil {
			return
		}

		// Reconnect after a brief delay
		time.Sleep(time.Second)
	}
}

func (c *KafkaConsumer) GetMessage() ([]byte, error) {
	select {
	case msg := <-c.handler.messages:
		return msg, nil
	case <-c.ctx.Done():
		return nil, fmt.Errorf("consumer closed")
	}
}

// GetConsumerLag retrieves the lag for each partition in the consumer group, returning a map of
// partition IDs to lag values. It returns an error if there are issues fetching partition metadata or offsets.
//
// This function reads offsets in partitions the consumer has been assigned. If you
// run several consumers in parallel, each will track lag only in its own subset of partitions.
func (c *KafkaConsumer) GetConsumerLag() (map[int32]int64, error) {
	lag := make(map[int32]int64)

	partitions, err := c.client.Partitions(c.topic)
	if err != nil {
		return nil, fmt.Errorf("failed to get partitions: %w", err)
	}

	// Create an offset manager for this consumer group
	offsetManager, err := sarama.NewOffsetManagerFromClient(c.groupID, c.client)
	if err != nil {
		return nil, fmt.Errorf("failed to create offset manager: %w", err)
	}
	defer offsetManager.Close()

	for _, partition := range partitions {
		latestOffset, err := c.client.GetOffset(c.topic, partition, sarama.OffsetNewest)
		if err != nil {
			return nil, fmt.Errorf("failed to get latest offset for partition %d: %w", partition, err)
		}

		// Get partition offset manager
		partitionOffsetManager, err := offsetManager.ManagePartition(c.topic, partition)
		if err != nil {
			return nil, fmt.Errorf("failed to get partition offset manager for partition %d: %w", partition, err)
		}

		// Get the current offset for this consumer group
		currentOffset, _ := partitionOffsetManager.NextOffset()
		if currentOffset == -1 {
			// No committed offset found, using 0 to show full lag
			currentOffset = 0
		}

		lag[partition] = latestOffset - currentOffset
	}

	return lag, nil
}

// Implementation of sarama.ConsumerGroupHandler interface
func (h *consumerGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	close(h.ready)
	return nil
}

func (h *consumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error {
	h.ready = make(chan bool)
	return nil
}

func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for message := range claim.Messages() {
		select {
		case h.messages <- message.Value:
			session.MarkMessage(message, "")
		case <-session.Context().Done():
			return nil
		}
	}
	return nil
}
