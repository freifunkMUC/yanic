package respondd

/**
 * This database type is for injecting into another yanic instance.
 */
import (
	"bufio"
	"compress/flate"
	"encoding/json"
	"log/slog"
	"net"
	"time"

	"codeberg.org/FreifunkBremen/yanic/data"
	"codeberg.org/FreifunkBremen/yanic/database"
	"codeberg.org/FreifunkBremen/yanic/lib/log"
	"codeberg.org/FreifunkBremen/yanic/runtime"
)

type Connection struct {
	database.Connection
	config Config
	conn   net.Conn
}

type Config map[string]interface{}

func (c Config) Type() string {
	return c["type"].(string)
}

func (c Config) Address() string {
	return c["address"].(string)
}

var logger *slog.Logger

func init() {
	database.RegisterAdapter("respondd", Connect)
	logger = slog.With("type", "database-yanic")
}

func Connect(configuration map[string]interface{}) (database.Connection, error) {
	config := Config(configuration)

	conn, err := net.Dial(config.Type(), config.Address())
	if err != nil {
		return nil, err
	}

	return &Connection{conn: conn, config: config}, nil
}

func (conn *Connection) InsertNode(node *runtime.Node) {
	res := &data.ResponseData{
		Nodeinfo:   node.Nodeinfo,
		Statistics: node.Statistics,
		Neighbours: node.Neighbours,
	}

	writer := bufio.NewWriterSize(conn.conn, 8192)

	flater, err := flate.NewWriter(writer, flate.BestCompression)
	if err != nil {
		logger.Error("could not create flater", log.AttrError(err))
		return
	}
	defer func() {
		if err := flater.Close(); err != nil {
			logger.Error("could not close flater", log.AttrError(err))
		}
	}()
	err = json.NewEncoder(flater).Encode(res)
	if err != nil {
		nodeid := "unknown"
		if node.Nodeinfo != nil && node.Nodeinfo.NodeID != "" {
			nodeid = node.Nodeinfo.NodeID
		}
		logger.Error("could not encode node", log.AttrError(err), "node_id", nodeid)
		return
	}
	err = flater.Flush()
	if err != nil {
		logger.Error("could not compress", log.AttrError(err))
	}
	err = writer.Flush()
	if err != nil {
		logger.Error("could not send", log.AttrError(err))
	}
}

func (conn *Connection) InsertLink(link *runtime.Link, time time.Time) {
}

func (conn *Connection) InsertGlobals(stats *runtime.GlobalStats, time time.Time, site string, domain string) {
}

func (conn *Connection) PruneNodes(deleteAfter time.Duration) {
}

func (conn *Connection) Close() {
	if err := conn.conn.Close(); err != nil {
		logger.Error("cound not close socket", log.AttrError(err))
	}
}
