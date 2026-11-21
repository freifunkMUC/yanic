package cmd

import (
	"log/slog"
	"os"

	"codeberg.org/FreifunkBremen/yanic/database"
	"codeberg.org/FreifunkBremen/yanic/lib/log"
	"codeberg.org/FreifunkBremen/yanic/respond"
	"codeberg.org/FreifunkBremen/yanic/runtime"
	"codeberg.org/FreifunkBremen/yanic/webserver"

	"github.com/BurntSushi/toml"
)

// Config represents the whole configuration
type Config struct {
	Respondd  respond.Config
	Webserver webserver.Config
	Nodes     runtime.NodesConfig
	Database  database.Config
}

var (
	configPath string
	collector  *respond.Collector
	nodes      *runtime.Nodes
)

func loadConfig() *Config {
	config, err := ReadConfigFile(configPath)
	if err != nil {
		slog.Error("unable to load config file", log.AttrError(err))
		os.Exit(2)
	}
	return config
}

// ReadConfigFile reads a config model from path of a yml file
func ReadConfigFile(path string) (config *Config, err error) {
	config = &Config{}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("failed to close after read config", log.AttrError(err))
		}
	}()

	_, err = toml.NewDecoder(file).Decode(config)
	if err != nil {
		return nil, err
	}

	return
}
