package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	configPath := flag.String("config", "", "path to gateway YAML config")
	flag.Parse()

	if *configPath == "" {
		*configPath = os.Getenv("GATEWAY_CONFIG")
	}

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "missing config path: pass --config or set GATEWAY_CONFIG")
		os.Exit(2)
	}

	fmt.Fprintf(os.Stdout, "GatewayKit scaffold ready; config path: %s\n", *configPath)
}
