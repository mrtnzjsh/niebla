package zfs

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// StreamConfig specifies parameters for streaming a ZFS snapshot across the 10GbE network link.
type StreamConfig struct {
	ZFSPath          string
	SSHPath          string
	LocalDataset     string
	RemoteHost       string
	RemoteUser       string
	RemotePort       int
	RemoteKeyFile    string
	RemoteDataset    string
	RawEncryptedSend bool
	BandwidthLimitMB int
}

// Streamer manages high-throughput ZFS snapshot streams.
type Streamer struct {
	cfg StreamConfig
}

// NewStreamer creates a new Streamer instance.
func NewStreamer(cfg StreamConfig) *Streamer {
	if cfg.ZFSPath == "" {
		cfg.ZFSPath = "zfs"
	}
	if cfg.SSHPath == "" {
		cfg.SSHPath = "ssh"
	}
	if cfg.RemotePort <= 0 {
		cfg.RemotePort = 22
	}
	return &Streamer{cfg: cfg}
}

// BuildSendArgs constructs the argument slice for zfs send.
func (s *Streamer) BuildSendArgs(targetSnap, baseSnap string) []string {
	var args []string
	args = append(args, "send")
	if s.cfg.RawEncryptedSend {
		args = append(args, "-w")
	}
	if baseSnap != "" {
		args = append(args, "-i", baseSnap)
	}
	args = append(args, targetSnap)
	return args
}

// BuildSSHRecvArgs constructs the argument slice for ssh to invoke remote zfs recv.
func (s *Streamer) BuildSSHRecvArgs() []string {
	var args []string
	if s.cfg.RemotePort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", s.cfg.RemotePort))
	}
	if s.cfg.RemoteKeyFile != "" {
		args = append(args, "-i", s.cfg.RemoteKeyFile)
	}
	args = append(args,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "Ciphers=aes128-gcm@openssh.com,aes256-gcm@openssh.com",
		fmt.Sprintf("%s@%s", s.cfg.RemoteUser, s.cfg.RemoteHost),
		fmt.Sprintf("zfs recv -F %s", s.cfg.RemoteDataset),
	)
	return args
}

// SendIncrementalStream streams a snapshot (full or incremental from baseSnap) to the remote server over SSH.
func (s *Streamer) SendIncrementalStream(ctx context.Context, targetSnap, baseSnap string) error {
	sendArgs := s.BuildSendArgs(targetSnap, baseSnap)
	sendCmd := exec.CommandContext(ctx, s.cfg.ZFSPath, sendArgs...)

	sshArgs := s.BuildSSHRecvArgs()
	recvCmd := exec.CommandContext(ctx, s.cfg.SSHPath, sshArgs...)

	// Connect sendCmd stdout to recvCmd stdin via pipe
	pr, pw := io.Pipe()
	sendCmd.Stdout = pw
	recvCmd.Stdin = pr

	var sendErrBuf, recvErrBuf strings.Builder
	sendCmd.Stderr = &sendErrBuf
	recvCmd.Stderr = &recvErrBuf

	if err := sendCmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return fmt.Errorf("failed to start zfs send: %w", err)
	}

	if err := recvCmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		if sendCmd.Process != nil {
			_ = sendCmd.Process.Kill()
		}
		return fmt.Errorf("failed to start remote zfs recv over ssh: %w (stderr: %s)", err, recvErrBuf.String())
	}

	sendErrCh := make(chan error, 1)
	go func() {
		err := sendCmd.Wait()
		_ = pw.Close()
		sendErrCh <- err
	}()

	recvErr := recvCmd.Wait()
	_ = pr.Close()
	sendErr := <-sendErrCh

	if sendErr != nil {
		return fmt.Errorf("zfs send failed: %w (stderr: %s)", sendErr, sendErrBuf.String())
	}
	if recvErr != nil {
		return fmt.Errorf("remote zfs recv failed: %w (stderr: %s)", recvErr, recvErrBuf.String())
	}

	return nil
}
