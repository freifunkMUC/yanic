package influxdb

import (
	"testing"
	"time"

	influxdbAPI "github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"github.com/stretchr/testify/assert"

	"github.com/FreifunkBremen/yanic/runtime"
)

// fakeWriteAPI records points instead of sending them to a server
type fakeWriteAPI struct {
	points []*write.Point
}

func (f *fakeWriteAPI) WriteRecord(line string)                                   {}
func (f *fakeWriteAPI) WritePoint(point *write.Point)                             { f.points = append(f.points, point) }
func (f *fakeWriteAPI) Flush()                                                    {}
func (f *fakeWriteAPI) Errors() <-chan error                                      { return nil }
func (f *fakeWriteAPI) SetWriteFailedCallback(cb influxdbAPI.WriteFailedCallback) {}

func testInsertLink(t *testing.T, link *runtime.Link) *write.Point {
	api := &fakeWriteAPI{}
	conn := &Connection{
		config:   Config{},
		writeAPI: map[string]influxdbAPI.WriteAPI{MeasurementLink: api},
	}
	conn.InsertLink(link, time.Now())
	assert.Len(t, api.points, 1)
	point := api.points[0]
	assert.Equal(t, MeasurementLink, point.Name())
	return point
}

func pointFields(point *write.Point) map[string]interface{} {
	fields := map[string]interface{}{}
	for _, f := range point.FieldList() {
		fields[f.Key] = f.Value
	}
	return fields
}

func pointTags(point *write.Point) map[string]string {
	tags := map[string]string{}
	for _, tag := range point.TagList() {
		tags[tag.Key] = tag.Value
	}
	return tags
}

func TestInsertLinkBatmanIV(t *testing.T) {
	assert := assert.New(t)

	point := testInsertLink(t, &runtime.Link{
		SourceID:       "f4f26dd7a30b",
		SourceAddress:  "f4:f2:6d:d7:a3:0b",
		SourceHostname: "nodeB",
		TargetID:       "f4f26dd7a30a",
		TargetAddress:  "f4:f2:6d:d7:a3:0a",
		TQ:             0.5,
		Type:           runtime.WirelessLinkType,
	})

	tags := pointTags(point)
	assert.Equal("f4f26dd7a30b", tags["source.id"])
	assert.Equal("nodeB", tags["source.hostname"])
	assert.NotContains(tags, "target.hostname")
	assert.Equal("wifi", tags["type"])

	fields := pointFields(point)
	assert.Equal(float64(50), fields["tq"])
	assert.NotContains(fields, "throughput")
}

func TestInsertLinkBatmanV(t *testing.T) {
	assert := assert.New(t)

	throughput := uint32(24000)
	point := testInsertLink(t, &runtime.Link{
		SourceID:      "f4f26dd7a30d",
		SourceAddress: "f4:f2:6d:d7:a3:0d",
		TargetID:      "f4f26dd7a30c",
		TargetAddress: "f4:f2:6d:d7:a3:0c",
		Throughput:    &throughput,
		Type:          runtime.WirelessLinkType,
	})

	fields := pointFields(point)
	// must be int64, an uncast uint32 would be converted to uint64 by the client
	assert.Equal(int64(24000), fields["throughput"])
	assert.NotContains(fields, "tq")
	assert.Contains(write.PointToLineProtocol(point, time.Second), " throughput=24000i ")
}
