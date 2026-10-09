package respond

import (
	"bytes"
	"compress/flate"
	"encoding/json"
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

func (res *Response) parse(customFields []CustomFieldConfig) (*data.ResponseData, error) {
	// Deflate
	deflater := flate.NewReader(bytes.NewReader(res.Raw))
	defer func() {
		if err := deflater.Close(); err != nil {
			slog.Error("failed to close uncompression", log.AttrError(err))
		}
	}()
	jsonData, err := io.ReadAll(deflater)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
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

	return rdata, err
}
