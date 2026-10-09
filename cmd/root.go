package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/ThreeDotsLabs/humanslog"
	"github.com/spf13/cobra"
)

var (
	logFormat  string
	logSource  bool
	timestamps bool
	logLevel   slog.Level
)

const (
	logFormatJSON  = "json"
	logFormatText  = "text"
	logFormatHuman = "human"
)

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "yanic",
	Short: "Yet another node info collector",
	Long:  `A respondd client that fetches, stores and publishes information about a Freifunk network.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.
	RootCmd.PersistentFlags().StringVar(&logFormat, "log-format", logFormatJSON, "log output in (json,text,human)")
	RootCmd.PersistentFlags().BoolVar(&logSource, "log-source", false, "log with line of code")
	RootCmd.PersistentFlags().BoolVar(&timestamps, "timestamps", false, "Enables timestamps for log output")
	RootCmd.PersistentFlags().TextVar(&logLevel, "log-level", slog.LevelInfo, "Show log message starting at level (debug,info,warn,error)")
}

func initConfig() {
	handlerOption := &slog.HandlerOptions{
		Level:     logLevel,
		AddSource: logSource,
	}
	if !timestamps {
		handlerOption.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		}
	}
	var h slog.Handler
	switch logFormat {
	case logFormatHuman:
		h = humanslog.NewHandler(os.Stdout, &humanslog.Options{
			HandlerOptions: handlerOption,
		})
	case logFormatText:
		h = slog.NewTextHandler(os.Stdout, handlerOption)
	default:
		h = slog.NewJSONHandler(os.Stdout, handlerOption)
	}
	slog.SetDefault(slog.New(h))
}
