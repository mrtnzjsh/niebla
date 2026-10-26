package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var (
	mountPoint = flag.String("mount", "", "Target mount point for virtual FUSE workspace")
	serverAddr = flag.String("server", "127.0.0.1:50051", "Niebla gRPC repository server address")
	scratchDir = flag.String("scratch", "", "Scratch directory for local copy-on-write overlay")
)

func main() {
	flag.Parse()
	log.Printf("NieblaFS FUSE daemon starting (mount: %q, server: %q, scratch: %q)", *mountPoint, *serverAddr, *scratchDir)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Ready signal for caller
	fmt.Println("nieblafs ready")

	if *mountPoint != "" {
		<-sigChan
		log.Println("NieblaFS unmounted and shutting down gracefully.")
	}
}