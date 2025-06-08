package internal

import (
	"context"
	"log"
	"sync"

	"github.com/prometheus/prometheus/prompb"
	metrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// MetricsProcessor stitches Kafka client, decoder and VM client together. It creates
// Kafka client and starts consumer loop when you call Start(). Then it calls processMessages()
// on a go routine which starts a loop that takes next message from the Kafka client, runs it through
// the decoder and sends produced VM metrics to VM client.
// Call to Close() stops all elements of the pipeline.
type MetricsProcessor struct {
	ctx            context.Context
	Kafka          *KafkaConsumer
	Vm             *VMClient
	messageCounter int
	decoder        *Decoder
	identity       int
	cancel         context.CancelFunc
	mu             sync.Mutex
	doNotPush      bool
}

// NewMetricsProcessor initializes and returns a new instance of MetricsProcessor. Builds Kafka client using
// passed consumer configuration `cc`. Builds VM client using `vmConfig`. Argument `identity` is the number
// of metrics processor created when the program was called with cli flag `--parallelism` with value greater than 1
func NewMetricsProcessor(parentCtx context.Context, cc ConsumerConfig, vmConfig VMClientConfig, identity int, drain bool) *MetricsProcessor {
	ctx, cancel := context.WithCancel(parentCtx)

	consumer, err := NewKafkaConsumer(ctx, cc)
	if err != nil {
		log.Fatalf("Failed to create consumer: %v", err)
	}

	vmClient, err := NewVMClient(vmConfig)
	if err != nil {
		log.Fatalf("Failed to create VM client: %v", err)
	}

	var decoder *Decoder
	if drain {
		decoder = NewDecoder(func(req *prompb.WriteRequest) error {
			return nil
		})
	} else {
		decoder = NewDecoder(vmClient.PushMetric)
	}

	return &MetricsProcessor{
		ctx:       ctx,
		cancel:    cancel,
		Kafka:     consumer,
		Vm:        vmClient,
		decoder:   decoder,
		identity:  identity,
		doNotPush: drain,
	}
}

func (s *MetricsProcessor) Start() {
	go s.Kafka.Start()
	go s.processMessages()
}

func (s *MetricsProcessor) Close() {
	log.Printf("%d: closing message processor", s.identity)
	s.cancel()
	s.Kafka.Close()
	s.Vm.Close()
	log.Printf("%d: message processor closed", s.identity)
}

func (s *MetricsProcessor) GetMessageCounter() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messageCounter
}

func (s *MetricsProcessor) ResetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messageCounter = 0
}

func (s *MetricsProcessor) processMessages() {

	log.Printf("%d: starting message processing", s.identity)
	for {
		select {
		case <-s.ctx.Done():
			log.Printf("%d: message processing stopped", s.identity)
			return
		default:
			msg, err := s.Kafka.GetMessage()
			if err != nil {
				log.Printf("%d: error getting message: %v", s.identity, err)
				continue
			}

			// Process the message
			s.processMessage(msg, s.identity)
		}
	}
}

func (s *MetricsProcessor) processMessage(msg []byte, num int) {
	// Example message processing
	s.mu.Lock()
	s.messageCounter++
	s.mu.Unlock()
	md := metrics.MetricsData{}
	err := proto.Unmarshal(msg, &md)
	if err != nil {
		log.Printf("%d: error unmarshaling message: %v", num, err)
		return
	}

	// remember that we have passed callback function to the Decoder when we created it.
	// Generated VM metrics are sent to VM client via that callback
	err = s.decoder.Process(&md)
	if err != nil {
		log.Printf("%d: error decoding message: %v", num, err)
		return
	}
}
