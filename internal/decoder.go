package internal

import (
	"fmt"
	"time"

	"github.com/prometheus/prometheus/prompb"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	v1 "go.opentelemetry.io/proto/otlp/common/v1" // Use this for common types
	metrics "go.opentelemetry.io/proto/otlp/metrics/v1"
	resource "go.opentelemetry.io/proto/otlp/resource/v1"

	"log"
	"strconv"
)

// VMMetric represents a Victoria Metrics metric with its labels and value
type VMMetric struct {
	Name      string
	Labels    map[string][]string
	Value     float64
	Timestamp uint64
}

// MetricCallback is a function type that will be called to push metrics to Victoria Metrics
type MetricCallback func(request *prompb.WriteRequest) error

// Decoder processes OpenTelemetry metrics and converts them to Victoria Metrics format which
// it sends to the caller by calling callback provided to it via constructor parameter
type Decoder struct {
	callback  MetricCallback
	deviceRef *resource.Resource // Store reference to the most recent device Resource
	deviceId  int
}

// NewDecoder creates a new instance of Decoder
func NewDecoder(callback MetricCallback) *Decoder {
	return &Decoder{
		callback: callback,
	}
}

// ToPromTimeSeries converts VMMetric to Prometheus TimeSeries
func (m *VMMetric) ToPromTimeSeries() *prompb.TimeSeries {
	// Convert labels to prometheus format
	labels := make([]prompb.Label, 0)
	for k, v := range m.Labels {
		if len(v) > 0 {
			labels = append(labels, prompb.Label{
				Name:  k,
				Value: v[0],
			})
		}
	}
	// Add __name__ label
	labels = append(labels, prompb.Label{
		Name:  "__name__",
		Value: "Nsg" + m.Name,
	})

	return &prompb.TimeSeries{
		Labels: labels,
		Samples: []prompb.Sample{{
			Value:     m.Value,
			Timestamp: int64(m.Timestamp),
		}},
	}
}

// Process processes the OpenTelemetry MetricsData message
func (d *Decoder) Process(data *metrics.MetricsData) error {
	writeRequest := &prompb.WriteRequest{
		Timeseries: make([]prompb.TimeSeries, 0),
	}
	for _, rm := range data.ResourceMetrics {
		if err := d.processResourceMetrics(rm, writeRequest); err != nil {
			return fmt.Errorf("failed to process resource metrics: %w", err)
		}
	}
	d.callback(writeRequest)
	return nil
}

func (d *Decoder) processResourceMetrics(rm *metrics.ResourceMetrics, writeRequest *prompb.WriteRequest) error {
	if rm.Resource == nil || rm.Resource.Attributes == nil {
		return nil
	}

	// Get resource type
	var resourceType string
	for _, attr := range rm.Resource.Attributes {
		if attr.Key == "type" {
			resourceType = attr.GetValue().GetStringValue()
			break
		}
	}

	switch resourceType {
	case "variable":
		return nil // Ignore variable type

	case "device":
		// Store device reference for later use
		d.deviceRef = rm.Resource

		// Create device_info metric
		metric := &VMMetric{
			Name:      "device_info",
			Labels:    make(map[string][]string),
			Value:     1,
			Timestamp: uint64(time.Now().UnixMilli()),
		}

		// Add all attributes as labels, converting "id" to "device_id"
		for _, attr := range rm.Resource.Attributes {
			if attr.Key == "type" {
				continue
			}
			if attr.Key == "id" {
				d.deviceId = int(attr.GetValue().GetIntValue())
				d.appendLabel(metric.Labels, "device_id", strconv.Itoa(d.deviceId))
			} else {
				d.appendLabel(metric.Labels, attr.Key, attr.GetValue().GetStringValue())
			}
		}
		writeRequest.Timeseries = append(writeRequest.Timeseries, *metric.ToPromTimeSeries())
		//log.Printf("ResourceType: %s: %v", resourceType, metric)
		return nil

	case "Interface", "GenericHardwareComponent", "ProtocolDescriptor", "ChassisAlarm", "MplsTunnel":
		if len(rm.ScopeMetrics) == 0 {
			return nil // Skip if no scope metrics
		}

		if d.deviceRef == nil {
			return fmt.Errorf("no device reference found for %s", resourceType)
		}

		return d.processScopeMetrics(rm, writeRequest)

	default:
		return fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

func (d *Decoder) appendLabel(labels map[string][]string, key string, value string) {
	labelValues, ok := labels[key]
	if !ok {
		labels[key] = []string{value}
	} else {
		labels[key] = append(labelValues, value)
	}
}

func (d *Decoder) processScopeMetrics(rm *metrics.ResourceMetrics, writeRequest *prompb.WriteRequest) error {
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			// Process each data point
			for _, dp := range d.getDataPoints(metric) {
				vmMetric := &VMMetric{
					Name:   metric.Name,
					Labels: make(map[string][]string),
					Value:  d.extractValue(dp),
					// Timestamp: uint64(time.Now().UnixMilli()),
					Timestamp: dp.GetTimeUnixNano() / 1000000, // should be ms
				}

				// Add resource attributes as labels
				for _, attr := range rm.Resource.Attributes {
					if attr.Key == "type" {
						continue
					}

					if attr.GetValue().Value == nil {
						continue
					}
					val := d.getValueAsString(attr)
					if val != "" {
						d.appendLabel(vmMetric.Labels, attr.Key, val)
					}
				}

				// Add device_id from stored device reference
				d.appendLabel(vmMetric.Labels, "device_id", strconv.Itoa(d.deviceId))

				writeRequest.Timeseries = append(writeRequest.Timeseries, *vmMetric.ToPromTimeSeries())
				//log.Printf("Processed metric: %s with value: %f", vmMetric.Name, vmMetric.Value)
			}
		}
	}
	return nil
}

// getDataPoints returns the data points from a metric based on its type
func (d *Decoder) getDataPoints(metric *metrics.Metric) []*metrics.NumberDataPoint {
	switch metric.Data.(type) {
	case *metrics.Metric_Gauge:
		return metric.GetGauge().DataPoints
	case *metrics.Metric_Sum:
		return metric.GetSum().DataPoints
	default:
		return nil
	}
}

// extractValue extracts the numeric value from a NumberDataPoint
func (d *Decoder) extractValue(dp *metrics.NumberDataPoint) float64 {
	switch dp.Value.(type) {
	case *metrics.NumberDataPoint_AsDouble:
		return dp.GetAsDouble()
	case *metrics.NumberDataPoint_AsInt:
		return float64(dp.GetAsInt())
	default:
		return 0
	}
}

func (d *Decoder) getValueAsString(attr *v1.KeyValue) string {
	switch v := attr.GetValue().Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(v.IntValue, 10)
	default:
		log.Printf("Unsupported attribute value type for key: %s", attr.Key)
		return ""
	}

}
