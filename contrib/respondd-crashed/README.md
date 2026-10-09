# respondd-crashed

This tool ping every "offline" node at every ip address of a meshviewer.json to detect, if a respondd deamon is not running anymore.


## give access to run ping
```bash
 sudo setcap cap_net_raw=+ep %GOPATH/bin/respondd-crashed
```


## Usage

```
Usage of ./respondd-crashed:
  -ll-iface string
    	interface to ping linklocal-address
  -log-format string
    	log output in (json,text,human) (default "json")
  -log-level value
    	Show log message starting at level (debug,info,warn,error) (default INFO)
  -log-source
    	log with line of code
  -meshviewer-url string
    	file path or http-url to meshviewer.json from yanic (default "meshviewer.json")
  -ping-count int
    	count of pings (default 3)
  -ping-timeout duration
    	timeout to wait for response (default 5s)
  -run-every duration
    	repeat check every (default 1m0s)
  -status-path string
    	path to store status (default "respondd-crashed.json")
  -timestamps
    	Enables timestamps for log output
```
