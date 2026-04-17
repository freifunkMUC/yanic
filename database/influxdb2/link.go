package influxdb

import (
	"time"

	"github.com/FreifunkBremen/yanic/runtime"

	influxdb "github.com/influxdata/influxdb-client-go/v2"
)

// InsertLink adds a link data point
func (conn *Connection) InsertLink(link *runtime.Link, t time.Time) {
	fields := map[string]interface{}{}
	if link.Throughput != nil {
		// cast to int64: the InfluxDB2 client would write uint32 as an unsigned (u) field
		fields["throughput"] = int64(*link.Throughput)
	} else {
		fields["tq"] = link.TQ * 100
	}
	p := influxdb.NewPoint(MeasurementLink,
		conn.config.Tags(),
		fields,
		t).
		AddTag("source.id", link.SourceID).
		AddTag("source.addr", link.SourceAddress).
		AddTag("target.id", link.TargetID).
		AddTag("target.addr", link.TargetAddress).
		AddTag("type", link.Type.String())
	if link.SourceHostname != "" {
		p.AddTag("source.hostname", link.SourceHostname)
	}
	if link.TargetHostname != "" {
		p.AddTag("target.hostname", link.TargetHostname)
	}
	conn.writeAPI[MeasurementLink].WritePoint(p)
}
