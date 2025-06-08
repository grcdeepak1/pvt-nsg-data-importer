package internal

import (
	"context"
	"log"
	"time"
)

const SelfMonitoringInterval = time.Duration(10) * time.Second

type Monitor struct {
	ctx        context.Context
	processors []*MetricsProcessor
}

func NewMonitor(ctx context.Context, processors []*MetricsProcessor) *Monitor {
	return &Monitor{ctx: ctx, processors: processors}
}

func (s *Monitor) Start() {
	ticker := time.NewTicker(SelfMonitoringInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			log.Printf("Lag monitoring stopped")
			return
		case <-ticker.C:
			s.report()
		}
	}

}

// report() reads some useful statistics from Kafka client and VM client and logs it every SelfMonitoringInterval.
//
// In production, you should build metrics from this and send them to your Prometheus or Victoria Metrics
// instead of, or in addition to, logging
//
// Right now this collects and logs the following:
//   - number of received messages per 10 sec
//   - receive rate in msg/sec
//   - total (combined) consumer group lag computed as sum of lag in each partition.
//   - count of metrics sent to Victoria Metrics
//   - count of errors observed while pushing to Victoria Metrics
//
// The lag is extremely important because it indicates whether your program has enough
// capacity to handle incoming data stream. If the data comes too fast, the lag will grow.
// In production you should have a metric in Prometheus or VM to track this lag, this is
// how you know that your collector is working properly, is not stuck and is not losing
// data.
func (s *Monitor) report() {
	var mcount int64 = 0
	var totalLag int64 = 0
	var vmMetricsCount int64 = 0
	var vmErrorCount int64 = 0
	var vm204Count int64 = 0
	var vm200Count int64 = 0

	for _, processor := range s.processors {
		lag, err := processor.Kafka.GetConsumerLag()
		if err != nil {
			log.Printf("Error getting lag: %v", err)
			continue
		}
		mcount += int64(processor.GetMessageCounter())
		processor.ResetStats()

		for _, partitionLag := range lag {
			//log.Printf("Partition %d lag: %d", partition, partitionLag)
			totalLag += partitionLag
		}

		vmMetricsCount += processor.Vm.GetMetricsCounter()
		vmErrorCount += processor.Vm.GetErrorCounter()
		vm204Count += processor.Vm.Get204Counter()
		vm200Count += processor.Vm.Get200Counter()
		processor.Vm.ResetStats()
	}

	receiverRate := float64(mcount) / float64(SelfMonitoringInterval.Seconds())

	log.Printf("total lag across all partitions: %d; processed %d messages; rate %.2f msg/sec; VM metrics pushed: %d; VM errors: %d, VM_204: %d, VM_200: %d",
		totalLag, mcount, receiverRate, vmMetricsCount, vmErrorCount, vm204Count, vm200Count)

}
