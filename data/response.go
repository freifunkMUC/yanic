package data

import "encoding/json"

// ResponseData struct
type ResponseData struct {
	Neighbours   *Neighbours            `json:"neighbours"`
	Nodeinfo     *Nodeinfo              `json:"nodeinfo"`
	Statistics   *Statistics            `json:"statistics"`
	CustomFields map[string]interface{} `json:"-"`
	Metrics      []MetricPoint          `json:"-"`
	// Raw holds the received JSON of each section (nodeinfo, statistics, neighbours).
	// The respondd database sends it on unchanged, so keys without a Go field are kept.
	Raw map[string]json.RawMessage `json:"-"`
}

// MetricPoint is one object of respondd data selected by a
// [respondd.metrics] rule. Tags hold the captured keys; Fields hold the
// numeric values below the matched object.
type MetricPoint struct {
	Section     string // respondd section: nodeinfo, statistics or neighbours
	Measurement string
	Tags        map[string]string
	Fields      map[string]float64
}

// MetricsReservedMeasurements are measurements with built-in data.
// A rule can not write to them.
var MetricsReservedMeasurements = map[string]bool{
	"node": true, "link": true, "dhcp": true, "global": true,
	"firmware": true, "model": true, "autoupdater": true,
}

// MetricsReservedTags are tags that Yanic sets on every metric point.
var MetricsReservedTags = map[string]bool{
	"nodeid": true, "hostname": true, "site": true, "domain": true,
}

// ValidMetricsMeasurement reports whether name is a valid measurement for metrics.
func ValidMetricsMeasurement(name string) bool {
	return ValidMetricsIdentifier(name, 64) && !MetricsReservedMeasurements[name]
}

// ValidMetricsIdentifier checks [a-z][a-z0-9_]{0,max-1}.
func ValidMetricsIdentifier(s string, max int) bool {
	if len(s) == 0 || len(s) > max || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}
