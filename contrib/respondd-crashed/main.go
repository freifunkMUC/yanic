package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"codeberg.org/FreifunkBremen/yanic/lib/log"
	"github.com/digineo/go-ping"
)

var (
	runEvery time.Duration

	iface string

	pingCount   int
	pingTimeout time.Duration

	meshviewerURL string
	statusPath    string
)

func main() {

	flag.DurationVar(&runEvery, "run-every", time.Duration(time.Minute), "repeat check every")

	flag.StringVar(&iface, "ll-iface", "", "interface to ping linklocal-address")

	flag.IntVar(&pingCount, "ping-count", 3, "count of pings")
	flag.DurationVar(&pingTimeout, "ping-timeout", time.Duration(time.Second*5), "timeout to wait for response")

	flag.StringVar(&statusPath, "status-path", "respondd-crashed.json", "path to store status")
	flag.StringVar(&meshviewerURL, "meshviewer-url", "meshviewer.json", "file path or http-url to meshviewer.json from yanic")

	// setup log
	flag.StringVar(&logFormat, "log-format", logFormatJSON, "log output in (json,text,human)")
	flag.BoolVar(&logSource, "log-source", false, "log with line of code")
	flag.BoolVar(&timestamps, "timestamps", false, "Enables timestamps for log output")
	flag.TextVar(&logLevel, "log-level", slog.LevelInfo, "Show log message starting at level (debug,info,warn,error)")

	flag.Parse()

	setupLog()

	pinger, err := ping.New("", "::")
	if err != nil {
		log.Panic("not able to bind pinger", log.AttrError(err))
	}

	timer := time.NewTimer(runEvery)

	stop := false

	wg := sync.WaitGroup{}

	slog.Info("start tester")

	func() {
		wg.Add(1)
		for !stop {
			select {
			case <-timer.C:
				run(pinger)
				timer.Reset(runEvery)
			}
		}
		timer.Stop()
		wg.Done()
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigs
	stop = true
	wg.Wait()
	slog.Info("stopped", "signal", sig)

}
