package respond

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/tidwall/gjson"

	"codeberg.org/FreifunkBremen/yanic/data"
	"codeberg.org/FreifunkBremen/yanic/lib/log"
)

const (
	// default multicast group used by announced
	MulticastAddressDefault = "ff05:0:0:0:0:0:2:1001"

	// default udp port used by announced
	PortDefault = 1001

	// maximum receivable size
	MaxDataGramSize = 8192

	// maximum size of a decompressed response
	MaxDecompressedSize = 1 << 20
)

// Response of the respond request
type Response struct {
	Address *net.UDPAddr
	Raw     []byte
}

func NewRespone(res *data.ResponseData, addr *net.UDPAddr) (*Response, error) {
	buf := new(bytes.Buffer)
	flater, err := flate.NewWriter(buf, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := flater.Close(); err != nil {
			slog.Error("failed to close compression", log.AttrError(err))
		}
	}()

	if err = json.NewEncoder(flater).Encode(res); err != nil {
		return nil, err
	}

	err = flater.Flush()

	return &Response{
		Raw:     buf.Bytes(),
		Address: addr,
	}, err
}

func (res *Response) parse(customFields []CustomFieldConfig, metrics *metricsExtractor) (*data.ResponseData, error) {
	// Deflate
	deflater := flate.NewReader(bytes.NewReader(res.Raw))
	defer func() {
		if err := deflater.Close(); err != nil {
			slog.Error("failed to close uncompression", log.AttrError(err))
		}
	}()
	jsonData, err := io.ReadAll(io.LimitReader(deflater, MaxDecompressedSize+1))
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	if len(jsonData) > MaxDecompressedSize {
		return nil, fmt.Errorf("decompressed response is larger than %d bytes", MaxDecompressedSize)
	}

	// Unmarshal
	rdata := &data.ResponseData{}
	err = json.Unmarshal(jsonData, rdata)

	rdata.CustomFields = make(map[string]interface{})
	if !gjson.Valid(string(jsonData)) {
		slog.Info("JSON data is invalid", "jsonData", jsonData)
	} else {
		jsonParsed := gjson.Parse(string(jsonData))
		for _, customField := range customFields {
			field := jsonParsed.Get(customField.Path)
			if field.Exists() {
				rdata.CustomFields[customField.Name] = field.String()
			}
		}
	}

	if err == nil {
		rdata.Metrics = metrics.extract(jsonData)
		rdata.Raw = rawSections(jsonData)
	}

	return rdata, err
}

// rawSections returns the JSON of each known respondd section of a response.
func rawSections(jsonData []byte) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(jsonData, &all); err != nil {
		return nil
	}
	sections := make(map[string]json.RawMessage, 3)
	for _, name := range []string{"nodeinfo", "statistics", "neighbours"} {
		if raw, ok := all[name]; ok && len(raw) > 0 && string(raw) != "null" {
			sections[name] = raw
		}
	}
	if len(sections) == 0 {
		return nil
	}
	return sections
}
