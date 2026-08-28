package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"cascade-demoops/backend/internal/directtransport"
)

func main() {
	controlAddr := flag.String("control-addr", env("CASCADE_DIRECT_CONTROL_ADDR", "0.0.0.0:18443"), "fixed TLS control listener")
	workerAddr := flag.String("worker-addr", env("CASCADE_DIRECT_WORKER_ADDR", "127.0.0.1:18444"), "loopback Browser Agent worker API")
	bindHost := flag.String("data-bind-host", env("CASCADE_DIRECT_DATA_BIND_HOST", "0.0.0.0"), "dedicated data listener host")
	advertisedHost := flag.String("advertised-host", os.Getenv("CASCADE_DIRECT_ADVERTISED_HOST"), "public DNS name used in leased data URLs")
	portStart := flag.Int("data-port-start", envInt("CASCADE_DIRECT_DATA_PORT_START", 24000), "first dedicated App port")
	portEnd := flag.Int("data-port-end", envInt("CASCADE_DIRECT_DATA_PORT_END", 24031), "last dedicated App port")
	spoolRoot := flag.String("spool-root", env("CASCADE_DIRECT_SPOOL_ROOT", "/var/lib/cascade-browser-agent"), "validated Browser Agent job spool")
	leaseTTL := flag.Duration("lease-ttl", envDuration("CASCADE_DIRECT_LEASE_TTL", 40*time.Minute), "dedicated data-port lease lifetime")
	flag.Parse()
	config := directtransport.Config{ControlAddr: *controlAddr, WorkerAddr: *workerAddr, DataBindHost: *bindHost, AdvertisedHost: *advertisedHost, DataPortStart: *portStart, DataPortEnd: *portEnd, TLSCertificateFile: os.Getenv("CASCADE_DIRECT_TLS_CERT"), TLSPrivateKeyFile: os.Getenv("CASCADE_DIRECT_TLS_KEY"), BootstrapToken: os.Getenv("CASCADE_DIRECT_BOOTSTRAP_TOKEN"), WorkerToken: os.Getenv("CASCADE_DIRECT_WORKER_TOKEN"), SpoolRoot: *spoolRoot, LeaseTTL: *leaseTTL}
	gateway, err := directtransport.NewGateway(config)
	must(err)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	fmt.Fprintf(os.Stdout, "Cascade Browser Agent gateway control=%s worker=%s data_ports=%d-%d\n", *controlAddr, *workerAddr, *portStart, *portEnd)
	must(gateway.Run(ctx))
}
func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err == nil && value > 0 {
		return value
	}
	return fallback
}
func envDuration(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(name))
	if err == nil && value > 0 {
		return value
	}
	return fallback
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
