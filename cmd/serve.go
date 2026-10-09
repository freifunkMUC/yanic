package cmd

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	allDatabase "codeberg.org/FreifunkBremen/yanic/database/all"
	"codeberg.org/FreifunkBremen/yanic/lib/log"
	allOutput "codeberg.org/FreifunkBremen/yanic/output/all"
	"codeberg.org/FreifunkBremen/yanic/respond"
	"codeberg.org/FreifunkBremen/yanic/runtime"
	"codeberg.org/FreifunkBremen/yanic/webserver"

	"github.com/spf13/cobra"
)

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:     "serve",
	Short:   "Runs the yanic server",
	Example: "yanic serve --config /etc/yanic.toml",
	Run: func(cmd *cobra.Command, args []string) {
		config := loadConfig()

		err := allDatabase.Start(config.Database)
		if err != nil {
			log.Panic("could not connect to database", log.AttrError(err))
		}
		defer allDatabase.Close()

		nodes = runtime.NewNodes(&config.Nodes)
		nodes.Start()

		err = allOutput.Start(nodes, config.Nodes)
		if err != nil {
			log.Panic("error on init outputs", log.AttrError(err))
		}
		defer allOutput.Close()

		if config.Webserver.Enable {
			slog.Info("starting webserver", "address", config.Webserver.Bind)
			srv := webserver.New(config.Webserver.Bind, config.Webserver.Webroot)
			go webserver.Start(srv)
			defer func() {
				if err := srv.Close(); err != nil {
					log.Panic("could not stop webserver", log.AttrError(err))
				}
			}()
		}

		if config.Respondd.Enable {
			// Delaying startup to start at a multiple of `duration` since the zero time.
			if duration := config.Respondd.Synchronize.Duration; duration > 0 {
				now := time.Now()
				delay := duration - now.Sub(now.Truncate(duration))
				slog.Info("delaying respondd request", "delay", delay.Seconds())
				time.Sleep(delay)
			}

			collector = respond.NewCollector(allDatabase.Conn, nodes, &config.Respondd)
			collector.Start(config.Respondd.CollectInterval.Duration)
			defer collector.Close()
		}

		// Wait for INT/TERM
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigs
		slog.Info("received syscall", "signal", sig)

	},
}

func init() {
	RootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVarP(&configPath, "config", "c", "config.toml", "Path to configuration file")
}
