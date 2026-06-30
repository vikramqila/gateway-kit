package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"gatewaykit/internal/config"
	"gatewaykit/internal/gateway"
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

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf(":%d", cfg.Gateway.Port)
	fmt.Fprintf(os.Stdout, "GatewayKit listening on %s with %d routes\n", addr, len(cfg.Gateway.Routes))
	if err := http.ListenAndServe(addr, gateway.NewHandler(cfg.Gateway)); err != nil {
		fmt.Fprintf(os.Stderr, "serve gateway: %v\n", err)
		os.Exit(1)
	}
}
