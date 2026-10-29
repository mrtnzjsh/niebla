package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
	"niebla/cmd/nieblafs/internal/fs"
	"niebla/cmd/nieblafs/internal/metadata"
	"niebla/pkg/nieblafs/control"
)

var (
	mountPoint = flag.String("mount", "", "Target mount point for virtual FUSE workspace")
	serverAddr = flag.String("server", "127.0.0.1:50051", "Niebla gRPC repository server address")
	scratchDir = flag.String("scratch", "", "Scratch directory for local copy-on-write overlay")
	cacheDir   = flag.String("cache", "", "CAS blob cache directory")
	treeHash   = flag.String("tree-hash", "", "Initial root Merkle tree hash")
	socketPath = flag.String("socket", "", "Unix domain control socket path for nieblafs IPC")
)

type daemonControlHandler struct {
	cache *metadata.MetadataCache
}

func (h *daemonControlHandler) HandlePing(ctx context.Context) error {
	return nil
}

func (h *daemonControlHandler) HandleReanchor(ctx context.Context, newRootTreeHash string, clID int64) (int, error) {
	purged := h.cache.Reanchor(newRootTreeHash, clID)
	log.Printf("Control: re-anchored to tree %s (CL %d), purged %d cached inodes", newRootTreeHash, clID, purged)
	return purged, nil
}

func (h *daemonControlHandler) HandleInvalidate(ctx context.Context, paths []string) (int, error) {
	purged := 0
	for _, p := range paths {
		if h.cache.Invalidate(p) {
			purged++
		}
	}
	log.Printf("Control: invalidated %d of %d paths", purged, len(paths))
	return purged, nil
}

func (h *daemonControlHandler) HandleStatus(ctx context.Context) (control.Response, error) {
	return control.Response{
		Status:        "ok",
		RootTreeHash:  h.cache.RootTreeHash(),
		CLID:          h.cache.CLID(),
		CachedEntries: h.cache.Count(),
	}, nil
}

func main() {
	flag.Parse()
	log.Printf("NieblaFS FUSE daemon starting (mount: %q, server: %q, scratch: %q, socket: %q)",
		*mountPoint, *serverAddr, *scratchDir, *socketPath)

	metaCache := metadata.NewMetadataCache()
	if *treeHash != "" {
		metaCache.SetRootTreeHash(*treeHash)
	}

	var ctrlServer *control.Server
	if *socketPath != "" {
		handler := &daemonControlHandler{cache: metaCache}
		ctrlServer = control.NewServer(*socketPath, handler)
		if err := ctrlServer.Start(); err != nil {
			log.Printf("warning: failed to start control socket on %s: %v", *socketPath, err)
		} else {
			defer ctrlServer.Close()
		}
	}

	var fuseConn *fuse.Conn
	if *mountPoint != "" {
		c, err := fuse.Mount(
			*mountPoint,
			fuse.FSName("nieblafs"),
			fuse.Subtype("nieblafs"),
		)
		if err != nil {
			log.Printf("warning: FUSE mount failed on %s: %v", *mountPoint, err)
		} else {
			fuseConn = c
			fsInstance := fs.NewFS(*scratchDir, *cacheDir, metaCache)
			go func() {
				if err := fusefs.Serve(fuseConn, fsInstance); err != nil {
					log.Printf("fusefs.Serve terminated: %v", err)
				}
			}()
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Ready signal for caller
	fmt.Println("nieblafs ready")

	<-sigChan
	log.Println("NieblaFS shutting down gracefully...")
	if fuseConn != nil {
		_ = fuseConn.Close()
	}
	if *mountPoint != "" {
		_ = fuse.Unmount(*mountPoint)
	}
}
