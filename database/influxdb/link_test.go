package influxdb

import (
	"testing"
	"time"

	client "github.com/influxdata/influxdb1-client/v2"
	"github.com/stretchr/testify/assert"

	"github.com/FreifunkBremen/yanic/runtime"
)

func testInsertLink(t *testing.T, link *runtime.Link) *client.Point {
	connection := &Connection{
		config: map[string]interface{}{},
		points: make(chan *client.Point, 1),
	}
	connection.InsertLink(link, time.Now())
	point := <-connection.points
	assert.NotNil(t, point)
	assert.Equal(t, MeasurementLink, point.Name())
	return point
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

	tags := point.Tags()
	assert.Equal("f4f26dd7a30b", tags["source.id"])
	assert.Equal("nodeB", tags["source.hostname"])
	assert.NotContains(tags, "target.hostname")
	assert.Equal("wifi", tags["type"])

	fields, err := point.Fields()
	assert.NoError(err)
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

	fields, err := point.Fields()
	assert.NoError(err)
	// influxdb1-client encodes uint32 as a signed integer field
	assert.Equal(int64(24000), fields["throughput"])
	assert.NotContains(fields, "tq")
	assert.Contains(point.String(), " throughput=24000i ")
}
