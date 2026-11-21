package graphite

import (
	"log/slog"
	"sync"

	"codeberg.org/FreifunkBremen/yanic/database"
	"codeberg.org/FreifunkBremen/yanic/lib/log"

	"github.com/fgrosse/graphigo"
)

const (
	MeasurementNode               = "node"        // Measurement for per-node statistics
	MeasurementGlobal             = "global"      // Measurement for summarized global statistics
	CounterMeasurementFirmware    = "firmware"    // Measurement for firmware statistics
	CounterMeasurementModel       = "model"       // Measurement for model statistics
	CounterMeasurementAutoupdater = "autoupdater" // Measurement for autoupdater
)

type Connection struct {
	database.Connection
	client graphigo.Client
	points chan []graphigo.Metric
	wg     sync.WaitGroup
}

type Config map[string]interface{}

func (c Config) Address() string {
	return c["address"].(string)
}

func (c Config) Prefix() string {
	return c["prefix"].(string)
}

func Connect(configuration map[string]interface{}) (database.Connection, error) {
	config := Config(configuration)

	con := &Connection{
		client: graphigo.Client{
			Address: config.Address(),
			Prefix:  config.Prefix(),
		},
		points: make(chan []graphigo.Metric, 1000),
	}

	if err := con.client.Connect(); err != nil {
		return nil, err
	}

	con.wg.Add(1)
	go con.addWorker()

	return con, nil
}

func (c *Connection) Close() {
	close(c.points)
	if c.client.Connection != nil {
		if err := c.client.Close(); err != nil {
			slog.Error("unable close connection", log.AttrError(err))
		}
	}
}

func (c *Connection) addWorker() {
	defer c.wg.Done()
	defer c.Close()
	for point := range c.points {
		if err := c.client.SendAll(point); err != nil {
			log.Panic("unable to store data", log.AttrError(err), "database", "graphite")
			return
		}
	}
}

func (c *Connection) addPoint(point []graphigo.Metric) {
	c.points <- point
}

func init() {
	database.RegisterAdapter("graphite", Connect)
}
