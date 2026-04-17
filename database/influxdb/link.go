package influxdb

import (
	"time"

	"github.com/FreifunkBremen/yanic/runtime"
	models "github.com/influxdata/influxdb1-client/models"
)

// InsertLink adds a link data point
func (conn *Connection) InsertLink(link *runtime.Link, t time.Time) {
	tags := models.Tags{}
	tags.SetString("source.id", link.SourceID)
	tags.SetString("source.addr", link.SourceAddress)
	tags.SetString("target.id", link.TargetID)
	tags.SetString("target.addr", link.TargetAddress)
	tags.SetString("type", link.Type.String())
	if link.SourceHostname != "" {
		tags.SetString("source.hostname", link.SourceHostname)
	}
	if link.TargetHostname != "" {
		tags.SetString("target.hostname", link.TargetHostname)
	}

	fields := models.Fields{}
	if link.Throughput != nil {
		fields["throughput"] = *link.Throughput
	} else {
		fields["tq"] = link.TQ * 100
	}
	conn.addPoint(MeasurementLink, tags, fields, t)
}
